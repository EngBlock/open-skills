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
	Force                  bool
	Skills                 []string
	AllowInsecureTransport bool
	Limits                 resourceLimits
	ParseError             error
}

type updateResult struct {
	checked int
	updated int
	failed  int
	items   []updateJSONResult
	failure *automationJSONError
}

type updateJSONOutput struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Scope         updateScope          `json:"scope"`
	Results       []updateJSONResult   `json:"results"`
	Summary       updateJSONSummary    `json:"summary"`
	Error         *automationJSONError `json:"error,omitempty"`
}

type updateJSONResult struct {
	Name     string      `json:"name"`
	Scope    state.Scope `json:"scope"`
	Status   string      `json:"status"`
	Revision *string     `json:"revision,omitempty"`
}

type updateJSONSummary struct {
	Checked int `json:"checked"`
	Updated int `json:"updated"`
	Failed  int `json:"failed"`
}

type gitUpdateAction struct {
	name   string
	entry  state.LockEntry
	skill  localSkill
	remove bool
}

func updateResults(names []string, scope state.Scope, status string) []updateJSONResult {
	items := make([]updateJSONResult, 0, len(names))
	for _, name := range names {
		items = append(items, updateJSONResult{Name: name, Scope: scope, Status: status})
	}
	return items
}

func failedUpdateResult(names []string, scope state.Scope) updateResult {
	return updateResult{checked: len(names), failed: len(names), items: updateResults(names, scope, "failed")}
}

func setUpdateStatus(items []updateJSONResult, name, status, revision string) {
	for index := range items {
		if items[index].Name != name {
			continue
		}
		items[index].Status = status
		if revision != "" {
			value := revision
			items[index].Revision = &value
		} else {
			items[index].Revision = nil
		}
		return
	}
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
	interactive := !invocation.JSON && interactiveInput(invocation.Stdin)
	invocation.Stdin = bufio.NewReader(invocation.Stdin)
	options := parseUpdateOptions(arguments)
	if options.ParseError != nil {
		recordAutomationError(invocation, options.ParseError, "invalid_arguments")
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

	result := updateResult{items: []updateJSONResult{}}
	if scope == updateGlobal || scope == updateBoth {
		if scope == updateBoth {
			_, _ = fmt.Fprintln(invocation.Stdout, "Global skills")
		}
		current := updateScopeSkills(invocation, options, apply, state.Global, project, home)
		result.checked += current.checked
		result.updated += current.updated
		result.failed += current.failed
		result.items = append(result.items, current.items...)
		if result.failure == nil {
			result.failure = current.failure
		}
	}
	if scope == updateProject || scope == updateBoth {
		if scope == updateBoth {
			_, _ = fmt.Fprintln(invocation.Stdout, "Project skills")
		}
		current := updateScopeSkills(invocation, options, apply, state.Project, project, home)
		result.checked += current.checked
		result.updated += current.updated
		result.failed += current.failed
		result.items = append(result.items, current.items...)
		if result.failure == nil {
			result.failure = current.failure
		}
	}
	if len(options.Skills) > 0 && result.checked == 0 {
		_, _ = fmt.Fprintf(invocation.Stdout, "No installed skills found matching: %s\n", strings.Join(options.Skills, ", "))
	}
	if apply && result.updated > 0 {
		_, _ = fmt.Fprintf(invocation.Stdout, "Updated %d skill(s)\n", result.updated)
	}
	sort.Slice(result.items, func(i, j int) bool {
		if result.items[i].Scope != result.items[j].Scope {
			return result.items[i].Scope < result.items[j].Scope
		}
		return result.items[i].Name < result.items[j].Name
	})
	if invocation.JSON {
		if result.failure != nil && result.checked == 0 && len(result.items) == 0 {
			recordAutomationFailure(invocation, result.failure.Code, result.failure.Message, result.failure.Path)
		} else {
			output := updateJSONOutput{
				SchemaVersion: automationSchemaVersion, Scope: scope, Results: result.items,
				Summary: updateJSONSummary{Checked: result.checked, Updated: result.updated, Failed: result.failed},
			}
			if result.failed > 0 {
				output.Error = &automationJSONError{Code: "partial_failure", Message: fmt.Sprintf("Failed to check or update %d skill(s)", result.failed)}
			}
			recordAutomationSuccess(invocation, output)
		}
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
		case "-f", "--force":
			options.Force = true
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
	lockPath, _ := installationLockLocation(scope, project, home)
	mode := advisoryLockShared
	if apply {
		mode = advisoryLockExclusive
	}
	result := updateResult{failed: 1}
	err := withStateAndInstallationLocks(invocation, lockPath, removalSkillDirectories(scope, project, home), mode, func() error {
		result = updateScopeSkillsLocked(invocation, options, apply, scope, project, home)
		return nil
	})
	if err != nil {
		message := fmt.Sprintf("Acquire advisory locks: %v", err)
		_, _ = fmt.Fprintln(invocation.Stderr, message)
		failure := automationJSONError{Code: "operation_failed", Message: sanitizeHuman(message)}
		return updateResult{failed: 1, failure: &failure}
	}
	return result
}

func updateScopeSkillsLocked(invocation Invocation, options updateOptions, apply bool, scope state.Scope, project, home string) updateResult {
	snapshot, err := state.Inspect(state.InspectOptions{
		Scope: scope, Project: project, Home: home, XDGStateHome: os.Getenv("XDG_STATE_HOME"), XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
	})
	if err != nil {
		message := fmt.Sprintf("Inspect %s skills: %v", scope, err)
		_, _ = fmt.Fprintln(invocation.Stderr, message)
		failure := stateAutomationError(err, message)
		return updateResult{failed: 1, failure: &failure}
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
			result.items = append(result.items, current.items...)
			if result.failure == nil {
				result.failure = current.failure
			}
			continue
		}
		current := updateGitSource(invocation, snapshot, names, source, options, apply, scope, project, home)
		result.checked += current.checked
		result.updated += current.updated
		result.failed += current.failed
		result.items = append(result.items, current.items...)
		if result.failure == nil {
			result.failure = current.failure
		}
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
		return failedUpdateResult(names, scope)
	}
	workspace, err := materializeUpdateSource(git, options.Limits, gitAcquisitionPolicy{AllowInsecureTransport: options.AllowInsecureTransport, Notice: invocation.Stderr})
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", displaySource, err)
		return failedUpdateResult(names, scope)
	}
	defer func() { _ = workspace.remove() }()

	discovered, err := discoverLocalSkillsWithLimits(workspace.Root, true, options.Limits)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Discover skills from %s: %v\n", displaySource, err)
		return failedUpdateResult(names, scope)
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
		return failedUpdateResult(names, scope)
	}
	budget := newResourceBudget(options.Limits)
	for _, skill := range selected {
		content, contentErr := prepareSkillContentWithBudget(skill.Path, budget)
		if contentErr == nil {
			contentErr = content.rejectLFSPointers()
		}
		if contentErr != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", skill.Name, contentErr)
			return failedUpdateResult(names, scope)
		}
		skill.content = content
		byPath[filepath.ToSlash(filepath.Join(skill.RelativePath, "SKILL.md"))] = skill
	}

	result := updateResult{checked: len(names), items: updateResults(names, scope, "unchanged")}
	for index := range result.items {
		value := workspace.Commit
		result.items[index].Revision = &value
	}
	actions := []gitUpdateAction{}
	for _, name := range names {
		entry := snapshot.Lock.Skills[name]
		skill, found := byPath[entry.SkillPath]
		if !found {
			setUpdateStatus(result.items, name, "missing_upstream", workspace.Commit)
			remove := apply && !options.Yes && !invocation.JSON && promptDeleteMissing(invocation, name, displaySource)
			if !remove {
				_, _ = fmt.Fprintf(invocation.Stdout, "Missing upstream skill: %s\n", name)
			} else {
				actions = append(actions, gitUpdateAction{name: name, entry: entry, remove: true})
			}
			continue
		}
		changed, err := checkedSkillChanged(entry, scope, workspace, skill)
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", name, err)
			result.failed++
			setUpdateStatus(result.items, name, "failed", workspace.Commit)
			continue
		}
		if !changed {
			continue
		}
		setUpdateStatus(result.items, name, "update_available", workspace.Commit)
		_, _ = fmt.Fprintf(invocation.Stdout, "Update available: %s (%s)\n", name, workspace.Commit)
		if apply {
			actions = append(actions, gitUpdateAction{name: name, entry: entry, skill: skill})
		}
	}
	if !apply || len(actions) == 0 {
		return result
	}
	localChanges := []localChange{}
	for _, action := range actions {
		changes, err := updateActionLocalChanges(action, snapshot, scope, project, home)
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Inspect installed %s: %v\n", action.name, err)
			result.failed += len(actions)
			for _, pending := range actions {
				setUpdateStatus(result.items, pending.name, "failed", workspace.Commit)
			}
			return result
		}
		localChanges = append(localChanges, changes...)
	}
	if err := authorizeLocalChanges(invocation, localChanges, options.Force); err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		result.failed += len(actions)
		for _, action := range actions {
			setUpdateStatus(result.items, action.name, "failed", workspace.Commit)
		}
		return result
	}
	updates := make([]updatedSkillInstallation, 0, len(actions))
	removals := make([]skillRemovalPlan, 0, len(actions))
	for _, action := range actions {
		if action.remove {
			plan, err := planSkillRemoval(action.name, snapshot.Lock, scope, project, home, action.entry.Agents, len(action.entry.Agents) == 0)
			if err != nil {
				_, _ = fmt.Fprintf(invocation.Stderr, "Plan removal of missing %s: %v\n", action.name, err)
				result.failed += len(actions)
				for _, pending := range actions {
					setUpdateStatus(result.items, pending.name, "failed", workspace.Commit)
				}
				return result
			}
			removals = append(removals, plan)
			continue
		}
		agents := append([]string(nil), action.entry.Agents...)
		if len(agents) == 0 {
			agents = installedAgents(snapshot, action.skill.Name)
		}
		updates = append(updates, updatedSkillInstallation{
			skill:      action.skill,
			provenance: installationProvenance{Identity: git.Identity, URL: git.URL, Type: action.entry.SourceType, Ref: workspace.Commit, Workspace: &workspace},
			agents:     agents, copyMode: copiedPlacementExists(action.skill.Name, agents, scope, project, home), subagents: action.entry.Subagents,
		})
	}
	if err := installUpdatedSkills(updates, removals, scope, project, home); err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Update %s: %v\n", displaySource, err)
		result.failed += len(actions)
		for _, action := range actions {
			setUpdateStatus(result.items, action.name, "failed", workspace.Commit)
		}
	} else {
		result.updated += len(updates)
		for _, action := range actions {
			status := "updated"
			if action.remove {
				status = "removed"
			}
			setUpdateStatus(result.items, action.name, status, workspace.Commit)
		}
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

func updateActionLocalChanges(action gitUpdateAction, snapshot state.Snapshot, scope state.Scope, project, home string) ([]localChange, error) {
	agents := append([]string(nil), action.entry.Agents...)
	if len(agents) == 0 {
		agents = installedAgents(snapshot, action.name)
	}
	if action.remove {
		return removalLocalChanges(action.name, &action.entry, scope, project, home, agents, len(agents) == 0)
	}
	copyMode := copiedPlacementExists(action.name, agents, scope, project, home)
	return installationLocalChanges(action.name, &action.entry, scope, project, home, agents, copyMode, action.entry.Subagents, "")
}

type updatedSkillInstallation struct {
	skill      localSkill
	provenance installationProvenance
	agents     []string
	copyMode   bool
	subagents  []string
}

func installUpdatedSkills(updates []updatedSkillInstallation, removals []skillRemovalPlan, scope state.Scope, project, home string) error {
	base := scopeBase(scope, project, home)
	lockPath, _ := installationLockLocation(scope, project, home)
	destinations := []string{}
	for _, removal := range removals {
		destinations = append(destinations, removal.paths...)
	}
	for _, update := range updates {
		paths, err := replacementPathsForSkills([]localSkill{update.skill}, scope, base, project, home, update.agents, update.subagents, update.copyMode)
		if err != nil {
			return err
		}
		destinations = append(destinations, paths...)
	}
	return withInstallationTransaction(lockPath, destinations, func(transaction *installationTransaction) error {
		if len(removals) > 0 {
			_, version := installationLockLocation(scope, project, home)
			document, err := transaction.readState(lockPath, version)
			if err != nil {
				return err
			}
			for _, removal := range removals {
				if err := applySkillRemoval(removal, document, transaction); err != nil {
					return fmt.Errorf("remove %s: %w", removal.name, err)
				}
			}
			if err := transaction.writeState(document, lockPath); err != nil {
				return err
			}
		}
		for _, update := range updates {
			if err := installLocalSkillTransaction(update.skill, update.provenance, scope, base, project, home, update.agents, update.copyMode, update.subagents, transaction); err != nil {
				return fmt.Errorf("install %s: %w", update.skill.Name, err)
			}
		}
		return nil
	})
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
		if agent == "eve" {
			continue
		}
		path, shared, supported := state.AgentSkillsPath(agent, scope, project, home, os.Getenv("XDG_CONFIG_HOME"))
		if !supported || shared {
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

func updateWellKnownSource(invocation Invocation, snapshot state.Snapshot, names []string, source string, options updateOptions, apply bool, scope state.Scope, project, home string) updateResult {
	displaySource := credentialFreeSource(source)
	provider, matches, err := parseWellKnownSource(source)
	if err != nil || !matches {
		if err == nil {
			err = fmt.Errorf("not a well-known source")
		}
		_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", displaySource, err)
		return failedUpdateResult(names, scope)
	}
	fetched, err := fetchWellKnownSkills(provider, options.Limits, names, newHTTPAcquisitionPolicy(options.AllowInsecureTransport, invocation.Stderr))
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", displaySource, err)
		return failedUpdateResult(names, scope)
	}
	root, urls, err := materializeWellKnownSkills(fetched)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Materialize %s: %v\n", displaySource, err)
		return failedUpdateResult(names, scope)
	}
	defer os.RemoveAll(root)
	discovered, err := discoverLocalSkillsWithLimits(root, true, options.Limits)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Discover skills from %s: %v\n", displaySource, err)
		return failedUpdateResult(names, scope)
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
		return failedUpdateResult(names, scope)
	}
	budget := newResourceBudget(options.Limits)
	for _, skill := range selected {
		content, contentErr := prepareSkillContentWithBudget(skill.Path, budget)
		if contentErr != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Check %s: %v\n", skill.Name, contentErr)
			return failedUpdateResult(names, scope)
		}
		skill.content = content
		byName[skill.Name] = skill
	}
	result := updateResult{checked: len(names), items: updateResults(names, scope, "unchanged")}
	actions := []gitUpdateAction{}
	for _, name := range names {
		entry := snapshot.Lock.Skills[name]
		skill, found := byName[name]
		if !found {
			setUpdateStatus(result.items, name, "missing_upstream", "")
			_, _ = fmt.Fprintf(invocation.Stdout, "Missing upstream skill: %s\n", name)
			continue
		}
		hash, _, hashErr := contentIdentity(skill.Path)
		if hashErr != nil {
			result.failed++
			setUpdateStatus(result.items, name, "failed", "")
			continue
		}
		current := entry.ComputedHash
		if scope == state.Global {
			current = entry.SkillFolderHash
		}
		if hash == current {
			continue
		}
		setUpdateStatus(result.items, name, "update_available", "")
		_, _ = fmt.Fprintf(invocation.Stdout, "Update available: %s\n", name)
		if apply {
			actions = append(actions, gitUpdateAction{name: name, entry: entry, skill: skill})
		}
	}
	if !apply || len(actions) == 0 {
		return result
	}
	localChanges := []localChange{}
	for _, action := range actions {
		changes, err := updateActionLocalChanges(action, snapshot, scope, project, home)
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Inspect installed %s: %v\n", action.name, err)
			result.failed += len(actions)
			for _, pending := range actions {
				setUpdateStatus(result.items, pending.name, "failed", "")
			}
			return result
		}
		localChanges = append(localChanges, changes...)
	}
	if err := authorizeLocalChanges(invocation, localChanges, options.Force); err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		result.failed += len(actions)
		for _, action := range actions {
			setUpdateStatus(result.items, action.name, "failed", "")
		}
		return result
	}
	updates := make([]updatedSkillInstallation, 0, len(actions))
	for _, action := range actions {
		agents := append([]string(nil), action.entry.Agents...)
		if len(agents) == 0 {
			agents = installedAgents(snapshot, action.skill.Name)
		}
		updates = append(updates, updatedSkillInstallation{
			skill:      action.skill,
			provenance: installationProvenance{Identity: provider.identity, URL: credentialFreeSource(urls[action.name]), Type: action.entry.SourceType},
			agents:     agents, copyMode: copiedPlacementExists(action.skill.Name, agents, scope, project, home), subagents: action.entry.Subagents,
		})
	}
	if err := installUpdatedSkills(updates, nil, scope, project, home); err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Update %s: %v\n", displaySource, err)
		result.failed += len(updates)
		for _, action := range actions {
			setUpdateStatus(result.items, action.name, "failed", "")
		}
	} else {
		result.updated += len(updates)
		for _, action := range actions {
			setUpdateStatus(result.items, action.name, "updated", "")
		}
	}
	return result
}
