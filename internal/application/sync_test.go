package application

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
	result := `"`
	for _, character := range value {
		if character == '\\' || character == '"' {
			result += `\\`
		}
		result += string(character)
	}
	return result + `"`
}
