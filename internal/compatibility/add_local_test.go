package compatibility

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeAddListsLocalSkillsOffline(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--list"},
		Files: []FileFixture{
			{Root: TempRoot, Path: "source/skills/alpha/SKILL.md", Data: []byte("---\nname: alpha\ndescription: Alpha local skill\n---\n")},
			{Root: TempRoot, Path: "source/skills/catalog/beta/SKILL.md", Data: []byte("---\nname: beta\ndescription: Beta local skill\n---\n")},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("add --list = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	if observation.Stdout != "alpha\nbeta\n" {
		t.Fatalf("list output = %q", observation.Stdout)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddInstallsFilteredLocalSkillIntoProjectTopologyAndLock(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--skill", "beta", "--agent", "claude-code"},
		Files: []FileFixture{
			{Root: TempRoot, Path: "source/skills/alpha/SKILL.md", Data: []byte("---\nname: alpha\ndescription: Alpha local skill\n---\n")},
			{Root: TempRoot, Path: "source/skills/beta/SKILL.md", Data: []byte("---\nname: beta\ndescription: Beta local skill\n---\n")},
			{Root: TempRoot, Path: "source/skills/beta/scripts/check.sh", Data: []byte("#!/bin/sh\necho checked\n"), Mode: 0o755},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" || observation.Stdout != "Installed beta\n" {
		t.Fatalf("add = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	canonical := filepath.Join(observation.Paths.Project, ".agents", "skills", "beta")
	canonicalSkill, ok := observation.Files[strings.TrimPrefix(filepath.Join(canonical, "SKILL.md"), observation.Paths.Root+string(filepath.Separator))]
	if !ok || string(canonicalSkill.Data) == "" {
		t.Fatalf("canonical skill was not installed: %#v", observation.Files)
	}
	link := observation.Files[strings.TrimPrefix(filepath.Join(observation.Paths.Project, ".claude", "skills", "beta"), observation.Paths.Root+string(filepath.Separator))]
	if link.Kind != FileKindSymlink {
		t.Fatalf("Claude target is not a canonical symlink: %#v", link)
	}
	var lock struct {
		Version int `json:"version"`
		Skills  map[string]struct {
			Source               string   `json:"source"`
			SourceType           string   `json:"sourceType"`
			ComputedHash         string   `json:"computedHash"`
			InstalledContentHash string   `json:"installedContentHash"`
			OwnedFiles           []string `json:"ownedFiles"`
		}
	}
	if err := json.Unmarshal(observation.Locks[ProjectLock], &lock); err != nil {
		t.Fatal(err)
	}
	entry := lock.Skills["beta"]
	if lock.Version != 1 || entry.SourceType != "local" || entry.Source == "" || entry.ComputedHash == "" || entry.ComputedHash != entry.InstalledContentHash || strings.Contains(entry.Source, "@") || len(entry.OwnedFiles) != 2 || entry.OwnedFiles[0] != "SKILL.md" || entry.OwnedFiles[1] != "scripts/check.sh" {
		t.Fatalf("project lock entry = %#v", entry)
	}
	if strings.Contains(string(observation.Locks[ProjectLock]), "installedAt") || strings.Contains(string(observation.Locks[ProjectLock]), "updatedAt") {
		t.Fatalf("project lock has churn-prone timestamps: %s", observation.Locks[ProjectLock])
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddInstallsLocalSkillGloballyAndRetainsGlobalSchema(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"a", "{{temp}}/source", "-g", "-y"},
		Files: []FileFixture{
			{Root: TempRoot, Path: "source/global-skill/SKILL.md", Data: []byte("---\nname: global-skill\ndescription: Global local skill\n---\n")},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" || observation.Stdout != "Installed global-skill\n" {
		t.Fatalf("global add = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	var lock struct {
		Version int `json:"version"`
		Skills  map[string]struct {
			SourceURL       string `json:"sourceUrl"`
			SourceType      string `json:"sourceType"`
			SkillFolderHash string `json:"skillFolderHash"`
			InstalledAt     string `json:"installedAt"`
			UpdatedAt       string `json:"updatedAt"`
		}
	}
	if err := json.Unmarshal(observation.Locks[XDGGlobalLock], &lock); err != nil {
		t.Fatal(err)
	}
	entry := lock.Skills["global-skill"]
	if lock.Version != 3 || entry.SourceType != "local" || entry.SourceURL == "" || entry.SkillFolderHash == "" || entry.InstalledAt == "" || entry.UpdatedAt == "" {
		t.Fatalf("global lock entry = %#v", entry)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddPromptsForMultipleLocalSkills(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:  []string{"add", "{{temp}}/source"},
		Stdin: []byte("beta\nuniversal\n"),
		Files: []FileFixture{
			{Root: TempRoot, Path: "source/alpha/SKILL.md", Data: []byte("---\nname: alpha\ndescription: Alpha local skill\n---\n")},
			{Root: TempRoot, Path: "source/beta/SKILL.md", Data: []byte("---\nname: beta\ndescription: Beta local skill\n---\n")},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || !strings.Contains(observation.Stdout, "Select skills to install") || !strings.Contains(observation.Stdout, "Installed beta") {
		t.Fatalf("interactive add = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	if _, found := observation.Files[filepath.Join("project", ".agents", "skills", "alpha", "SKILL.md")]; found {
		t.Fatalf("interactive selection installed unselected alpha: %#v", observation.Files)
	}
	assertOfflineShellObservation(t, observation)
}
