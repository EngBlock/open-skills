package application

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EngBlock/open-skills/internal/state"
)

type useOptions struct {
	Skill     string
	Agent     []string
	FullDepth bool
	Help      bool
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
		_, _ = fmt.Fprintf(invocation.Stderr, "Expected one source, received %d: %s\n", len(source), strings.Join(source, ", "))
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

	if !isClearlyLocalPath(source[0]) {
		_, _ = fmt.Fprintln(invocation.Stderr, "Only local paths are supported by this build of open-skills use.")
		return 1
	}
	root, err := filepath.Abs(source[0])
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Invalid local path: %v\n", err)
		return 1
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		_, _ = fmt.Fprintf(invocation.Stderr, "Local path does not exist: %s\n", root)
		return 1
	}

	skills, err := discoverLocalSkills(root, options.FullDepth)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Discover skills: %v\n", err)
		return 1
	}
	selected, err := selectUseSkill(skills, options.Skill, source[0])
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}

	materialized, err := materializeUseSkill(selected)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Materialize skill: %v\n", err)
		return 1
	}
	prompt := buildUsePrompt(materialized.skillMD, materialized.directory, materialized.hasSupportingFiles)
	if agent == "" {
		_, _ = io.WriteString(invocation.Stdout, prompt)
		return 0
	}
	return launchUseAgent(invocation, agent, prompt)
}

func parseUseOptions(arguments []string) ([]string, useOptions, []string) {
	source := []string{}
	options := useOptions{}
	errors := []string{}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch argument {
		case "--help", "-h":
			options.Help = true
		case "--full-depth":
			options.FullDepth = true
		case "--dangerously-accept-openclaw-risks":
			// This is only meaningful for remote OpenClaw sources. Retain the
			// accepted option so local invocation syntax stays compatible.
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
			if strings.HasPrefix(argument, "-") {
				errors = append(errors, "Unknown option: "+argument)
			} else {
				source = append(source, argument)
			}
		}
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

func selectUseSkill(skills []localSkill, selector, source string) (localSkill, error) {
	if len(skills) == 0 {
		return localSkill{}, errors.New("No valid skills found. Skills require a SKILL.md with name and description.")
	}
	if selector == "" {
		if len(skills) == 1 {
			return skills[0], nil
		}
		names := skillNamesSlice(skills)
		return localSkill{}, fmt.Errorf("This source contains multiple skills. Specify exactly one skill:\n%s\n\nExamples:\n  open-skills use %s@%s\n  open-skills use %s --skill %s", listSkillNames(names), source, names[0], source, names[0])
	}

	matches := []localSkill{}
	for _, skill := range skills {
		if strings.EqualFold(skill.Name, selector) {
			matches = append(matches, skill)
		}
	}
	if len(matches) == 0 {
		return localSkill{}, fmt.Errorf("No matching skill found for: %s\nAvailable skills:\n%s", selector, listSkillNames(skillNamesSlice(skills)))
	}
	if len(matches) > 1 {
		return localSkill{}, fmt.Errorf("Skill selector %q matched multiple skills.", selector)
	}
	return matches[0], nil
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
	if err := copyUseSkillDirectory(skill.Path, directory); err != nil {
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

func copyUseSkillDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(destination, 0o755)
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "__pycache__" || entry.Name() == "__pypackages__") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && entry.Name() == "metadata.json" {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported non-regular skill file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
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
  -a, --agent <agent>   Start one supported agent interactively (claude-code, codex)
  --full-depth          Search nested directories like open-skills add --full-depth
  --dangerously-accept-openclaw-risks
                         Allow unverified OpenClaw community skills
  -h, --help            Show this help message

Examples:
  open-skills use EngBlock/open-skills@find-skills | claude
  open-skills use EngBlock/open-skills --skill find-skills --agent claude-code
  open-skills use EngBlock/open-skills@find-skills --agent codex
`
