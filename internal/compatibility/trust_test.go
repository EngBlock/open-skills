package compatibility

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeRemoteWellKnownAgentUseRecordsExactTrustBeforeInjection(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"use", "{{http:url}}", "--agent", "codex", "--trust", "--allow-insecure-transport"},
		HTTPRoutes: []HTTPRoute{
			{Method: http.MethodGet, Path: "/.well-known/agent-skills/index.json", Body: []byte(`{"skills":[{"name":"remote","description":"Remote skill","files":["SKILL.md"]}]}`)},
			{Method: http.MethodGet, Path: "/.well-known/agent-skills/remote/SKILL.md", Body: []byte("---\nname: remote\ndescription: Remote skill\n---\n\n# Remote\n\nDo remote work.\n")},
		},
		Commands: []CommandFixture{{Name: "codex", Stdout: "agent output\n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stdout != "agent output\n" || !strings.Contains(observation.Stderr, "Remote skill source: 127.0.0.1") || !strings.Contains(observation.Stderr, "Remote skill commit: sha256:") {
		t.Fatalf("remote agent use = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	if len(observation.SpawnedCommands) != 1 || len(observation.SpawnedCommands[0].Args) != 1 || !strings.Contains(observation.SpawnedCommands[0].Args[0], "Remote skill source: 127.0.0.1") || !strings.Contains(observation.SpawnedCommands[0].Args[0], "Remote skill commit: sha256:") || !strings.Contains(observation.SpawnedCommands[0].Args[0], "Do remote work.") {
		t.Fatalf("agent prompt did not receive provenance before instructions: %#v", observation.SpawnedCommands)
	}
	file := observation.Files[filepath.Join("home", ".config", "open-skills", "trust.json")]
	if file.Kind != FileKindRegular || file.Mode.Perm() != 0o600 {
		t.Fatalf("trust store mode = %s %o", file.Kind, file.Mode.Perm())
	}
	var store struct {
		Approvals []map[string]any `json:"approvals"`
	}
	if err := json.Unmarshal(file.Data, &store); err != nil {
		t.Fatalf("trust store is invalid: %v: %q", err, file.Data)
	}
	if len(store.Approvals) != 1 || len(store.Approvals[0]) != 3 || store.Approvals[0]["source"] != "127.0.0.1" || !strings.HasPrefix(store.Approvals[0]["commit"].(string), "sha256:") {
		t.Fatalf("trust approvals = %#v", store.Approvals)
	}
}

func TestNativeTrustListRejectsUnsanitizedStoredIdentity(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"trust", "list"},
		Files: []FileFixture{{
			Root: HomeRoot,
			Path: filepath.Join(".config", "open-skills", "trust.json"),
			Data: []byte(`{"version":1,"approvals":[{"source":"owner/repository\u001b[31m","commit":"abc123","approvedAt":"2026-01-02T03:04:05Z"}]}`),
		}},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || observation.Stdout != "" || strings.Contains(observation.Stderr, "\x1b") {
		t.Fatalf("unsafe trust list = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeTrustListEmptyJSONIsDeterministicAndOffline(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"trust", "list", "--json"},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" || observation.Stdout != "{\"version\":1,\"approvals\":[]}\n" {
		t.Fatalf("empty trust list = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeTrustListReportsApprovalsOffline(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"trust", "list", "--json"},
		Files:   trustStoreFixture(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("trust list = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	var output struct {
		Version   int `json:"version"`
		Approvals []struct {
			Source, Commit, ApprovedAt string
		} `json:"approvals"`
	}
	if err := json.Unmarshal([]byte(observation.Stdout), &output); err != nil {
		t.Fatalf("trust list returned invalid JSON: %v: %q", err, observation.Stdout)
	}
	if output.Version != 1 || len(output.Approvals) != 2 || output.Approvals[0].Source != "example.test/skills" || output.Approvals[1].Source != "owner/repository" {
		t.Fatalf("trust list output = %#v", output)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeTrustRevokeRejectsEmptyExactCommitWithoutBroadMutation(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"trust", "revoke", "owner/repository", "--commit", "", "--yes"},
		Files:   trustStoreFixture(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || observation.Stdout != "" || observation.Stderr == "" {
		t.Fatalf("empty exact revoke = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	file := observation.Files[filepath.Join("home", ".config", "open-skills", "trust.json")]
	if string(file.Data) != string(trustStoreFixture()[0].Data) {
		t.Fatalf("empty exact revoke mutated trust store: %q", file.Data)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeTrustRevokeExactCommitIsDeterministicAndOffline(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"trust", "revoke", "owner/repository", "--commit", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		Files:   trustStoreFixture(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("trust revoke = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	file := observation.Files[filepath.Join("home", ".config", "open-skills", "trust.json")]
	var remaining struct {
		Approvals []struct{ Source, Commit string } `json:"approvals"`
	}
	if err := json.Unmarshal(file.Data, &remaining); err != nil {
		t.Fatalf("remaining trust store is invalid: %v: %q", err, file.Data)
	}
	if len(remaining.Approvals) != 1 || remaining.Approvals[0].Source != "example.test/skills" {
		t.Fatalf("remaining approvals = %#v", remaining.Approvals)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeTrustBroadRevokeRequiresConfirmationOffline(t *testing.T) {
	target := buildShellTarget(t)
	harness := Harness{}
	unconfirmed, err := harness.Run(context.Background(), target, Scenario{
		Args:    []string{"trust", "revoke", "owner/repository"},
		Files:   trustStoreFixture(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unconfirmed.ExitCode != 1 || unconfirmed.Stdout != "" || unconfirmed.Stderr == "" {
		t.Fatalf("unconfirmed revoke = exit %d stdout %q stderr %q", unconfirmed.ExitCode, unconfirmed.Stdout, unconfirmed.Stderr)
	}
	assertOfflineShellObservation(t, unconfirmed)

	confirmed, err := harness.Run(context.Background(), target, Scenario{
		Args:    []string{"trust", "revoke", "owner/repository", "--yes"},
		Files:   trustStoreFixture(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.ExitCode != 0 || confirmed.Stderr != "" {
		t.Fatalf("confirmed revoke = exit %d stdout %q stderr %q", confirmed.ExitCode, confirmed.Stdout, confirmed.Stderr)
	}
	file := confirmed.Files[filepath.Join("home", ".config", "open-skills", "trust.json")]
	if string(file.Data) == "" || string(file.Data) == string(trustStoreFixture()[0].Data) {
		t.Fatalf("confirmed broad revoke did not update trust store: %q", file.Data)
	}
	assertOfflineShellObservation(t, confirmed)
}

func TestNativeTrustClearRequiresConfirmationAndRemovesStoreOffline(t *testing.T) {
	target := buildShellTarget(t)
	harness := Harness{}
	unconfirmed, err := harness.Run(context.Background(), target, Scenario{
		Args:    []string{"trust", "clear"},
		Files:   trustStoreFixture(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unconfirmed.ExitCode != 1 || unconfirmed.Stdout != "" || unconfirmed.Stderr == "" {
		t.Fatalf("unconfirmed clear = exit %d stdout %q stderr %q", unconfirmed.ExitCode, unconfirmed.Stdout, unconfirmed.Stderr)
	}

	confirmed, err := harness.Run(context.Background(), target, Scenario{
		Args:    []string{"trust", "clear", "--yes"},
		Files:   trustStoreFixture(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.ExitCode != 0 || confirmed.Stderr != "" {
		t.Fatalf("confirmed clear = exit %d stdout %q stderr %q", confirmed.ExitCode, confirmed.Stdout, confirmed.Stderr)
	}
	if _, exists := confirmed.Files[filepath.Join("home", ".config", "open-skills", "trust.json")]; exists {
		t.Fatal("trust clear left the trust store on disk")
	}
	assertOfflineShellObservation(t, unconfirmed)
	assertOfflineShellObservation(t, confirmed)
}

func trustStoreFixture() []FileFixture {
	store := []byte(`{
  "version": 1,
  "approvals": [
    {"source":"owner/repository","commit":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","approvedAt":"2026-01-02T03:04:05Z"},
    {"source":"example.test/skills","commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","approvedAt":"2026-01-01T03:04:05Z"}
  ]
}
`)
	return []FileFixture{{Root: HomeRoot, Path: filepath.Join(".config", "open-skills", "trust.json"), Data: store}}
}
