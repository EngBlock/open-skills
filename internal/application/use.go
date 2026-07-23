package application

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/EngBlock/open-skills/internal/state"
	truststore "github.com/EngBlock/open-skills/internal/trust"
)

type useOptions struct {
	Skill                          string
	SkillPath                      string
	Agent                          []string
	FullDepth                      bool
	Trust                          bool
	DangerouslyAcceptOpenClawRisks bool
	AllowInsecureTransport         bool
	Help                           bool
	Limits                         resourceLimits
}

type useAgentConfig struct {
	command string
}

var useAgentConfigs = map[string]useAgentConfig{
	"claude-code": {command: "claude"},
	"codex":       {command: "codex"},
}

func runUse(invocation Invocation, arguments []string) int {
	source, options, parseErrors := parseUseOptions(arguments)
	if options.Help {
		_, _ = fmt.Fprint(invocation.Stdout, useHelp)
		return 0
	}
	if len(parseErrors) > 0 {
		_, _ = fmt.Fprintln(invocation.Stderr, strings.Join(parseErrors, "\n"))
		return 1
	}
	if len(source) == 0 {
		_, _ = fmt.Fprintf(invocation.Stderr, "Missing required argument: source\n\n%s", useHelp)
		return 1
	}
	if len(source) > 1 {
		displaySources := make([]string, 0, len(source))
		for _, value := range source {
			displaySources = append(displaySources, credentialFreeSource(value))
		}
		_, _ = fmt.Fprintf(invocation.Stderr, "Expected one source, received %d: %s\n", len(source), strings.Join(displaySources, ", "))
		return 1
	}

	agent := ""
	if len(options.Agent) == 1 {
		agent = options.Agent[0]
		if _, ok := useAgentConfigs[agent]; !ok {
			_, _ = fmt.Fprintln(invocation.Stderr, formatUnsupportedUseAgent(agent))
			return 1
		}
	}

	rawSource := source[0]
	displaySource := rawSource
	root := ""
	repositoryRoot := ""
	selector := options.Skill
	var remote *remoteUseProvenance
	remoteIdentity := ""
	remoteGitRoot := ""
	isRemoteGit := false
	isRemote := false
	localPath, localPathErr := filepath.Abs(rawSource)
	localInfo, localStatErr := os.Stat(localPath)
	if localPathErr == nil && localStatErr == nil && localInfo.IsDir() {
		root = localPath
		repositoryRoot = root
		if options.Limits.hasRemoteOverrides() {
			_, _ = fmt.Fprintln(invocation.Stderr, "--max-file-bytes, --max-total-bytes, and --max-files apply only to remote sources")
			return 1
		}
	} else if isClearlyLocalPath(rawSource) {
		if localPathErr != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Invalid local path: %v\n", localPathErr)
			return 1
		}
		_, _ = fmt.Fprintf(invocation.Stderr, "Local path does not exist: %s\n", localPath)
		return 1
	} else if wellKnown, matches, parseErr := parseWellKnownSource(rawSource); matches {
		if parseErr != nil {
			_, _ = fmt.Fprintln(invocation.Stderr, parseErr)
			return 1
		}
		var err error
		selector, err = resolveUseSelector(wellKnown.directName, selector)
		if err != nil {
			_, _ = fmt.Fprintln(invocation.Stderr, err)
			return 1
		}
		selectors := []string{}
		if selector != "" {
			selectors = []string{selector}
		}
		fetched, err := fetchWellKnownSkills(wellKnown, options.Limits, selectors, newHTTPAcquisitionPolicy(options.AllowInsecureTransport, invocation.Stderr))
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Discover well-known skills: %v\n", err)
			return 1
		}
		root, _, err = materializeWellKnownSkills(fetched)
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Materialize well-known skills: %v\n", err)
			return 1
		}
		defer os.RemoveAll(root)
		repositoryRoot = root
		remoteIdentity = wellKnown.identity
		displaySource = wellKnown.identity
		isRemote = true
	} else {
		git, err := parseGitSource(rawSource)
		if err != nil {
			_, _ = fmt.Fprintln(invocation.Stderr, err)
			return 1
		}
		displaySource = git.Identity
		if isOpenClawGitSource(git) && !options.DangerouslyAcceptOpenClawRisks {
			_, _ = fmt.Fprintf(invocation.Stderr, "OpenClaw skills are unverified community submissions.\nSkills run with full agent permissions and could be malicious.\nIf you understand the risks, re-run with: open-skills use %s --dangerously-accept-openclaw-risks\n", displaySource)
			return 1
		}
		selector, err = resolveUseSelector(git.SkillFilter, selector)
		if err != nil {
			_, _ = fmt.Fprintln(invocation.Stderr, err)
			return 1
		}
		workspace, err := materializeGitSourceWithPolicy(git, options.Limits, gitAcquisitionPolicy{AllowInsecureTransport: options.AllowInsecureTransport, Notice: invocation.Stderr})
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Acquire Git source: %v\n", err)
			return 1
		}
		defer workspace.remove()
		root = workspace.Root
		repositoryRoot = workspace.Root
		if git.Subpath != "" {
			root = filepath.Join(root, filepath.FromSlash(git.Subpath))
			info, statErr := os.Stat(root)
			if statErr != nil || !info.IsDir() {
				_, _ = fmt.Fprintf(invocation.Stderr, "Git source subpath does not exist or is not a directory: %s\n", git.Subpath)
				return 1
			}
		}
		remote = &remoteUseProvenance{source: git.Identity, commit: workspace.Commit}
		remoteGitRoot = workspace.Root
		isRemoteGit = true
		isRemote = true
	}

	skills, err := discoverLocalSkillsWithLimits(root, options.FullDepth, options.Limits)
	if err == nil {
		err = assignRepositoryRelativePaths(skills, repositoryRoot)
	}
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Discover skills: %v\n", err)
		return 1
	}
	selected, err := selectUseSkill(skills, selector, options.SkillPath, displaySource)
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}
	if isRemote {
		selected.content, err = prepareSkillContentWithBudget(selected.Path, newResourceBudget(options.Limits))
		if err == nil && isRemoteGit {
			err = selected.content.rejectLFSPointers()
		}
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Use %s: %v\n", selected.Name, err)
			return 1
		}
	}
	if isRemoteGit {
		relative, err := filepath.Rel(remoteGitRoot, filepath.Join(selected.Path, "SKILL.md"))
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			_, _ = fmt.Fprintln(invocation.Stderr, "Selected Git skill path escapes the acquired source.")
			return 1
		}
		remote.skillPath = filepath.ToSlash(relative)
	}
	if remote == nil && remoteIdentity != "" {
		revision, err := remoteContentRevision(selected.Path)
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Identify remote skill content: %v\n", err)
			return 1
		}
		remote = &remoteUseProvenance{source: remoteIdentity, commit: revision}
	}

	materialized, err := materializeUseSkill(selected)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Materialize skill: %v\n", err)
		return 1
	}
	prompt := buildUsePrompt(materialized.skillMD, materialized.directory, materialized.hasSupportingFiles)
	if remote != nil {
		prompt = buildRemoteUsePrompt(*remote, prompt)
	}
	if agent == "" {
		_, _ = io.WriteString(invocation.Stdout, prompt)
		return 0
	}
	if remote != nil {
		if err := authorizeRemoteAgentUse(invocation, options, *remote, selected); err != nil {
			_, _ = fmt.Fprintln(invocation.Stderr, err)
			return 1
		}
	}
	return launchUseAgent(invocation, agent, prompt)
}

func parseUseOptions(arguments []string) ([]string, useOptions, []string) {
	source := []string{}
	options := useOptions{Limits: defaultResourceLimits()}
	errors := []string{}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch argument {
		case "--help", "-h":
			options.Help = true
		case "--full-depth":
			options.FullDepth = true
		case "--trust":
			options.Trust = true
		case "--allow-insecure-transport":
			options.AllowInsecureTransport = true
		case "--yes", "-y":
			errors = append(errors, argument+" does not authorize remote agent trust; use --trust")
		case "--dangerously-accept-openclaw-risks":
			options.DangerouslyAcceptOpenClawRisks = true
		case "--skill", "-s":
			if index+1 >= len(arguments) || strings.HasPrefix(arguments[index+1], "-") {
				errors = append(errors, argument+" requires a skill name")
				continue
			}
			if options.Skill != "" {
				errors = append(errors, "Only one --skill value can be provided")
				index++
				continue
			}
			index++
			options.Skill = arguments[index]
		case "--skill-path":
			if index+1 >= len(arguments) || strings.HasPrefix(arguments[index+1], "-") {
				errors = append(errors, argument+" requires a repository-relative skill path")
				continue
			}
			if options.SkillPath != "" {
				errors = append(errors, "Only one --skill-path value can be provided")
				index++
				continue
			}
			index++
			options.SkillPath = arguments[index]
		case "--agent", "-a":
			start := len(options.Agent)
			for index+1 < len(arguments) && !strings.HasPrefix(arguments[index+1], "-") {
				index++
				options.Agent = append(options.Agent, arguments[index])
			}
			if len(options.Agent) == start {
				errors = append(errors, argument+" requires an agent name")
			}
		default:
			matched, next, limitErr := parseResourceLimitOption(arguments, index, &options.Limits)
			if limitErr != nil {
				errors = append(errors, limitErr.Error())
				continue
			}
			if matched {
				index = next
				continue
			}
			if strings.HasPrefix(argument, "-") {
				errors = append(errors, "Unknown option: "+argument)
			} else {
				source = append(source, argument)
			}
		}
	}

	if options.Skill != "" && options.SkillPath != "" {
		errors = append(errors, "Provide either --skill or --skill-path, not both")
	}

	if len(options.Agent) > 0 {
		invalid := []string{}
		for _, agent := range options.Agent {
			if agent != "*" && !state.IsAgentID(agent) {
				invalid = append(invalid, agent)
			}
		}
		if contains(options.Agent, "*") {
			errors = append(errors, "open-skills use --agent does not support '*'; specify exactly one agent.")
		}
		if len(options.Agent) > 1 {
			errors = append(errors, "open-skills use --agent accepts exactly one agent.")
		}
		if len(invalid) > 0 {
			errors = append(errors, fmt.Sprintf("Invalid agents: %s\nValid agents: %s", strings.Join(invalid, ", "), strings.Join(state.AgentIDs(), ", ")))
		}
	}
	return source, options, errors
}

func isOpenClawGitSource(source gitSource) bool {
	identity := source.Identity
	if strings.Contains(identity, "://") {
		if parsed, err := url.Parse(identity); err == nil {
			identity = strings.Trim(parsed.Path, "/")
		}
	} else if host, path, found := strings.Cut(identity, ":"); found && strings.Contains(host, ".") {
		identity = strings.Trim(path, "/")
	}
	owner, _, _ := strings.Cut(identity, "/")
	return strings.EqualFold(owner, "openclaw")
}

func selectUseSkill(skills []localSkill, selector, skillPath, source string) (localSkill, error) {
	if len(skills) == 0 {
		return localSkill{}, errors.New("No valid skills found. Skills require a SKILL.md with name and description.")
	}
	if selector != "" && skillPath != "" {
		return localSkill{}, errors.New("Provide either --skill or --skill-path, not both")
	}
	if skillPath != "" {
		selected, err := selectSkillsByPath(skills, []string{skillPath})
		if err != nil {
			return localSkill{}, err
		}
		return selected[0], nil
	}
	if selector == "" {
		if len(skills) == 1 {
			return skills[0], nil
		}
		names := skillNamesSlice(skills)
		return localSkill{}, fmt.Errorf("This source contains multiple skills. Specify exactly one skill:\n%s\n\nExamples:\n  open-skills use %s@%s\n  open-skills use %s --skill %s\n  open-skills use %s --skill-path %s", listSkillNames(skillLabelsWithCollisionPaths(skills)), source, names[0], source, names[0], source, skills[0].RelativePath)
	}

	matches := []localSkill{}
	key := normalizedSkillName(selector)
	for _, skill := range skills {
		if normalizedSkillName(skill.Name) == key {
			matches = append(matches, skill)
		}
	}
	if len(matches) == 0 {
		return localSkill{}, fmt.Errorf("No matching skill found for: %s\nAvailable skills:\n%s", selector, listSkillNames(skillLabelsWithCollisionPaths(skills)))
	}
	if len(matches) > 1 {
		return localSkill{}, formatSkillAmbiguity(selector, matches)
	}
	return matches[0], nil
}

func skillLabelsWithCollisionPaths(skills []localSkill) []string {
	labels := make([]string, 0, len(skills))
	colliding := collidingSkillPaths(skills)
	for _, skill := range skills {
		label := displaySkillName(skill.Name)
		if colliding[normalizedSkillName(skill.Name)] {
			label += " [" + displaySkillPath(skill.RelativePath) + "]"
		}
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

func skillNamesSlice(skills []localSkill) []string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	sort.Strings(names)
	return names
}

func listSkillNames(names []string) string {
	items := make([]string, 0, len(names))
	for _, name := range names {
		items = append(items, "  - "+name)
	}
	return strings.Join(items, "\n")
}

type remoteUseProvenance struct {
	source    string
	commit    string
	skillPath string
}

type materializedUseSkill struct {
	directory          string
	skillMD            string
	hasSupportingFiles bool
}

func materializeUseSkill(skill localSkill) (materializedUseSkill, error) {
	root, err := os.MkdirTemp("", "skills-use-")
	if err != nil {
		return materializedUseSkill{}, err
	}
	directory := filepath.Join(root, state.SanitizeName(skill.Name))
	content := skill.content
	if content == nil {
		content, err = prepareSkillContent(skill.Path)
		if err != nil {
			_ = os.RemoveAll(root)
			return materializedUseSkill{}, err
		}
	}
	if err := writeUseSkillContent(content, directory); err != nil {
		_ = os.RemoveAll(root)
		return materializedUseSkill{}, err
	}
	skillMD, err := os.ReadFile(filepath.Join(directory, "SKILL.md"))
	if err != nil {
		_ = os.RemoveAll(root)
		return materializedUseSkill{}, err
	}
	hasSupportingFiles, err := useSkillHasSupportingFiles(directory)
	if err != nil {
		_ = os.RemoveAll(root)
		return materializedUseSkill{}, err
	}
	return materializedUseSkill{directory: directory, skillMD: string(skillMD), hasSupportingFiles: hasSupportingFiles}, nil
}

func writeUseSkillContent(content *skillContent, destination string) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	for _, relative := range content.directories {
		if useContentPathExcluded(relative, true) {
			continue
		}
		if err := os.MkdirAll(filepath.Join(destination, relative), 0o755); err != nil {
			return err
		}
	}
	for _, file := range content.files {
		if useContentPathExcluded(file.relative, false) {
			continue
		}
		target := filepath.Join(destination, file.relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, file.data, file.mode); err != nil {
			return err
		}
	}
	return nil
}

func useContentPathExcluded(relative string, directory bool) bool {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for index, part := range parts {
		isDirectory := index < len(parts)-1 || directory
		if isDirectory && (part == ".git" || part == "__pycache__" || part == "__pypackages__") {
			return true
		}
		if !isDirectory && part == "metadata.json" {
			return true
		}
	}
	return false
}

func useSkillHasSupportingFiles(root string) (bool, error) {
	hasFiles := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || hasFiles || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !strings.EqualFold(filepath.ToSlash(relative), "SKILL.md") {
			hasFiles = true
		}
		return nil
	})
	return hasFiles, err
}

func resolveUseSelector(sourceSelector, optionSelector string) (string, error) {
	if sourceSelector != "" && optionSelector != "" && !strings.EqualFold(sourceSelector, optionSelector) {
		return "", fmt.Errorf("Conflicting skill selectors: source selects %q but --skill selects %q. Provide one selector.", sourceSelector, optionSelector)
	}
	if optionSelector != "" {
		return optionSelector, nil
	}
	return sourceSelector, nil
}

func buildRemoteUsePrompt(provenance remoteUseProvenance, prompt string) string {
	return fmt.Sprintf("Remote skill source: %s\nRemote skill commit: %s\n\n%s", provenance.source, provenance.commit, prompt)
}

func buildUsePrompt(skillMD, supportDirectory string, hasSupportingFiles bool) string {
	prompt := "You are being given a Skill to execute for the user's next request.\n\n" +
		"Use the following SKILL.md as your instructions:\n\n" +
		"<SKILL.md>\n" + skillMD + "\n</SKILL.md>"
	if hasSupportingFiles {
		prompt += "\n\nSupporting files for this skill were downloaded to:\n" + supportDirectory +
			"\n\nWhen the SKILL.md references relative paths, read them from that directory."
	}
	return prompt + "\n"
}

func authorizeRemoteAgentUse(invocation Invocation, options useOptions, provenance remoteUseProvenance, skill localSkill) error {
	_, _ = fmt.Fprintf(invocation.Stderr, "Remote skill source: %s\nRemote skill commit: %s\n", provenance.source, provenance.commit)
	store, err := truststore.Open()
	if err != nil {
		return fmt.Errorf("read trust approvals: %w", err)
	}
	if store.Contains(provenance.source, provenance.commit) || installedCommitApproved(skill.Name, provenance) {
		return nil
	}
	if options.Trust {
		if err := store.Approve(provenance.source, provenance.commit, time.Now()); err != nil {
			return fmt.Errorf("record trust approval: %w", err)
		}
		return nil
	}
	if !invocation.Interactive {
		return errors.New("remote agent use is not trusted; automation must re-run with --trust (not --yes) to approve this exact source commit")
	}
	_, _ = fmt.Fprint(invocation.Stderr, "Trust this exact source commit and launch the agent? [y/N] ")
	answer, err := readInputLine(invocation.Stdin)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read trust confirmation: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return errors.New("remote agent use cancelled")
	}
	if err := store.Approve(provenance.source, provenance.commit, time.Now()); err != nil {
		return fmt.Errorf("record trust approval: %w", err)
	}
	return nil
}

func installedCommitApproved(skillName string, provenance remoteUseProvenance) bool {
	project, projectErr := os.Getwd()
	home, homeErr := os.UserHomeDir()
	if projectErr != nil || homeErr != nil {
		return false
	}
	base := state.InspectOptions{
		Project: project, Home: home,
		XDGStateHome: os.Getenv("XDG_STATE_HOME"), XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
	}
	for _, scope := range []state.Scope{state.Project, state.Global} {
		options := base
		options.Scope = scope
		snapshot, err := state.Inspect(options)
		if err != nil {
			continue
		}
		for _, installed := range snapshot.Skills {
			if installed.Name != skillName || installed.Lock == nil {
				continue
			}
			entry := installed.Lock
			if entry.Ref != provenance.commit || entry.SkillPath != provenance.skillPath || (entry.Source != provenance.source && entry.SourceURL != provenance.source) {
				continue
			}
			expectedHash := entry.InstalledContentHash
			if expectedHash == "" && scope == state.Project {
				expectedHash = entry.ComputedHash
			}
			if expectedHash == "" {
				continue
			}
			actualHash, _, err := contentIdentity(installed.CanonicalPath)
			if err == nil && actualHash == expectedHash {
				return true
			}
		}
	}
	return false
}

func launchUseAgent(invocation Invocation, agent, prompt string) int {
	config := useAgentConfigs[agent]
	command := exec.Command(config.command, prompt)
	command.Stdin = invocation.Stdin
	command.Stdout = invocation.Stdout
	command.Stderr = invocation.Stderr
	if err := command.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			_, _ = fmt.Fprintf(invocation.Stderr, "Could not launch %s: command not found: %s\n", state.AgentDisplayName(agent), config.command)
			return 1
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return exitError.ExitCode()
		}
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}
	return 0
}

func formatUnsupportedUseAgent(agent string) string {
	return fmt.Sprintf("Running %s is not supported yet.\nSupported agents for open-skills use --agent: claude-code, codex", state.AgentDisplayName(agent))
}

const useHelp = `Usage: open-skills use <source>[@<skill>] [options]

Generate a prompt for using one skill without installing it.

Options:
  -s, --skill <skill>   Select the skill to use
  --skill-path <path>   Select an exact repository-relative skill directory
  -a, --agent <agent>   Start one supported agent interactively (claude-code, codex)
  --full-depth          Search nested directories like open-skills add --full-depth
  --max-file-bytes <n>  Remote per-file limit (default: 10485760)
  --max-total-bytes <n> Remote total-content limit (default: 104857600)
  --max-files <n>       Remote file-count limit (default: 5000)
  --max-depth <n>       Full-depth traversal ceiling (default: 20)
  --allow-insecure-transport
                        Allow plaintext HTTP/Git sources with a warning
  --trust               Approve this exact remote source commit for agent use
  --dangerously-accept-openclaw-risks
                         Allow unverified OpenClaw community skills
  -h, --help            Show this help message

Examples:
  open-skills use EngBlock/open-skills@find-skills | claude
  open-skills use EngBlock/open-skills --skill find-skills --agent claude-code
  open-skills use EngBlock/open-skills@find-skills --agent codex
`
