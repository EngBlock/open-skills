package application

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitCloneTimeoutEnvironmentPrecedence(t *testing.T) {
	t.Run("canonical wins a conflict", func(t *testing.T) {
		t.Setenv(cloneTimeoutEnvironment, "1250")
		t.Setenv(legacyCloneTimeoutEnvironment, "9000")
		if actual := gitCloneTimeout(); actual.String() != "1.25s" {
			t.Fatalf("gitCloneTimeout() = %s; want 1.25s", actual)
		}
	})
	t.Run("present empty canonical disables legacy fallback", func(t *testing.T) {
		t.Setenv(cloneTimeoutEnvironment, "")
		t.Setenv(legacyCloneTimeoutEnvironment, "9000")
		if actual := gitCloneTimeout(); actual.String() != "5m0s" {
			t.Fatalf("gitCloneTimeout() = %s; want 5m0s", actual)
		}
	})
	t.Run("legacy remains supported", func(t *testing.T) {
		previous, existed := os.LookupEnv(cloneTimeoutEnvironment)
		if err := os.Unsetenv(cloneTimeoutEnvironment); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(cloneTimeoutEnvironment, previous)
			} else {
				_ = os.Unsetenv(cloneTimeoutEnvironment)
			}
		})
		t.Setenv(legacyCloneTimeoutEnvironment, "2250")
		if actual := gitCloneTimeout(); actual.String() != "2.25s" {
			t.Fatalf("gitCloneTimeout() = %s; want 2.25s", actual)
		}
	})
}

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
	for _, source := range []string{"https://user:secret@example.test/repository.git", "https://token@example.test/repository.git", "https://github.com/owner/repository/tree/main/skills/../escape", "https://github.com/owner/repo%1B%5B31m", "owner/repository#", "git@example.test:repository\x1b[31m"} {
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
	if _, err := parseGitSource("https://example.test/%zz?access_token=review-secret"); err == nil || strings.Contains(err.Error(), "review-secret") {
		t.Fatalf("malformed source error was not credential-safe: %v", err)
	}
	query, err := parseGitSource("https://example.test/repository.git?access_token=secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query.Identity, "secret") || strings.Contains(query.URL, "secret") || !strings.Contains(query.CloneURL, "secret") {
		t.Fatalf("HTTP source did not separate query credentials from sanitized identity: %#v", query)
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

func TestGitSourceInstallsConfinedRepositorySymlink(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	skill := filepath.Join(repository, "skills", "fixture")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: fixture\ndescription: Git fixture\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "payload.txt"), []byte("confined\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("payload.txt", filepath.Join(skill, "alias.txt")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	runFixtureGit(t, repository, "init", "-q", "-b", "main")
	runFixtureGit(t, repository, "add", ".")
	runFixtureGit(t, repository, "commit", "-q", "-m", "fixture")
	commit := strings.TrimSpace(runFixtureGit(t, repository, "rev-parse", "HEAD"))

	project, home := t.TempDir(), t.TempDir()
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
	source := "file://" + filepath.ToSlash(repository) + "#" + commit
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source, "--yes", "--agent", "universal"}); exit != 0 {
		t.Fatalf("runAdd = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	installed := filepath.Join(project, ".agents", "skills", "fixture", "alias.txt")
	info, err := os.Lstat(installed)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || string(data) != "confined\n" {
		t.Fatalf("installed Git symlink = mode %v content %q", info.Mode(), data)
	}
}

func TestGitSelectedContentLimitsIgnoreUnselectedRepositoryFile(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	skill := filepath.Join(repository, "skills", "small")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: small\ndescription: small skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "unselected.bin"), bytes.Repeat([]byte{'x'}, int(defaultMaxFileBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	deepIgnored := filepath.Join(repository, "node_modules")
	for depth := 0; depth < defaultMaxDepth+1; depth++ {
		deepIgnored = filepath.Join(deepIgnored, "nested")
	}
	if err := os.MkdirAll(deepIgnored, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deepIgnored, "ignored.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, repository, "init", "-q", "-b", "main")
	runFixtureGit(t, repository, "add", ".")
	runFixtureGit(t, repository, "commit", "-q", "-m", "fixture")

	project, home := t.TempDir(), t.TempDir()
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
	source := "file://" + filepath.ToSlash(repository)
	arguments := []string{source, "--skill", "small", "--agent", "universal", "--yes"}
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, arguments); exit != 0 {
		t.Fatalf("selected Git install = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "small", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteResourceOverrideRaisesGitAcquisitionLimitsNonInteractively(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "SKILL.md"), []byte("---\nname: large\ndescription: large Git skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const payloadSize = 33<<20 + 1
	if err := os.WriteFile(filepath.Join(repository, "payload.bin"), bytes.Repeat([]byte{'x'}, payloadSize), 0o644); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, repository, "init", "-q", "-b", "main")
	runFixtureGit(t, repository, "add", ".")
	runFixtureGit(t, repository, "commit", "-q", "-m", "fixture")
	commit := strings.TrimSpace(runFixtureGit(t, repository, "rev-parse", "HEAD"))

	project, home := t.TempDir(), t.TempDir()
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
	source := "file://" + filepath.ToSlash(repository) + "#" + commit
	var stdout, stderr bytes.Buffer
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source, "--yes", "--agent", "universal"}); exit != 1 || !strings.Contains(stderr.String(), "--max-file-bytes") {
		t.Fatalf("default runAdd = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "large")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default resource failure left installed content: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "skills-lock.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default resource failure left a lock: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	arguments := []string{source, "--yes", "--agent", "universal", "--max-file-bytes", "35651585", "--max-total-bytes", "36700160"}
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, arguments); exit != 0 {
		t.Fatalf("override runAdd = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if info, err := os.Stat(filepath.Join(project, ".agents", "skills", "large", "payload.bin")); err != nil || info.Size() != payloadSize {
		t.Fatalf("overridden payload = size %v error %v", func() int64 {
			if info == nil {
				return -1
			}
			return info.Size()
		}(), err)
	}

	if err := os.WriteFile(filepath.Join(repository, "payload.bin"), bytes.Repeat([]byte{'y'}, payloadSize), 0o644); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, repository, "add", ".")
	runFixtureGit(t, repository, "commit", "-q", "-m", "update large payload")
	stdout.Reset()
	stderr.Reset()
	updateArguments := []string{"update", "--project", "--yes", "--max-file-bytes", "35651585", "--max-total-bytes", "36700160"}
	if exit := Run(nil, Invocation{Args: updateArguments, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 0 || !strings.Contains(stdout.String(), "Updated 1 skill(s)") {
		t.Fatalf("override update = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	installed, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "large", "payload.bin"))
	if err != nil || len(installed) != payloadSize || installed[0] != 'y' {
		t.Fatalf("updated payload = length %d first byte %q error %v", len(installed), func() byte {
			if len(installed) == 0 {
				return 0
			}
			return installed[0]
		}(), err)
	}
}

func TestD03MaterializeGitSourceRejectsPlaintextTransportBeforeGitRuns(t *testing.T) {
	for _, source := range []gitSource{{URL: "http://example.test/repository.git"}, {URL: "git://example.test/repository.git"}, {URL: "ext::command"}} {
		if _, err := materializeGitSource(source, defaultResourceLimits()); err == nil {
			t.Errorf("materializeGitSource(%q) succeeded", source.URL)
		}
	}
}

func TestMaterializeGitSourceRedactsQueryCredentialsFromErrors(t *testing.T) {
	source, err := parseGitSource("file:///missing/repository.git?access_token=review-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := materializeGitSource(source, defaultResourceLimits()); err == nil || strings.Contains(err.Error(), "review-secret") {
		t.Fatalf("Git acquisition error was not credential-safe: %v", err)
	}
}

func TestD06MaterializeGitSourceCleansFailedWorkspace(t *testing.T) {
	workspaceRoot := t.TempDir()
	t.Setenv("TMPDIR", workspaceRoot)
	if _, err := materializeGitSource(gitSource{URL: "file:///missing/repository.git"}, defaultResourceLimits()); err == nil {
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

func TestExtractGitArchivePreservesSymlinksForConfinement(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for _, header := range []*tar.Header{
		{Name: "skill/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "skill/payload.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len("inside\n"))},
	} {
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Name == "skill/payload.txt" {
			if _, err := writer.Write([]byte("inside\n")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.WriteHeader(&tar.Header{Name: "skill/alias.txt", Typeflag: tar.TypeSymlink, Linkname: "payload.txt", Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if err := extractGitArchive(archive.Bytes(), destination); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(destination, "skill", "alias.txt")
	info, err := os.Lstat(alias)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("archive symlink mode = %v", info.Mode())
	}
	target, err := os.Readlink(alias)
	if err != nil || target != "payload.txt" {
		t.Fatalf("archive symlink target = %q, %v", target, err)
	}
}

func TestArchiveSymlinkFallbackDereferencesConfinedContent(t *testing.T) {
	root := t.TempDir()
	skill := filepath.Join(root, "skill")
	if err := os.MkdirAll(filepath.Join(skill, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "payload.txt"), []byte("inside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "references", "guide.md"), []byte("guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	links := []archiveSymlink{
		{path: filepath.Join(skill, "alias.txt"), target: "payload.txt"},
		{path: filepath.Join(skill, "reference-copy"), target: "references"},
	}
	if err := materializeArchiveLinkFallbacks(root, links); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{
		filepath.Join(skill, "alias.txt"):                  "inside\n",
		filepath.Join(skill, "reference-copy", "guide.md"): "guide\n",
	} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink != 0 || string(data) != expected {
			t.Fatalf("fallback content %s = mode %v data %q", path, info.Mode(), data)
		}
	}
}

func TestGitLinkFallbacksIgnoreUnselectedRepositoryLinks(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	unselected := filepath.Join(root, "unselected")
	for _, directory := range []string{selected, unselected} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(selected, "payload.txt"), []byte("inside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := gitWorkspace{Root: root, FallbackLinks: []archiveSymlink{
		{path: filepath.Join(selected, "alias.txt"), target: "payload.txt"},
		{path: filepath.Join(unselected, "escape"), target: "../outside"},
	}}
	if err := materializeGitLinkFallbacksForSkills(&workspace, []localSkill{{Name: "selected", Path: selected}}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(selected, "alias.txt")); err != nil || string(data) != "inside\n" {
		t.Fatalf("selected fallback = %q, %v", data, err)
	}
	if len(workspace.FallbackLinks) != 1 || workspace.FallbackLinks[0].path != filepath.Join(unselected, "escape") {
		t.Fatalf("remaining fallbacks = %#v", workspace.FallbackLinks)
	}
	if _, err := os.Lstat(filepath.Join(unselected, "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unselected fallback was materialized: %v", err)
	}
}

func TestArchiveSymlinkFallbackRejectsParentBrokenAndCyclicLinks(t *testing.T) {
	for name, links := range map[string][]archiveSymlink{
		"parent": {{path: filepath.Join(t.TempDir(), "skill", "link"), target: "../outside"}},
		"broken": {{path: filepath.Join(t.TempDir(), "skill", "link"), target: "missing"}},
	} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Dir(filepath.Dir(links[0].path))
			if err := os.MkdirAll(filepath.Dir(links[0].path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := materializeArchiveLinkFallbacks(root, links); err == nil {
				t.Fatalf("%s fallback succeeded", name)
			}
		})
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	cycle := []archiveSymlink{
		{path: filepath.Join(root, "skill", "first"), target: "second"},
		{path: filepath.Join(root, "skill", "second"), target: "first"},
	}
	if err := materializeArchiveLinkFallbacks(root, cycle); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("cyclic fallback error = %v", err)
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
	if err := writer.WriteHeader(&tar.Header{Name: "large", Mode: 0o644, Size: defaultMaxFileBytes + 1}); err != nil {
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
