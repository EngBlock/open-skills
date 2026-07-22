package compatibility

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const removeSkill = "---\nname: owned-skill\ndescription: remove fixture\n---\n# owned\n"

func projectRemoveLock(agents []string) []byte {
	encoded, _ := json.Marshal(map[string]any{
		"version": 1,
		"skills": map[string]any{
			"owned-skill": map[string]any{
				"source": "fixture", "sourceType": "local", "computedHash": "fixture-hash",
				"installedContentHash": "fixture-hash", "ownedFiles": []string{"SKILL.md"}, "agents": agents,
			},
		},
	})
	return encoded
}

func TestNativeRemoveKeepsCanonicalAndOtherOwnedPlacement(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"remove", "owned-skill", "--agent", "claude-code", "--yes"},
		Files: []FileFixture{
			{Root: ProjectRoot, Path: ".agents/skills/owned-skill/SKILL.md", Data: []byte(removeSkill)},
			{Root: ProjectRoot, Path: ".claude/skills/owned-skill", Symlink: "../../../.agents/skills/owned-skill"},
			{Root: ProjectRoot, Path: ".cursor/skills/owned-skill/SKILL.md", Data: []byte(removeSkill)},
			{Root: ProjectRoot, Path: "skills-lock.json", Data: projectRemoveLock([]string{"claude-code", "cursor"})},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" || !strings.Contains(observation.Stdout, "Successfully removed 1 skill") {
		t.Fatalf("remove = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	if _, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "owned-skill", "SKILL.md")); !ok {
		t.Fatalf("canonical content was removed: %#v", observation.Files)
	}
	if _, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".cursor", "skills", "owned-skill", "SKILL.md")); !ok {
		t.Fatalf("other placement was removed: %#v", observation.Files)
	}
	if _, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".claude", "skills", "owned-skill")); ok {
		t.Fatalf("selected placement remains: %#v", observation.Files)
	}
	lock, ok := observation.ParsedLocks[ProjectLock].(map[string]any)
	if !ok {
		t.Fatalf("project lock missing or malformed: %#v", observation.ParsedLocks)
	}
	skills := lock["skills"].(map[string]any)
	entry := skills["owned-skill"].(map[string]any)
	agents := entry["agents"].([]any)
	if len(agents) != 1 || agents[0] != "cursor" {
		t.Fatalf("remaining placements = %#v", agents)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeRemoveAllDeletesFinalProjectState(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"rm", "--all"},
		Files: []FileFixture{
			{Root: ProjectRoot, Path: ".agents/skills/owned-skill/SKILL.md", Data: []byte(removeSkill)},
			{Root: ProjectRoot, Path: ".claude/skills/owned-skill", Symlink: "../../../.agents/skills/owned-skill"},
			{Root: ProjectRoot, Path: "skills-lock.json", Data: projectRemoveLock([]string{"claude-code"})},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" || !strings.Contains(observation.Stdout, "Successfully removed 1 skill") {
		t.Fatalf("remove all = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	for _, path := range []string{
		filepath.Join(observation.Paths.Project, ".agents", "skills", "owned-skill"),
		filepath.Join(observation.Paths.Project, ".claude", "skills", "owned-skill"),
	} {
		if _, ok := fileAt(observation, path); ok {
			t.Fatalf("final placement remains at %s: %#v", path, observation.Files)
		}
	}
	lock := observation.ParsedLocks[ProjectLock].(map[string]any)
	if skills := lock["skills"].(map[string]any); len(skills) != 0 {
		t.Fatalf("final lock entry remains: %#v", skills)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeRemoveSupportsSkillFlagInteractiveSelectionAndGlobalScope(t *testing.T) {
	globalLock, _ := json.Marshal(map[string]any{
		"version": 3,
		"skills": map[string]any{
			"owned-skill": map[string]any{
				"source": "fixture", "sourceType": "local", "sourceUrl": "fixture", "skillFolderHash": "fixture-hash",
				"installedAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z",
			},
		},
	})
	for _, test := range []struct {
		name  string
		args  []string
		stdin string
		env   map[string]string
	}{
		{name: "skill flag", args: []string{"r", "--skill", "owned-skill", "--yes"}},
		{name: "interactive", args: []string{"remove"}, stdin: "owned-skill\ny\n"},
		{name: "agent noninteractive", args: []string{"remove", "owned-skill", "--agent", "universal"}, env: map[string]string{"AI_AGENT": "fixture"}},
		{name: "global", args: []string{"remove", "--skill", "owned-skill", "--global", "--yes"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := []FileFixture{
				{Root: ProjectRoot, Path: ".agents/skills/owned-skill/SKILL.md", Data: []byte(removeSkill)},
				{Root: ProjectRoot, Path: "skills-lock.json", Data: projectRemoveLock([]string{"universal"})},
			}
			if test.name == "global" {
				files = []FileFixture{
					{Root: HomeRoot, Path: ".agents/skills/owned-skill/SKILL.md", Data: []byte(removeSkill)},
					{Root: HomeRoot, Path: ".local/state/skills/.skill-lock.json", Data: globalLock},
				}
			}
			observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{Args: test.args, Stdin: []byte(test.stdin), Env: test.env, Files: files, Offline: true})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != 0 || observation.Stderr != "" {
				t.Fatalf("remove = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			base := observation.Paths.Project
			if test.name == "global" {
				base = observation.Paths.Home
			}
			if _, ok := fileAt(observation, filepath.Join(base, ".agents", "skills", "owned-skill")); ok {
				t.Fatalf("removed skill remains: %#v", observation.Files)
			}
			assertOfflineShellObservation(t, observation)
		})
	}
}
