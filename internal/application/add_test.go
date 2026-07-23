package application

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EngBlock/open-skills/internal/state"
)

func TestNormalizedSkillNameCombinesUnicodeCaseAndInstallSanitization(t *testing.T) {
	for _, names := range [][]string{
		{"Case", "case"},
		{"white space", "white/space"},
		{"../safe", "safe"},
		{"café", "cafe\u0301"},
		{"Ｆｕｌｌ", "full"},
		{"STRASSE", "straße"},
	} {
		left, right := normalizedSkillName(names[0]), normalizedSkillName(names[1])
		if left != right {
			t.Errorf("normalizedSkillName(%q) = %q, normalizedSkillName(%q) = %q", names[0], left, names[1], right)
		}
	}
}

func TestRepositoryRelativePathsRemainRootedAboveASearchSubpath(t *testing.T) {
	repository := t.TempDir()
	directory := filepath.Join(repository, "catalog", "selected")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: selected\ndescription: selected\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := discoverLocalSkills(filepath.Join(repository, "catalog"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := assignRepositoryRelativePaths(skills, repository); err != nil {
		t.Fatal(err)
	}
	selected, err := selectSkillsByPath(skills, []string{"catalog/selected"})
	if err != nil || len(selected) != 1 || selected[0].Path != directory {
		t.Fatalf("repository-rooted selection = %#v, %v", selected, err)
	}
}

func TestCollisionDisplaysQuoteUntrustedControlCharacters(t *testing.T) {
	if got := displaySkillName("safe\nforged"); got != `"safe\nforged"` {
		t.Fatalf("displaySkillName = %q", got)
	}
	if got := displaySkillPath("skills/\x1b[31mforged"); got != `"skills/\x1b[31mforged"` {
		t.Fatalf("displaySkillPath = %q", got)
	}
}

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

func TestRunAddLetsInteractiveUserResolveAmbiguousNameByRepositoryPath(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	for _, fixture := range []struct {
		path, name, marker string
	}{
		{"skills/first", "same", "first"},
		{"skills/second", "SAME", "second"},
	} {
		directory := filepath.Join(source, filepath.FromSlash(fixture.path))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		contents := "---\nname: " + fixture.name + "\ndescription: collision\n---\n# " + fixture.marker + "\n"
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
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
	code := runAdd(Invocation{
		Stdin: bytes.NewBufferString("skills/second\n"), Stdout: &stdout, Stderr: &stderr, Interactive: true,
	}, []string{source, "--skill", "same", "--agent", "universal", "--yes"})
	if code != 0 || stderr.String() != "" {
		t.Fatalf("runAdd = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	installed, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "same", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installed), "# second") {
		t.Fatalf("wrong collision candidate installed: %q", installed)
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
	if err := installLocalSkill(localSkill{Name: "topology-skill", Path: source}, installationProvenance{Identity: filepath.Join(project, "skills"), URL: filepath.Join(project, "skills"), Type: "local"}, state.Project, project, project, project, []string{"openclaw"}, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
		t.Fatalf("source was removed: %v", err)
	}
}
