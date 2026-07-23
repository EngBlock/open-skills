package compatibility

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeAddReportsEveryNormalizedNameCollisionByRepositoryPath(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/source", "--list"},
		Files:   collisionSkillFixtures(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("add --list = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	for _, path := range []string{
		"skills/case-upper", "skills/case-lower",
		"skills/exact-first", "skills/exact-second",
		"skills/space", "skills/punctuation",
		"skills/traversal", "skills/safe",
		"skills/unicode-composed", "skills/unicode-decomposed",
	} {
		if !strings.Contains(observation.Stdout, path) {
			t.Errorf("list output does not report collision path %q: %q", path, observation.Stdout)
		}
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddRejectsAmbiguousNameWithoutInstalling(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/source", "--skill", "CASE", "--agent", "universal", "--yes"},
		Files:   collisionSkillFixtures(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "ambiguous") {
		t.Fatalf("ambiguous add = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	for _, path := range []string{"skills/case-upper", "skills/case-lower"} {
		if !strings.Contains(observation.Stderr, path) {
			t.Errorf("ambiguity error does not report %q: %q", path, observation.Stderr)
		}
	}
	assertNoSkillMutation(t, observation)
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddRejectsSanitizationAndTraversalNameCollisions(t *testing.T) {
	for _, test := range []struct {
		selector, first, second string
	}{
		{"white-space", "skills/space", "skills/punctuation"},
		{"safe", "skills/traversal", "skills/safe"},
	} {
		t.Run(test.selector, func(t *testing.T) {
			observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
				Args: []string{"add", "{{temp}}/source", "--skill", test.selector, "--agent", "universal", "--yes"}, Files: collisionSkillFixtures(), Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, test.first) || !strings.Contains(observation.Stderr, test.second) {
				t.Fatalf("ambiguous %s add = exit %d stdout %q stderr %q", test.selector, observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			assertNoSkillMutation(t, observation)
			assertOfflineShellObservation(t, observation)
		})
	}
}

func TestNativeAddRejectsImplicitAllWhenNamesCollide(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--agent", "universal", "--yes"},
		Files: []FileFixture{
			collisionSkill("source/skills/first/SKILL.md", "same", "first"),
			collisionSkill("source/skills/second/SKILL.md", "SAME", "second"),
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "skills/first") || !strings.Contains(observation.Stderr, "skills/second") {
		t.Fatalf("implicit all collision = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertNoSkillMutation(t, observation)
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddSelectsExactRepositoryRelativeSkillPath(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/source", "--skill-path", "skills/case-lower", "--agent", "universal", "--yes"},
		Files:   collisionSkillFixtures(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" || observation.Stdout != "Installed case\n" {
		t.Fatalf("path-selected add = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	installed := observation.Files[filepath.Join("project", ".agents", "skills", "case", "SKILL.md")]
	if !strings.Contains(string(installed.Data), "# lower") {
		t.Fatalf("wrong collision candidate installed: %#v", installed)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddRejectsMultipleExactPathsWithSameInstallIdentity(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/source", "--skill-path", "skills/case-upper", "skills/case-lower", "--agent", "universal", "--yes"},
		Files:   collisionSkillFixtures(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "skills/case-upper") || !strings.Contains(observation.Stderr, "skills/case-lower") {
		t.Fatalf("colliding exact paths = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertNoSkillMutation(t, observation)
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddAcceptsPortableBackslashSkillPath(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/source", "--skill-path", `skills\case-lower`, "--agent", "universal", "--yes"},
		Files:   collisionSkillFixtures(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" || observation.Stdout != "Installed case\n" {
		t.Fatalf("portable path add = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddDirectSkillSourceBypassesRepositoryCollision(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/source/skills/case-lower", "--agent", "universal", "--yes"},
		Files:   collisionSkillFixtures(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" || observation.Stdout != "Installed case\n" {
		t.Fatalf("direct source add = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddRejectsUnsafeSkillPaths(t *testing.T) {
	for _, selector := range []string{"../skills/case-lower", "skills/../case-lower", `skills\..\case-lower`, "/skills/case-lower", `C:\skills\case-lower`, `\\server\share\case-lower`} {
		t.Run(selector, func(t *testing.T) {
			observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
				Args:    []string{"add", "{{temp}}/source", "--skill-path", selector, "--agent", "universal", "--yes"},
				Files:   collisionSkillFixtures(),
				Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != 1 || !strings.Contains(strings.ToLower(observation.Stderr), "skill path") {
				t.Fatalf("unsafe path %q = exit %d stdout %q stderr %q", selector, observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			assertNoSkillMutation(t, observation)
			assertOfflineShellObservation(t, observation)
		})
	}
}

func TestNativeUseRejectsAmbiguousNormalizedNameAndAcceptsExactPath(t *testing.T) {
	files := []FileFixture{
		collisionSkill("source/skills/composed/SKILL.md", "café", "composed"),
		collisionSkill("source/skills/decomposed/SKILL.md", "cafe\u0301", "decomposed"),
	}
	ambiguous, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"use", "{{temp}}/source", "--skill", "café"},
		Files:   files,
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ambiguous.ExitCode != 1 || !strings.Contains(ambiguous.Stderr, "skills/composed") || !strings.Contains(ambiguous.Stderr, "skills/decomposed") {
		t.Fatalf("ambiguous use = exit %d stdout %q stderr %q", ambiguous.ExitCode, ambiguous.Stdout, ambiguous.Stderr)
	}

	selected, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"use", "{{temp}}/source", "--skill-path", "skills/decomposed"},
		Files:   files,
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if selected.ExitCode != 0 || selected.Stderr != "" || !strings.Contains(selected.Stdout, "# decomposed") || strings.Contains(selected.Stdout, "# composed") {
		t.Fatalf("path-selected use = exit %d stdout %q stderr %q", selected.ExitCode, selected.Stdout, selected.Stderr)
	}
	assertOfflineShellObservation(t, selected)
}

func TestNativeSyncRejectsNormalizedCollisionsBeforeMutation(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"experimental_sync", "--agent", "universal", "--yes"},
		Files: []FileFixture{
			{Root: ProjectRoot, Path: "node_modules/first/skills/one/SKILL.md", Data: []byte("---\nname: sync name\ndescription: collision fixture\n---\n# first\n")},
			{Root: ProjectRoot, Path: "node_modules/second/skills/two/SKILL.md", Data: []byte("---\nname: sync/name\ndescription: collision fixture\n---\n# second\n")},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "node_modules/first/skills/one") || !strings.Contains(observation.Stderr, "node_modules/second/skills/two") {
		t.Fatalf("colliding sync = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertNoSkillMutation(t, observation)
	assertOfflineShellObservation(t, observation)
}

func assertNoSkillMutation(t *testing.T, observation Observation) {
	t.Helper()
	if _, found := observation.Locks[ProjectLock]; found {
		t.Fatalf("failure wrote a project lock: %s", observation.Locks[ProjectLock])
	}
	for path := range observation.Files {
		if strings.HasPrefix(filepath.ToSlash(path), "project/.agents/") {
			t.Fatalf("failure wrote an installation path %s", path)
		}
	}
}

func collisionSkillFixtures() []FileFixture {
	return []FileFixture{
		collisionSkill("source/skills/case-upper/SKILL.md", "Case", "upper"),
		collisionSkill("source/skills/case-lower/SKILL.md", "case", "lower"),
		collisionSkill("source/skills/exact-first/SKILL.md", "duplicate", "exact first"),
		collisionSkill("source/skills/exact-second/SKILL.md", "duplicate", "exact second"),
		collisionSkill("source/skills/space/SKILL.md", "white space", "space"),
		collisionSkill("source/skills/punctuation/SKILL.md", "white/space", "punctuation"),
		collisionSkill("source/skills/traversal/SKILL.md", "../safe", "traversal"),
		collisionSkill("source/skills/safe/SKILL.md", "safe", "safe"),
		collisionSkill("source/skills/unicode-composed/SKILL.md", "café", "composed"),
		collisionSkill("source/skills/unicode-decomposed/SKILL.md", "cafe\u0301", "decomposed"),
	}
}

func collisionSkill(path, name, marker string) FileFixture {
	return FileFixture{Root: TempRoot, Path: path, Data: []byte("---\nname: " + name + "\ndescription: collision fixture\n---\n# " + marker + "\n")}
}
