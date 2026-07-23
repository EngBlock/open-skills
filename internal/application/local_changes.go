package application

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/EngBlock/open-skills/internal/state"
)

type localChange struct {
	Skill  string
	Status string
	Path   string
}

type actualInstalledPath struct {
	kind       string
	hash       string
	executable bool
}

func captureInstalledPlacements(skillName string, scope state.Scope, project, home string, agents []string, copyMode bool, subagents []string, canonicalOwned bool) (map[string]state.InstalledPlacement, error) {
	kinds, err := installationPlacementKinds(scope, agents, copyMode, subagents)
	if err != nil {
		return nil, err
	}
	if !canonicalOwned {
		delete(kinds, "canonical")
	}
	result := make(map[string]state.InstalledPlacement, len(kinds))
	ids := sortedPlacementIDs(kinds)
	for _, id := range ids {
		kind := kinds[id]
		placement := state.InstalledPlacement{Kind: kind}
		if kind == "link" {
			placement.LinkTarget = "canonical"
		} else {
			path, err := logicalPlacementPath(id, skillName, scope, project, home)
			if err != nil {
				return nil, err
			}
			paths, err := snapshotInstalledDirectory(path)
			if err != nil {
				return nil, fmt.Errorf("record installed placement %s: %w", path, err)
			}
			placement.Paths = make(map[string]state.InstalledPathState, len(paths))
			for relative, current := range paths {
				placement.Paths[relative] = state.InstalledPathState{Kind: current.kind, Hash: current.hash, Executable: current.executable}
			}
		}
		result[id] = placement
	}
	return result, nil
}

func installationPlacementKinds(scope state.Scope, agents []string, copyMode bool, subagents []string) (map[string]string, error) {
	result := map[string]string{}
	allEve := len(agents) > 0
	for _, agent := range agents {
		allEve = allEve && agent == "eve"
	}
	if (!copyMode && !allEve) || len(agents) == 0 || contains(agents, "universal") {
		result["canonical"] = "canonical"
	}
	for _, agent := range agents {
		if agent == "eve" {
			for _, subagent := range eveTargets(subagents) {
				result[evePlacementID(subagent)] = "copy"
			}
			continue
		}
		_, universal, supported := state.AgentSkillsPath(agent, scope, "", "", "")
		if !supported {
			return nil, fmt.Errorf("agent %q does not support this scope", agent)
		}
		if universal {
			result["canonical"] = "canonical"
		} else if copyMode {
			result["agent:"+agent] = "copy"
		} else {
			result["canonical"] = "canonical"
			result["agent:"+agent] = "link"
		}
	}
	return result, nil
}

func installationLocalChanges(skillName string, entry *state.LockEntry, scope state.Scope, project, home string, agents []string, copyMode bool, subagents []string, sourcePath string) ([]localChange, error) {
	incoming, err := installationPlacementKinds(scope, agents, copyMode, subagents)
	if err != nil {
		return nil, err
	}
	if entry != nil && len(entry.InstalledPlacements) > 0 {
		for id := range entry.InstalledPlacements {
			if _, exists := incoming[id]; !exists {
				incoming[id] = entry.InstalledPlacements[id].Kind
			}
		}
		changes, err := exactPlacementChanges(skillName, *entry, incoming, scope, project, home)
		return filterOverlappingSourceChanges(changes, sourcePath), err
	}
	changes, err := legacyPlacementChanges(skillName, entry, incoming, scope, project, home)
	return filterOverlappingSourceChanges(changes, sourcePath), err
}

func filterOverlappingSourceChanges(changes []localChange, sourcePath string) []localChange {
	if sourcePath == "" {
		return changes
	}
	result := changes[:0]
	for _, change := range changes {
		if !pathsOverlap(sourcePath, change.Path) {
			result = append(result, change)
		}
	}
	return result
}

func removalLocalChanges(skillName string, entry *state.LockEntry, scope state.Scope, project, home string, targetAgents []string, allAgents bool) ([]localChange, error) {
	final := removalIsFinal(skillName, entry, scope, project, home, targetAgents, allAgents)
	targets := map[string]string{}
	if final && entry != nil {
		for id, placement := range entry.InstalledPlacements {
			targets[id] = placement.Kind
		}
	}
	if final {
		canonical, _ := logicalPlacementPath("canonical", skillName, scope, project, home)
		if _, err := os.Lstat(canonical); err == nil {
			targets["canonical"] = "canonical"
		}
	}
	for _, agent := range targetAgents {
		if agent == "eve" {
			for _, id := range existingEvePlacementIDs(skillName, project) {
				if placement, exists := installedPlacement(entry, id); exists {
					targets[id] = placement.Kind
				} else {
					targets[id] = "copy"
				}
			}
			if entry != nil {
				for _, subagent := range eveTargets(entry.Subagents) {
					id := evePlacementID(subagent)
					if _, exists := targets[id]; !exists {
						targets[id] = "copy"
					}
				}
			}
			continue
		}
		directory, universal, supported := state.AgentSkillsPath(agent, scope, project, home, os.Getenv("XDG_CONFIG_HOME"))
		if !supported || universal {
			continue
		}
		id := "agent:" + agent
		path := filepath.Join(directory, state.SanitizeName(skillName))
		if !final && pathSharedWithRemainingAgent(path, skillName, scope, project, home, targetAgents, installedAgentOwnership(entry)) {
			continue
		}
		if placement, exists := installedPlacement(entry, id); exists {
			targets[id] = placement.Kind
			continue
		}
		info, err := os.Lstat(path)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			targets[id] = "link"
		} else {
			targets[id] = "copy"
		}
	}
	targets, err := deduplicateRemovalTargets(skillName, entry, targets, scope, project, home)
	if err != nil {
		return nil, err
	}
	if entry != nil && len(entry.InstalledPlacements) > 0 {
		return exactPlacementChanges(skillName, *entry, targets, scope, project, home)
	}
	return legacyPlacementChangesForTargets(skillName, entry, targets, scope, project, home)
}

func deduplicateRemovalTargets(skillName string, entry *state.LockEntry, targets map[string]string, scope state.Scope, project, home string) (map[string]string, error) {
	result := make(map[string]string, len(targets))
	physicalIDs := make(map[string]string, len(targets))
	for _, id := range sortedPlacementIDs(targets) {
		placementPath, err := logicalPlacementPath(id, skillName, scope, project, home)
		if err != nil {
			return nil, err
		}
		absolute, err := filepath.Abs(placementPath)
		if err != nil {
			return nil, err
		}
		physical := filepath.Clean(absolute)
		if previous, exists := physicalIDs[physical]; exists {
			_, previousRecorded := installedPlacement(entry, previous)
			_, currentRecorded := installedPlacement(entry, id)
			if currentRecorded && !previousRecorded {
				delete(result, previous)
				result[id] = targets[id]
				physicalIDs[physical] = id
			}
			continue
		}
		physicalIDs[physical] = id
		result[id] = targets[id]
	}
	return result, nil
}

func installedPlacement(entry *state.LockEntry, id string) (state.InstalledPlacement, bool) {
	if entry == nil {
		return state.InstalledPlacement{}, false
	}
	placement, exists := entry.InstalledPlacements[id]
	return placement, exists
}

func existingEvePlacementIDs(skillName, project string) []string {
	ids := []string{}
	root := filepath.Join(state.EveSkillsPath(project, ""), state.SanitizeName(skillName))
	if _, err := os.Lstat(root); err == nil {
		ids = append(ids, "eve:root")
	}
	entries, _ := os.ReadDir(filepath.Join(project, "agent", "subagents"))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(state.EveSkillsPath(project, entry.Name()), state.SanitizeName(skillName))
		if _, err := os.Lstat(path); err == nil {
			ids = append(ids, evePlacementID(entry.Name()))
		}
	}
	sort.Strings(ids)
	return ids
}

func installedAgentOwnership(entry *state.LockEntry) []string {
	if entry == nil {
		return nil
	}
	return entry.Agents
}

func pathSharedWithRemainingAgent(path, skillName string, scope state.Scope, project, home string, targetAgents, installedAgents []string) bool {
	for _, agent := range installedAgents {
		if agent == "eve" || contains(targetAgents, agent) {
			continue
		}
		for _, candidate := range removalAgentPaths(agent, skillName, scope, project, home) {
			if sameLocalPath(path, candidate) {
				return true
			}
		}
	}
	return false
}

func exactPlacementChanges(skillName string, entry state.LockEntry, targets map[string]string, scope state.Scope, project, home string) ([]localChange, error) {
	changes := []localChange{}
	for _, id := range sortedPlacementIDs(targets) {
		path, err := logicalPlacementPath(id, skillName, scope, project, home)
		if err != nil {
			return nil, err
		}
		expected, recorded := entry.InstalledPlacements[id]
		if !recorded {
			if _, err := os.Lstat(path); err == nil {
				changes = append(changes, localChange{Skill: skillName, Status: "untracked", Path: path})
			} else if !errors.Is(err, fs.ErrNotExist) && !hasNonDirectoryAncestor(path) {
				return nil, err
			}
			continue
		}
		if expected.Kind == "link" {
			changes = append(changes, compareInstalledLink(skillName, path, scope, project, home)...)
			continue
		}
		actual, err := snapshotInstalledDirectory(path)
		if errors.Is(err, fs.ErrNotExist) {
			changes = append(changes, localChange{Skill: skillName, Status: "deleted", Path: path})
			continue
		}
		if err != nil {
			changes = append(changes, localChange{Skill: skillName, Status: "changed", Path: path})
			continue
		}
		changes = append(changes, diffInstalledPaths(skillName, path, expected.Paths, actual)...)
	}
	return normalizeLocalChanges(changes), nil
}

func compareInstalledLink(skillName, linkPath string, scope state.Scope, project, home string) []localChange {
	info, err := os.Lstat(linkPath)
	if errors.Is(err, fs.ErrNotExist) {
		return []localChange{{Skill: skillName, Status: "deleted", Path: linkPath}}
	}
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return []localChange{{Skill: skillName, Status: "changed", Path: linkPath}}
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		return []localChange{{Skill: skillName, Status: "changed", Path: linkPath}}
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	canonical, err := logicalPlacementPath("canonical", skillName, scope, project, home)
	if err != nil || !sameLocalPath(target, canonical) {
		return []localChange{{Skill: skillName, Status: "changed", Path: linkPath}}
	}
	return nil
}

func legacyPlacementChanges(skillName string, entry *state.LockEntry, targets map[string]string, scope state.Scope, project, home string) ([]localChange, error) {
	paths := map[string]string{}
	for id, kind := range targets {
		paths[id] = kind
	}
	if entry != nil {
		for _, agent := range entry.Agents {
			if agent == "eve" {
				for _, subagent := range eveTargets(entry.Subagents) {
					paths[evePlacementID(subagent)] = "copy"
				}
				continue
			}
			directory, universal, supported := state.AgentSkillsPath(agent, scope, project, home, os.Getenv("XDG_CONFIG_HOME"))
			if !supported {
				continue
			}
			id := "agent:" + agent
			candidate := filepath.Join(directory, state.SanitizeName(skillName))
			if universal {
				paths["canonical"] = "canonical"
				continue
			}
			info, err := os.Lstat(candidate)
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				paths[id] = "link"
				paths["canonical"] = "canonical"
			} else {
				paths[id] = "copy"
			}
		}
	}
	canonical, _ := logicalPlacementPath("canonical", skillName, scope, project, home)
	if _, err := os.Lstat(canonical); err == nil {
		paths["canonical"] = "canonical"
	}
	return legacyPlacementChangesForTargets(skillName, entry, paths, scope, project, home)
}

func legacyPlacementChangesForTargets(skillName string, entry *state.LockEntry, paths map[string]string, scope state.Scope, project, home string) ([]localChange, error) {
	changes := []localChange{}
	for _, id := range sortedPlacementIDs(paths) {
		path, err := logicalPlacementPath(id, skillName, scope, project, home)
		if err != nil {
			return nil, err
		}
		if entry == nil {
			if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) || err != nil && hasNonDirectoryAncestor(path) {
				continue
			} else if err != nil {
				return nil, err
			}
		}
		if paths[id] == "link" {
			changes = append(changes, compareInstalledLink(skillName, path, scope, project, home)...)
			continue
		}
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) || err != nil && hasNonDirectoryAncestor(path) {
			continue
		} else if err != nil {
			return nil, err
		}
		status := "untracked"
		if entry != nil {
			// Legacy aggregate hashes do not include executable modes or per-file
			// identities. They cannot prove that a destructive operation is safe.
			status = "unverifiable modified content"
		}
		changes = append(changes, localChange{Skill: skillName, Status: status, Path: path})
	}
	return normalizeLocalChanges(changes), nil
}

func diffInstalledPaths(skillName, root string, expected map[string]state.InstalledPathState, actual map[string]actualInstalledPath) []localChange {
	changes := []localChange{}
	for relative, wanted := range expected {
		current, exists := actual[relative]
		path := filepath.Join(root, filepath.FromSlash(relative))
		if !exists {
			changes = append(changes, localChange{Skill: skillName, Status: "deleted", Path: path})
			continue
		}
		if current.kind != wanted.Kind || current.hash != wanted.Hash {
			changes = append(changes, localChange{Skill: skillName, Status: "changed", Path: path})
			continue
		}
		if runtime.GOOS != "windows" && current.kind == "file" && current.executable != wanted.Executable {
			changes = append(changes, localChange{Skill: skillName, Status: "mode changed", Path: path})
		}
	}
	for relative := range actual {
		if _, exists := expected[relative]; !exists {
			changes = append(changes, localChange{Skill: skillName, Status: "added", Path: filepath.Join(root, filepath.FromSlash(relative))})
		}
	}
	return changes
}

func snapshotInstalledDirectory(root string) (map[string]actualInstalledPath, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("installed placement root is not a directory")
	}
	result := map[string]actualInstalledPath{}
	var scan func(string, string) error
	scan = func(directory, relativeDirectory string) error {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			relative := entry.Name()
			if relativeDirectory != "" {
				relative = filepath.ToSlash(filepath.Join(relativeDirectory, entry.Name()))
			}
			path := filepath.Join(directory, entry.Name())
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				sum := sha256.Sum256([]byte(target))
				result[relative] = actualInstalledPath{kind: "symlink", hash: fmt.Sprintf("%x", sum[:])}
			case info.IsDir():
				result[relative] = actualInstalledPath{kind: "directory"}
				if err := scan(path, relative); err != nil {
					return err
				}
			case info.Mode().IsRegular():
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				hash := sha256.New()
				_, copyErr := io.Copy(hash, file)
				closeErr := file.Close()
				if copyErr != nil {
					return copyErr
				}
				if closeErr != nil {
					return closeErr
				}
				result[relative] = actualInstalledPath{kind: "file", hash: fmt.Sprintf("%x", hash.Sum(nil)), executable: info.Mode().Perm()&0o111 != 0}
			default:
				return fmt.Errorf("unsupported installed path type at %s", path)
			}
		}
		return nil
	}
	if err := scan(root, ""); err != nil {
		return nil, err
	}
	return result, nil
}

func logicalPlacementPath(id, skillName string, scope state.Scope, project, home string) (string, error) {
	name := state.SanitizeName(skillName)
	base := project
	if scope == state.Global {
		base = home
	}
	if id == "canonical" {
		return filepath.Join(base, ".agents", "skills", name), nil
	}
	if strings.HasPrefix(id, "agent:") {
		agent := strings.TrimPrefix(id, "agent:")
		directory, _, supported := state.AgentSkillsPath(agent, scope, project, home, os.Getenv("XDG_CONFIG_HOME"))
		if !supported {
			return "", fmt.Errorf("agent %q does not support this scope", agent)
		}
		return filepath.Join(directory, name), nil
	}
	if strings.HasPrefix(id, "eve:") && scope == state.Project {
		subagent := strings.TrimPrefix(id, "eve:")
		if subagent == "root" {
			subagent = ""
		}
		return filepath.Join(state.EveSkillsPath(project, subagent), name), nil
	}
	return "", fmt.Errorf("invalid logical placement %q", id)
}

func evePlacementID(subagent string) string {
	if subagent == "" {
		return "eve:root"
	}
	return "eve:" + state.SanitizeName(subagent)
}

func sortedPlacementIDs(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for id := range values {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func normalizeLocalChanges(changes []localChange) []localChange {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Skill != changes[j].Skill {
			return changes[i].Skill < changes[j].Skill
		}
		if changes[i].Path != changes[j].Path {
			return changes[i].Path < changes[j].Path
		}
		return changes[i].Status < changes[j].Status
	})
	result := changes[:0]
	for _, change := range changes {
		if len(result) > 0 && result[len(result)-1] == change {
			continue
		}
		result = append(result, change)
	}
	return result
}

func authorizeLocalChanges(invocation Invocation, changes []localChange, force bool) error {
	changes = normalizeLocalChanges(changes)
	if len(changes) == 0 || force {
		return nil
	}
	if !invocation.Interactive || runningInAgent() {
		details := make([]string, 0, len(changes))
		for _, change := range changes {
			details = append(details, fmt.Sprintf("%s: %s", sanitizeHuman(change.Status), sanitizeHuman(change.Path)))
		}
		return fmt.Errorf("installed skill content has local changes (%s); re-run with --force to authorize their destruction", strings.Join(details, ", "))
	}
	_, _ = fmt.Fprintln(invocation.Stdout, "Locally modified installed content:")
	for _, change := range changes {
		_, _ = fmt.Fprintf(invocation.Stdout, "  %s: %s\n", sanitizeHuman(change.Status), sanitizeHuman(change.Path))
	}
	_, _ = fmt.Fprint(invocation.Stdout, "Discard these local changes? [y/N] ")
	line, err := readInputLine(invocation.Stdin)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read local-change confirmation: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return errors.New("destruction of local changes cancelled")
	}
	return nil
}
