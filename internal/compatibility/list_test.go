package compatibility

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativeListReadsNPMProjectStateOffline(t *testing.T) {
	target := buildShellTarget(t)
	fixtures := stateFixtureFiles(t, "npm-0.1.2/project", ProjectRoot)
	observation, err := (Harness{}).Run(context.Background(), target, Scenario{
		Args:    []string{"list"},
		Files:   fixtures,
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("list = exit %d, stdout %q, stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	for _, text := range []string{"Project Skills", "project-alpha", ".agents", "EngBlock/project-skills"} {
		if !strings.Contains(observation.Stdout, text) {
			t.Errorf("list stdout does not contain %q: %q", text, observation.Stdout)
		}
	}
	wantLock, err := os.ReadFile(filepath.Join("testdata", "state", "npm-0.1.2", "project", "skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := observation.Locks[ProjectLock]; string(got) != string(wantLock) {
		t.Fatalf("project lock changed:\nwant %q\n got %q", wantLock, got)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeListJSONIsVersionedDeterministicAndMachineOnly(t *testing.T) {
	target := buildShellTarget(t)
	observation, err := (Harness{}).Run(context.Background(), target, Scenario{
		Args:    []string{"list", "--json"},
		Files:   stateFixtureFiles(t, "npm-0.1.2/project", ProjectRoot),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("list --json = exit %d, stdout %q, stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	var output struct {
		SchemaVersion int `json:"schemaVersion"`
		Scope         string
		Skills        []struct {
			Name       string
			Path       string
			Scope      string
			Agents     []string
			Source     *string
			SourceURL  *string `json:"sourceUrl"`
			SourceType *string `json:"sourceType"`
		}
	}
	if err := json.Unmarshal([]byte(observation.Stdout), &output); err != nil {
		t.Fatalf("stdout is not machine-only JSON: %v\n%s", err, observation.Stdout)
	}
	if output.SchemaVersion != 1 || output.Scope != "project" {
		t.Fatalf("JSON envelope = schema %d scope %q", output.SchemaVersion, output.Scope)
	}
	if len(output.Skills) != 2 || output.Skills[0].Name != "project-alpha" || output.Skills[1].Name != "project-zeta" {
		t.Fatalf("skills are not deterministic: %#v", output.Skills)
	}
	alpha := output.Skills[0]
	if alpha.Scope != "project" || alpha.Source == nil || *alpha.Source != "EngBlock/project-skills" || alpha.SourceURL == nil || *alpha.SourceURL != "https://github.com/EngBlock/project-skills" || alpha.SourceType == nil || *alpha.SourceType != "github" {
		t.Fatalf("project-alpha JSON = %#v", alpha)
	}
	if len(alpha.Agents) != 0 || strings.Contains(observation.Stdout, "\x1b[") {
		t.Fatalf("JSON contains unexpected agents or ANSI: %#v", alpha)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeListHumanOutputGroupsGlobalPluginSkills(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"list", "-g"}, Files: stateFixtureFiles(t, "npm-0.1.2/global", HomeRoot), Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("global list = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	for _, text := range []string{"Global Skills", "Native Tools", "npm-global-beta", "~", "EngBlock/global-skills"} {
		if !strings.Contains(observation.Stdout, text) {
			t.Errorf("human output does not contain %q: %q", text, observation.Stdout)
		}
	}
}

func TestNativeListReadsNPMGlobalStateFromXDGLocation(t *testing.T) {
	target := buildShellTarget(t)
	observation, err := (Harness{}).Run(context.Background(), target, Scenario{
		Args:    []string{"ls", "--global", "--json"},
		Files:   stateFixtureFiles(t, "npm-0.1.2/global", HomeRoot),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("ls --global --json = exit %d, stdout %q, stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	var output struct {
		SchemaVersion int `json:"schemaVersion"`
		Scope         string
		Skills        []struct {
			Name      string
			Scope     string
			Source    *string
			SourceURL *string `json:"sourceUrl"`
		}
	}
	if err := json.Unmarshal([]byte(observation.Stdout), &output); err != nil {
		t.Fatal(err)
	}
	if output.SchemaVersion != 1 || output.Scope != "global" || len(output.Skills) != 1 {
		t.Fatalf("global JSON = %#v", output)
	}
	skill := output.Skills[0]
	if skill.Name != "npm-global-beta" || skill.Scope != "global" || skill.Source == nil || *skill.Source != "EngBlock/global-skills" || skill.SourceURL == nil || *skill.SourceURL != "https://github.com/EngBlock/global-skills" {
		t.Fatalf("global skill = %#v", skill)
	}
	if _, ok := observation.Locks[XDGGlobalLock]; !ok {
		t.Fatal("XDG global lock was not recognized")
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeListReadsUpstreamProjectAndLegacyGlobalStateWithoutRewriting(t *testing.T) {
	target := buildShellTarget(t)
	tests := []struct {
		name         string
		fixture      string
		root         FixtureRoot
		args         []string
		env          map[string]string
		want         []string
		lockLocation LockLocation
		lockPath     string
	}{
		{
			name:         "project",
			fixture:      "upstream-v1.5.20/project",
			root:         ProjectRoot,
			args:         []string{"list"},
			want:         []string{"Project Skills", "upstream-project", "vercel-labs/skills"},
			lockLocation: ProjectLock,
			lockPath:     "skills-lock.json",
		},
		{
			name:         "legacy global",
			fixture:      "upstream-v1.5.20/global",
			root:         HomeRoot,
			args:         []string{"list", "-g"},
			env:          map[string]string{"XDG_STATE_HOME": ""},
			want:         []string{"Global Skills", "upstream-global", "vercel-labs/skills"},
			lockLocation: LegacyGlobalLock,
			lockPath:     filepath.Join(".agents", ".skill-lock.json"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation, err := (Harness{}).Run(context.Background(), target, Scenario{
				Args: test.args, Env: test.env, Files: stateFixtureFiles(t, test.fixture, test.root), Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != 0 || observation.Stderr != "" {
				t.Fatalf("list = exit %d, stdout %q, stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			for _, text := range test.want {
				if !strings.Contains(observation.Stdout, text) {
					t.Errorf("stdout does not contain %q: %q", text, observation.Stdout)
				}
			}
			wantLock, err := os.ReadFile(filepath.Join("testdata", "state", filepath.FromSlash(test.fixture), test.lockPath))
			if err != nil {
				t.Fatal(err)
			}
			if got := observation.Locks[test.lockLocation]; string(got) != string(wantLock) {
				t.Fatalf("lock was rewritten:\nwant %q\n got %q", wantLock, got)
			}
			assertOfflineShellObservation(t, observation)
		})
	}
}

func TestNativeListAttributesSharedAgentDirectoryToDetectedOwner(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"list", "--json"},
		Files: []FileFixture{
			{Root: HomeRoot, Path: ".qoder-cn", Directory: true},
			{Root: ProjectRoot, Path: ".qoder/skills/shared-skill/SKILL.md", Data: []byte("---\nname: shared-skill\ndescription: Shared path skill\n---\n")},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Skills []struct{ Agents []string }
	}
	if err := json.Unmarshal([]byte(observation.Stdout), &output); err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || len(output.Skills) != 1 || !reflect.DeepEqual(output.Skills[0].Agents, []string{"Qoder CN"}) {
		t.Fatalf("shared directory attribution = exit %d %#v", observation.ExitCode, output.Skills)
	}
}

func TestNativeListOrdersAgentsLikeBaselineRegistry(t *testing.T) {
	fixtures := []FileFixture{
		{Root: ProjectRoot, Path: ".agents/skills/ordered/SKILL.md", Data: []byte("---\nname: ordered\ndescription: Agent ordering\n---\n")},
		{Root: ProjectRoot, Path: ".jazz/skills/ordered", Symlink: "{{project}}/.agents/skills/ordered"},
		{Root: ProjectRoot, Path: ".iflow/skills/ordered", Symlink: "{{project}}/.agents/skills/ordered"},
		{Root: ProjectRoot, Path: ".neovate/skills/ordered", Symlink: "{{project}}/.agents/skills/ordered"},
		{Root: HomeRoot, Path: ".config/opencode", Directory: true},
	}
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"list", "--json"}, Files: fixtures, Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Skills []struct{ Agents []string }
	}
	if err := json.Unmarshal([]byte(observation.Stdout), &output); err != nil {
		t.Fatal(err)
	}
	want := []string{"Jazz", "iFlow CLI", "OpenCode", "Neovate"}
	if observation.ExitCode != 0 || len(output.Skills) != 1 || !reflect.DeepEqual(output.Skills[0].Agents, want) {
		t.Fatalf("agent order = exit %d %#v; want %#v", observation.ExitCode, output.Skills, want)
	}
}

func TestNativeListRetainsAgentFilters(t *testing.T) {
	fixtures := stateFixtureFiles(t, "npm-0.1.2/project", ProjectRoot)
	fixtures = append(fixtures,
		FileFixture{Root: HomeRoot, Path: ".claude", Directory: true},
		FileFixture{Root: HomeRoot, Path: ".cursor", Directory: true},
		FileFixture{Root: ProjectRoot, Path: ".claude/skills/project-alpha", Symlink: "{{project}}/.agents/skills/project-alpha"},
	)
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"list", "--json", "--agent", "claude-code"}, Files: fixtures, Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("filtered list = exit %d, stdout %q, stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	var output struct {
		Skills []struct {
			Name   string
			Agents []string
		}
	}
	if err := json.Unmarshal([]byte(observation.Stdout), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Skills) != 2 || output.Skills[0].Name != "project-alpha" || len(output.Skills[0].Agents) != 1 || output.Skills[0].Agents[0] != "Claude Code" {
		t.Fatalf("filtered project-alpha = %#v", output.Skills)
	}
	if len(output.Skills[1].Agents) != 0 {
		t.Fatalf("filter leaked another agent: %#v", output.Skills[1])
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeListDoesNotInferEveFromSubagentShapedDirectories(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"list", "--json", "-a", "eve"},
		Files:   []FileFixture{{Root: ProjectRoot, Path: "agent/subagents/research/skills/not-eve/SKILL.md", Data: []byte("---\nname: not-eve\ndescription: Not an Eve project\n---\n")}},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct{ Skills []any }
	if err := json.Unmarshal([]byte(observation.Stdout), &output); err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || len(output.Skills) != 0 {
		t.Fatalf("non-Eve project reported Eve skills: exit %d %#v", observation.ExitCode, output.Skills)
	}
}

func TestNativeListAgentFilterIncludesEveSubagentSkills(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"list", "--json", "-a", "eve"},
		Files: []FileFixture{
			{Root: ProjectRoot, Path: "package.json", Data: []byte(`{"dependencies":{"eve":"1.0.0"}}`)},
			{Root: ProjectRoot, Path: "agent/subagents/research/skills/subagent-skill/SKILL.md", Data: []byte("---\nname: subagent-skill\ndescription: Eve subagent skill\n---\n")},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Skills []struct {
			Name   string
			Agents []string
		}
	}
	if err := json.Unmarshal([]byte(observation.Stdout), &output); err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || len(output.Skills) != 1 || output.Skills[0].Name != "subagent-skill" || !reflect.DeepEqual(output.Skills[0].Agents, []string{"Eve"}) {
		t.Fatalf("Eve filtered output = exit %d %#v stderr %q", observation.ExitCode, output, observation.Stderr)
	}
}

func TestNativeListHumanOutputSanitizesUntrustedStateMetadata(t *testing.T) {
	lock := []byte(`{"version":1,"skills":{"safe-name":{"source":"owner/\nFORGED\u001b[31m","sourceType":"github","computedHash":"hash"}}}`)
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"list"},
		Files: []FileFixture{
			{Root: ProjectRoot, Path: "skills-lock.json", Data: lock},
			{Root: ProjectRoot, Path: ".agents/skills/safe-name/SKILL.md", Data: []byte("---\nname: safe-name\ndescription: safe\n---\n")},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("list = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	if !strings.Contains(observation.Stdout, "Source: owner/ FORGED") || strings.Contains(observation.Stdout, "\nFORGED") || strings.Contains(observation.Stdout, "\x1b") {
		t.Fatalf("unsafe human metadata: %q", observation.Stdout)
	}
}

func TestNativeListJSONRejectsInvalidAgentWithStructuredError(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"list", "--json", "-a", "not-an-agent"}, Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		SchemaVersion int `json:"schemaVersion"`
		Error         struct{ Code string }
	}
	if err := json.Unmarshal([]byte(observation.Stdout), &output); err != nil {
		t.Fatalf("invalid-agent stdout is not JSON: %v: %q", err, observation.Stdout)
	}
	if observation.ExitCode != 1 || output.SchemaVersion != 1 || output.Error.Code != "invalid_agent" {
		t.Fatalf("invalid-agent result = exit %d output %#v", observation.ExitCode, output)
	}
	if !strings.Contains(observation.Stderr, "Invalid agents: not-an-agent") || !strings.Contains(observation.Stderr, "Valid agents:") {
		t.Fatalf("missing human diagnostic on stderr: %q", observation.Stderr)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeListRejectsMalformedStateWithoutChangingIt(t *testing.T) {
	malformed := []byte("{\n  \"version\": 1,\n  \"skills\": {\n<<<<<<< ours\n")
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"list", "--json"},
		Files:   []FileFixture{{Root: ProjectRoot, Path: "skills-lock.json", Data: malformed}},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 {
		t.Fatalf("malformed state exit = %d, stdout %q, stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	var output struct {
		SchemaVersion int `json:"schemaVersion"`
		Error         struct {
			Code string
			Path string
		}
	}
	if err := json.Unmarshal([]byte(observation.Stdout), &output); err != nil {
		t.Fatalf("JSON failure stdout is not machine-readable: %v\n%s", err, observation.Stdout)
	}
	if output.SchemaVersion != 1 || output.Error.Code != "state_malformed" || !strings.HasSuffix(output.Error.Path, "skills-lock.json") {
		t.Fatalf("malformed JSON error = %#v", output)
	}
	if !strings.Contains(observation.Stderr, "not changed") || !strings.Contains(observation.Stderr, "back up") {
		t.Fatalf("missing recovery guidance: %q", observation.Stderr)
	}
	if got := observation.Locks[ProjectLock]; string(got) != string(malformed) {
		t.Fatalf("malformed state changed:\nwant %q\n got %q", malformed, got)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeListRejectsUnsupportedAndStructurallyInvalidState(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		root     FixtureRoot
		path     string
		data     string
		wantCode string
		location LockLocation
	}{
		{name: "older project", args: []string{"list", "--json"}, root: ProjectRoot, path: "skills-lock.json", data: `{"version":0,"skills":{}}`, wantCode: "state_version_older", location: ProjectLock},
		{name: "newer project", args: []string{"list", "--json"}, root: ProjectRoot, path: "skills-lock.json", data: `{"version":2,"skills":{}}`, wantCode: "state_version_newer", location: ProjectLock},
		{name: "older global", args: []string{"list", "-g", "--json"}, root: HomeRoot, path: ".local/state/skills/.skill-lock.json", data: `{"version":2,"skills":{}}`, wantCode: "state_version_older", location: XDGGlobalLock},
		{name: "newer global", args: []string{"list", "-g", "--json"}, root: HomeRoot, path: ".local/state/skills/.skill-lock.json", data: `{"version":4,"skills":{}}`, wantCode: "state_version_newer", location: XDGGlobalLock},
		{name: "invalid project entry", args: []string{"list", "--json"}, root: ProjectRoot, path: "skills-lock.json", data: `{"version":1,"skills":{"broken":{"source":7}}}`, wantCode: "state_malformed", location: ProjectLock},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := []byte(test.data)
			observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
				Args: test.args, Files: []FileFixture{{Root: test.root, Path: test.path, Data: data}}, Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			var output struct {
				SchemaVersion int `json:"schemaVersion"`
				Error         struct{ Code string }
			}
			if err := json.Unmarshal([]byte(observation.Stdout), &output); err != nil {
				t.Fatalf("failure stdout is not JSON: %v: %q", err, observation.Stdout)
			}
			if observation.ExitCode != 1 || output.SchemaVersion != 1 || output.Error.Code != test.wantCode {
				t.Fatalf("failure = exit %d output %#v stderr %q", observation.ExitCode, output, observation.Stderr)
			}
			if got := observation.Locks[test.location]; string(got) != string(data) {
				t.Fatalf("rejected state changed: want %q got %q", data, got)
			}
			if !strings.Contains(observation.Stderr, "not changed") {
				t.Fatalf("missing preservation guidance: %q", observation.Stderr)
			}
		})
	}
}

func stateFixtureFiles(t *testing.T, name string, root FixtureRoot) []FileFixture {
	t.Helper()
	base := filepath.Join("testdata", "state", filepath.FromSlash(name))
	fixtures := []FileFixture{}
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		fixtures = append(fixtures, FileFixture{Root: root, Path: relative, Data: data})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixtures
}
