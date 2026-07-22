package application

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitSourceSupportsGitHubGitLabAndGenericForms(t *testing.T) {
	t.Setenv("GH_HOST", "github.example.test")
	cases := []struct {
		source                         string
		identity, url, sourceType, ref string
		subpath, filter                string
	}{
		{"owner/repository", "https://github.example.test/owner/repository.git", "https://github.example.test/owner/repository.git", "git", "", "", ""},
		{"owner/repository@demo", "https://github.example.test/owner/repository.git", "https://github.example.test/owner/repository.git", "git", "", "", "demo"},
		{"github:owner/repository/skills/demo@demo", "https://github.example.test/owner/repository.git", "https://github.example.test/owner/repository.git", "git", "", "skills/demo", "demo"},
		{"https://github.com/owner/repository/tree/release/skills/demo#branch@selected", "owner/repository", "https://github.com/owner/repository.git", "github", "release", "skills/demo", "selected"},
		{"gitlab:group/nested/repository#v1", "group/nested/repository", "https://gitlab.com/group/nested/repository.git", "gitlab", "v1", "", ""},
		{"https://gitlab.example.test/group/repository/-/tree/main/skills/demo", "group/repository", "https://gitlab.example.test/group/repository.git", "gitlab", "main", "skills/demo", ""},
		{"ssh://git@example.test:2222/group/repository.git#main@demo", "ssh://example.test:2222/group/repository.git", "ssh://example.test:2222/group/repository.git", "git", "main", "", "demo"},
		{"git@example.test:group/repository.git#main", "example.test:group/repository.git", "example.test:group/repository.git", "git", "main", "", ""},
	}
	for _, test := range cases {
		t.Run(test.source, func(t *testing.T) {
			actual, err := parseGitSource(test.source)
			if err != nil {
				t.Fatal(err)
			}
			if actual.Identity != test.identity || actual.URL != test.url || actual.Type != test.sourceType || actual.RequestedRef != test.ref || actual.Subpath != test.subpath || actual.SkillFilter != test.filter {
				t.Fatalf("parseGitSource(%q) = %#v", test.source, actual)
			}
		})
	}
}

func TestParseGitSourceRejectsUnsafeCredentialsAndSubpaths(t *testing.T) {
	for _, source := range []string{"https://user:secret@example.test/repository.git", "https://token@example.test/repository.git", "https://github.com/owner/repository/tree/main/skills/../escape", "owner/repository#"} {
		if _, err := parseGitSource(source); err == nil {
			t.Errorf("parseGitSource(%q) succeeded", source)
		}
	}
	ssh, err := parseGitSource("ssh://git@example.test/group/repository.git")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ssh.Identity, "git@") || strings.Contains(ssh.URL, "git@") || !strings.Contains(ssh.CloneURL, "git@") {
		t.Fatalf("SSH source did not separate persisted identity from clone URL: %#v", ssh)
	}
}

func TestD04GitSourceInstallsExactCommitAndCleansWorkspace(t *testing.T) {
	repository, firstCommit, secondCommit := createGitFixture(t)
	workspaceRoot := t.TempDir()
	t.Setenv("TMPDIR", workspaceRoot)
	project := t.TempDir()
	home := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))

	var stdout, stderr bytes.Buffer
	source := "file://" + filepath.ToSlash(repository) + "#" + firstCommit
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source, "--yes", "--agent", "universal"}); exit != 0 {
		t.Fatalf("runAdd = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	installed, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "fixture", "payload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != "first\n" {
		t.Fatalf("installed payload = %q; wanted the requested commit %s rather than HEAD %s", installed, firstCommit, secondCommit)
	}
	lockData, err := os.ReadFile(filepath.Join(project, "skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Skills map[string]struct {
			Source, SourceURL, SourceType, Ref, SkillPath string
			ComputedHash                                  string `json:"computedHash"`
		}
	}
	if err := json.Unmarshal(lockData, &lock); err != nil {
		t.Fatal(err)
	}
	entry := lock.Skills["fixture"]
	if entry.Source != source[:strings.LastIndex(source, "#")] || entry.SourceURL != entry.Source || entry.SourceType != "git" || entry.Ref != firstCommit || entry.SkillPath != "skills/fixture/SKILL.md" || entry.ComputedHash == "" {
		t.Fatalf("Git lock entry = %#v", entry)
	}
	stdout.Reset()
	stderr.Reset()
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source, "--global", "--yes", "--agent", "universal"}); exit != 0 {
		t.Fatalf("global runAdd = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	globalData, err := os.ReadFile(filepath.Join(home, "state", "skills", ".skill-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var global struct {
		Skills map[string]struct {
			SourceURL, SourceType, Ref, SkillPath, SkillFolderHash string
		}
	}
	if err := json.Unmarshal(globalData, &global); err != nil {
		t.Fatal(err)
	}
	globalEntry := global.Skills["fixture"]
	if globalEntry.SourceURL != entry.Source || globalEntry.SourceType != "git" || globalEntry.Ref != firstCommit || globalEntry.SkillPath != "skills/fixture/SKILL.md" || globalEntry.SkillFolderHash == "" {
		t.Fatalf("global Git lock entry = %#v", globalEntry)
	}
	matches, err := filepath.Glob(filepath.Join(workspaceRoot, "open-skills-git-*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range matches {
		if strings.Contains(match, "open-skills-git-") {
			// The fixture installs one source and its workspace must not survive.
			// Any unrelated workspace has already been cleaned by this test run.
			if info, err := os.Stat(match); err == nil && info.IsDir() {
				t.Fatalf("Git workspace remains after successful install: %s", match)
			}
		}
	}
}

func TestD03MaterializeGitSourceRejectsPlaintextTransportBeforeGitRuns(t *testing.T) {
	for _, source := range []gitSource{{URL: "http://example.test/repository.git"}, {URL: "git://example.test/repository.git"}, {URL: "ext::command"}} {
		if _, err := materializeGitSource(source); err == nil {
			t.Errorf("materializeGitSource(%q) succeeded", source.URL)
		}
	}
}

func TestD06MaterializeGitSourceCleansFailedWorkspace(t *testing.T) {
	workspaceRoot := t.TempDir()
	t.Setenv("TMPDIR", workspaceRoot)
	if _, err := materializeGitSource(gitSource{URL: "file:///missing/repository.git"}); err == nil {
		t.Fatal("materializeGitSource succeeded for a missing repository")
	}
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed Git acquisition left workspace entries: %#v", entries)
	}
}

func TestD06ExtractGitArchiveRejectsEscapingEntries(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o644, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractGitArchive(archive.Bytes(), t.TempDir()); err == nil {
		t.Fatal("extractGitArchive accepted a path escaping the checkout")
	}
	archive.Reset()
	writer = tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: "large", Mode: 0o644, Size: maxGitArchiveBytes + 1}); err != nil {
		t.Fatal(err)
	}
	if err := extractGitArchive(archive.Bytes(), t.TempDir()); err == nil {
		t.Fatal("extractGitArchive accepted an oversized entry")
	}
}

func createGitFixture(t *testing.T) (string, string, string) {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(filepath.Join(repository, "skills", "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, contents string) {
		if err := os.WriteFile(filepath.Join(repository, path), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("skills/fixture/SKILL.md", "---\nname: fixture\ndescription: Git fixture\n---\n")
	write("skills/fixture/payload.txt", "first\n")
	runFixtureGit(t, repository, "init", "-q", "-b", "main")
	runFixtureGit(t, repository, "add", ".")
	runFixtureGit(t, repository, "commit", "-q", "-m", "first")
	first := strings.TrimSpace(runFixtureGit(t, repository, "rev-parse", "HEAD"))
	write("skills/fixture/payload.txt", "second\n")
	runFixtureGit(t, repository, "add", ".")
	runFixtureGit(t, repository, "commit", "-q", "-m", "second")
	second := strings.TrimSpace(runFixtureGit(t, repository, "rev-parse", "HEAD"))
	return repository, first, second
}

func runFixtureGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}
