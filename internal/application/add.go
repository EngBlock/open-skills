package application

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/EngBlock/open-skills/internal/state"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type addOptions struct {
	Global     bool
	Agents     []string
	Skills     []string
	SkillPaths []string
	List       bool
	Yes        bool
	All        bool
	FullDepth  bool
	Copy       bool
	Subagents  []string
}

type localSkill struct {
	Name         string
	Path         string
	RelativePath string
}

func materializeWellKnownSkills(fetched []wellKnownFetchedSkill) (string, map[string]string, error) {
	if len(fetched) == 0 {
		return "", nil, fmt.Errorf("no skills to materialize")
	}
	root, err := os.MkdirTemp("", "open-skills-well-known-")
	if err != nil {
		return "", nil, err
	}
	fail := func(cause error) (string, map[string]string, error) {
		_ = os.RemoveAll(root)
		return "", nil, cause
	}
	sourceURLs := make(map[string]string, len(fetched))
	for _, skill := range fetched {
		if !validWellKnownSkillName(skill.Name) || len(skill.Files) == 0 || skill.SourceURL == "" {
			return fail(fmt.Errorf("invalid fetched well-known skill"))
		}
		directory := filepath.Join(root, skill.Name)
		for name, contents := range skill.Files {
			if !validWellKnownFilePath(name) {
				return fail(fmt.Errorf("unsafe fetched file path %q", name))
			}
			if strings.EqualFold(name, "SKILL.md") {
				name = "SKILL.md"
			}
			target := filepath.Join(directory, filepath.FromSlash(name))
			relative, err := filepath.Rel(directory, target)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return fail(fmt.Errorf("fetched file path escapes skill directory: %q", name))
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fail(err)
			}
			if err := os.WriteFile(target, contents, 0o644); err != nil {
				return fail(err)
			}
		}
		name, ok := readSkill(directory)
		if !ok {
			return fail(fmt.Errorf("well-known skill %q has no valid SKILL.md", skill.Name))
		}
		if _, duplicate := sourceURLs[name]; duplicate {
			return fail(fmt.Errorf("duplicate well-known skill name %q", name))
		}
		sourceURLs[name] = skill.SourceURL
	}
	return root, sourceURLs, nil
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
	repositoryRoot := absoluteSource
	provenance := installationProvenance{Identity: source, URL: source, Type: "local"}
	var workspace gitWorkspace
	isGit := false
	wellKnownURLs := map[string]string{}
	if info, statErr := os.Stat(absoluteSource); statErr == nil && info.IsDir() {
		// Existing directories always retain local-source behavior, even when a
		// directory happens to resemble an owner/repository shorthand.
	} else if isClearlyLocalPath(source) {
		_, _ = fmt.Fprintf(invocation.Stderr, "Local path does not exist or is not a directory: %s\n", source)
		return 1
	} else if wellKnown, matches, parseErr := parseWellKnownSource(source); matches {
		if parseErr != nil {
			_, _ = fmt.Fprintln(invocation.Stderr, parseErr)
			return 1
		}
		fetched, fetchErr := fetchWellKnownSkills(wellKnown)
		if fetchErr != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Discover well-known skills: %v\n", fetchErr)
			return 1
		}
		absoluteSource, wellKnownURLs, err = materializeWellKnownSkills(fetched)
		repositoryRoot = absoluteSource
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Materialize well-known skills: %v\n", err)
			return 1
		}
		defer os.RemoveAll(absoluteSource)
		provenance = installationProvenance{Identity: wellKnown.identity, Type: "well-known"}
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
		repositoryRoot = workspace.Root
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

	if len(options.Skills) > 0 && len(options.SkillPaths) > 0 {
		_, _ = fmt.Fprintln(invocation.Stderr, "Provide either --skill or --skill-path, not both")
		return 1
	}
	skills, err := discoverLocalSkills(absoluteSource, options.FullDepth)
	if err == nil {
		err = assignRepositoryRelativePaths(skills, repositoryRoot)
	}
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Discover skills: %v\n", err)
		return 1
	}
	if len(skills) == 0 {
		_, _ = fmt.Fprintln(invocation.Stderr, "No valid skills found. Skills require a SKILL.md with name and description.")
		return 1
	}
	if options.List {
		colliding := collidingSkillPaths(skills)
		for _, skill := range skills {
			if colliding[normalizedSkillName(skill.Name)] {
				_, _ = fmt.Fprintf(invocation.Stdout, "%s\t%s\n", displaySkillName(skill.Name), displaySkillPath(skill.RelativePath))
			} else {
				_, _ = fmt.Fprintln(invocation.Stdout, displaySkillName(skill.Name))
			}
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
		skillProvenance := provenance
		if sourceURL, ok := wellKnownURLs[skill.Name]; ok {
			skillProvenance.URL = sourceURL
		}
		if err := installLocalSkill(skill, skillProvenance, scope, base, project, home, agents, options.Copy, options.Subagents); err != nil {
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
		case "--skill-path":
			values, next := optionValues(arguments, index)
			if len(values) == 0 {
				return "", options, fmt.Errorf("%s requires at least one repository-relative skill path", argument)
			}
			options.SkillPaths = append(options.SkillPaths, values...)
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
	if len(options.Skills) > 0 && len(options.SkillPaths) > 0 {
		return "", options, fmt.Errorf("Provide either --skill or --skill-path, not both")
	}
	if options.All {
		if len(options.SkillPaths) > 0 {
			return "", options, fmt.Errorf("--all cannot be combined with --skill-path")
		}
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
	add := func(directory string) {
		name, ok := readSkill(directory)
		if !ok {
			return
		}
		relative, err := filepath.Rel(root, directory)
		if err != nil {
			return
		}
		result = append(result, localSkill{Name: name, Path: directory, RelativePath: filepath.ToSlash(relative)})
	}
	add(root)
	if len(result) > 0 && !fullDepth {
		return result, nil
	}
	maxDepth := 3 // root/skills/<category>/<skill> is the baseline local catalog layout.
	err := filepath.WalkDir(root, func(walkPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() || walkPath == root {
			return nil
		}
		relative, err := filepath.Rel(root, walkPath)
		if err != nil {
			return nil
		}
		normalized := filepath.ToSlash(relative)
		depth := len(strings.Split(normalized, "/"))
		if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "dist" || entry.Name() == "build" || entry.Name() == "__pycache__" || (!fullDepth && depth > maxDepth) {
			return filepath.SkipDir
		}
		// Without --full-depth only conventional skills catalogs get a second
		// nested level; unrelated root folders remain shallow.
		if !fullDepth && depth > 1 && !strings.HasPrefix(normalized, "skills/") {
			return filepath.SkipDir
		}
		add(walkPath)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].RelativePath < result[j].RelativePath
	})
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

func assignRepositoryRelativePaths(skills []localSkill, repositoryRoot string) error {
	for index := range skills {
		relative, err := filepath.Rel(repositoryRoot, skills[index].Path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("skill path escapes repository root: %s", skills[index].Path)
		}
		relative = filepath.ToSlash(relative)
		if strings.IndexFunc(relative, unicode.IsControl) >= 0 {
			return fmt.Errorf("repository-relative skill path contains control characters: %q", relative)
		}
		skills[index].RelativePath = relative
	}
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Name != skills[j].Name {
			return skills[i].Name < skills[j].Name
		}
		return skills[i].RelativePath < skills[j].RelativePath
	})
	return nil
}

func normalizedSkillName(name string) string {
	return state.SanitizeName(cases.Fold().String(norm.NFKC.String(name)))
}

func displaySkillName(name string) string {
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return strconv.Quote(name)
	}
	return name
}

func displaySkillPath(relativePath string) string {
	if strings.IndexFunc(relativePath, unicode.IsControl) >= 0 {
		return strconv.Quote(relativePath)
	}
	return relativePath
}

func skillCollisionGroups(skills []localSkill) map[string][]localSkill {
	groups := make(map[string][]localSkill)
	for _, skill := range skills {
		key := normalizedSkillName(skill.Name)
		groups[key] = append(groups[key], skill)
	}
	for key, group := range groups {
		if len(group) < 2 {
			delete(groups, key)
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].RelativePath < group[j].RelativePath })
		groups[key] = group
	}
	return groups
}

func collidingSkillPaths(skills []localSkill) map[string]bool {
	result := make(map[string]bool)
	for key := range skillCollisionGroups(skills) {
		result[key] = true
	}
	return result
}

func formatSkillAmbiguity(name string, matches []localSkill) error {
	paths := make([]string, 0, len(matches))
	for _, skill := range matches {
		paths = append(paths, "  - "+displaySkillPath(skill.RelativePath))
	}
	sort.Strings(paths)
	return fmt.Errorf("Skill name %q is ambiguous; it matches multiple repository-relative paths:\n%s\nSelect one with --skill-path <relative-path> or use that skill directory as the source.", name, strings.Join(paths, "\n"))
}

func normalizeSkillPathSelector(value string) (string, error) {
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("Skill path must not contain control characters: %q", value)
	}
	normalized := strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if normalized == "" || path.IsAbs(normalized) || strings.HasPrefix(normalized, "//") || len(normalized) >= 3 && normalized[1] == ':' && normalized[2] == '/' {
		return "", fmt.Errorf("Skill path must be a repository-relative path: %q", value)
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", fmt.Errorf("Skill path must not contain path traversal: %q", value)
		}
	}
	normalized = path.Clean(normalized)
	if normalized == "SKILL.md" {
		return ".", nil
	}
	if strings.HasSuffix(normalized, "/SKILL.md") {
		normalized = strings.TrimSuffix(normalized, "/SKILL.md")
	}
	return normalized, nil
}

func selectSkillsByPath(skills []localSkill, selectors []string) ([]localSkill, error) {
	byPath := make(map[string]localSkill, len(skills))
	for _, skill := range skills {
		byPath[skill.RelativePath] = skill
	}
	selected := make([]localSkill, 0, len(selectors))
	seen := make(map[string]bool, len(selectors))
	for _, selector := range selectors {
		normalized, err := normalizeSkillPathSelector(selector)
		if err != nil {
			return nil, err
		}
		skill, found := byPath[normalized]
		if !found {
			return nil, fmt.Errorf("No skill found at repository-relative skill path: %s", normalized)
		}
		if !seen[normalized] {
			selected = append(selected, skill)
			seen[normalized] = true
		}
	}
	return selected, nil
}

func ensureNoSelectedCollisions(skills []localSkill) error {
	groups := skillCollisionGroups(skills)
	if len(groups) == 0 {
		return nil
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sections := make([]string, 0, len(keys))
	for _, key := range keys {
		paths := make([]string, 0, len(groups[key]))
		for _, skill := range groups[key] {
			paths = append(paths, "    - "+displaySkillPath(skill.RelativePath))
		}
		sections = append(sections, fmt.Sprintf("  - %s:\n%s", key, strings.Join(paths, "\n")))
	}
	return fmt.Errorf("Normalized skill name collisions are ambiguous:\n%s\nSelect one candidate from each collision with --skill-path <relative-path> or use a skill directory as the source.", strings.Join(sections, "\n"))
}

func selectSkillNames(invocation Invocation, skills []localSkill, selectors []string) ([]localSkill, error) {
	byName := make(map[string][]localSkill)
	for _, skill := range skills {
		key := normalizedSkillName(skill.Name)
		byName[key] = append(byName[key], skill)
	}
	selected := []localSkill{}
	selectedPaths := make(map[string]bool)
	missing := []string{}
	for _, selector := range selectors {
		matches := byName[normalizedSkillName(selector)]
		if len(matches) == 0 {
			missing = append(missing, selector)
			continue
		}
		if len(matches) > 1 {
			if !invocation.Interactive {
				return nil, formatSkillAmbiguity(selector, matches)
			}
			paths := make([]string, 0, len(matches))
			for _, skill := range matches {
				paths = append(paths, displaySkillPath(skill.RelativePath))
			}
			sort.Strings(paths)
			_, _ = fmt.Fprintf(invocation.Stdout, "Skill %q is ambiguous. Select a repository-relative path (%s): ", selector, strings.Join(paths, ", "))
			line, err := readInputLine(invocation.Stdin)
			if err != nil && err != io.EOF {
				return nil, err
			}
			chosen, err := selectSkillsByPath(matches, []string{strings.TrimSpace(line)})
			if err != nil {
				return nil, err
			}
			matches = chosen
		}
		for _, skill := range matches {
			if !selectedPaths[skill.RelativePath] {
				selected = append(selected, skill)
				selectedPaths[skill.RelativePath] = true
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("No matching skills found for: %s", strings.Join(missing, ", "))
	}
	return selected, nil
}

func selectLocalSkills(invocation Invocation, skills []localSkill, options addOptions) ([]localSkill, error) {
	if len(options.SkillPaths) > 0 {
		selected, err := selectSkillsByPath(skills, options.SkillPaths)
		if err != nil {
			return nil, err
		}
		if err := ensureNoSelectedCollisions(selected); err != nil {
			return nil, err
		}
		return selected, nil
	}
	if len(options.Skills) > 0 {
		if len(options.Skills) == 1 && options.Skills[0] == "*" {
			if err := ensureNoSelectedCollisions(skills); err != nil {
				return nil, err
			}
			return skills, nil
		}
		return selectSkillNames(invocation, skills, options.Skills)
	}
	if len(skills) == 1 {
		return skills, nil
	}
	if options.Yes {
		if err := ensureNoSelectedCollisions(skills); err != nil {
			return nil, err
		}
		return skills, nil
	}
	labels := make([]string, 0, len(skills))
	colliding := collidingSkillPaths(skills)
	for _, skill := range skills {
		label := displaySkillName(skill.Name)
		if colliding[normalizedSkillName(skill.Name)] {
			label += " [" + displaySkillPath(skill.RelativePath) + "]"
		}
		labels = append(labels, label)
	}
	_, _ = fmt.Fprintf(invocation.Stdout, "Select skills to install (%s, or * for all): ", strings.Join(labels, ", "))
	line, err := readInputLine(invocation.Stdin)
	if err != nil && err != io.EOF {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("installation cancelled; select skills with --skill or --skill-path")
	}
	if line == "*" {
		return selectLocalSkills(invocation, skills, addOptions{Skills: []string{"*"}})
	}
	if byPath, pathErr := selectSkillsByPath(skills, []string{line}); pathErr == nil {
		return byPath, nil
	}
	selectors := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' })
	return selectSkillNames(invocation, skills, selectors)
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
	if provenance.Type == "well-known" && provenance.Ref == "" {
		provenance.Ref, err = remoteContentRevision(skill.Path)
		if err != nil {
			return err
		}
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
