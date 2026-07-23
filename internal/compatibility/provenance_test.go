package compatibility

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const priorProvenanceSkill = "---\nname: provenance-skill\ndescription: prior source\n---\n# prior\n"
const replacementProvenanceSkill = "---\nname: provenance-skill\ndescription: replacement source\n---\n# replacement\n"

func provenanceLock(source string) []byte {
	contentHash := sha256.Sum256([]byte("SKILL.md" + priorProvenanceSkill))
	fileHash := sha256.Sum256([]byte(priorProvenanceSkill))
	return []byte(fmt.Sprintf(`{"version":1,"skills":{"provenance-skill":{"source":%s,"sourceType":"local","computedHash":"%x","installedContentHash":"%x","ownedFiles":["SKILL.md"],"agents":["universal"],"installedPlacements":{"canonical":{"kind":"canonical","paths":{"SKILL.md":{"kind":"file","hash":"%x"}}}}}}}`, quoteFixtureJSON(source), contentHash, contentHash, fileHash))
}

func quoteFixtureJSON(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func provenanceFixtures(lockSource string) []FileFixture {
	return []FileFixture{
		{Root: TempRoot, Path: "replacement/SKILL.md", Data: []byte(replacementProvenanceSkill)},
		{Root: ProjectRoot, Path: ".agents/skills/provenance-skill/SKILL.md", Data: []byte(priorProvenanceSkill)},
		{Root: ProjectRoot, Path: "skills-lock.json", Data: provenanceLock(lockSource)},
	}
}

func TestNativeAddRequiresReplaceForCrossSourceCollisionEvenWithYes(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/replacement", "--agent", "universal", "--yes"},
		Files:   provenanceFixtures("{{temp}}/prior"),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "--replace") {
		t.Fatalf("cross-source add = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertPriorProvenanceState(t, observation)
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddPreflightsEverySourceCollisionBeforeMutation(t *testing.T) {
	lock := []byte(`{"version":1,"skills":{"alpha":{"source":"{{temp}}/replacement","sourceType":"local","computedHash":"alpha-old"},"zeta":{"source":"{{temp}}/prior","sourceType":"local","computedHash":"zeta-old"}}}`)
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/replacement", "--skill", "alpha", "zeta", "--agent", "universal", "--yes"},
		Files: []FileFixture{
			{Root: TempRoot, Path: "replacement/alpha/SKILL.md", Data: []byte("---\nname: alpha\ndescription: replacement\n---\n# alpha replacement\n")},
			{Root: TempRoot, Path: "replacement/zeta/SKILL.md", Data: []byte("---\nname: zeta\ndescription: replacement\n---\n# zeta replacement\n")},
			{Root: ProjectRoot, Path: ".agents/skills/alpha/SKILL.md", Data: []byte("---\nname: alpha\ndescription: prior\n---\n# alpha prior\n")},
			{Root: ProjectRoot, Path: ".agents/skills/zeta/SKILL.md", Data: []byte("---\nname: zeta\ndescription: prior\n---\n# zeta prior\n")},
			{Root: ProjectRoot, Path: "skills-lock.json", Data: lock},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "--replace") {
		t.Fatalf("multi-skill collision = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	alpha, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "alpha", "SKILL.md"))
	if !ok || !strings.Contains(string(alpha.Data), "# alpha prior") {
		t.Fatalf("same-source skill changed before later collision failed: %#v", alpha)
	}
	if string(observation.Locks[ProjectLock]) != string([]byte(strings.ReplaceAll(string(lock), "{{temp}}", filepath.ToSlash(observation.Paths.Temp)))) {
		t.Fatalf("lock changed before collision authorization: %s", observation.Locks[ProjectLock])
	}
}

func TestNativeAddAllowsSameSourceReinstallWithoutReplace(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/replacement", "--agent", "universal", "--yes"},
		Files:   provenanceFixtures("{{temp}}/replacement"),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("same-source reinstall = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	installed, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "provenance-skill", "SKILL.md"))
	if !ok || !strings.Contains(string(installed.Data), "# replacement") {
		t.Fatalf("same-source content was not reinstalled: %#v", installed)
	}
}

func TestNativeAddAllowsExplicitCrossSourceReplacement(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/replacement", "--agent", "universal", "--replace", "--yes"},
		Files:   provenanceFixtures("{{temp}}/prior"),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("explicit replacement = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	installed, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "provenance-skill", "SKILL.md"))
	if !ok || !strings.Contains(string(installed.Data), "# replacement") {
		t.Fatalf("replacement content was not installed: %#v", installed)
	}
	if !strings.Contains(string(observation.Locks[ProjectLock]), filepath.ToSlash(filepath.Join(observation.Paths.Temp, "replacement"))) {
		t.Fatalf("replacement provenance was not recorded: %s", observation.Locks[ProjectLock])
	}
}

func TestNativeAddReplaceRollsBackContentAndLockWhenPlacementFails(t *testing.T) {
	fixtures := append(provenanceFixtures("{{temp}}/prior"),
		FileFixture{Root: ProjectRoot, Path: ".claude/skills/provenance-skill/SKILL.md", Data: []byte(priorProvenanceSkill)},
		FileFixture{Root: ProjectRoot, Path: ".pi", Data: []byte("placement obstruction")},
	)
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/replacement", "--agent", "universal", "claude-code", "pi", "--copy", "--replace", "--force", "--yes"},
		Files:   fixtures,
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "Install provenance-skill") {
		t.Fatalf("failed replacement = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertPriorProvenanceState(t, observation)
	placement, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".claude", "skills", "provenance-skill", "SKILL.md"))
	if !ok || string(placement.Data) != priorProvenanceSkill {
		t.Fatalf("prior Claude placement changed: %#v", placement)
	}
}

func TestNativeSyncRequiresDedicatedReplaceAuthorizationForCrossSourceCollision(t *testing.T) {
	files := []FileFixture{
		{Root: ProjectRoot, Path: "node_modules/replacement-package/SKILL.md", Data: []byte(replacementProvenanceSkill)},
		{Root: ProjectRoot, Path: ".agents/skills/provenance-skill/SKILL.md", Data: []byte(priorProvenanceSkill)},
		{Root: ProjectRoot, Path: "skills-lock.json", Data: provenanceLock("prior-package")},
	}
	for _, test := range []struct {
		name        string
		arguments   []string
		wantCode    int
		wantContent string
	}{
		{name: "yes is insufficient", arguments: []string{"experimental_sync", "--agent", "universal", "--yes"}, wantCode: 1, wantContent: "# prior"},
		{name: "replace is explicit", arguments: []string{"experimental_sync", "--agent", "universal", "--yes", "--replace"}, wantCode: 0, wantContent: "# replacement"},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{Args: test.arguments, Files: files, Offline: true})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != test.wantCode {
				t.Fatalf("sync = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			installed, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "provenance-skill", "SKILL.md"))
			if !ok || !strings.Contains(string(installed.Data), test.wantContent) {
				t.Fatalf("sync content = %#v; want marker %q", installed, test.wantContent)
			}
			if test.wantCode == 1 && !strings.Contains(observation.Stderr, "--replace") {
				t.Fatalf("sync rejection omitted replacement guidance: %q", observation.Stderr)
			}
		})
	}
}

func assertPriorProvenanceState(t *testing.T, observation Observation) {
	t.Helper()
	installed, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "provenance-skill", "SKILL.md"))
	if !ok || string(installed.Data) != priorProvenanceSkill {
		t.Fatalf("prior content changed: %#v", installed)
	}
	wantLock := string(provenanceLock(filepath.ToSlash(filepath.Join(observation.Paths.Temp, "prior"))))
	if string(observation.Locks[ProjectLock]) != wantLock {
		t.Fatalf("prior lock changed:\nwant %s\n got %s", wantLock, observation.Locks[ProjectLock])
	}
}
