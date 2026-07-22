package application

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EngBlock/open-skills/internal/state"
)

type removeOptions struct {
	Global bool
	Agents []string
	Skills []string
	Yes    bool
	All    bool
}

type removalCandidate struct {
	Name string
}

func runRemove(invocation Invocation, arguments []string) int {
	invocation.Stdin = bufio.NewReader(invocation.Stdin)
	options, err := parseRemoveOptions(arguments)
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}
	if runningInAgent() {
		options.Yes = true
		_, _ = fmt.Fprintln(invocation.Stdout, "Agent detected — removing non-interactively")
	}
	project, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Determine project directory: %v\n", err)
		return 1
	}
	home, err := os.UserHomeDir()
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Determine home directory: %v\n", err)
		return 1
	}
	scope := state.Project
	if options.Global {
		scope = state.Global
	}
	targetAgents, allAgents, err := selectRemoveAgents(options, scope)
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}

	snapshot, err := state.Inspect(state.InspectOptions{
		Scope: scope, Project: project, Home: home, XDGStateHome: os.Getenv("XDG_STATE_HOME"), XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
	})
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Failed to inspect skill state: %v\n", err)
		return 1
	}
	candidates := removalCandidates(snapshot, scope, project, home)
	selected, cancelled, err := selectRemoveSkills(invocation, candidates, options)
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}
	if cancelled {
		_, _ = fmt.Fprintln(invocation.Stdout, "Removal cancelled.")
		return 0
	}
	if len(selected) == 0 {
		_, _ = fmt.Fprintln(invocation.Stdout, "No skills found to remove.")
		return 0
	}
	if !options.Yes {
		_, _ = fmt.Fprintln(invocation.Stdout, "Skills to remove:")
		for _, skill := range selected {
			_, _ = fmt.Fprintf(invocation.Stdout, "  - %s\n", skill.Name)
		}
		_, _ = fmt.Fprintf(invocation.Stdout, "Are you sure you want to uninstall %d skill(s)? [y/N] ", len(selected))
		line, readErr := readInputLine(invocation.Stdin)
		if readErr != nil && readErr != io.EOF {
			_, _ = fmt.Fprintf(invocation.Stderr, "Read confirmation: %v\n", readErr)
			return 1
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			_, _ = fmt.Fprintln(invocation.Stdout, "Removal cancelled.")
			return 0
		}
	}

	removed, failures := 0, 0
	for _, skill := range selected {
		if err := removeSkill(skill.Name, snapshot.Lock, scope, project, home, targetAgents, allAgents); err != nil {
			failures++
			_, _ = fmt.Fprintf(invocation.Stderr, "Remove %s: %v\n", skill.Name, err)
			continue
		}
		removed++
	}
	if removed > 0 {
		_, _ = fmt.Fprintf(invocation.Stdout, "Successfully removed %d skill(s)\n", removed)
	}
	if failures > 0 {
		_, _ = fmt.Fprintf(invocation.Stderr, "Failed to remove %d skill(s)\n", failures)
		return 1
	}
	_, _ = fmt.Fprintln(invocation.Stdout, "Done!")
	return 0
}

func parseRemoveOptions(arguments []string) (removeOptions, error) {
	options := removeOptions{}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch argument {
		case "-g", "--global":
			options.Global = true
		case "-y", "--yes":
			options.Yes = true
		case "--all":
			options.All = true
			options.Yes = true
		case "-a", "--agent":
			values, next := optionValues(arguments, index)
			if len(values) == 0 {
				return options, fmt.Errorf("%s requires at least one agent", argument)
			}
			options.Agents = append(options.Agents, values...)
			index = next
		case "-s", "--skill":
			values, next := optionValues(arguments, index)
			if len(values) == 0 {
				return options, fmt.Errorf("%s requires at least one skill", argument)
			}
			options.Skills = append(options.Skills, values...)
			index = next
		default:
			if strings.HasPrefix(argument, "-") {
				return options, fmt.Errorf("Unknown option: %s", argument)
			}
			options.Skills = append(options.Skills, argument)
		}
	}
	if options.All {
		options.Skills = []string{"*"}
		options.Agents = []string{"*"}
	}
	return options, nil
}

func selectRemoveAgents(options removeOptions, scope state.Scope) ([]string, bool, error) {
	requested := append([]string(nil), options.Agents...)
	allAgents := len(requested) == 0 || contains(requested, "*")
	if allAgents {
		requested = state.AgentIDs()
	}
	seen := make(map[string]bool, len(requested))
	selected := make([]string, 0, len(requested))
	invalid := []string{}
	for _, agent := range requested {
		if !state.IsAgentID(agent) {
			invalid = append(invalid, agent)
			continue
		}
		if !state.AgentSupportedInScope(agent, scope) {
			if allAgents {
				continue
			}
			return nil, false, fmt.Errorf("agent %q does not support %s installation", agent, scope)
		}
		if !seen[agent] {
			seen[agent] = true
			selected = append(selected, agent)
		}
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		return nil, false, fmt.Errorf("Invalid agents: %s\nValid agents: %s", strings.Join(invalid, ", "), strings.Join(state.AgentIDs(), ", "))
	}
	return selected, allAgents, nil
}

func removalCandidates(snapshot state.Snapshot, scope state.Scope, project, home string) []removalCandidate {
	bySanitized := make(map[string]string)
	for _, skill := range snapshot.Skills {
		bySanitized[state.SanitizeName(skill.Name)] = skill.Name
	}
	for name := range snapshot.Lock.Skills {
		// Lock keys are authoritative: they are the only name that can delete a
		// stale entry whose canonical directory has already disappeared.
		bySanitized[state.SanitizeName(name)] = name
	}
	for _, directory := range removalSkillDirectories(scope, project, home) {
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				key := state.SanitizeName(entry.Name())
				if _, exists := bySanitized[key]; !exists {
					bySanitized[key] = entry.Name()
				}
			}
		}
	}
	keys := make([]string, 0, len(bySanitized))
	for key := range bySanitized {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]removalCandidate, 0, len(keys))
	for _, key := range keys {
		result = append(result, removalCandidate{Name: bySanitized[key]})
	}
	return result
}

func selectRemoveSkills(invocation Invocation, candidates []removalCandidate, options removeOptions) ([]removalCandidate, bool, error) {
	if len(candidates) == 0 {
		return nil, false, nil
	}
	requested := append([]string(nil), options.Skills...)
	if len(requested) == 0 {
		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, candidate.Name)
		}
		_, _ = fmt.Fprintf(invocation.Stdout, "Select skills to remove (%s, or * for all): ", strings.Join(names, ", "))
		line, err := readInputLine(invocation.Stdin)
		if err != nil && err != io.EOF {
			return nil, false, err
		}
		requested = strings.FieldsFunc(strings.TrimSpace(line), func(r rune) bool { return r == ',' || r == ' ' })
		if len(requested) == 0 {
			return nil, true, nil
		}
	}
	if contains(requested, "*") {
		return candidates, false, nil
	}
	bySanitized := make(map[string]removalCandidate, len(candidates))
	for _, candidate := range candidates {
		bySanitized[state.SanitizeName(candidate.Name)] = candidate
	}
	selected := []removalCandidate{}
	seen := make(map[string]bool)
	missing := []string{}
	for _, name := range requested {
		candidate, exists := bySanitized[state.SanitizeName(name)]
		if !exists {
			missing = append(missing, name)
			continue
		}
		key := state.SanitizeName(candidate.Name)
		if !seen[key] {
			seen[key] = true
			selected = append(selected, candidate)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, false, fmt.Errorf("No matching skills found for: %s", strings.Join(missing, ", "))
	}
	return selected, false, nil
}

func removeSkill(name string, document *state.Document, scope state.Scope, project, home string, targetAgents []string, allAgents bool) error {
	lockName, entry := document.EntryWithName(name)
	canonicalBase := project
	if scope == state.Global {
		canonicalBase = home
	}
	canonical := filepath.Join(canonicalBase, ".agents", "skills", state.SanitizeName(name))
	for _, agent := range targetAgents {
		for _, placement := range removalAgentPaths(agent, name, scope, project, home) {
			if sameLocalPath(placement, canonical) {
				continue
			}
			if err := os.RemoveAll(placement); err != nil {
				return err
			}
		}
	}

	remainingAgents := []string(nil)
	remainingSubagents := []string(nil)
	if entry != nil {
		remainingAgents = subtractStrings(entry.Agents, targetAgents)
		if contains(targetAgents, "eve") {
			remainingSubagents = nil
		} else {
			remainingSubagents = append([]string(nil), entry.Subagents...)
		}
	}
	final := allAgents
	if !final && entry != nil && len(entry.Agents) > 0 && len(remainingAgents) == 0 {
		final = true
	}
	// A surviving noncanonical directory is an independently usable placement.
	// Keep canonical content and state even if older lock data did not record it.
	if final && hasRemainingPlacement(name, scope, project, home, targetAgents) {
		final = false
	}
	if final {
		if err := os.RemoveAll(canonical); err != nil {
			return err
		}
		if lockName != "" {
			document.RemoveInstallation(lockName)
		}
	} else if lockName != "" && entry != nil && len(entry.Agents) > 0 {
		document.RetainInstallationPlacements(lockName, remainingAgents, remainingSubagents)
	}
	if lockName != "" {
		lockPath := filepath.Join(project, "skills-lock.json")
		if scope == state.Global {
			lockPath = filepath.Join(os.Getenv("XDG_STATE_HOME"), "skills", ".skill-lock.json")
			if os.Getenv("XDG_STATE_HOME") == "" {
				lockPath = filepath.Join(home, ".agents", ".skill-lock.json")
			}
		}
		if err := document.Write(lockPath); err != nil {
			return err
		}
	}
	return nil
}

func removalSkillDirectories(scope state.Scope, project, home string) []string {
	result := []string{}
	for _, agent := range state.AgentIDs() {
		path, _, supported := state.AgentSkillsPath(agent, scope, project, home, os.Getenv("XDG_CONFIG_HOME"))
		if supported {
			result = append(result, path)
		}
	}
	if scope == state.Project {
		result = append(result, state.EveSkillsPath(project, ""))
		subagents, _ := os.ReadDir(filepath.Join(project, "agent", "subagents"))
		for _, subagent := range subagents {
			if subagent.IsDir() {
				result = append(result, state.EveSkillsPath(project, subagent.Name()))
			}
		}
	}
	return result
}

func removalAgentPaths(agent, name string, scope state.Scope, project, home string) []string {
	if agent == "eve" {
		if scope == state.Global {
			return nil
		}
		paths := []string{filepath.Join(state.EveSkillsPath(project, ""), state.SanitizeName(name))}
		subagents, _ := os.ReadDir(filepath.Join(project, "agent", "subagents"))
		for _, subagent := range subagents {
			if subagent.IsDir() {
				paths = append(paths, filepath.Join(state.EveSkillsPath(project, subagent.Name()), state.SanitizeName(name)))
			}
		}
		return paths
	}
	base, _, supported := state.AgentSkillsPath(agent, scope, project, home, os.Getenv("XDG_CONFIG_HOME"))
	if !supported {
		return nil
	}
	return []string{filepath.Join(base, state.SanitizeName(name))}
}

func hasRemainingPlacement(name string, scope state.Scope, project, home string, removedAgents []string) bool {
	for _, agent := range state.AgentIDs() {
		if contains(removedAgents, agent) || agent == "eve" {
			continue
		}
		base, universal, supported := state.AgentSkillsPath(agent, scope, project, home, os.Getenv("XDG_CONFIG_HOME"))
		if !supported || universal {
			continue
		}
		if _, err := os.Lstat(filepath.Join(base, state.SanitizeName(name))); err == nil {
			return true
		}
	}
	if scope == state.Project && !contains(removedAgents, "eve") {
		for _, path := range removalAgentPaths("eve", name, scope, project, home) {
			if _, err := os.Lstat(path); err == nil {
				return true
			}
		}
	}
	return false
}

func subtractStrings(values, remove []string) []string {
	result := []string{}
	for _, value := range values {
		if !contains(remove, value) {
			result = append(result, value)
		}
	}
	return result
}
