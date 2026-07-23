package compatibility

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func decodeSingleJSONDocument(t *testing.T, stdout string, target any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(stdout))
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("stdout is not JSON: %v: %q", err, stdout)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout contains more than one JSON document: %v: %q", err, stdout)
	}
	if strings.Contains(stdout, "\x1b") {
		t.Fatalf("JSON stdout contains terminal escapes: %q", stdout)
	}
}

func TestNativeGlobalJSONModeSupportsManagementCommands(t *testing.T) {
	target := buildShellTarget(t)

	t.Run("list accepts the global flag position", func(t *testing.T) {
		observation, err := (Harness{}).Run(context.Background(), target, Scenario{
			Args: []string{"--json", "list"}, Offline: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var output struct {
			SchemaVersion int `json:"schemaVersion"`
			Scope         string
			Skills        []any
		}
		decodeSingleJSONDocument(t, observation.Stdout, &output)
		if observation.ExitCode != 0 || observation.Stderr != "" || output.SchemaVersion != 1 || output.Scope != "project" || len(output.Skills) != 0 {
			t.Fatalf("list JSON = exit %d output %#v stderr %q", observation.ExitCode, output, observation.Stderr)
		}
	})

	t.Run("add", func(t *testing.T) {
		observation, err := (Harness{}).Run(context.Background(), target, Scenario{
			Args:    []string{"add", "{{temp}}/source", "--agent", "universal", "--json"},
			Files:   []FileFixture{{Root: TempRoot, Path: "source/SKILL.md", Data: []byte("---\nname: deterministic\ndescription: JSON fixture\n---\n")}},
			Offline: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var output struct {
			SchemaVersion int `json:"schemaVersion"`
			Scope         string
			Installed     []struct {
				Name, Path, Source, SourceType string
				Agents                         []string
				Revision                       *string
			}
		}
		decodeSingleJSONDocument(t, observation.Stdout, &output)
		if observation.ExitCode != 0 || observation.Stderr != "" || output.SchemaVersion != 1 || output.Scope != "project" || len(output.Installed) != 1 {
			t.Fatalf("add JSON = exit %d output %#v stderr %q", observation.ExitCode, output, observation.Stderr)
		}
		installed := output.Installed[0]
		if installed.Name != "deterministic" || installed.SourceType != "local" || !reflect.DeepEqual(installed.Agents, []string{"universal"}) || installed.Revision != nil || !strings.HasSuffix(installed.Path, filepath.Join(".agents", "skills", "deterministic")) {
			t.Fatalf("installed result = %#v", installed)
		}
	})

	t.Run("remove", func(t *testing.T) {
		observation, err := (Harness{}).Run(context.Background(), target, Scenario{
			Args: []string{"remove", "owned-skill", "--agent", "universal", "--yes", "--json"},
			Files: []FileFixture{
				{Root: ProjectRoot, Path: ".agents/skills/owned-skill/SKILL.md", Data: []byte(removeSkill)},
				{Root: ProjectRoot, Path: "skills-lock.json", Data: projectRemoveLock([]string{"universal"})},
			},
			Offline: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var output struct {
			SchemaVersion int `json:"schemaVersion"`
			Scope         string
			Removed       []struct {
				Name   string
				Agents []string
			}
		}
		decodeSingleJSONDocument(t, observation.Stdout, &output)
		if observation.ExitCode != 0 || observation.Stderr != "" || output.SchemaVersion != 1 || output.Scope != "project" || len(output.Removed) != 1 || output.Removed[0].Name != "owned-skill" || !reflect.DeepEqual(output.Removed[0].Agents, []string{"universal"}) {
			t.Fatalf("remove JSON = exit %d output %#v stderr %q", observation.ExitCode, output, observation.Stderr)
		}
	})

	for _, command := range []string{"check", "update"} {
		t.Run(command, func(t *testing.T) {
			observation, err := (Harness{}).Run(context.Background(), target, Scenario{
				Args: []string{command, "--project", "--json"}, Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			var output struct {
				SchemaVersion int `json:"schemaVersion"`
				Scope         string
				Results       []any
				Summary       struct{ Checked, Updated, Failed int }
			}
			decodeSingleJSONDocument(t, observation.Stdout, &output)
			if observation.ExitCode != 0 || observation.Stderr != "" || output.SchemaVersion != 1 || output.Scope != "project" || len(output.Results) != 0 || output.Summary != (struct{ Checked, Updated, Failed int }{}) {
				t.Fatalf("%s JSON = exit %d output %#v stderr %q", command, observation.ExitCode, output, observation.Stderr)
			}
		})
	}

	t.Run("trust inspection retains its documented versioned schema", func(t *testing.T) {
		observation, err := (Harness{}).Run(context.Background(), target, Scenario{
			Args: []string{"--json", "trust", "list"}, Offline: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var output struct {
			Version   int `json:"version"`
			Approvals []any
		}
		decodeSingleJSONDocument(t, observation.Stdout, &output)
		if observation.ExitCode != 0 || observation.Stderr != "" || output.Version != 1 || len(output.Approvals) != 0 {
			t.Fatalf("trust JSON = exit %d output %#v stderr %q", observation.ExitCode, output, observation.Stderr)
		}
	})
}

func TestNativeJSONFailuresUseStableCodesAndOneDocument(t *testing.T) {
	target := buildShellTarget(t)
	for _, test := range []struct {
		name     string
		args     []string
		wantCode string
	}{
		{name: "list", args: []string{"list", "--json", "--unknown"}, wantCode: "invalid_arguments"},
		{name: "list missing agent", args: []string{"list", "--json", "--agent"}, wantCode: "invalid_arguments"},
		{name: "add", args: []string{"add", "--json"}, wantCode: "invalid_arguments"},
		{name: "remove", args: []string{"remove", "--json", "--unknown"}, wantCode: "invalid_arguments"},
		{name: "check", args: []string{"check", "--json", "--unknown"}, wantCode: "invalid_arguments"},
		{name: "update", args: []string{"update", "--json", "--unknown"}, wantCode: "invalid_arguments"},
		{name: "trust", args: []string{"trust", "list", "--json", "--unknown"}, wantCode: "invalid_arguments"},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation, err := (Harness{}).Run(context.Background(), target, Scenario{Args: test.args, Offline: true})
			if err != nil {
				t.Fatal(err)
			}
			var output struct {
				SchemaVersion int `json:"schemaVersion"`
				Version       int `json:"version"`
				Error         struct{ Code, Message string }
			}
			decodeSingleJSONDocument(t, observation.Stdout, &output)
			if observation.ExitCode != 1 || output.Error.Code != test.wantCode || output.Error.Message == "" || observation.Stderr == "" {
				t.Fatalf("failure = exit %d output %#v stderr %q", observation.ExitCode, output, observation.Stderr)
			}
			if test.name == "trust" {
				if output.Version != 1 || output.SchemaVersion != 0 {
					t.Fatalf("trust failure version = %#v", output)
				}
			} else if output.SchemaVersion != 1 || output.Version != 0 {
				t.Fatalf("management failure version = %#v", output)
			}
		})
	}
}

func TestNativeJSONStateFailuresUseTheStateCodeAcrossCommands(t *testing.T) {
	target := buildShellTarget(t)
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "list", args: []string{"list", "--json"}},
		{name: "remove", args: []string{"remove", "--all", "--json"}},
		{name: "check", args: []string{"check", "--project", "--json"}},
		{name: "update", args: []string{"update", "--project", "--json"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			malformed := []byte(`{"version":1,"skills":`)
			observation, err := (Harness{}).Run(context.Background(), target, Scenario{
				Args:    test.args,
				Files:   []FileFixture{{Root: ProjectRoot, Path: "skills-lock.json", Data: malformed}},
				Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			var output struct {
				SchemaVersion int `json:"schemaVersion"`
				Error         struct{ Code, Path string }
			}
			decodeSingleJSONDocument(t, observation.Stdout, &output)
			if observation.ExitCode != 1 || output.SchemaVersion != 1 || output.Error.Code != "state_malformed" || !strings.HasSuffix(output.Error.Path, "skills-lock.json") || observation.Stderr == "" {
				t.Fatalf("%s state failure = exit %d output %#v stderr %q", test.name, observation.ExitCode, output, observation.Stderr)
			}
			if got := observation.Locks[ProjectLock]; !bytes.Equal(got, malformed) {
				t.Fatalf("%s changed malformed state: want %q got %q", test.name, malformed, got)
			}
		})
	}

	t.Run("add", func(t *testing.T) {
		malformed := []byte(`{"version":1,"skills":`)
		observation, err := (Harness{}).Run(context.Background(), target, Scenario{
			Args: []string{"add", "{{temp}}/source", "--agent", "universal", "--json"},
			Files: []FileFixture{
				{Root: ProjectRoot, Path: "skills-lock.json", Data: malformed},
				{Root: TempRoot, Path: "source/SKILL.md", Data: []byte("---\nname: fixture\ndescription: Fixture\n---\n")},
			},
			Offline: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var output struct {
			SchemaVersion int `json:"schemaVersion"`
			Error         struct{ Code, Path string }
		}
		decodeSingleJSONDocument(t, observation.Stdout, &output)
		if observation.ExitCode != 1 || output.SchemaVersion != 1 || output.Error.Code != "state_malformed" || !strings.HasSuffix(output.Error.Path, "skills-lock.json") || observation.Stderr == "" {
			t.Fatalf("add state failure = exit %d output %#v stderr %q", observation.ExitCode, output, observation.Stderr)
		}
		if got := observation.Locks[ProjectLock]; !bytes.Equal(got, malformed) {
			t.Fatalf("add changed malformed state: want %q got %q", malformed, got)
		}
	})
}

func TestNativeJSONResultsUseCanonicalOrdering(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{temp}}/source", "--skill", "beta", "alpha", "--agent", "universal", "claude-code", "--json"},
		Files: []FileFixture{
			{Root: TempRoot, Path: "source/alpha/SKILL.md", Data: []byte("---\nname: alpha\ndescription: Alpha\n---\n")},
			{Root: TempRoot, Path: "source/beta/SKILL.md", Data: []byte("---\nname: beta\ndescription: Beta\n---\n")},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Installed []struct {
			Name   string
			Agents []string
		}
	}
	decodeSingleJSONDocument(t, observation.Stdout, &output)
	if observation.ExitCode != 0 || observation.Stderr != "" || len(output.Installed) != 2 || output.Installed[0].Name != "alpha" || output.Installed[1].Name != "beta" || !reflect.DeepEqual(output.Installed[0].Agents, []string{"claude-code", "universal"}) || !reflect.DeepEqual(output.Installed[1].Agents, []string{"claude-code", "universal"}) {
		t.Fatalf("ordered add JSON = exit %d output %#v stderr %q", observation.ExitCode, output, observation.Stderr)
	}
}

func TestNativeTrustJSONFailureIsVersionedAndMachineOnly(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"trust", "list", "--json"},
		Files: []FileFixture{{
			Root: HomeRoot,
			Path: filepath.Join(".config", "open-skills", "trust.json"),
			Data: []byte(`{"version":1,"approvals":[{"source":"unsafe\u001b[31m","commit":"abc","approvedAt":"2026-01-02T03:04:05Z"}]}`),
		}},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Version int `json:"version"`
		Error   struct{ Code string }
	}
	decodeSingleJSONDocument(t, observation.Stdout, &output)
	if observation.ExitCode != 1 || output.Version != 1 || output.Error.Code != "operation_failed" || observation.Stderr == "" || strings.Contains(observation.Stdout, "\x1b") {
		t.Fatalf("trust failure = exit %d output %#v stdout %q stderr %q", observation.ExitCode, output, observation.Stdout, observation.Stderr)
	}
}

func TestNativeJSONExplicitMissingRemoveSelectorFails(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"remove", "missing", "--yes", "--json"}, Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		SchemaVersion int `json:"schemaVersion"`
		Error         struct{ Code string }
	}
	decodeSingleJSONDocument(t, observation.Stdout, &output)
	if observation.ExitCode != 1 || output.SchemaVersion != 1 || output.Error.Code != "selection_failed" || observation.Stderr == "" {
		t.Fatalf("missing remove = exit %d output %#v stderr %q", observation.ExitCode, output, observation.Stderr)
	}
}

func TestNativeJSONModeNeverPromptsOrImplicitlyAuthorizes(t *testing.T) {
	target := buildShellTarget(t)

	t.Run("add selection", func(t *testing.T) {
		observation, err := (Harness{}).Run(context.Background(), target, Scenario{
			Args:  []string{"add", "{{temp}}/source", "--json"},
			Stdin: []byte("beta\nuniversal\n"),
			Files: []FileFixture{
				{Root: TempRoot, Path: "source/alpha/SKILL.md", Data: []byte("---\nname: alpha\ndescription: Alpha\n---\n")},
				{Root: TempRoot, Path: "source/beta/SKILL.md", Data: []byte("---\nname: beta\ndescription: Beta\n---\n")},
			},
			Offline: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var output struct {
			SchemaVersion int `json:"schemaVersion"`
			Error         struct{ Code string }
		}
		decodeSingleJSONDocument(t, observation.Stdout, &output)
		if observation.ExitCode != 1 || output.Error.Code != "selection_required" || strings.Contains(observation.Stdout, "Select skills") {
			t.Fatalf("add selection = exit %d output %#v stdout %q stderr %q", observation.ExitCode, output, observation.Stdout, observation.Stderr)
		}
	})

	t.Run("remove confirmation", func(t *testing.T) {
		lock := projectRemoveLock([]string{"universal"})
		observation, err := (Harness{}).Run(context.Background(), target, Scenario{
			Args:  []string{"remove", "owned-skill", "--json"},
			Stdin: []byte("y\n"),
			Files: []FileFixture{
				{Root: ProjectRoot, Path: ".agents/skills/owned-skill/SKILL.md", Data: []byte(removeSkill)},
				{Root: ProjectRoot, Path: "skills-lock.json", Data: lock},
			},
			Offline: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var output struct {
			Error struct{ Code string }
		}
		decodeSingleJSONDocument(t, observation.Stdout, &output)
		if observation.ExitCode != 1 || output.Error.Code != "confirmation_required" || strings.Contains(observation.Stdout, "Are you sure") {
			t.Fatalf("remove confirmation = exit %d output %#v stdout %q stderr %q", observation.ExitCode, output, observation.Stdout, observation.Stderr)
		}
		if got := observation.Locks[ProjectLock]; !bytes.Equal(got, lock) {
			t.Fatalf("unconfirmed JSON remove changed state: want %q got %q", lock, got)
		}
	})
}
