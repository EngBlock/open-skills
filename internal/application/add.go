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
	"strconv"
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
	Subagents []string
}

type localSkill struct {
	Name string
	Path string
}

// runAdd owns source acquisition, discovery, selection, canonical installation
// topology, and compatible lock mutation. Git sources are materialized in an
// isolated temporary checkout, then use the exact same discovery and install
// path as local directories.
func runAdd(invocation Invocation, arguments []string) int {
	invocation.Stdin = bufio.NewReader(invocation.Stdin)
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
	provenance := installationProvenance{Identity: source, URL: source, Type: "local"}
	var workspace gitWorkspace
	isGit := false
	if info, statErr := os.Stat(absoluteSource); statErr == nil && info.IsDir() {
		// Existing directories always retain local-source behavior, even when a
		// directory happens to resemble an owner/repository shorthand.
	} else if isClearlyLocalPath(source) {
		_, _ = fmt.Fprintf(invocation.Stderr, "Local path does not exist or is not a directory: %s\n", source)
		return 1
	} else {
		git, parseErr := parseGitSource(source)
		if parseErr != nil {
			_, _ = fmt.Fprintln(invocation.Stderr, parseErr)
			return 1
		}
		workspace, err = materializeGitSource(git)
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Acquire Git source: %v\n", err)
			return 1
		}
		defer func() {
			if removeErr := workspace.remove(); removeErr != nil {
				_, _ = fmt.Fprintf(invocation.Stderr, "Remove Git workspace: %v\n", removeErr)
			}
		}()
		absoluteSource = workspace.Root
		if git.Subpath != "" {
			absoluteSource = filepath.Join(absoluteSource, filepath.FromSlash(git.Subpath))
			info, statErr := os.Stat(absoluteSource)
			if statErr != nil || !info.IsDir() {
				_, _ = fmt.Fprintf(invocation.Stderr, "Git source subpath does not exist or is not a directory: %s\n", git.Subpath)
				return 1
			}
		}
		if git.SkillFilter != "" {
			options.Skills = append(options.Skills, git.SkillFilter)
		}
		provenance = installationProvenance{Identity: git.Identity, URL: git.URL, Type: git.Type, Ref: workspace.Commit, Workspace: &workspace}
		isGit = true
	}

	skills, err := discoverLocalSkills(absoluteSource, options.FullDepth)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Discover skills: %v\n", err)
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
	if isGit {
		for _, skill := range selected {
			if err := rejectLFSPointers(skill.Path); err != nil {
				_, _ = fmt.Fprintf(invocation.Stderr, "Install %s: %v\n", skill.Name, err)
				return 1
			}
		}
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
	agents, err := selectInstallAgents(invocation, options, scope, project, home)
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}
	for _, skill := range selected {
		if err := installLocalSkill(skill, provenance, scope, base, project, home, agents, options.Copy, options.Subagents); err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Install %s: %v\n", skill.Name, err)
			return 1
		}
		_, _ = fmt.Fprintf(invocation.Stdout, "Installed %s\n", skill.Name)
	}
	return 0
}

// runAddLocal is retained for callers that exercised the original native seam.
func runAddLocal(invocation Invocation, arguments []string) int {
	return runAdd(invocation, arguments)
}

func isClearlyLocalPath(source string) bool {
	return source == "." || source == ".." || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || filepath.IsAbs(source)
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
		case "--subagent":
			values, next := optionValues(arguments, index)
			if len(values) == 0 {
				return "", options, fmt.Errorf("%s requires at least one subagent", argument)
			}
			options.Subagents = append(options.Subagents, values...)
			index = next
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
	line, err := readInputLine(invocation.Stdin)
	if err != nil && err != io.EOF {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("installation cancelled; select skills with --skill or use --yes")
	}
	return selectLocalSkills(invocation, skills, addOptions{Skills: strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' })})
}

func readInputLine(input io.Reader) (string, error) {
	if reader, ok := input.(*bufio.Reader); ok {
		return reader.ReadString('\n')
	}
	return bufio.NewReader(input).ReadString('\n')
}

func selectInstallAgents(invocation Invocation, options addOptions, scope state.Scope, project, home string) ([]string, error) {
	if scope == state.Global && len(options.Subagents) > 0 {
		return nil, fmt.Errorf("Eve subagents do not support global installation")
	}
	requested := append([]string(nil), options.Agents...)
	if len(options.Subagents) > 0 && !contains(requested, "eve") && !contains(requested, "*") {
		requested = append(requested, "eve")
	}
	if len(requested) == 0 {
		requested = state.DetectedAgentIDs(state.InspectOptions{
			Scope: scope, Project: project, Home: home, XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		})
		detectedEve := contains(requested, "eve")
		if len(requested) == 0 && options.Yes {
			requested = state.AgentIDs()
		}
		// An Eve project is a dedicated topology: npm selects Eve alone rather
		// than adding the shared universal directory alongside it.
		if detectedEve {
			requested = []string{"eve"}
		} else if len(requested) > 0 {
			// npm automatically includes all universal consumers when it detects agents.
			requested = append(requested, state.UniversalAgentIDs(scope)...)
		} else {
			_, _ = fmt.Fprint(invocation.Stdout, "Select agents to install (or * for all): ")
			line, err := readInputLine(invocation.Stdin)
			if err != nil && err != io.EOF {
				return nil, err
			}
			requested = strings.FieldsFunc(strings.TrimSpace(line), func(r rune) bool { return r == ',' || r == ' ' })
			if len(requested) == 0 {
				return nil, fmt.Errorf("installation cancelled; select agents with --agent or use --yes")
			}
		}
	}
	if contains(requested, "*") {
		requested = state.AgentIDs()
	}

	selected := make([]string, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	for _, agent := range requested {
		if !state.IsAgentID(agent) {
			return nil, fmt.Errorf("Invalid agents: %s", agent)
		}
		if !state.AgentSupportedInScope(agent, scope) {
			if contains(options.Agents, "*") || (len(options.Agents) == 0 && options.Yes) {
				continue
			}
			return nil, fmt.Errorf("agent %q does not support %s installation", agent, scope)
		}
		if !seen[agent] {
			seen[agent] = true
			selected = append(selected, agent)
		}
	}
	return selected, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func skillNames(skills []localSkill) string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return strings.Join(names, ", ")
}

type installationProvenance struct {
	Identity  string
	URL       string
	Type      string
	Ref       string
	Workspace *gitWorkspace
}

func installLocalSkill(skill localSkill, provenance installationProvenance, scope state.Scope, base, project, home string, agents []string, copyMode bool, subagents []string) error {
	canonical := filepath.Join(base, ".agents", "skills", state.SanitizeName(skill.Name))
	allEve := len(agents) > 0
	for _, agent := range agents {
		allEve = allEve && agent == "eve"
	}
	needsCanonical := (!copyMode && !allEve) || len(agents) == 0 || contains(agents, "universal")
	if needsCanonical && !pathsOverlap(skill.Path, canonical) {
		if err := replaceDirectoryFromSource(skill.Path, canonical); err != nil {
			return err
		}
	}
	installedAgents := []string{}
	for _, agent := range agents {
		if agent == "eve" {
			for _, subagent := range eveTargets(subagents) {
				destination := filepath.Join(state.EveSkillsPath(project, subagent), state.SanitizeName(skill.Name))
				if pathsOverlap(skill.Path, destination) {
					continue
				}
				if err := replaceEveDirectoryFromSource(skill.Path, destination); err != nil {
					return err
				}
			}
			installedAgents = append(installedAgents, agent)
			continue
		}
		destination, universal, ok := state.AgentSkillsPath(agent, scope, project, home, os.Getenv("XDG_CONFIG_HOME"))
		if !ok {
			return fmt.Errorf("agent %q does not support this scope", agent)
		}
		destination = filepath.Join(destination, state.SanitizeName(skill.Name))
		if universal {
			// Universal adapters share canonical content in both project and global scope.
			destination = canonical
			if !pathsOverlap(skill.Path, destination) {
				if err := replaceDirectoryFromSource(skill.Path, destination); err != nil {
					return err
				}
			}
			installedAgents = append(installedAgents, agent)
			continue
		}
		if pathsOverlap(skill.Path, destination) {
			installedAgents = append(installedAgents, agent)
			continue
		}
		if !copyMode && scope == state.Project && agent != "claude-code" && !state.ProjectAgentRootExists(agent, project) {
			continue
		}
		if copyMode {
			if err := replaceDirectoryFromSource(skill.Path, destination); err != nil {
				return err
			}
		} else if !sameLocalPath(canonical, destination) {
			if err := replaceWithSymlink(canonical, destination); err != nil {
				return err
			}
		}
		installedAgents = append(installedAgents, agent)
	}
	hash, owned, err := contentIdentity(skill.Path)
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
	folderHash := hash
	skillPath := ""
	if provenance.Workspace != nil {
		var err error
		if provenance.Type == "github" {
			folderHash, err = gitTreeHash(*provenance.Workspace, skill.Path)
			if err != nil {
				return err
			}
		}
		relative, err := filepath.Rel(provenance.Workspace.Root, filepath.Join(skill.Path, "SKILL.md"))
		if err != nil {
			return fmt.Errorf("determine Git skill path: %w", err)
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("Git skill path escapes workspace: %s", skill.Path)
		}
		skillPath = filepath.ToSlash(relative)
	}
	if err := document.RecordInstallation(skill.Name, state.InstallationRecord{
		Source: provenance.Identity, SourceURL: provenance.URL, SourceType: provenance.Type,
		Ref: provenance.Ref, SkillPath: skillPath, InstalledContentHash: hash, SkillFolderHash: folderHash, OwnedFiles: owned,
		Agents: installedAgents, Subagents: recordedEveTargets(installedAgents, subagents),
	}); err != nil {
		return err
	}
	return document.Write(lockPath)
}

func eveTargets(subagents []string) []string {
	if len(subagents) == 0 {
		return []string{""}
	}
	result := make([]string, 0, len(subagents))
	seen := make(map[string]bool, len(subagents))
	for _, subagent := range subagents {
		if subagent == "root" || subagent == "." {
			subagent = ""
		}
		if !seen[subagent] {
			seen[subagent] = true
			result = append(result, subagent)
		}
	}
	return result
}

func recordedEveTargets(agents, subagents []string) []string {
	if !contains(agents, "eve") {
		return nil
	}
	targets := eveTargets(subagents)
	if len(targets) == 1 && targets[0] == "" {
		return nil
	}
	return targets
}

func replaceEveDirectoryFromSource(source, destination string) error {
	if err := replaceDirectoryFromSource(source, destination); err != nil {
		return err
	}
	skillPath := filepath.Join(destination, "SKILL.md")
	contents, err := os.ReadFile(skillPath)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return nil
	}
	end := -1
	for index := 1; index < len(lines); index++ {
		if lines[index] == "---" {
			end = index
			break
		}
	}
	if end < 0 {
		return nil
	}
	allowed := []string{}
	metadata := []string{}
	for index := 1; index < end; index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "description", "license":
			allowed = append(allowed, key+": "+value)
		case "metadata":
			for index++; index < end && (strings.HasPrefix(lines[index], " ") || strings.HasPrefix(lines[index], "\t")); index++ {
				metadataKey, metadataValue, isField := strings.Cut(strings.TrimSpace(lines[index]), ":")
				if isField && isEveMetadataString(strings.TrimSpace(metadataValue)) {
					metadata = append(metadata, strings.TrimSpace(metadataKey)+": "+strings.TrimSpace(metadataValue))
				}
			}
			index--
		}
	}
	if len(metadata) > 0 {
		allowed = append(allowed, "metadata:")
		for _, entry := range metadata {
			allowed = append(allowed, "  "+entry)
		}
	}
	frontmatter := append([]string{"---"}, allowed...)
	frontmatter = append(frontmatter, "---")
	return os.WriteFile(skillPath, []byte(strings.Join(append(frontmatter, lines[end+1:]...), "\n")), 0o644)
}

func isEveMetadataString(value string) bool {
	if value == "" || strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") {
		return false
	}
	lower := strings.ToLower(value)
	if lower == "true" || lower == "false" || lower == "null" || lower == "~" {
		return false
	}
	if _, err := strconv.ParseFloat(strings.ReplaceAll(value, "_", ""), 64); err == nil {
		return false
	}
	return true
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
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
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

func pathsOverlap(left, right string) bool {
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	for _, pair := range [][2]string{{leftAbsolute, rightAbsolute}, {rightAbsolute, leftAbsolute}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))) {
			return true
		}
	}
	return false
}
