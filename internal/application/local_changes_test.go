package application

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EngBlock/open-skills/internal/state"
)

func TestExactPlacementChangesDetectOwnedPathChanges(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*testing.T, string)
		wantStatus string
		wantPath   string
	}{
		{
			name: "changed", wantStatus: "changed", wantPath: "SKILL.md",
			mutate: func(t *testing.T, root string) { writeTestFile(t, filepath.Join(root, "SKILL.md"), "changed", 0o644) },
		},
		{
			name: "added", wantStatus: "added", wantPath: "added.txt",
			mutate: func(t *testing.T, root string) { writeTestFile(t, filepath.Join(root, "added.txt"), "added", 0o644) },
		},
		{
			name: "deleted", wantStatus: "deleted", wantPath: "SKILL.md",
			mutate: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "executable mode", wantStatus: "mode changed", wantPath: "SKILL.md",
			mutate: func(t *testing.T, root string) {
				if runtime.GOOS == "windows" {
					t.Skip("Windows does not expose portable executable bits")
				}
				if err := os.Chmod(filepath.Join(root, "SKILL.md"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			root := filepath.Join(project, ".agents", "skills", "fixture")
			writeTestFile(t, filepath.Join(root, "SKILL.md"), "original", 0o644)
			placement := captureTestPlacement(t, root, "canonical")
			entry := state.LockEntry{InstalledPlacements: map[string]state.InstalledPlacement{"canonical": placement}}
			test.mutate(t, root)
			changes, err := exactPlacementChanges("fixture", entry, map[string]string{"canonical": "canonical"}, state.Project, project, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if !containsLocalChange(changes, test.wantStatus, filepath.Join(root, test.wantPath)) {
				t.Fatalf("changes = %#v; want %s %s", changes, test.wantStatus, test.wantPath)
			}
		})
	}
}

func TestCanonicalLinksAndDivergentCopiesAreInspectedIndependently(t *testing.T) {
	t.Run("canonical link is deduplicated and retargeting is detected", func(t *testing.T) {
		project := t.TempDir()
		home := t.TempDir()
		canonical := filepath.Join(project, ".agents", "skills", "fixture")
		writeTestFile(t, filepath.Join(canonical, "SKILL.md"), "original", 0o644)
		link := filepath.Join(project, ".claude", "skills", "fixture")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "fixture"), link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		placements, err := captureInstalledPlacements("fixture", state.Project, project, home, []string{"claude-code"}, false, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		entry := state.LockEntry{InstalledPlacements: placements, Agents: []string{"claude-code"}}
		changes, err := installationLocalChanges("fixture", &entry, state.Project, project, home, []string{"claude-code"}, false, nil, "")
		if err != nil || len(changes) != 0 {
			t.Fatalf("clean linked placement changes = %#v, %v", changes, err)
		}
		writeTestFile(t, filepath.Join(canonical, "SKILL.md"), "modified", 0o644)
		changes, err = installationLocalChanges("fixture", &entry, state.Project, project, home, []string{"claude-code"}, false, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 1 || changes[0].Path != filepath.Join(canonical, "SKILL.md") {
			t.Fatalf("canonical change was not deduplicated: %#v", changes)
		}
		writeTestFile(t, filepath.Join(project, "other", "SKILL.md"), "modified", 0o644)
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", "..", "other"), link); err != nil {
			t.Fatal(err)
		}
		changes, err = installationLocalChanges("fixture", &entry, state.Project, project, home, []string{"claude-code"}, false, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if !containsLocalChange(changes, "changed", link) {
			t.Fatalf("retargeted link was not reported: %#v", changes)
		}
	})

	t.Run("copies diverge independently", func(t *testing.T) {
		project := t.TempDir()
		home := t.TempDir()
		for _, relative := range []string{".claude/skills/fixture/SKILL.md", ".continue/skills/fixture/SKILL.md"} {
			writeTestFile(t, filepath.Join(project, filepath.FromSlash(relative)), "original", 0o644)
		}
		placements, err := captureInstalledPlacements("fixture", state.Project, project, home, []string{"claude-code", "continue"}, true, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		entry := state.LockEntry{InstalledPlacements: placements, Agents: []string{"claude-code", "continue"}}
		continued := filepath.Join(project, ".continue", "skills", "fixture", "SKILL.md")
		writeTestFile(t, continued, "continue edit", 0o644)
		changes, err := installationLocalChanges("fixture", &entry, state.Project, project, home, []string{"claude-code", "continue"}, true, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 1 || changes[0].Path != continued {
			t.Fatalf("divergent copy changes = %#v", changes)
		}
	})
}

func TestExactPlacementChangesIncludeNodeModules(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, ".agents", "skills", "fixture")
	dependency := filepath.Join(root, "node_modules", "dependency", "data.txt")
	writeTestFile(t, filepath.Join(root, "SKILL.md"), "skill", 0o644)
	writeTestFile(t, dependency, "original", 0o644)
	placement := captureTestPlacement(t, root, "canonical")
	entry := state.LockEntry{InstalledPlacements: map[string]state.InstalledPlacement{"canonical": placement}}
	writeTestFile(t, dependency, "local edit", 0o644)
	changes, err := exactPlacementChanges("fixture", entry, map[string]string{"canonical": "canonical"}, state.Project, project, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !containsLocalChange(changes, "changed", dependency) {
		t.Fatalf("node_modules change was not detected: %#v", changes)
	}
}

func TestLegacyPlacementChangesFailClosedWithoutModeMetadata(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, ".agents", "skills", "legacy")
	file := filepath.Join(root, "SKILL.md")
	writeTestFile(t, file, "current", 0o644)
	hash, owned, err := contentIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(file, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	entry := state.LockEntry{InstalledContentHash: hash, OwnedFiles: owned, Agents: []string{"universal"}}
	changes, err := installationLocalChanges("legacy", &entry, state.Project, project, t.TempDir(), []string{"universal"}, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !containsLocalChange(changes, "unverifiable modified content", root) {
		t.Fatalf("legacy placement without mode metadata did not fail closed: %#v", changes)
	}
}

func TestAuthorizeLocalChangesPrintsSanitizedStablePaths(t *testing.T) {
	changes := []localChange{
		{Skill: "zeta", Status: "changed", Path: "/tmp/zeta\nforged"},
		{Skill: "alpha", Status: "added", Path: "/tmp/alpha"},
	}
	var stdout bytes.Buffer
	err := authorizeLocalChanges(Invocation{Interactive: true, Stdin: bytes.NewBufferString("yes\n"), Stdout: &stdout}, changes, false)
	if err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if strings.Contains(output, "zeta\nforged") || !strings.Contains(output, "added: /tmp/alpha\n  changed: /tmp/zeta forged") {
		t.Fatalf("prompt output is not sanitized and stable: %q", output)
	}
	if err := authorizeLocalChanges(Invocation{Stdin: bytes.NewReader(nil)}, changes, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("noninteractive authorization error = %v", err)
	}
	t.Setenv("AI_AGENT", "test")
	if err := authorizeLocalChanges(Invocation{Interactive: true, Stdin: bytes.NewBufferString("yes\n"), Stdout: &stdout}, changes, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("detected agent was allowed to confirm interactively: %v", err)
	}
}

func TestPartialRemovalOfCleanLinkIgnoresSurvivingCanonicalChanges(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	canonical := filepath.Join(project, ".agents", "skills", "fixture")
	copyPath := filepath.Join(project, ".continue", "skills", "fixture")
	writeTestFile(t, filepath.Join(canonical, "SKILL.md"), "original", 0o644)
	writeTestFile(t, filepath.Join(copyPath, "SKILL.md"), "original", 0o644)
	link := filepath.Join(project, ".claude", "skills", "fixture")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", ".agents", "skills", "fixture"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	canonicalPlacement := captureTestPlacement(t, canonical, "canonical")
	copyPlacement := captureTestPlacement(t, copyPath, "copy")
	entry := state.LockEntry{
		Agents: []string{"claude-code", "continue"},
		InstalledPlacements: map[string]state.InstalledPlacement{
			"canonical":         canonicalPlacement,
			"agent:claude-code": {Kind: "link", LinkTarget: "canonical"},
			"agent:continue":    copyPlacement,
		},
	}
	writeTestFile(t, filepath.Join(canonical, "SKILL.md"), "local edit", 0o644)
	changes, err := removalLocalChanges("fixture", &entry, state.Project, project, home, []string{"claude-code"}, false)
	if err != nil || len(changes) != 0 {
		t.Fatalf("partial clean-link removal saw surviving canonical changes: %#v, %v", changes, err)
	}
	writeTestFile(t, filepath.Join(copyPath, "SKILL.md"), "copy edit", 0o644)
	changes, err = removalLocalChanges("fixture", &entry, state.Project, project, home, []string{"continue"}, false)
	if err != nil || !containsLocalChange(changes, "changed", filepath.Join(copyPath, "SKILL.md")) {
		t.Fatalf("partial copy removal missed local changes: %#v, %v", changes, err)
	}
	changes, err = removalLocalChanges("fixture", &entry, state.Project, project, home, []string{"claude-code", "continue"}, true)
	if err != nil || !containsLocalChange(changes, "changed", filepath.Join(canonical, "SKILL.md")) {
		t.Fatalf("final removal missed canonical changes: %#v, %v", changes, err)
	}
}

func TestFinalRemovalProtectsUnrecordedAgentAndEvePlacements(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	canonical := filepath.Join(project, ".agents", "skills", "fixture")
	continuePath := filepath.Join(project, ".continue", "skills", "fixture")
	evePath := filepath.Join(project, "agent", "subagents", "secret", "skills", "fixture")
	writeTestFile(t, filepath.Join(canonical, "SKILL.md"), "original", 0o644)
	writeTestFile(t, filepath.Join(continuePath, "notes.txt"), "local copy", 0o644)
	writeTestFile(t, filepath.Join(evePath, "notes.txt"), "local Eve copy", 0o644)
	entry := state.LockEntry{
		Agents: []string{"universal"},
		InstalledPlacements: map[string]state.InstalledPlacement{
			"canonical": captureTestPlacement(t, canonical, "canonical"),
		},
	}
	changes, err := removalLocalChanges("fixture", &entry, state.Project, project, home, state.AgentIDs(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{continuePath, evePath} {
		if !containsLocalChange(changes, "untracked", path) {
			t.Fatalf("unrecorded removal target %s was not protected: %#v", path, changes)
		}
	}
}

func TestPartialRemovalKeepsPhysicalPathSharedByAnotherAgent(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	shared := filepath.Join(project, ".qoder", "skills", "fixture")
	writeTestFile(t, filepath.Join(shared, "SKILL.md"), "original", 0o644)
	placement := captureTestPlacement(t, shared, "copy")
	manifest := map[string]state.InstalledPlacement{
		"agent:qoder":    placement,
		"agent:qoder-cn": placement,
	}
	document, err := state.Read(filepath.Join(project, "skills-lock.json"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := document.RecordInstallation("fixture", state.InstallationRecord{
		Source: "source", SourceType: "local", InstalledContentHash: "hash", OwnedFiles: []string{"SKILL.md"},
		InstalledPlacements: manifest, Agents: []string{"qoder", "qoder-cn"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := document.Write(filepath.Join(project, "skills-lock.json")); err != nil {
		t.Fatal(err)
	}
	entry := document.Entry("fixture")
	changes, err := removalLocalChanges("fixture", entry, state.Project, project, home, []string{"qoder"}, false)
	if err != nil || len(changes) != 0 {
		t.Fatalf("shared surviving placement was treated as destructive: %#v, %v", changes, err)
	}
	if err := removeSkill("fixture", document, state.Project, project, home, []string{"qoder"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(shared, "SKILL.md")); err != nil {
		t.Fatalf("shared placement was removed: %v", err)
	}
	retained := document.Entry("fixture")
	if retained == nil || !contains(retained.Agents, "qoder-cn") || contains(retained.Agents, "qoder") {
		t.Fatalf("retained shared placement metadata = %#v", retained)
	}
}

func TestFinalRemovalDeduplicatesUninstalledAgentAliases(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	shared := filepath.Join(project, ".qoder", "skills", "fixture")
	writeTestFile(t, filepath.Join(shared, "SKILL.md"), "original", 0o644)
	manifest := map[string]state.InstalledPlacement{
		"agent:qoder": captureTestPlacement(t, shared, "copy"),
	}
	document, err := state.Read(filepath.Join(project, "skills-lock.json"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := document.RecordInstallation("fixture", state.InstallationRecord{
		Source: "source", SourceType: "local", InstalledContentHash: "hash", OwnedFiles: []string{"SKILL.md"},
		InstalledPlacements: manifest, Agents: []string{"qoder"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := document.Write(filepath.Join(project, "skills-lock.json")); err != nil {
		t.Fatal(err)
	}
	entry := document.Entry("fixture")
	changes, err := removalLocalChanges("fixture", entry, state.Project, project, home, state.AgentIDs(), true)
	if err != nil || len(changes) != 0 {
		t.Fatalf("uninstalled alias created a false conflict: %#v, %v", changes, err)
	}
	if err := removeSkill("fixture", document, state.Project, project, home, state.AgentIDs(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shared); !os.IsNotExist(err) {
		t.Fatalf("sole-owned shared path was not removed: %v", err)
	}
	if document.Entry("fixture") != nil {
		t.Fatal("sole-owned shared placement remained in lock state")
	}
}

func captureTestPlacement(t *testing.T, root, kind string) state.InstalledPlacement {
	t.Helper()
	paths, err := snapshotInstalledDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := make(map[string]state.InstalledPathState, len(paths))
	for relative, current := range paths {
		manifest[relative] = state.InstalledPathState{Kind: current.kind, Hash: current.hash, Executable: current.executable}
	}
	return state.InstalledPlacement{Kind: kind, Paths: manifest}
}

func writeTestFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func containsLocalChange(changes []localChange, status, path string) bool {
	for _, change := range changes {
		if change.Status == status && change.Path == path {
			return true
		}
	}
	return false
}
