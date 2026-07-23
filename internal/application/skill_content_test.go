package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparedSkillContentDoesNotReopenMutableSourcePaths(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skill")
	outside := filepath.Join(root, "outside.txt")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(source, "payload.txt")
	if err := os.WriteFile(payload, []byte("confined\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := prepareSkillContent(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(payload); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, payload); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	destination := filepath.Join(root, "installed")
	if err := content.replaceDirectory(destination); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(filepath.Join(destination, "payload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != "confined\n" {
		t.Fatalf("installed mutable source content = %q", installed)
	}
	_, files, err := content.identity()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "payload.txt" {
		t.Fatalf("identity files = %#v", files)
	}
}

func TestPrepareSkillContentSharesLimitsAcrossSelectedSkills(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, directory := range []string{first, second} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(first, "payload"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "payload"), []byte("123456"), 0o644); err != nil {
		t.Fatal(err)
	}
	limits := resourceLimits{MaxFileBytes: 10, MaxTotalBytes: 10, MaxFiles: 2, MaxDepth: 2}
	budget := newResourceBudget(limits)
	if _, err := prepareSkillContentWithBudget(first, budget); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSkillContentWithBudget(second, budget); !isResourceLimitError(err) || !strings.Contains(err.Error(), "total limit") {
		t.Fatalf("second selected skill error = %v", err)
	}
}

func TestPrepareSkillContentCountsDereferencedAliasesAsInstalledFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("payload", filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	limits := resourceLimits{MaxFileBytes: 5, MaxTotalBytes: 9, MaxFiles: 2, MaxDepth: 2}
	if _, err := prepareSkillContentWithBudget(root, newResourceBudget(limits)); !isResourceLimitError(err) || !strings.Contains(err.Error(), "total limit") {
		t.Fatalf("dereferenced alias error = %v", err)
	}
}

func TestContentIdentityRetainsNodeModulesExclusion(t *testing.T) {
	root := t.TempDir()
	dependencies := []string{
		filepath.Join(root, "node_modules", "dependency", "index.js"),
		filepath.Join(root, "references", "node_modules", "dependency", "index.js"),
	}
	for _, dependency := range dependencies {
		if err := os.MkdirAll(filepath.Dir(dependency), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dependency, []byte("first\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, owned, err := contentIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, dependency := range dependencies {
		if err := os.WriteFile(dependency, []byte("second\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	second, _, err := contentIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(owned) != 1 || owned[0] != "SKILL.md" {
		t.Fatalf("node_modules changed identity: first %q second %q owned %#v", first, second, owned)
	}
}

func TestPrepareSkillContentAllowsFilesystemEquivalentAbsoluteLink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Source")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(root, "payload.txt")
	if err := os.WriteFile(payload, []byte("inside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alternate := strings.Replace(root, "Source", "source", 1)
	if _, err := os.Stat(alternate); err != nil {
		t.Skip("filesystem is case-sensitive")
	}
	if err := os.Symlink(filepath.Join(alternate, "payload.txt"), filepath.Join(root, "absolute.txt")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	content, err := prepareSkillContent(root)
	if err != nil {
		t.Fatalf("filesystem-equivalent absolute link was rejected: %v", err)
	}
	if len(content.files) != 2 {
		t.Fatalf("prepared files = %#v", content.files)
	}
}

func TestPrepareSkillContentRejectsSelectedDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "selected")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if _, err := prepareSkillContent(link); err == nil || !strings.Contains(err.Error(), "selected skill directory is a symbolic link") {
		t.Fatalf("selected directory symlink error = %v", err)
	}
}
