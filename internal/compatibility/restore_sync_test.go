package compatibility

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSyncDiscoversSupportedNodeModuleLayoutsOffline(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"experimental_sync", "--agent", "claude-code", "--yes"},
		Files: []FileFixture{
			{Root: ProjectRoot, Path: "node_modules/root/SKILL.md", Data: []byte("---\nname: root\ndescription: root\n---\n")},
			{Root: ProjectRoot, Path: "node_modules/root/skills/ignored/SKILL.md", Data: []byte("---\nname: ignored\ndescription: ignored\n---\n")},
			{Root: ProjectRoot, Path: "node_modules/catalog/skills/alpha/SKILL.md", Data: []byte("---\nname: alpha\ndescription: alpha\n---\n")},
			{Root: ProjectRoot, Path: "node_modules/agents/.agents/skills/beta/SKILL.md", Data: []byte("---\nname: beta\ndescription: beta\n---\n")},
			{Root: ProjectRoot, Path: "node_modules/@scope/tool/SKILL.md", Data: []byte("---\nname: scoped\ndescription: scoped\n---\n")},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("sync = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	for _, name := range []string{"root", "alpha", "beta", "scoped"} {
		canonical, exists := fileAt(observation, filepath.Join("project", ".agents", "skills", name, "SKILL.md"))
		if !exists || canonical.Kind != FileKindRegular {
			t.Fatalf("missing canonical %s: %#v", name, canonical)
		}
		link, exists := fileAt(observation, filepath.Join("project", ".claude", "skills", name))
		if !exists || link.Kind != FileKindSymlink {
			t.Fatalf("missing Claude link %s: %#v", name, link)
		}
	}
	if _, found := observation.Files["project/.agents/skills/ignored/SKILL.md"]; found {
		t.Fatalf("root package skill did not short-circuit nested discovery: %#v", observation.Files)
	}
	var lock struct {
		Version int `json:"version"`
		Skills  map[string]struct {
			Source     string   `json:"source"`
			SourceType string   `json:"sourceType"`
			Computed   string   `json:"computedHash"`
			Agents     []string `json:"agents"`
		}
	}
	if err := json.Unmarshal(observation.Locks[ProjectLock], &lock); err != nil {
		t.Fatal(err)
	}
	if lock.Version != 1 || len(lock.Skills) != 4 || lock.Skills["scoped"].Source != "@scope/tool" || lock.Skills["alpha"].SourceType != "node_modules" || lock.Skills["root"].Computed == "" || strings.Join(lock.Skills["beta"].Agents, ",") != "claude-code" {
		t.Fatalf("sync lock = %#v", lock)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeSyncFollowsLinkedNodeModulePackages(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"experimental_sync", "-a", "universal", "-y"},
		Files: []FileFixture{
			{Root: ProjectRoot, Path: "vendor/linked/SKILL.md", Data: []byte("---\nname: linked\ndescription: linked\n---\n")},
			{Root: ProjectRoot, Path: "node_modules/linked", Symlink: "../vendor/linked"},
		},
		Offline: true,
	})
	if err != nil || observation.ExitCode != 0 || !strings.Contains(observation.Stdout, "Synced linked from linked") {
		t.Fatalf("linked sync = %#v, %v", observation, err)
	}
	file, exists := observation.Files["project/.agents/skills/linked/SKILL.md"]
	if !exists || file.Kind != FileKindRegular {
		t.Fatalf("linked package skill was not installed: %#v", file)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeSyncSkipsUnchangedSkillsUnlessForced(t *testing.T) {
	harness := Harness{}
	files := []FileFixture{{Root: ProjectRoot, Path: "node_modules/demo/SKILL.md", Data: []byte("---\nname: demo\ndescription: demo\n---\n")}}
	first, err := harness.Run(context.Background(), buildShellTarget(t), Scenario{Args: []string{"experimental_sync", "-a", "universal", "-y"}, Files: files, Offline: true})
	if err != nil || first.ExitCode != 0 {
		t.Fatalf("initial sync = %#v, %v", first, err)
	}
	second, err := harness.Run(context.Background(), buildShellTarget(t), Scenario{Args: []string{"experimental_sync", "-a", "universal", "-y"}, Files: append(files, FileFixture{Root: ProjectRoot, Path: "skills-lock.json", Data: first.Locks[ProjectLock]}), Offline: true})
	if err != nil || second.ExitCode != 0 || second.Stdout != "demo already up to date\n" {
		t.Fatalf("unchanged sync = %#v, %v", second, err)
	}
	forced, err := harness.Run(context.Background(), buildShellTarget(t), Scenario{Args: []string{"experimental_sync", "-a", "universal", "-y", "--force"}, Files: append(files, FileFixture{Root: ProjectRoot, Path: "skills-lock.json", Data: first.Locks[ProjectLock]}), Offline: true})
	if err != nil || forced.ExitCode != 0 || !strings.Contains(forced.Stdout, "Synced demo from demo") {
		t.Fatalf("forced sync = %#v, %v", forced, err)
	}
}

func TestNativeInstallRestoresOnlyRecordedLocalNodeModuleSkillsAndPlacements(t *testing.T) {
	lock := `{
  "version": 1,
  "skills": {
    "restore-me": {
      "source": "package-a",
      "sourceType": "node_modules",
      "computedHash": "old",
      "agents": ["claude-code"],
      "subagents": ["research"]
    }
  }
}`
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"install"},
		Files: []FileFixture{
			{Root: ProjectRoot, Path: "skills-lock.json", Data: []byte(lock)},
			{Root: ProjectRoot, Path: "node_modules/package-a/SKILL.md", Data: []byte("---\nname: restore-me\ndescription: restored\n---\n")},
			{Root: ProjectRoot, Path: "node_modules/package-b/SKILL.md", Data: []byte("---\nname: unrelated\ndescription: unrelated\n---\n")},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stdout != "Restored restore-me\n" || observation.Stderr != "" {
		t.Fatalf("restore = %#v", observation)
	}
	for _, path := range []string{
		"project/.agents/skills/restore-me/SKILL.md",
		"project/agent/subagents/research/skills/restore-me/SKILL.md",
	} {
		file, exists := observation.Files[path]
		if !exists || file.Kind != FileKindRegular {
			t.Fatalf("missing restored file %s: %#v", path, file)
		}
	}
	link, exists := observation.Files["project/.claude/skills/restore-me"]
	if !exists || link.Kind != FileKindSymlink {
		t.Fatalf("missing restored Claude link: %#v", link)
	}
	if _, found := observation.Files["project/.agents/skills/unrelated/SKILL.md"]; found {
		t.Fatalf("restore installed an unlocked package skill: %#v", observation.Files)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeInstallFailsClosedForUnavailableRemoteSource(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"experimental_install"},
		Files:   []FileFixture{{Root: ProjectRoot, Path: "skills-lock.json", Data: []byte(`{"version":1,"skills":{"remote":{"source":"acme/repo","sourceType":"github","computedHash":"hash"}}}`)}},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "Cannot restore remote offline") {
		t.Fatalf("remote restore = %#v", observation)
	}
	if _, found := observation.Files["project/.agents/skills/remote/SKILL.md"]; found {
		t.Fatalf("remote restore mutated project: %#v", observation.Files)
	}
	assertOfflineShellObservation(t, observation)
}
