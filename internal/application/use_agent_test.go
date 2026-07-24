package application

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUseLaunchesPiInteractivelyWithGeneratedPrompt(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\nname: launch\ndescription: launch test fixture\n---\n\n# Launch\n"
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	promptLog := filepath.Join(t.TempDir(), "prompt")
	script := "#!/bin/sh\n" +
		"[ \"$#\" -eq 1 ] || exit 99\n" +
		"printf '%s' \"$1\" > \"$PI_PROMPT_LOG\"\n" +
		"printf 'pi test output\\n'\n" +
		"exit 23\n"
	if err := os.WriteFile(filepath.Join(bin, "pi"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("PI_PROMPT_LOG", promptLog)

	var stdout, stderr bytes.Buffer
	exit := runUse(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, Interactive: true}, []string{source, "--agent", "pi"})
	if exit != 23 || stdout.String() != "pi test output\n" || stderr.String() != "" {
		t.Fatalf("runUse = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	prompt, err := os.ReadFile(promptLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), "Use the following SKILL.md as your instructions:") || !strings.Contains(string(prompt), skillMD) {
		t.Fatalf("Pi prompt = %q", prompt)
	}
}
