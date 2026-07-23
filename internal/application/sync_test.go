package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoArgumentInstallAliasesRestoreProjectLock(t *testing.T) {
	for _, command := range []string{"install", "i"} {
		t.Run(command, func(t *testing.T) {
			project := t.TempDir()
			source := filepath.Join(project, "source")
			if err := os.MkdirAll(source, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: restored\ndescription: restored\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			lock := `{"version":1,"skills":{"restored":{"source":` + quoteJSON(source) + `,"sourceType":"local","computedHash":"old"}}}`
			if err := os.WriteFile(filepath.Join(project, "skills-lock.json"), []byte(lock), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(project)
			var stdout, stderr bytes.Buffer
			if code := Run(context.Background(), Invocation{Args: []string{command}, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); code != 0 {
				t.Fatalf("%s exit %d stdout %q stderr %q", command, code, stdout.String(), stderr.String())
			}
			if stdout.String() != "Restored restored\n" || stderr.String() != "" {
				t.Fatalf("%s output stdout %q stderr %q", command, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "restored", "SKILL.md")); err != nil {
				t.Fatalf("%s did not restore canonical skill: %v", command, err)
			}
		})
	}
}

func TestRestorePreflightsEveryRecordedSourceBeforeMutation(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(project, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: alpha\ndescription: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := `{"version":1,"skills":{"alpha":{"source":` + quoteJSON(source) + `,"sourceType":"local","computedHash":"old"},"zeta":{"source":` + quoteJSON(filepath.Join(project, "missing")) + `,"sourceType":"local","computedHash":"old"}}}`
	if err := os.WriteFile(filepath.Join(project, "skills-lock.json"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(project, "home"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(project, "state"))
	t.Chdir(project)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Invocation{Args: []string{"install"}, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr})
	if code != 1 || !strings.Contains(stderr.String(), "Cannot restore zeta offline") {
		t.Fatalf("restore with missing source = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "Restored alpha") {
		t.Fatalf("partial restore reported success: %q", stdout.String())
	}
	if _, err := os.Lstat(filepath.Join(project, ".agents", "skills", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("restore mutated alpha before complete preflight: %v", err)
	}
}

func TestInstallAliasWithSourceRemainsAnAddAlias(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(project, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: listed\ndescription: listed\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), Invocation{Args: []string{"i", source, "--list"}, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); code != 0 || stdout.String() != "listed\n" || stderr.String() != "" {
		t.Fatalf("i source --list = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
}

func quoteJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
