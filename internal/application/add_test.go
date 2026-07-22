package application

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EngBlock/open-skills/internal/state"
)

func TestPathsOverlap(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", "topology-skill")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if !pathsOverlap(source, filepath.Join(root, "skills", "topology-skill")) {
		t.Fatal("identical paths do not overlap")
	}
	if !pathsOverlap(filepath.Join(root, "skills"), filepath.Join(root, "skills", "topology-skill")) {
		t.Fatal("ancestor paths do not overlap")
	}
}

func TestRunAddSkipsOverlappingAgentDestination(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(project, "skills", "topology-skill")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: topology-skill\ndescription: test\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runAddLocal(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{"skills", "--agent", "openclaw", "--copy", "--yes"}); code != 0 {
		t.Fatalf("runAddLocal = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
		t.Fatalf("source was removed: %v", err)
	}
}

func TestInstallLocalSkillSkipsOverlappingAgentDestination(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(project, "skills", "topology-skill")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: topology-skill\ndescription: test\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installLocalSkill(localSkill{Name: "topology-skill", Path: source}, filepath.Join(project, "skills"), state.Project, project, project, project, []string{"openclaw"}, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
		t.Fatalf("source was removed: %v", err)
	}
}
