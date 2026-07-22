package application

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EngBlock/open-skills/internal/state"
)

type syncOptions struct {
	Agents []string
	Yes    bool
	Force  bool
}

type nodeModuleSkill struct {
	localSkill
	Package string
}

func runSync(invocation Invocation, arguments []string) int {
	invocation.Stdin = bufio.NewReader(invocation.Stdin)
	options, err := parseSyncOptions(arguments)
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}
	project, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Determine project directory: %v\n", err)
		return 1
	}
	skills, err := discoverNodeModuleSkills(filepath.Join(project, "node_modules"))
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Scan node_modules for skills: %v\n", err)
		return 1
	}
	if len(skills) == 0 {
		_, _ = fmt.Fprintln(invocation.Stdout, "No skills found")
		return 0
	}
	lock, err := state.Read(filepath.Join(project, "skills-lock.json"), 1)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Read skills-lock.json: %v\n", err)
		return 1
	}
	toInstall := make([]nodeModuleSkill, 0, len(skills))
	for _, skill := range skills {
		hash, _, err := contentIdentity(skill.Path)
		if err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Hash %s: %v\n", skill.Name, err)
			return 1
		}
		if !options.Force && lock.Entry(skill.Name) != nil && lock.Entry(skill.Name).ComputedHash == hash {
			_, _ = fmt.Fprintf(invocation.Stdout, "%s already up to date\n", skill.Name)
			continue
		}
		toInstall = append(toInstall, skill)
	}
	if len(toInstall) == 0 {
		return 0
	}
	agents, err := selectSyncAgents(invocation, options, project)
	if err != nil {
		_, _ = fmt.Fprintln(invocation.Stderr, err)
		return 1
	}
	for _, skill := range toInstall {
		if err := installRecordedSkill(skill.localSkill, skill.Package, "node_modules", project, agents, nil); err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Sync %s: %v\n", skill.Name, err)
			return 1
		}
		_, _ = fmt.Fprintf(invocation.Stdout, "Synced %s from %s\n", skill.Name, skill.Package)
	}
	return 0
}

func parseSyncOptions(arguments []string) (syncOptions, error) {
	options := syncOptions{}
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "-y", "--yes":
			options.Yes = true
		case "-f", "--force":
			options.Force = true
		case "-a", "--agent":
			values, next := optionValues(arguments, index)
			if len(values) == 0 {
				return options, fmt.Errorf("%s requires at least one agent", arguments[index])
			}
			options.Agents = append(options.Agents, values...)
			index = next
		default:
			return options, fmt.Errorf("Unknown option: %s", arguments[index])
		}
	}
	return options, nil
}

func selectSyncAgents(invocation Invocation, options syncOptions, project string) ([]string, error) {
	if len(options.Agents) > 0 {
		return selectInstallAgents(invocation, addOptions{Agents: options.Agents}, state.Project, project, "")
	}
	detected := state.DetectedAgentIDs(state.InspectOptions{
		Scope: state.Project, Project: project, Home: "", XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
	})
	if options.Yes {
		if len(detected) == 0 {
			return state.UniversalAgentIDs(state.Project), nil
		}
		return uniqueAgents(append(detected, state.UniversalAgentIDs(state.Project)...)), nil
	}
	if len(detected) == 1 {
		return uniqueAgents(append(detected, state.UniversalAgentIDs(state.Project)...)), nil
	}
	return selectInstallAgents(invocation, addOptions{}, state.Project, project, "")
}

func uniqueAgents(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

// discoverNodeModuleSkills recognizes the npm 0.1.2 package layouts: a root
// SKILL.md, skills/<name>/SKILL.md, and .agents/skills/<name>/SKILL.md. It
// deliberately never runs package code or resolves package metadata.
func discoverNodeModuleSkills(nodeModules string) ([]nodeModuleSkill, error) {
	packages, err := nodeModulePackages(nodeModules)
	if os.IsNotExist(err) {
		return []nodeModuleSkill{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := []nodeModuleSkill{}
	seen := make(map[string]bool)
	for _, packageDir := range packages {
		for _, skill := range discoverPackageSkills(packageDir.path) {
			if seen[skill.Name] {
				continue
			}
			seen[skill.Name] = true
			result = append(result, nodeModuleSkill{localSkill: skill, Package: packageDir.name})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Package == result[j].Package {
			return result[i].Name < result[j].Name
		}
		return result[i].Package < result[j].Package
	})
	return result, nil
}

type packageDirectory struct {
	name string
	path string
}

func nodeModulePackages(nodeModules string) ([]packageDirectory, error) {
	entries, err := os.ReadDir(nodeModules)
	if err != nil {
		return nil, err
	}
	result := []packageDirectory{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path, ok := resolvedDirectory(filepath.Join(nodeModules, entry.Name()))
		if !ok {
			continue
		}
		if !strings.HasPrefix(entry.Name(), "@") {
			result = append(result, packageDirectory{name: entry.Name(), path: path})
			continue
		}
		scoped, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, child := range scoped {
			childPath, ok := resolvedDirectory(filepath.Join(path, child.Name()))
			if ok {
				result = append(result, packageDirectory{name: entry.Name() + "/" + child.Name(), path: childPath})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

func resolvedDirectory(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	return resolved, true
}

func discoverPackageSkills(packageDir string) []localSkill {
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
	add(packageDir)
	if len(result) > 0 {
		return result
	}
	for _, directory := range []string{packageDir, filepath.Join(packageDir, "skills"), filepath.Join(packageDir, ".agents", "skills")} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				add(filepath.Join(directory, entry.Name()))
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func runInstallFromLock(invocation Invocation) int {
	project, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Determine project directory: %v\n", err)
		return 1
	}
	lock, err := state.Read(filepath.Join(project, "skills-lock.json"), 1)
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Read skills-lock.json: %v\n", err)
		return 1
	}
	if len(lock.Skills) == 0 {
		_, _ = fmt.Fprintln(invocation.Stdout, "No project skills found in skills-lock.json")
		return 0
	}
	names := make([]string, 0, len(lock.Skills))
	for name := range lock.Skills {
		names = append(names, name)
	}
	sort.Strings(names)
	failed := false
	for _, name := range names {
		entry := lock.Skills[name]
		agents := entry.Agents
		if len(agents) == 0 {
			agents = state.UniversalAgentIDs(state.Project)
		}
		if len(entry.Subagents) > 0 && !contains(agents, "eve") {
			agents = append(agents, "eve")
		}
		skill, found := restoreSourceSkill(project, name, entry)
		if !found {
			_, _ = fmt.Fprintf(invocation.Stderr, "Cannot restore %s offline: its recorded source is unavailable locally\n", name)
			failed = true
			continue
		}
		if err := installRecordedSkill(skill, entry.Source, entry.SourceType, project, agents, entry.Subagents); err != nil {
			_, _ = fmt.Fprintf(invocation.Stderr, "Restore %s: %v\n", name, err)
			failed = true
			continue
		}
		_, _ = fmt.Fprintf(invocation.Stdout, "Restored %s\n", name)
	}
	if failed {
		return 1
	}
	return 0
}

func restoreSourceSkill(project, name string, entry state.LockEntry) (localSkill, bool) {
	if entry.SourceType == "node_modules" {
		if !safeNodeModuleName(entry.Source) {
			return localSkill{}, false
		}
		for _, skill := range discoverPackageSkills(filepath.Join(project, "node_modules", filepath.FromSlash(entry.Source))) {
			if skill.Name == name {
				return skill, true
			}
		}
	}
	for _, source := range []string{entry.Source, entry.SourceURL, filepath.Join(project, ".agents", "skills", state.SanitizeName(name))} {
		if source == "" {
			continue
		}
		if skill, ok := localSkillNamed(source, name); ok {
			return skill, true
		}
	}
	return localSkill{}, false
}

func safeNodeModuleName(name string) bool {
	if name == "" || filepath.IsAbs(name) {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func localSkillNamed(source, name string) (localSkill, bool) {
	if actual, ok := readSkill(source); ok && actual == name {
		return localSkill{Name: actual, Path: source}, true
	}
	skills, err := discoverLocalSkills(source, false)
	if err != nil {
		return localSkill{}, false
	}
	for _, skill := range skills {
		if skill.Name == name {
			return skill, true
		}
	}
	return localSkill{}, false
}

func installRecordedSkill(skill localSkill, source, sourceType, project string, agents, subagents []string) error {
	if err := installLocalSkill(skill, installationProvenance{Identity: source, URL: source, Type: sourceType}, state.Project, project, project, "", agents, false, subagents); err != nil {
		return err
	}
	hash, owned, err := contentIdentity(skill.Path)
	if err != nil {
		return err
	}
	lockPath := filepath.Join(project, "skills-lock.json")
	lock, err := state.Read(lockPath, 1)
	if err != nil {
		return err
	}
	actual := lock.Entry(skill.Name)
	if actual == nil {
		return fmt.Errorf("installation did not record %s", skill.Name)
	}
	return recordAndWrite(lock, lockPath, skill.Name, state.InstallationRecord{
		Source: source, SourceType: sourceType, InstalledContentHash: hash, OwnedFiles: owned,
		Agents: actual.Agents, Subagents: recordedEveTargets(actual.Agents, subagents),
	})
}

func recordAndWrite(lock *state.Document, path, name string, record state.InstallationRecord) error {
	if err := lock.RecordInstallation(name, record); err != nil {
		return err
	}
	return lock.Write(path)
}
