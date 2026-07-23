package application

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestConfirmedTrustClearRemovesMalformedStore(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	path := filepath.Join(config, "open-skills", "trust.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("malformed"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := runTrust(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{"clear", "--yes"})
	if exit != 0 || stderr.String() != "" {
		t.Fatalf("runTrust = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("trust clear left malformed store: %v", err)
	}
}

func TestInteractiveBroadTrustRevokePromptsBeforeMutation(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	path := filepath.Join(config, "open-skills", "trust.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := []byte(`{"version":1,"approvals":[{"source":"owner/repository","commit":"abc123","approvedAt":"2026-01-02T03:04:05Z"}]}`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := runTrust(Invocation{Stdin: bytes.NewBufferString("yes\n"), Stdout: &stdout, Stderr: &stderr, Interactive: true}, []string{"revoke", "owner/repository"})
	if exit != 0 || stdout.String() == "" || stderr.String() == "" {
		t.Fatalf("runTrust = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("broad revoke did not remove the only approval: %v", err)
	}
}
