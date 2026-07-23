package application

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/EngBlock/open-skills/internal/state"
)

func authorizeSourceReplacements(invocation Invocation, skills []localSkill, provenance installationProvenance, scope state.Scope, project, home string, options addOptions) (map[string]bool, error) {
	lockPath, version := installationLockLocation(scope, project, home)
	document, err := state.Read(lockPath, version)
	if err != nil {
		return nil, fmt.Errorf("read installation state: %w", err)
	}
	replacements := make(map[string]bool)
	for _, skill := range skills {
		replacement, err := authorizeSourceReplacement(invocation, skill.Name, document.Entry(skill.Name), provenance, project, options.Replace)
		if err != nil {
			return nil, err
		}
		if replacement {
			replacements[state.SanitizeName(skill.Name)] = true
		}
	}
	return replacements, nil
}

func authorizeSourceReplacement(invocation Invocation, skillName string, entry *state.LockEntry, provenance installationProvenance, project string, explicitlyAllowed bool) (bool, error) {
	if entry == nil || sameInstallationSource(*entry, provenance, project) {
		return false, nil
	}
	if explicitlyAllowed {
		return true, nil
	}
	if !invocation.Interactive || runningInAgent() {
		return false, fmt.Errorf("%s is installed from a different source; re-run with --replace to authorize replacement", sanitizeHuman(skillName))
	}
	_, _ = fmt.Fprintf(invocation.Stdout, "Installed source (%s): %s\n", sanitizeHuman(entry.SourceType), displaySourceIdentity(entry.SourceType, entry.Source))
	_, _ = fmt.Fprintf(invocation.Stdout, "Replacement source (%s): %s\n", sanitizeHuman(provenance.Type), displaySourceIdentity(provenance.Type, provenance.Identity))
	_, _ = fmt.Fprintf(invocation.Stdout, "Replace %s? [y/N] ", sanitizeHuman(skillName))
	line, readErr := readInputLine(invocation.Stdin)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, fmt.Errorf("read replacement confirmation: %w", readErr)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return false, fmt.Errorf("replacement cancelled for %s", sanitizeHuman(skillName))
	}
	return true, nil
}

func sameInstallationSource(existing state.LockEntry, incoming installationProvenance, project string) bool {
	if existing.SourceType != incoming.Type {
		return false
	}
	existingIdentities := uniqueSourceIdentities(existing.SourceType, project, existing.Source, existing.SourceURL)
	incomingIdentities := uniqueSourceIdentities(incoming.Type, project, incoming.Identity, incoming.URL)
	for _, oldIdentity := range existingIdentities {
		for _, newIdentity := range incomingIdentities {
			if existing.SourceType == "local" {
				if sameLocalPath(oldIdentity, newIdentity) {
					return true
				}
				continue
			}
			if oldIdentity == newIdentity {
				return true
			}
		}
	}
	return false
}

func uniqueSourceIdentities(sourceType, project string, values ...string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		value = sanitizedSourceIdentity(sourceType, value)
		if sourceType == "local" {
			if !filepath.IsAbs(value) {
				value = filepath.Join(project, value)
			}
			value = filepath.Clean(value)
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func sanitizedSourceIdentity(sourceType, identity string) string {
	if sourceType == "local" {
		return sanitizeHuman(identity)
	}
	return credentialFreeSource(identity)
}

func displaySourceIdentity(sourceType, identity string) string {
	return sanitizeHuman(sanitizedSourceIdentity(sourceType, identity))
}

func installationLockLocation(scope state.Scope, project, home string) (string, int) {
	if scope == state.Project {
		return filepath.Join(project, "skills-lock.json"), 1
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "skills", ".skill-lock.json"), 3
	}
	return filepath.Join(home, ".agents", ".skill-lock.json"), 3
}

func hasNonDirectoryAncestor(path string) bool {
	for current := filepath.Dir(path); current != filepath.Dir(current); current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			return !info.IsDir()
		}
	}
	return false
}

func replacementPathsForSkills(skills []localSkill, scope state.Scope, base, project, home string, agents, subagents []string, copyMode bool) ([]string, error) {
	result := []string{}
	allEve := len(agents) > 0
	for _, agent := range agents {
		allEve = allEve && agent == "eve"
	}
	needsCanonical := (!copyMode && !allEve) || len(agents) == 0 || contains(agents, "universal")
	for _, skill := range skills {
		canonical := filepath.Join(base, ".agents", "skills", state.SanitizeName(skill.Name))
		if needsCanonical {
			result = append(result, canonical)
		}
		for _, agent := range agents {
			if agent == "eve" {
				for _, subagent := range eveTargets(subagents) {
					result = append(result, filepath.Join(state.EveSkillsPath(project, subagent), state.SanitizeName(skill.Name)))
				}
				continue
			}
			directory, universal, ok := state.AgentSkillsPath(agent, scope, project, home, os.Getenv("XDG_CONFIG_HOME"))
			if !ok {
				return nil, fmt.Errorf("agent %q does not support this scope", agent)
			}
			if universal {
				result = append(result, canonical)
				continue
			}
			if !copyMode && scope == state.Project && agent != "claude-code" && !state.ProjectAgentRootExists(agent, project) {
				continue
			}
			result = append(result, filepath.Join(directory, state.SanitizeName(skill.Name)))
		}
	}
	return uniqueReplacementPaths(result)
}

func uniqueReplacementPaths(paths []string) ([]string, error) {
	unique := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		key := filepath.Clean(absolute)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, path)
		}
	}
	return unique, nil
}

func copyReplacementPath(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.Symlink(target, destination)
	}
	if info.IsDir() {
		if err := os.MkdirAll(destination, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyReplacementPath(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(destination, info.Mode().Perm())
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported placement file mode %s", info.Mode())
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Chmod(info.Mode().Perm()); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}
