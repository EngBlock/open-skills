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

type updateScope string

const (
	updateProject updateScope = "project"
	updateGlobal  updateScope = "global"
	updateBoth    updateScope = "both"
)

type updateOptions struct {
	Global                 bool
	Project                bool
	Yes                    bool
	Skills                 []string
	AllowInsecureTransport bool
	Limits                 resourceLimits
	ParseError             error
}

type updateResult struct {
	checked int
	updated int
	failed  int
}

// materializeUpdateSource is a seam for controlled branch-movement tests. A
// workspace contains an archived, resolved commit, so all installation after a
// check must use it rather than reacquiring the moving source.
var materializeUpdateSource = materializeGitSourceWithPolicy

func runCheck(invocation Invocation, arguments []string) int {
	return runUpdate(invocation, arguments, false)
}

func runUpgrade(invocation Invocation, arguments []string) int {
	return runUpdate(invocation, arguments, true)
}

func runUpdate(invocation Invocation, arguments []string, apply bool) int {
	interactive := interactiveInput(invocation.Stdin)
	invocation.Stdin = bufio.NewReader(invocation.Stdin)
	options := parseUpdateOptions(arguments)
	if options.ParseError != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, options.ParseError)
		return 1
	}
	scope, cancelled, err := resolveUpdateScope(invocation, options, interactive)
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}
	if cancelled {
		_, _ = fmt.Fprintln(invocation.Stdout, "Update cancelled.")
		return 0
	}
	if apply {
		_, _ = fmt.Fprintln(invocation.Stdout, "Checking for skill updates…")
	} else {
		_, _ = fmt.Fprintln(invocation.Stdout, "Checking installed skills…")
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

	result := updateResult{}
	if scope == updateGlobal || scope == updateBoth {
		if scope == updateBoth {
			_, _ = fmt.Fprintln(invocation.Stdout, "Global skills")
		}
		current := updateScopeSkills(invocation, options, apply, state.Global, project, home)
		result.checked += current.checked
		result.updated += current.updated
		result.failed += current.failed
	}
	if scope == updateProject || scope == updateBoth {
		if scope == updateBoth {
			_, _ = fmt.Fprintln(invocation.Stdout, "Project skills")
		}
		current := updateScopeSkills(invocation, options, apply, state.Project, project, home)
		result.checked += current.checked
		result.updated += current.updated
		result.failed += current.failed
	}
	if len(options.Skills) > 0 && result.checked == 0 {
		_, _ = fmt.Fprintf(invocation.Stdout, "No installed skills found matching: %s\n", strings.Join(options.Skills, ", "))
	}
	if apply && result.updated > 0 {
		_, _ = fmt.Fprintf(invocation.Stdout, "Updated %d skill(s)\n", result.updated)
	}
	if result.failed > 0 {
		_, _ = fmt.Fprintf(invocation.Stderr, "Failed to check or update %d skill(s)\n", result.failed)
		return 1
	}
	return 0
}

func parseUpdateOptions(arguments []string) updateOptions {
	options := updateOptions{Limits: defaultResourceLimits()}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch argument {
		case "-g", "--global":
			options.Global = true
		case "-p", "--project":
			options.Project = true
		case "-y", "--yes":
			options.Yes = true
		case "--allow-insecure-transport":
			options.AllowInsecureTransport = true
		default:
			matched, next, err := parseResourceLimitOption(arguments, index, &options.Limits)
			if err != nil {
				options.ParseError = err
				return options
			}
			if matched {
				index = next
				continue
			}
			if strings.HasPrefix(argument, "-") {
				options.ParseError = fmt.Errorf("Unknown option: %s", argument)
				return options
			}
			options.Skills = append(options.Skills, argument)
		}
	}
	return options
}

func resolveUpdateScope(invocation Invocation, options updateOptions, interactive bool) (updateScope, bool, error) {
	if len(options.Skills) > 0 {
		if options.Global {
			return updateGlobal, false, nil
		}
		if options.Project {
			return updateProject, false, nil
		}
		return updateBoth, false, nil
	}
	if options.Global && options.Project {
		return updateBoth, false, nil
	}
	if options.Global {
		return updateGlobal, false, nil
	}
	if options.Project {
		return updateProject, false, nil
	}
	if options.Yes || !interactive || runningInAgent() {
		if hasProjectSkills() {
			return updateProject, false, nil
		}
		return updateGlobal, false, nil
	}
	_, _ = fmt.Fprint(invocation.Stdout, "Update scope (project, global, or both): ")
	line, err := readInputLine(invocation.Stdin)
	if err != nil && err != io.EOF {
		return "", false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "project", "p":
		return updateProject, false, nil
	case "global", "g":
		return updateGlobal, false, nil
	case "both", "b":
		return updateBoth, false, nil
	case "":
		return "", true, nil
	default:
		return "", false, fmt.Errorf("invalid update scope %q", strings.TrimSpace(line))
	}
}

func interactiveInput(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func hasProjectSkills() bool {
	if _, err := os.Stat("skills-lock.json"); err == nil {
		return true
	}
	entries, err := os.ReadDir(filepath.Join(".agents", "skills"))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if _, err := os.Stat(filepath.Join(".agents", "skills", entry.Name(), "SKILL.md")); err == nil {
				return true
			}
		}
	}
	return false
}

func updateScopeSkills(invocation Invocation, options updateOptions, apply bool, scope state.Scope, project, home string) updateResult {
	snapshot, err := state.Inspect(state.InspectOptions{
		Scope: scope, Project: project, Home: home, XDGStateHome: os.Getenv("XDG_STATE_HOME"), XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
	})
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Inspect %s skills: %v\n", scope, err)
		return updateResult{failed: 1}
	}

	groups := map[string][]string{}
	for name, entry := range snapshot.Lock.Skills {
		if !matchesUpdateSkill(name, options.Skills) || !updateableSource(entry.SourceType) || (entry.SourceType != "well-known" && entry.SkillPath == "") {
			continue
		}
		source := updateSourceKey(entry)
		groups[entry.SourceType+"\x00"+source] = append(groups[entry.SourceType+"\x00"+source], name)
	}

	result := updateResult{}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		names := groups[key]
		sort.Strings(names)
		entry := snapshot.Lock.Skills[names[0]]
		source := updateSourceKey(entry)
		if entry.SourceType == "well-known" {
			current := updateWellKnownSource(invocation, snapshot, names, source, options, apply, scope, project, home)
			result.checked += current.checked
			result.updated += current.updated
			result.failed += current.failed
			continue
		}
		current := updateGitSource(invocation, snapshot, names, source, options, apply, scope, project, home)
		result.checked += current.checked
		result.updated += current.updated
		result.failed += current.failed
	}
	return result
}

func updateSourceKey(entry state.LockEntry) string {
	source := firstNonempty(entry.SourceURL, entry.Source)
	if entry.SourceType != "well-known" {
		return source
	}
	provider, matches, err := parseWellKnownSource(source)
	if err == nil && matches && provider.directName == "" {
		return provider.baseURL.String()
	}
	// Direct URLs need their direct-name fallback when an index is absent, so
	// do not collapse them to a provider base that loses this information.
	return source
}

func matchesUpdateSkill(name string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if strings.EqualFold(name, filter) {
			return true
		}
	}
	return false
}

func updateableSource(sourceType string) bool {
	switch sourceType {
	case "github", "git", "gitlab", "well-known":
		return true
	default:
		return false
	}
}

func updateGitSource(invocation Invocation, snapshot state.Snapshot, names []string, source string, options updateOptions, apply bool, scope state.Scope, project, home string) updateResult {
	displaySource := credentialFreeSource(source)
	git, err := parseGitSource(updateGitSourceInput(snapshot.Lock.Skills[names[0]], source))
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", displaySource, err)
		return updateResult{checked: len(names), failed: len(names)}
	}
	workspace, err := materializeUpdateSource(git, options.Limits, gitAcquisitionPolicy{AllowInsecureTransport: options.AllowInsecureTransport, Notice: invocation.Stderr})
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", displaySource, err)
		return updateResult{checked: len(names), failed: len(names)}
	}
	defer func() { _ = workspace.remove() }()

	discovered, err := discoverLocalSkillsWithLimits(workspace.Root, true, options.Limits)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Discover skills from %s: %v\n", displaySource, err)
		return updateResult{checked: len(names), failed: len(names)}
	}
	byPath := make(map[string]localSkill, len(discovered))
	for _, skill := range discovered {
		relative, relErr := filepath.Rel(workspace.Root, filepath.Join(skill.Path, "SKILL.md"))
		if relErr == nil {
			byPath[filepath.ToSlash(relative)] = skill
		}
	}
	selected := make([]localSkill, 0, len(names))
	for _, name := range names {
		if skill, found := byPath[snapshot.Lock.Skills[name].SkillPath]; found {
			selected = append(selected, skill)
		}
	}
	if err := ensureNoSelectedCollisions(selected); err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", displaySource, err)
		return updateResult{checked: len(names), failed: len(names)}
	}
	budget := newResourceBudget(options.Limits)
	for _, skill := range selected {
		content, contentErr := prepareSkillContentWithBudget(skill.Path, budget)
		if contentErr == nil {
			contentErr = content.rejectLFSPointers()
		}
		if contentErr != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", skill.Name, contentErr)
			return updateResult{checked: len(names), failed: len(names)}
		}
		skill.content = content
		byPath[filepath.ToSlash(filepath.Join(skill.RelativePath, "SKILL.md"))] = skill
	}

	result := updateResult{checked: len(names)}
	for _, name := range names {
		entry := snapshot.Lock.Skills[name]
		skill, found := byPath[entry.SkillPath]
		if !found {
			if apply && !options.Yes && promptDeleteMissing(invocation, name, displaySource) {
				if err := removeInstalledSkill(name, snapshot.Lock, scope, project, home); err != nil {
					_, _ = fmt.Fprintf(invocation.Stderr, "Remove missing %s: %v\n", name, err)
					result.failed++
				}
			} else {
				_, _ = fmt.Fprintf(invocation.Stdout, "Missing upstream skill: %s\n", name)
			}
			continue
		}
		changed, err := checkedSkillChanged(entry, scope, workspace, skill)
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", name, err)
			result.failed++
			continue
		}
		if !changed {
			continue
		}
		_, _ = fmt.Fprintf(invocation.Stdout, "Update available: %s (%s)\n", name, workspace.Commit)
		if !apply {
			continue
		}
		if err := refreshCheckedSkill(skill, entry, git, workspace, snapshot, scope, project, home); err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Update %s: %v\n", name, err)
			result.failed++
			continue
		}
		result.updated++
	}
	return result
}

// updateGitSourceInput reconstructs the Git transport form from the
// credential-redacted provenance stored in compatible locks. Git hosts use the
// conventional git SSH account; credentials themselves are never persisted.
func updateGitSourceInput(entry state.LockEntry, source string) string {
	if entry.SourceType != "git" {
		return source
	}
	if strings.HasPrefix(source, "ssh://") && !strings.HasPrefix(source, "ssh://git@") {
		return "ssh://git@" + strings.TrimPrefix(source, "ssh://")
	}
	if !strings.Contains(source, "://") && strings.Contains(source, ":") && !strings.Contains(source, "@") {
		return "git@" + source
	}
	return source
}

func checkedSkillChanged(entry state.LockEntry, scope state.Scope, workspace gitWorkspace, skill localSkill) (bool, error) {
	contentHash, _, err := contentIdentity(skill.Path)
	if err != nil {
		return false, err
	}
	if scope == state.Project {
		return contentHash != entry.ComputedHash, nil
	}
	if entry.SourceType == "github" {
		treeHash, err := gitTreeHash(workspace, skill.Path)
		if err != nil {
			return false, err
		}
		return treeHash != entry.SkillFolderHash, nil
	}
	return contentHash != entry.SkillFolderHash, nil
}

func refreshCheckedSkill(skill localSkill, entry state.LockEntry, source gitSource, workspace gitWorkspace, snapshot state.Snapshot, scope state.Scope, project, home string) error {
	agents := append([]string(nil), entry.Agents...)
	if len(agents) == 0 {
		agents = installedAgents(snapshot, skill.Name)
	}
	provenance := installationProvenance{Identity: source.Identity, URL: source.URL, Type: entry.SourceType, Ref: workspace.Commit, Workspace: &workspace}
	if len(agents) == 0 {
		// Older compatible locks did not record placements. Updating canonical
		// content is still useful and avoids inventing ownership of adapters.
		return installLocalSkill(skill, provenance, scope, scopeBase(scope, project, home), project, home, nil, false, nil)
	}
	copyMode := copiedPlacementExists(skill.Name, agents, scope, project, home)
	return installLocalSkill(skill, provenance, scope, scopeBase(scope, project, home), project, home, agents, copyMode, entry.Subagents)
}

func installedAgents(snapshot state.Snapshot, skillName string) []string {
	for _, skill := range snapshot.Skills {
		if skill.Name == skillName {
			return append([]string(nil), skill.Agents...)
		}
	}
	return nil
}

func scopeBase(scope state.Scope, project, home string) string {
	if scope == state.Global {
		return home
	}
	return project
}

func copiedPlacementExists(skillName string, agents []string, scope state.Scope, project, home string) bool {
	for _, agent := range agents {
		if agent == "eve" || contains(state.UniversalAgentIDs(scope), agent) {
			continue
		}
		path, _, supported := state.AgentSkillsPath(agent, scope, project, home, os.Getenv("XDG_CONFIG_HOME"))
		if !supported {
			continue
		}
		entry, err := os.Lstat(filepath.Join(path, state.SanitizeName(skillName)))
		if err == nil && entry.IsDir() && entry.Mode()&os.ModeSymlink == 0 {
			return true
		}
	}
	return false
}

func promptDeleteMissing(invocation Invocation, name, source string) bool {
	_, _ = fmt.Fprintf(invocation.Stdout, "%s is missing from %s. Remove its local copies? [y/N] ", name, source)
	line, err := readInputLine(invocation.Stdin)
	if err != nil && err != io.EOF {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func removeInstalledSkill(name string, lock *state.Document, scope state.Scope, project, home string) error {
	entry := lock.Entry(name)
	if entry == nil {
		return nil
	}
	return removeSkill(name, lock, scope, project, home, entry.Agents, len(entry.Agents) == 0)
}

func updateWellKnownSource(invocation Invocation, snapshot state.Snapshot, names []string, source string, options updateOptions, apply bool, scope state.Scope, project, home string) updateResult {
	displaySource := credentialFreeSource(source)
	provider, matches, err := parseWellKnownSource(source)
	if err != nil || !matches {
		if err == nil {
			err = fmt.Errorf("not a well-known source")
		}
		_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", displaySource, err)
		return updateResult{checked: len(names), failed: len(names)}
	}
	fetched, err := fetchWellKnownSkills(provider, options.Limits, names, newHTTPAcquisitionPolicy(options.AllowInsecureTransport, invocation.Stderr))
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", displaySource, err)
		return updateResult{checked: len(names), failed: len(names)}
	}
	root, urls, err := materializeWellKnownSkills(fetched)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Materialize %s: %v\n", displaySource, err)
		return updateResult{checked: len(names), failed: len(names)}
	}
	defer os.RemoveAll(root)
	discovered, err := discoverLocalSkillsWithLimits(root, true, options.Limits)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Discover skills from %s: %v\n", displaySource, err)
		return updateResult{checked: len(names), failed: len(names)}
	}
	byName := map[string]localSkill{}
	for _, skill := range discovered {
		byName[skill.Name] = skill
	}
	selected := make([]localSkill, 0, len(names))
	for _, name := range names {
		if skill, found := byName[name]; found {
			selected = append(selected, skill)
		}
	}
	if err := ensureNoSelectedCollisions(selected); err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", displaySource, err)
		return updateResult{checked: len(names), failed: len(names)}
	}
	budget := newResourceBudget(options.Limits)
	for _, skill := range selected {
		content, contentErr := prepareSkillContentWithBudget(skill.Path, budget)
		if contentErr != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", skill.Name, contentErr)
			return updateResult{checked: len(names), failed: len(names)}
		}
		skill.content = content
		byName[skill.Name] = skill
	}
	result := updateResult{checked: len(names)}
	for _, name := range names {
		entry := snapshot.Lock.Skills[name]
		skill, found := byName[name]
		if !found {
			_, _ = fmt.Fprintf(invocation.Stdout, "Missing upstream skill: %s\n", name)
			continue
		}
		hash, _, hashErr := contentIdentity(skill.Path)
		if hashErr != nil {
			result.failed++
			continue
		}
		current := entry.ComputedHash
		if scope == state.Global {
			current = entry.SkillFolderHash
		}
		if hash == current {
			continue
		}
		_, _ = fmt.Fprintf(invocation.Stdout, "Update available: %s\n", name)
		if !apply {
			continue
		}
		agents := entry.Agents
		if len(agents) == 0 {
			agents = installedAgents(snapshot, skill.Name)
		}
		if err := installLocalSkill(skill, installationProvenance{Identity: provider.identity, URL: credentialFreeSource(urls[name]), Type: entry.SourceType}, scope, scopeBase(scope, project, home), project, home, agents, copiedPlacementExists(skill.Name, agents, scope, project, home), entry.Subagents); err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Update %s: %v\n", name, err)
			result.failed++
			continue
		}
		result.updated++
	}
	return result
}
