package compatibility

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EngBlock/open-skills/internal/state"
)

const topologySkill = "---\nname: topology-skill\ndescription: topology fixture\n---\n# topology\n"

func sourceSkillFixture() FileFixture {
	return FileFixture{Root: TempRoot, Path: "source/SKILL.md", Data: []byte(topologySkill)}
}

func fileAt(observation Observation, path string) (FileState, bool) {
	relative := strings.TrimPrefix(path, observation.Paths.Root+string(filepath.Separator))
	state, ok := observation.Files[filepath.ToSlash(relative)]
	return state, ok
}

func TestNativeAddCreatesCanonicalSymlinkTopologyForExplicitAgent(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--agent", "claude-code", "--yes"},
		Files: []FileFixture{
			sourceSkillFixture(),
			{Root: ProjectRoot, Path: ".claude", Directory: true},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("add = exit %d stderr %q", observation.ExitCode, observation.Stderr)
	}
	canonical := filepath.Join(observation.Paths.Project, ".agents", "skills", "topology-skill", "SKILL.md")
	if skill, ok := fileAt(observation, canonical); !ok || skill.Kind != FileKindRegular {
		t.Fatalf("canonical skill = %#v", skill)
	}
	link, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".claude", "skills", "topology-skill"))
	if !ok || link.Kind != FileKindSymlink {
		t.Fatalf("Claude placement is not a symlink: %#v", link)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddCopyModeCreatesIndependentAgentPlacement(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/source", "--agent", "claude-code", "--copy", "--yes"},
		Files:   []FileFixture{sourceSkillFixture()},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("copy add = exit %d stderr %q", observation.ExitCode, observation.Stderr)
	}
	placement, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".claude", "skills", "topology-skill", "SKILL.md"))
	if !ok || placement.Kind != FileKindRegular {
		t.Fatalf("Claude copy placement = %#v", placement)
	}
	if _, found := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "topology-skill")); found {
		t.Fatalf("copy mode unexpectedly created canonical topology: %#v", observation.Files)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddUsesDetectedAgentAndUniversalCanonicalTopology(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--yes"},
		Files: []FileFixture{
			sourceSkillFixture(),
			{Root: HomeRoot, Path: ".claude", Directory: true},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("detected add = exit %d stderr %q", observation.ExitCode, observation.Stderr)
	}
	if _, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "topology-skill", "SKILL.md")); !ok {
		t.Fatalf("universal canonical skill missing: %#v", observation.Files)
	}
	if link, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".claude", "skills", "topology-skill")); !ok || link.Kind != FileKindSymlink {
		t.Fatalf("detected Claude link = %#v", link)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddAllRespectsGlobalAdapterAvailability(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/source", "--all", "--global"},
		Files:   []FileFixture{sourceSkillFixture()},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("global all add = exit %d stderr %q", observation.ExitCode, observation.Stderr)
	}
	if link, ok := fileAt(observation, filepath.Join(observation.Paths.Home, ".claude", "skills", "topology-skill")); !ok || link.Kind != FileKindSymlink {
		t.Fatalf("global Claude link = %#v", link)
	}
	if _, found := fileAt(observation, filepath.Join(observation.Paths.Home, "agent", "skills", "topology-skill")); found {
		t.Fatalf("global all installed Eve despite its project-only availability: %#v", observation.Files)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddInstallsEveryRetainedGlobalAdapter(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/source", "--all", "--global"},
		Files:   []FileFixture{sourceSkillFixture()},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("global all add = exit %d stderr %q", observation.ExitCode, observation.Stderr)
	}
	for _, agent := range state.AgentIDs() {
		path, universal, supported := state.AgentSkillsPath(agent, state.Global, observation.Paths.Project, observation.Paths.Home, filepath.Join(observation.Paths.Home, ".config"))
		if !supported {
			continue
		}
		if universal {
			path = filepath.Join(observation.Paths.Home, ".agents", "skills")
		}
		placement, ok := fileAt(observation, filepath.Join(path, "topology-skill"))
		if !ok {
			t.Fatalf("%s global placement missing at %s", agent, path)
		}
		if universal && placement.Kind != FileKindDirectory {
			t.Fatalf("%s universal placement = %#v", agent, placement)
		}
		if !universal && placement.Kind != FileKindSymlink {
			t.Fatalf("%s global placement = %#v", agent, placement)
		}
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddKeepsOpenClawLegacyGlobalPlacementExplicit(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--agent", "openclaw", "--global", "--yes"},
		Files: []FileFixture{
			sourceSkillFixture(),
			{Root: HomeRoot, Path: ".clawdbot", Directory: true},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("OpenClaw add = exit %d stderr %q", observation.ExitCode, observation.Stderr)
	}
	if link, ok := fileAt(observation, filepath.Join(observation.Paths.Home, ".clawdbot", "skills", "topology-skill")); !ok || link.Kind != FileKindSymlink {
		t.Fatalf("OpenClaw legacy link = %#v", link)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddRejectsUnavailableExplicitGlobalAgentBeforeMutation(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{temp}}/source", "--agent", "eve", "--global", "--yes"},
		Files:   []FileFixture{sourceSkillFixture()},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "does not support global installation") {
		t.Fatalf("global Eve add = exit %d stderr %q", observation.ExitCode, observation.Stderr)
	}
	if _, found := fileAt(observation, filepath.Join(observation.Paths.Home, ".agents", "skills", "topology-skill")); found {
		t.Fatalf("unavailable global Eve mutated installation topology: %#v", observation.Files)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddSelectsDetectedEveWithoutUniversalTopology(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--yes"},
		Files: []FileFixture{
			sourceSkillFixture(),
			{Root: ProjectRoot, Path: "agent", Directory: true},
			{Root: ProjectRoot, Path: "package.json", Data: []byte(`{"dependencies":{"eve":"1"}}`)},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("detected Eve add = exit %d stderr %q", observation.ExitCode, observation.Stderr)
	}
	if _, found := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "topology-skill")); found {
		t.Fatalf("detected Eve unexpectedly created universal topology: %#v", observation.Files)
	}
	if skill, ok := fileAt(observation, filepath.Join(observation.Paths.Project, "agent", "skills", "topology-skill", "SKILL.md")); !ok || skill.Kind != FileKindRegular {
		t.Fatalf("detected Eve placement = %#v", skill)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddTreatsMixedAgentWildcardAsAll(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--agent", "*", "claude-code", "--yes"},
		Files: []FileFixture{
			sourceSkillFixture(),
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("mixed wildcard add = exit %d stderr %q", observation.ExitCode, observation.Stderr)
	}
	if link, ok := fileAt(observation, filepath.Join(observation.Paths.Project, ".claude", "skills", "topology-skill")); !ok || link.Kind != FileKindSymlink {
		t.Fatalf("wildcard Claude placement = %#v", link)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeAddPlacesEveRootAndSubagentsAndRecordsPlacement(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--subagent", "root", "research", "writer", "--yes"},
		Files: []FileFixture{
			{Root: TempRoot, Path: "source/SKILL.md", Data: []byte("---\nname: topology-skill\ndescription: topology fixture\nlicense: MIT\nunsafe: discard\nmetadata:\n  owner: EngBlock\n  retries: 3\n---\n# topology\n")},
			{Root: ProjectRoot, Path: "agent", Directory: true},
			{Root: ProjectRoot, Path: "agent/subagents/research", Directory: true},
			{Root: ProjectRoot, Path: "agent/subagents/writer", Directory: true},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("Eve add = exit %d stderr %q", observation.ExitCode, observation.Stderr)
	}
	if _, found := fileAt(observation, filepath.Join(observation.Paths.Project, ".agents", "skills", "topology-skill")); found {
		t.Fatalf("Eve-only install unexpectedly created canonical topology: %#v", observation.Files)
	}
	for _, path := range []string{
		"agent/skills/topology-skill/SKILL.md",
		"agent/subagents/research/skills/topology-skill/SKILL.md",
		"agent/subagents/writer/skills/topology-skill/SKILL.md",
	} {
		if placement, ok := fileAt(observation, filepath.Join(observation.Paths.Project, path)); !ok || placement.Kind != FileKindRegular {
			t.Fatalf("Eve placement %s = %#v", path, placement)
		} else if strings.Contains(string(placement.Data), "name: topology-skill") || strings.Contains(string(placement.Data), "unsafe:") || strings.Contains(string(placement.Data), "retries:") {
			t.Fatalf("Eve placement retained unsupported frontmatter: %q", placement.Data)
		} else if !strings.Contains(string(placement.Data), "license: MIT") || !strings.Contains(string(placement.Data), "owner: EngBlock") {
			t.Fatalf("Eve placement lost supported frontmatter: %q", placement.Data)
		}
	}
	var lock struct {
		Skills map[string]struct {
			Subagents []string `json:"subagents"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(observation.Locks[ProjectLock], &lock); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lock.Skills["topology-skill"].Subagents, ","); got != ",research,writer" {
		t.Fatalf("recorded Eve subagents = %q", got)
	}
	assertOfflineShellObservation(t, observation)
}
