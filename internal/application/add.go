package application

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EngBlock/open-skills/internal/state"
)

type addOptions struct {
	Global    bool
	Agents    []string
	Skills    []string
	List      bool
	Yes       bool
	All       bool
	FullDepth bool
	Copy      bool
}

type localSkill struct {
	Name string
	Path string
}

// runAddLocal owns the complete offline local-source workflow: discovery,
// selection, canonical installation topology, and compatible lock mutation.
// Keeping those decisions here gives the command a small seam while state owns
// schema validation and persistence.
func runAddLocal(invocation Invocation, arguments []string) int {
	source, options, err := parseAddOptions(arguments)
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}
	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Invalid local path: %v\n", err)
		return 1
	}
	info, err := os.Stat(absoluteSource)
	if err != nil || !info.IsDir() {
		_, _ = fmt.Fprintf(invocation.Stderr, "Local path does not exist or is not a directory: %s\n", source)
		return 1
	}

	skills, err := discoverLocalSkills(absoluteSource, options.FullDepth)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Discover local skills: %v\n", err)
		return 1
	}
	if len(skills) == 0 {
		_, _ = fmt.Fprintln(invocation.Stderr, "No valid skills found. Skills require a SKILL.md with name and description.")
		return 1
	}
	if options.List {
		for _, skill := range skills {
			_, _ = fmt.Fprintln(invocation.Stdout, skill.Name)
		}
		return 0
	}

	selected, err := selectLocalSkills(invocation, skills, options)
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
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
	base := project
	if options.Global {
		scope = state.Global
		base = home
	}
	agents, err := selectInstallAgents(options)
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}
	for _, skill := range selected {
		if err := installLocalSkill(skill, absoluteSource, scope, base, project, home, agents, options.Copy); err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Install %s: %v\n", skill.Name, err)
			return 1
		}
		_, _ = fmt.Fprintf(invocation.Stdout, "Installed %s\n", skill.Name)
	}
	return 0
}

func parseAddOptions(arguments []string) (string, addOptions, error) {
	options := addOptions{}
	var source string
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch argument {
		case "-g", "--global":
			options.Global = true
		case "-y", "--yes":
			options.Yes = true
		case "-l", "--list":
			options.List = true
		case "--all":
			options.All = true
			options.Yes = true
		case "--full-depth":
			options.FullDepth = true
		case "--copy":
			options.Copy = true
		case "-a", "--agent":
			values, next := optionValues(arguments, index)
			if len(values) == 0 {
				return "", options, fmt.Errorf("%s requires at least one agent", argument)
			}
			options.Agents = append(options.Agents, values...)
			index = next
		case "-s", "--skill":
			values, next := optionValues(arguments, index)
			if len(values) == 0 {
				return "", options, fmt.Errorf("%s requires at least one skill", argument)
			}
			options.Skills = append(options.Skills, values...)
			index = next
		default:
			if strings.HasPrefix(argument, "-") {
				return "", options, fmt.Errorf("Unknown option: %s", argument)
			}
			if source != "" {
				return "", options, fmt.Errorf("add accepts one source")
			}
			source = argument
		}
	}
	if source == "" {
		return "", options, fmt.Errorf("Missing required argument: source")
	}
	if options.All {
		options.Skills = []string{"*"}
		options.Agents = []string{"*"}
	}
	return source, options, nil
}

func optionValues(arguments []string, index int) ([]string, int) {
	values := []string{}
	for index+1 < len(arguments) && !strings.HasPrefix(arguments[index+1], "-") {
		index++
		values = append(values, arguments[index])
	}
	return values, index
}

func discoverLocalSkills(root string, fullDepth bool) ([]localSkill, error) {
	result := []localSkill{}
	seen := make(map[string]bool)
	add := func(directory string) {
		name, ok := readSkill(directory)
		if !ok || seen[name] {
			return
		}
		seen[name] = true
		result = append(result, localSkill{Name: name, Path: directory})
	}
	add(root)
	if len(result) > 0 && !fullDepth {
		return result, nil
	}
	maxDepth := 3 // root/skills/<category>/<skill> is the baseline local catalog layout.
	if fullDepth {
		maxDepth = 5
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() || path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		normalized := filepath.ToSlash(relative)
		depth := len(strings.Split(normalized, "/"))
		if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "dist" || entry.Name() == "build" || entry.Name() == "__pycache__" || depth > maxDepth {
			return filepath.SkipDir
		}
		// Without --full-depth only conventional skills catalogs get a second
		// nested level; unrelated root folders remain shallow.
		if !fullDepth && depth > 1 && !strings.HasPrefix(normalized, "skills/") {
			return filepath.SkipDir
		}
		add(path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func readSkill(directory string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(directory, "SKILL.md"))
	if err != nil {
		return "", false
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", false
	}
	name, description := "", ""
	for _, line := range lines[1:] {
		if line == "---" {
			break
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	if name == "" || description == "" {
		return "", false
	}
	return name, true
}

func selectLocalSkills(invocation Invocation, skills []localSkill, options addOptions) ([]localSkill, error) {
	if len(options.Skills) > 0 {
		if len(options.Skills) == 1 && options.Skills[0] == "*" {
			return skills, nil
		}
		requested := make(map[string]bool)
		for _, name := range options.Skills {
			requested[strings.ToLower(name)] = true
		}
		selected := []localSkill{}
		for _, skill := range skills {
			if requested[strings.ToLower(skill.Name)] {
				selected = append(selected, skill)
				delete(requested, strings.ToLower(skill.Name))
			}
		}
		if len(requested) > 0 {
			missing := make([]string, 0, len(requested))
			for name := range requested {
				missing = append(missing, name)
			}
			sort.Strings(missing)
			return nil, fmt.Errorf("No matching skills found for: %s", strings.Join(missing, ", "))
		}
		return selected, nil
	}
	if len(skills) == 1 || options.Yes {
		return skills, nil
	}
	_, _ = fmt.Fprintf(invocation.Stdout, "Select skills to install (%s, or * for all): ", skillNames(skills))
	line, err := bufio.NewReader(invocation.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("installation cancelled; select skills with --skill or use --yes")
	}
	return selectLocalSkills(invocation, skills, addOptions{Skills: strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' })})
}

func selectInstallAgents(options addOptions) ([]string, error) {
	if len(options.Agents) == 0 {
		return nil, nil // Canonical topology alone serves universal consumers.
	}
	valid := make(map[string]bool)
	for _, agent := range state.AgentIDs() {
		valid[agent] = true
	}
	if len(options.Agents) == 1 && options.Agents[0] == "*" {
		return state.AgentIDs(), nil
	}
	seen := make(map[string]bool)
	selected := []string{}
	for _, agent := range options.Agents {
		if !valid[agent] {
			return nil, fmt.Errorf("Invalid agents: %s", agent)
		}
		if !seen[agent] {
			seen[agent] = true
			selected = append(selected, agent)
		}
	}
	return selected, nil
}

func skillNames(skills []localSkill) string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return strings.Join(names, ", ")
}

func installLocalSkill(skill localSkill, source string, scope state.Scope, base, project, home string, agents []string, copyMode bool) error {
	canonical := filepath.Join(base, ".agents", "skills", state.SanitizeName(skill.Name))
	if !sameLocalPath(skill.Path, canonical) {
		if err := replaceDirectoryFromSource(skill.Path, canonical); err != nil {
			return err
		}
	}
	for _, agent := range agents {
		destination, universal, ok := state.AgentSkillsPath(agent, scope, project, home, os.Getenv("XDG_CONFIG_HOME"))
		if !ok {
			return fmt.Errorf("agent %q does not support this scope", agent)
		}
		if universal {
			continue
		}
		destination = filepath.Join(destination, state.SanitizeName(skill.Name))
		if sameLocalPath(canonical, destination) {
			continue
		}
		if copyMode {
			if err := replaceDirectoryFromSource(canonical, destination); err != nil {
				return err
			}
		} else if err := replaceWithSymlink(canonical, destination); err != nil {
			return err
		}
	}
	hash, owned, err := contentIdentity(canonical)
	if err != nil {
		return err
	}
	lockPath := filepath.Join(project, "skills-lock.json")
	version := 1
	if scope == state.Global {
		lockPath = filepath.Join(os.Getenv("XDG_STATE_HOME"), "skills", ".skill-lock.json")
		if os.Getenv("XDG_STATE_HOME") == "" {
			lockPath = filepath.Join(home, ".agents", ".skill-lock.json")
		}
		version = 3
	}
	document, err := state.Read(lockPath, version)
	if err != nil {
		return err
	}
	if err := document.RecordInstallation(skill.Name, state.InstallationRecord{
		Source: source, SourceURL: source, SourceType: "local", InstalledContentHash: hash, OwnedFiles: owned,
	}); err != nil {
		return err
	}
	return document.Write(lockPath)
}

func replaceDirectoryFromSource(source, destination string) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	return copyDirectory(source, destination)
}

func copyDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == ".git" || strings.HasPrefix(filepath.ToSlash(relative), ".git/") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported non-regular source file: %s", path)
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

func replaceWithSymlink(source, destination string) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	relative, err := filepath.Rel(filepath.Dir(destination), source)
	if err != nil {
		return err
	}
	return os.Symlink(relative, destination)
}

func contentIdentity(directory string) (string, []string, error) {
	files := []string{}
	contents := make(map[string][]byte)
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported installed file: %s", path)
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, relative)
		contents[relative] = data
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, file := range files {
		_, _ = hash.Write([]byte(file))
		_, _ = hash.Write(contents[file])
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), files, nil
}

func sameLocalPath(left, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbsolute) == filepath.Clean(rightAbsolute)
}
