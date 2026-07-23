package compatibility

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

const modifiedProvenanceSkill = "---\nname: provenance-skill\ndescription: locally modified\n---\n# local edit\n"
const modifiedRemoveSkill = "---\nname: owned-skill\ndescription: locally modified\n---\n# local edit\n"

func TestNativeAddRequiresForceForLocalChangesEvenWithYes(t *testing.T) {
	for _, test := range []struct {
		name     string
		force    bool
		wantCode int
		marker   string
	}{
		{name: "yes is insufficient", wantCode: 1, marker: "# local edit"},
		{name: "force is explicit", force: true, wantCode: 0, marker: "# replacement"},
	} {
		t.Run(test.name, func(t *testing.T) {
			arguments := []string{"add", "{{temp}}/replacement", "--agent", "universal", "--yes"}
			if test.force {
				arguments = append(arguments, "--force")
			}
			observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
				Args: arguments,
				Files: []FileFixture{
					{Root: TempRoot, Path: "replacement/SKILL.md", Data: []byte(replacementProvenanceSkill)},
					{Root: ProjectRoot, Path: ".agents/skills/provenance-skill/SKILL.md", Data: []byte(modifiedProvenanceSkill)},
					{Root: ProjectRoot, Path: "skills-lock.json", Data: provenanceLock("{{temp:json}}/replacement")},
				},
				Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != test.wantCode {
				t.Fatalf("add = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			installed, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "provenance-skill", "SKILL.md"))
			if !ok || !strings.Contains(string(installed.Data), test.marker) {
				t.Fatalf("installed content = %#v; want marker %q", installed, test.marker)
			}
			if !test.force {
				if !strings.Contains(observation.Stderr, "--force") {
					t.Fatalf("local-change rejection omitted force guidance: %q", observation.Stderr)
				}
				wantLock := provenanceLock(filepath.ToSlash(filepath.Join(observation.Paths.Temp, "replacement")))
				if string(observation.Locks[ProjectLock]) != string(wantLock) {
					t.Fatalf("rejected add changed lock:\nwant %s\n got %s", wantLock, observation.Locks[ProjectLock])
				}
			}
		})
	}
}

func TestNativeAddRequiresReplaceAndForceIndependently(t *testing.T) {
	for _, test := range []struct {
		name        string
		flags       []string
		wantCode    int
		wantMissing string
	}{
		{name: "neither", wantCode: 1, wantMissing: "--replace"},
		{name: "replace only", flags: []string{"--replace"}, wantCode: 1, wantMissing: "--force"},
		{name: "force only", flags: []string{"--force"}, wantCode: 1, wantMissing: "--replace"},
		{name: "both", flags: []string{"--replace", "--force"}, wantCode: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			arguments := []string{"add", "{{temp}}/replacement", "--agent", "universal", "--yes"}
			arguments = append(arguments, test.flags...)
			observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
				Args: arguments,
				Files: []FileFixture{
					{Root: TempRoot, Path: "replacement/SKILL.md", Data: []byte(replacementProvenanceSkill)},
					{Root: ProjectRoot, Path: ".agents/skills/provenance-skill/SKILL.md", Data: []byte(modifiedProvenanceSkill)},
					{Root: ProjectRoot, Path: "skills-lock.json", Data: provenanceLock("{{temp:json}}/prior")},
				},
				Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != test.wantCode || test.wantMissing != "" && !strings.Contains(observation.Stderr, test.wantMissing) {
				t.Fatalf("add = exit %d stdout %q stderr %q; want code %d and %q", observation.ExitCode, observation.Stdout, observation.Stderr, test.wantCode, test.wantMissing)
			}
			installed, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "provenance-skill", "SKILL.md"))
			marker := "# local edit"
			if test.wantCode == 0 {
				marker = "# replacement"
			}
			if !ok || !strings.Contains(string(installed.Data), marker) {
				t.Fatalf("installed content = %#v; want marker %q", installed, marker)
			}
		})
	}
}

func TestNativeFinalRemoveProtectsUnrecordedDivergentCopies(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"remove", "--all"},
		Files: []FileFixture{
			{Root: ProjectRoot, Path: ".agents/skills/owned-skill/SKILL.md", Data: []byte(removeSkill)},
			{Root: ProjectRoot, Path: ".continue/skills/owned-skill/notes.txt", Data: []byte("unrecorded copy\n")},
			{Root: ProjectRoot, Path: "agent/subagents/secret/skills/owned-skill/notes.txt", Data: []byte("unrecorded Eve copy\n")},
			{Root: ProjectRoot, Path: "skills-lock.json", Data: projectRemoveLock([]string{"universal"})},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "--force") {
		t.Fatalf("remove with divergent copies = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	for _, path := range []string{
		filepath.Join(observation.Paths.Project, ".agents", "skills", "owned-skill", "SKILL.md"),
		filepath.Join(observation.Paths.Project, ".continue", "skills", "owned-skill", "notes.txt"),
		filepath.Join(observation.Paths.Project, "agent", "subagents", "secret", "skills", "owned-skill", "notes.txt"),
	} {
		if _, ok := fileAt(observation, path); !ok {
			t.Fatalf("rejected final remove deleted %s", path)
		}
	}
}

func TestNativeRemoveAndSyncRequireForceForLocalChanges(t *testing.T) {
	t.Run("remove", func(t *testing.T) {
		for _, force := range []bool{false, true} {
			arguments := []string{"remove", "owned-skill", "--yes"}
			if force {
				arguments = append(arguments, "--force")
			}
			observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
				Args: arguments,
				Files: []FileFixture{
					{Root: ProjectRoot, Path: ".agents/skills/owned-skill/SKILL.md", Data: []byte(modifiedRemoveSkill)},
					{Root: ProjectRoot, Path: "skills-lock.json", Data: projectRemoveLock([]string{"universal"})},
				},
				Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if force {
				if observation.ExitCode != 0 {
					t.Fatalf("forced remove = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
				}
				if _, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "owned-skill")); ok {
					t.Fatal("forced remove retained modified content")
				}
			} else {
				if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "--force") {
					t.Fatalf("unforced remove = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
				}
				if _, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "owned-skill", "SKILL.md")); !ok {
					t.Fatal("rejected remove changed modified content")
				}
				if string(observation.Locks[ProjectLock]) != string(projectRemoveLock([]string{"universal"})) {
					t.Fatal("rejected remove changed lock state")
				}
			}
		}
	})

	t.Run("sync", func(t *testing.T) {
		for _, force := range []bool{false, true} {
			arguments := []string{"experimental_sync", "--agent", "universal", "--yes"}
			if force {
				arguments = append(arguments, "--force")
			}
			syncLock := []byte(strings.Replace(string(provenanceLock("replacement-package")), `"sourceType":"local"`, `"sourceType":"node_modules"`, 1))
			observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
				Args: arguments,
				Files: []FileFixture{
					{Root: ProjectRoot, Path: "node_modules/replacement-package/SKILL.md", Data: []byte(replacementProvenanceSkill)},
					{Root: ProjectRoot, Path: ".agents/skills/provenance-skill/SKILL.md", Data: []byte(modifiedProvenanceSkill)},
					{Root: ProjectRoot, Path: "skills-lock.json", Data: syncLock},
				},
				Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != map[bool]int{false: 1, true: 0}[force] {
				t.Fatalf("sync force=%v = exit %d stdout %q stderr %q", force, observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			installed, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "provenance-skill", "SKILL.md"))
			marker := "# local edit"
			if force {
				marker = "# replacement"
			} else if !strings.Contains(observation.Stderr, "--force") {
				t.Fatalf("sync rejection omitted force guidance: %q", observation.Stderr)
			}
			if !ok || !strings.Contains(string(installed.Data), marker) {
				t.Fatalf("sync force=%v content = %#v; want marker %q", force, installed, marker)
			}
			if !force && string(observation.Locks[ProjectLock]) != string(syncLock) {
				t.Fatal("rejected sync changed lock state")
			}
		}
	})
}
