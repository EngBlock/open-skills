package compatibility

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func requireSymlinkFixtures(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	link := filepath.Join(directory, "link")
	if err := os.WriteFile(target, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("Windows symlink creation is unavailable: %v", err)
	}
}

func TestNativeAddDereferencesConfinedRepositorySymlinks(t *testing.T) {
	requireSymlinkFixtures(t)
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--agent", "claude-code", "--yes"},
		Files: []FileFixture{
			{Root: TempRoot, Path: "source/SKILL.md", Data: []byte("---\nname: confined-links\ndescription: Confined links\n---\n")},
			{Root: TempRoot, Path: "source/payload.txt", Data: []byte("internal payload\n")},
			{Root: TempRoot, Path: "source/references/guide.md", Data: []byte("internal guide\n")},
			{Root: TempRoot, Path: "source/relative.txt", Symlink: "payload.txt"},
			{Root: TempRoot, Path: "source/absolute.txt", Symlink: "{{temp}}/source/payload.txt"},
			{Root: TempRoot, Path: "source/reference-copy", Junction: "references"},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" || observation.Stdout != "Installed confined-links\n" {
		t.Fatalf("add = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	for path, contents := range map[string]string{
		filepath.Join("project", ".agents", "skills", "confined-links", "relative.txt"):               "internal payload\n",
		filepath.Join("project", ".agents", "skills", "confined-links", "absolute.txt"):               "internal payload\n",
		filepath.Join("project", ".agents", "skills", "confined-links", "reference-copy", "guide.md"): "internal guide\n",
	} {
		installed, exists := fileAt(observation, path)
		if !exists || installed.Kind != FileKindRegular || string(installed.Data) != contents {
			t.Fatalf("dereferenced content %s = %#v", path, installed)
		}
	}
	agentLink, exists := fileAt(observation, filepath.Join("project", ".claude", "skills", "confined-links"))
	if !exists || agentLink.Kind != FileKindSymlink {
		t.Fatalf("tool-created agent link was not preserved: %#v", agentLink)
	}
}

func TestNativeAddRejectsUnconfinedRepositorySymlinksWithoutPartialInstallation(t *testing.T) {
	requireSymlinkFixtures(t)
	tests := []struct {
		name       string
		fixtures   []FileFixture
		diagnostic string
	}{
		{
			name: "parent directory target",
			fixtures: []FileFixture{
				{Root: TempRoot, Path: "source/bad/sub/link.txt", Symlink: "../payload.txt"},
				{Root: TempRoot, Path: "source/bad/payload.txt", Data: []byte("inside\n")},
			},
			diagnostic: "parent-directory symlink target",
		},
		{
			name: "absolute escape",
			fixtures: []FileFixture{
				{Root: TempRoot, Path: "source/bad/link.txt", Symlink: "{{temp}}/source/outside.txt"},
				{Root: TempRoot, Path: "source/outside.txt", Data: []byte("outside secret\n")},
			},
			diagnostic: "symlink target escapes selected skill directory",
		},
		{
			name: "broken target",
			fixtures: []FileFixture{
				{Root: TempRoot, Path: "source/bad/link.txt", Symlink: "missing.txt"},
			},
			diagnostic: "broken symlink",
		},
		{
			name: "cyclic target",
			fixtures: []FileFixture{
				{Root: TempRoot, Path: "source/bad/first", Symlink: "second"},
				{Root: TempRoot, Path: "source/bad/second", Symlink: "first"},
			},
			diagnostic: "cyclic symlink",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixtures := []FileFixture{
				{Root: TempRoot, Path: "source/good/SKILL.md", Data: []byte("---\nname: good\ndescription: Good skill\n---\n")},
				{Root: TempRoot, Path: "source/good/payload.txt", Data: []byte("good\n")},
				{Root: TempRoot, Path: "source/bad/SKILL.md", Data: []byte("---\nname: bad\ndescription: Bad skill\n---\n")},
			}
			fixtures = append(fixtures, test.fixtures...)
			observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
				Args:    []string{"add", "{{temp}}/source", "--skill", "good", "bad", "--agent", "universal", "--yes"},
				Files:   fixtures,
				Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, test.diagnostic) {
				t.Fatalf("add = exit %d stdout %q stderr %q; want %q", observation.ExitCode, observation.Stdout, observation.Stderr, test.diagnostic)
			}
			if observation.Stdout != "" {
				t.Fatalf("failed preflight reported an installation: %q", observation.Stdout)
			}
			for path := range observation.Files {
				if strings.HasPrefix(filepath.ToSlash(path), "project/.agents/skills/") {
					t.Fatalf("failed preflight left partial installation %s", path)
				}
			}
			if _, exists := observation.Locks[ProjectLock]; exists {
				t.Fatalf("failed preflight wrote project lock: %s", observation.Locks[ProjectLock])
			}
		})
	}
}

func TestNativeAddConfinesDirectoryJunctions(t *testing.T) {
	success, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--agent", "universal", "--yes"},
		Files: []FileFixture{
			{Root: TempRoot, Path: "source/SKILL.md", Data: []byte("---\nname: junction-skill\ndescription: Junction skill\n---\n")},
			{Root: TempRoot, Path: "source/references/guide.md", Data: []byte("internal guide\n")},
			{Root: TempRoot, Path: "source/reference-copy", Junction: "references"},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	installed, exists := fileAt(success, filepath.Join("project", ".agents", "skills", "junction-skill", "reference-copy", "guide.md"))
	if success.ExitCode != 0 || !exists || installed.Kind != FileKindRegular || string(installed.Data) != "internal guide\n" {
		t.Fatalf("confined junction add = exit %d stdout %q stderr %q file %#v", success.ExitCode, success.Stdout, success.Stderr, installed)
	}

	escape, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source/skill", "--agent", "universal", "--yes"},
		Files: []FileFixture{
			{Root: TempRoot, Path: "source/outside/secret.txt", Data: []byte("outside secret\n")},
			{Root: TempRoot, Path: "source/skill/SKILL.md", Data: []byte("---\nname: escaping-junction\ndescription: Escaping junction\n---\n")},
			{Root: TempRoot, Path: "source/skill/link", Junction: "{{temp}}/source/outside"},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if escape.ExitCode != 1 || !strings.Contains(escape.Stderr, "symlink target escapes selected skill directory") {
		t.Fatalf("escaping junction add = exit %d stdout %q stderr %q", escape.ExitCode, escape.Stdout, escape.Stderr)
	}
	if _, exists := fileAt(escape, filepath.Join("project", ".agents", "skills", "escaping-junction")); exists {
		t.Fatalf("escaping junction left a partial installation")
	}
}
