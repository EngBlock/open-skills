package application

import (
	"encoding/json"
	"fmt"
	"strings"

	truststore "github.com/EngBlock/open-skills/internal/trust"
)

func runTrust(invocation Invocation, arguments []string) int {
	if len(arguments) == 0 {
		_, _ = fmt.Fprint(invocation.Stderr, trustHelp)
		return 1
	}
	if arguments[0] == "clear" {
		return runTrustClear(invocation, arguments[1:])
	}
	store, err := truststore.Open()
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Read trust approvals: %v\n", err)
		return 1
	}
	switch arguments[0] {
	case "list", "ls":
		return runTrustList(invocation, store, arguments[1:])
	case "revoke":
		return runTrustRevoke(invocation, store, arguments[1:])
	default:
		_, _ = fmt.Fprintf(invocation.Stderr, "Unknown trust command: %s\n", arguments[0])
		return 1
	}
}

func runTrustList(invocation Invocation, store *truststore.Store, arguments []string) int {
	jsonOutput := false
	for _, argument := range arguments {
		if argument == "--json" {
			jsonOutput = true
			continue
		}
		_, _ = fmt.Fprintf(invocation.Stderr, "Unknown option: %s\n", argument)
		return 1
	}
	approvals := store.Approvals()
	for index := range approvals {
		approvals[index].Source = credentialFreeSource(approvals[index].Source)
	}
	if jsonOutput {
		output := struct {
			Version   int                   `json:"version"`
			Approvals []truststore.Approval `json:"approvals"`
		}{Version: 1, Approvals: approvals}
		data, err := json.Marshal(output)
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Encode trust approvals: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(invocation.Stdout, string(data))
		return 0
	}
	if len(approvals) == 0 {
		_, _ = fmt.Fprintln(invocation.Stdout, "No trusted remote skill commits.")
		return 0
	}
	for _, approval := range approvals {
		_, _ = fmt.Fprintf(invocation.Stdout, "%s\t%s\t%s\n", approval.Source, approval.Commit, approval.ApprovedAt)
	}
	return 0
}

func runTrustRevoke(invocation Invocation, store *truststore.Store, arguments []string) int {
	if len(arguments) == 0 {
		_, _ = fmt.Fprintln(invocation.Stderr, "trust revoke requires a source")
		return 1
	}
	sourceArgument := arguments[0]
	commit := ""
	commitProvided := false
	yes := false
	for index := 1; index < len(arguments); index++ {
		switch arguments[index] {
		case "--commit":
			if commitProvided || index+1 >= len(arguments) || strings.HasPrefix(arguments[index+1], "-") || strings.TrimSpace(arguments[index+1]) == "" {
				_, _ = fmt.Fprintln(invocation.Stderr, "--commit requires one non-empty commit")
				return 1
			}
			index++
			commit = arguments[index]
			commitProvided = true
		case "--yes", "-y":
			yes = true
		default:
			_, _ = fmt.Fprintf(invocation.Stderr, "Unknown option: %s\n", arguments[index])
			return 1
		}
	}
	identity, err := normalizeTrustIdentity(sourceArgument, store.Approvals())
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}
	displayIdentity := credentialFreeSource(identity)
	if !commitProvided && !yes {
		if !invocation.Interactive {
			_, _ = fmt.Fprintln(invocation.Stderr, "Broad trust revocation requires confirmation; re-run with --yes.")
			return 1
		}
		confirmed, confirmErr := confirmBroadTrust(invocation, fmt.Sprintf("Revoke every trusted commit for %s? [y/N] ", displayIdentity))
		if confirmErr != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Read trust confirmation: %v\n", confirmErr)
			return 1
		}
		if !confirmed {
			_, _ = fmt.Fprintln(invocation.Stderr, "Trust revocation cancelled.")
			return 1
		}
	}
	removed, err := store.Revoke(identity, commit)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Revoke trust approval: %v\n", err)
		return 1
	}
	if removed == 0 {
		_, _ = fmt.Fprintln(invocation.Stdout, "No matching trust approval.")
	} else if !commitProvided {
		_, _ = fmt.Fprintf(invocation.Stdout, "Revoked %d trust approval(s) for %s.\n", removed, displayIdentity)
	} else {
		_, _ = fmt.Fprintf(invocation.Stdout, "Revoked trust for %s at %s.\n", displayIdentity, commit)
	}
	return 0
}

func runTrustClear(invocation Invocation, arguments []string) int {
	yes := false
	for _, argument := range arguments {
		if argument == "--yes" || argument == "-y" {
			yes = true
			continue
		}
		_, _ = fmt.Fprintf(invocation.Stderr, "Unknown option: %s\n", argument)
		return 1
	}
	if !yes {
		if !invocation.Interactive {
			_, _ = fmt.Fprintln(invocation.Stderr, "Clearing all trust approvals requires confirmation; re-run with --yes.")
			return 1
		}
		confirmed, err := confirmBroadTrust(invocation, "Clear every trust approval? [y/N] ")
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Read trust confirmation: %v\n", err)
			return 1
		}
		if !confirmed {
			_, _ = fmt.Fprintln(invocation.Stderr, "Trust clear cancelled.")
			return 1
		}
	}
	if err := truststore.ClearAll(); err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Clear trust approvals: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(invocation.Stdout, "Cleared all trust approvals.")
	return 0
}

func confirmBroadTrust(invocation Invocation, prompt string) (bool, error) {
	_, _ = fmt.Fprint(invocation.Stderr, prompt)
	answer, err := readInputLine(invocation.Stdin)
	if err != nil {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func normalizeTrustIdentity(raw string, approvals []truststore.Approval) (string, error) {
	raw = strings.TrimSpace(raw)
	for _, approval := range approvals {
		if approval.Source == raw {
			return raw, nil
		}
	}
	if source, matches, err := parseWellKnownSource(raw); matches {
		if err != nil {
			return "", err
		}
		return source.identity, nil
	}
	source, err := parseGitSource(raw)
	if err != nil {
		return "", fmt.Errorf("invalid trust source: %w", err)
	}
	return source.Identity, nil
}

const trustHelp = `Usage: open-skills trust <command>

Commands:
  list [--json]                                 List exact source-commit approvals
  revoke <source> [--commit <commit>] [--yes]  Revoke one commit or every commit for a source
  clear [--yes]                                Clear every trust approval
`
