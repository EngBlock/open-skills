package application

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EngBlock/open-skills/internal/state"
)

func TestParseUpdateOptionsAcceptsResourceOverrides(t *testing.T) {
	options := parseUpdateOptions([]string{"--project", "--yes", "--max-file-bytes", "11534336", "--max-total-bytes", "209715200", "--max-files", "6000", "--max-depth", "25"})
	if options.ParseError != nil {
		t.Fatal(options.ParseError)
	}
	if !options.Project || !options.Yes || len(options.Skills) != 0 || options.Limits.MaxFileBytes != 11534336 || options.Limits.MaxTotalBytes != 209715200 || options.Limits.MaxFiles != 6000 || options.Limits.MaxDepth != 25 {
		t.Fatalf("parsed update options = %#v", options)
	}
}

func TestCheckReportsUpdateWithoutChangingInstalledSkill(t *testing.T) {
	repository, first, _ := createGitFixture(t)
	project, home := updateTestDirectories(t)
	installGitFixture(t, project, home, repository, first, "universal")

	var stdout, stderr bytes.Buffer
	if exit := Run(nil, Invocation{Args: []string{"check", "--project", "--yes"}, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 0 {
		t.Fatalf("check = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	payload, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "fixture", "payload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "first\n" {
		t.Fatalf("check changed installed payload to %q", payload)
	}
	if !strings.Contains(stdout.String(), "Update available: fixture") {
		t.Fatalf("check output %q does not report the update", stdout.String())
	}
}

func TestMissingUpstreamUpdateProtectsUnrecordedEveCopies(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	root := filepath.Join(project, "agent", "skills", "fixture")
	unrecorded := filepath.Join(project, "agent", "subagents", "secret", "skills", "fixture")
	writeTestFile(t, filepath.Join(root, "SKILL.md"), "original", 0o644)
	writeTestFile(t, filepath.Join(unrecorded, "notes.txt"), "local work", 0o644)
	action := gitUpdateAction{
		name: "fixture", remove: true,
		entry: state.LockEntry{
			Agents: []string{"eve"},
			InstalledPlacements: map[string]state.InstalledPlacement{
				"eve:root": captureTestPlacement(t, root, "copy"),
			},
		},
	}
	changes, err := updateActionLocalChanges(action, state.Snapshot{}, state.Project, project, home)
	if err != nil {
		t.Fatal(err)
	}
	if !containsLocalChange(changes, "untracked", unrecorded) {
		t.Fatalf("missing-upstream update did not protect unrecorded Eve copy: %#v", changes)
	}
}

func TestUpdateRequiresForceBeforeOverwritingLocalChanges(t *testing.T) {
	repository, first, second := createGitFixture(t)
	project, home := updateTestDirectories(t)
	installGitFixture(t, project, home, repository, first, "universal")

	payloadPath := filepath.Join(project, ".agents", "skills", "fixture", "payload.txt")
	if err := os.WriteFile(payloadPath, []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	arguments := []string{"update", "--project", "--yes"}
	if exit := Run(nil, Invocation{Args: arguments, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 1 {
		t.Fatalf("unforced update = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--force") {
		t.Fatalf("unforced update omitted force guidance: %q", stderr.String())
	}
	payload, err := os.ReadFile(payloadPath)
	if err != nil || string(payload) != "local edit\n" {
		t.Fatalf("rejected update changed local content: %q, %v", payload, err)
	}
	if entry := readUpdateLock(t, filepath.Join(project, "skills-lock.json"), "fixture"); entry.Ref != first {
		t.Fatalf("rejected update changed lock ref to %q", entry.Ref)
	}

	stdout.Reset()
	stderr.Reset()
	arguments = append(arguments, "--force")
	if exit := Run(nil, Invocation{Args: arguments, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 0 {
		t.Fatalf("forced update = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	payload, err = os.ReadFile(payloadPath)
	if err != nil || string(payload) != "second\n" {
		t.Fatalf("forced update content = %q, %v", payload, err)
	}
	if entry := readUpdateLock(t, filepath.Join(project, "skills-lock.json"), "fixture"); entry.Ref != second {
		t.Fatalf("forced update lock ref = %q; want %q", entry.Ref, second)
	}
}

func TestUpdateInstallsExactlyCheckedCommitWhenBranchMoves(t *testing.T) {
	repository, first, second := createGitFixture(t)
	project, home := updateTestDirectories(t)
	installGitFixture(t, project, home, repository, first, "universal")

	original := materializeUpdateSource
	t.Cleanup(func() { materializeUpdateSource = original })
	materializeUpdateSource = func(source gitSource, limits resourceLimits, policy gitAcquisitionPolicy) (gitWorkspace, error) {
		workspace, err := materializeGitSourceWithPolicy(source, limits, policy)
		if err != nil {
			return workspace, err
		}
		if err := os.WriteFile(filepath.Join(repository, "skills", "fixture", "payload.txt"), []byte("third\n"), 0o644); err != nil {
			return gitWorkspace{}, err
		}
		runFixtureGit(t, repository, "add", ".")
		runFixtureGit(t, repository, "commit", "-q", "-m", "third")
		return workspace, nil
	}

	var stdout, stderr bytes.Buffer
	if exit := Run(nil, Invocation{Args: []string{"update", "--project", "--yes"}, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 0 {
		t.Fatalf("update = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	payload, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "fixture", "payload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "second\n" {
		t.Fatalf("installed payload = %q; want checked commit %s, not moved branch", payload, second)
	}
	entry := readUpdateLock(t, filepath.Join(project, "skills-lock.json"), "fixture")
	if entry.Ref != second {
		t.Fatalf("updated lock ref = %q; want checked commit %q", entry.Ref, second)
	}
}

func TestUpdateHandlesUnchangedMissingAndFailedGitSources(t *testing.T) {
	t.Run("unchanged", func(t *testing.T) {
		repository, _, latest := createGitFixture(t)
		project, home := updateTestDirectories(t)
		installGitFixture(t, project, home, repository, latest, "universal")
		var stdout, stderr bytes.Buffer
		if exit := Run(nil, Invocation{Args: []string{"upgrade", "--project", "--yes"}, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 0 {
			t.Fatalf("upgrade = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
		}
		if strings.Contains(stdout.String(), "Updated 1 skill") {
			t.Fatalf("unchanged source unexpectedly updated: %q", stdout.String())
		}
	})
	t.Run("missing", func(t *testing.T) {
		repository, first, _ := createGitFixture(t)
		project, _ := updateTestDirectories(t)
		installGitFixture(t, project, "", repository, first, "universal")
		if err := os.RemoveAll(filepath.Join(repository, "skills", "fixture")); err != nil {
			t.Fatal(err)
		}
		runFixtureGit(t, repository, "add", "-A")
		runFixtureGit(t, repository, "commit", "-q", "-m", "remove fixture")
		var stdout, stderr bytes.Buffer
		if exit := Run(nil, Invocation{Args: []string{"update", "--project", "--yes"}, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 0 {
			t.Fatalf("update = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "Missing upstream skill: fixture") {
			t.Fatalf("missing update output %q", stdout.String())
		}
		if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "fixture", "SKILL.md")); err != nil {
			t.Fatalf("non-interactive update removed missing skill: %v", err)
		}
	})
	t.Run("failed", func(t *testing.T) {
		project, _ := updateTestDirectories(t)
		lock := `{"version":1,"skills":{"fixture":{"source":"file:///missing/repository","sourceType":"git","sourceUrl":"file:///missing/repository","ref":"deadbeef","skillPath":"skills/fixture/SKILL.md","computedHash":"old"}}}`
		if err := os.WriteFile(filepath.Join(project, "skills-lock.json"), []byte(lock), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if exit := Run(nil, Invocation{Args: []string{"update", "--project", "--yes"}, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 1 {
			t.Fatalf("update failed source = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
		}
	})
}

func TestUpdatePreservesRecordedInstallationTopology(t *testing.T) {
	cases := []struct {
		name       string
		arguments  []string
		updateArgs []string
		paths      []string
		linkPath   string
	}{
		{
			name:       "project copy",
			arguments:  []string{"--yes", "--agent", "claude-code", "--copy"},
			updateArgs: []string{"update", "--project", "--yes"},
			paths:      []string{".claude/skills/fixture/payload.txt"},
			linkPath:   ".claude/skills/fixture",
		},
		{
			name:       "project link",
			arguments:  []string{"--yes", "--agent", "claude-code"},
			updateArgs: []string{"update", "--project", "--yes"},
			paths:      []string{".claude/skills/fixture/payload.txt", ".agents/skills/fixture/payload.txt"},
			linkPath:   ".claude/skills/fixture",
		},
		{
			name:       "eve targets",
			arguments:  []string{"--yes", "--subagent", "root", "research"},
			updateArgs: []string{"upgrade", "--project", "--yes"},
			paths:      []string{"agent/skills/fixture/payload.txt", "agent/subagents/research/skills/fixture/payload.txt"},
		},
		{
			name:       "global canonical",
			arguments:  []string{"--yes", "--global", "--agent", "universal"},
			updateArgs: []string{"update", "--global", "--yes"},
			paths:      []string{"{{home}}/.agents/skills/fixture/payload.txt"},
		},
		{
			name:       "global copy",
			arguments:  []string{"--yes", "--global", "--agent", "claude-code", "--copy"},
			updateArgs: []string{"update", "--global", "--yes"},
			paths:      []string{"{{home}}/.claude/skills/fixture/payload.txt"},
			linkPath:   "{{home}}/.claude/skills/fixture",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			repository, first, _ := createGitFixture(t)
			project, home := updateTestDirectories(t)
			source := "file://" + filepath.ToSlash(repository) + "#" + first
			arguments := append([]string{source}, test.arguments...)
			var stdout, stderr bytes.Buffer
			if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, arguments); exit != 0 {
				t.Fatalf("install = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
			}
			stdout.Reset()
			stderr.Reset()
			if exit := Run(nil, Invocation{Args: test.updateArgs, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 0 {
				t.Fatalf("update = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
			}
			for _, path := range test.paths {
				path = strings.ReplaceAll(path, "{{home}}", home)
				if !filepath.IsAbs(path) {
					path = filepath.Join(project, path)
				}
				payload, err := os.ReadFile(path)
				if err != nil || string(payload) != "second\n" {
					t.Fatalf("updated payload %s = %q, %v", path, payload, err)
				}
			}
			if test.linkPath != "" {
				linkPath := strings.ReplaceAll(test.linkPath, "{{home}}", home)
				if !filepath.IsAbs(linkPath) {
					linkPath = filepath.Join(project, linkPath)
				}
				info, err := os.Lstat(linkPath)
				if err != nil {
					t.Fatal(err)
				}
				if (test.name == "project copy" || test.name == "global copy") && info.Mode()&os.ModeSymlink != 0 {
					t.Fatalf("copy placement became a symlink")
				}
				if test.name == "project link" && info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("link placement became a copy")
				}
			}
		})
	}
}

func TestUpdateReconstructsCredentialRedactedSSHSource(t *testing.T) {
	for _, test := range []struct{ stored, want string }{
		{"ssh://example.test/owner/repository.git", "ssh://git@example.test/owner/repository.git"},
		{"example.test:owner/repository.git", "git@example.test:owner/repository.git"},
	} {
		if actual := updateGitSourceInput(state.LockEntry{SourceType: "git"}, test.stored); actual != test.want {
			t.Fatalf("updateGitSourceInput(%q) = %q; want %q", test.stored, actual, test.want)
		}
	}
}

func TestUpdateChecksAndAppliesRedirectedWellKnownSource(t *testing.T) {
	project, _ := updateTestDirectories(t)
	var requests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		switch request.URL.Path {
		case "/.well-known/agent-skills/index.json":
			_, _ = response.Write([]byte(`{"skills":[{"name":"fixture","description":"fixture","files":["SKILL.md"]}]}`))
		case "/.well-known/agent-skills/fixture/SKILL.md":
			_, _ = response.Write([]byte("---\nname: fixture\ndescription: fixture\n---\nchanged\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, destination.URL+request.URL.Path, http.StatusFound)
	}))
	defer source.Close()
	lock := `{"version":1,"skills":{"fixture":{"source":"` + source.URL + `","sourceType":"well-known","sourceUrl":"` + source.URL + `/.well-known/agent-skills/fixture/SKILL.md","skillPath":"fixture/SKILL.md","computedHash":"old","agents":["universal"]}}}`
	if err := os.WriteFile(filepath.Join(project, "skills-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	arguments := []string{"check", "--project", "--yes", "--allow-insecure-transport"}
	var stdout, stderr bytes.Buffer
	if exit := Run(nil, Invocation{Args: arguments, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 0 {
		t.Fatalf("check = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	destinationHost := strings.TrimPrefix(destination.URL, "http://")
	if requests.Load() == 0 || !strings.Contains(stdout.String(), "Update available: fixture") || !strings.Contains(stderr.String(), "final host "+destinationHost) {
		t.Fatalf("well-known check requests=%d stdout=%q stderr=%q", requests.Load(), stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	arguments[0] = "update"
	if exit := Run(nil, Invocation{Args: arguments, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 0 {
		t.Fatalf("update = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	installed, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "fixture", "SKILL.md"))
	if err != nil || !strings.Contains(string(installed), "changed") {
		t.Fatalf("updated content = %q, %v", installed, err)
	}
	updatedLock, err := os.ReadFile(filepath.Join(project, "skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updatedLock), destination.URL+"/.well-known/agent-skills/fixture/SKILL.md") {
		t.Fatalf("updated provenance omits redirected final URL: %s", updatedLock)
	}
}

func TestCheckUsesDirectWellKnownFallbackWithoutAnIndex(t *testing.T) {
	_, _ = updateTestDirectories(t)
	var directRequests atomic.Int32
	var changed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/.well-known/agent-skills/fixture/SKILL.md" {
			directRequests.Add(1)
			body := "---\nname: fixture\ndescription: fixture\n---\nfirst\n"
			if changed.Load() {
				body = "---\nname: fixture\ndescription: fixture\n---\nchanged\n"
			}
			_, _ = response.Write([]byte(body))
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	direct := server.URL + "/.well-known/agent-skills/fixture/SKILL.md"
	var stdout, stderr bytes.Buffer
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{direct, "--yes", "--agent", "universal", "--allow-insecure-transport"}); exit != 0 {
		t.Fatalf("install = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	changed.Store(true)
	stdout.Reset()
	stderr.Reset()
	if exit := Run(nil, Invocation{Args: []string{"check", "--project", "--yes", "--allow-insecure-transport"}, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}); exit != 0 {
		t.Fatalf("check = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if directRequests.Load() < 2 || !strings.Contains(stdout.String(), "Update available: fixture") {
		t.Fatalf("direct requests=%d output=%q", directRequests.Load(), stdout.String())
	}
}

func TestUpdateRejectsNewNormalizedCollisionsBeforeMutation(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	for _, fixture := range []struct{ path, name string }{{"skills/one", "alpha"}, {"skills/two", "beta"}} {
		directory := filepath.Join(repository, filepath.FromSlash(fixture.path))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		contents := "---\nname: " + fixture.name + "\ndescription: update collision\n---\n# " + fixture.name + "\n"
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runFixtureGit(t, repository, "init", "-q", "-b", "main")
	runFixtureGit(t, repository, "add", ".")
	runFixtureGit(t, repository, "commit", "-q", "-m", "initial")
	initial := strings.TrimSpace(runFixtureGit(t, repository, "rev-parse", "HEAD"))
	project, home := updateTestDirectories(t)
	installGitFixture(t, project, home, repository, initial, "universal")

	for _, fixture := range []struct{ path, name string }{{"skills/one", "same name"}, {"skills/two", "same/name"}} {
		contents := "---\nname: " + fixture.name + "\ndescription: update collision\n---\n# changed\n"
		if err := os.WriteFile(filepath.Join(repository, filepath.FromSlash(fixture.path), "SKILL.md"), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runFixtureGit(t, repository, "add", ".")
	runFixtureGit(t, repository, "commit", "-q", "-m", "collision")

	var stdout, stderr bytes.Buffer
	exit := Run(nil, Invocation{Args: []string{"update", "--project", "--yes"}, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr})
	if exit != 1 || !strings.Contains(stderr.String(), "skills/one") || !strings.Contains(stderr.String(), "skills/two") {
		t.Fatalf("collision update = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	for _, name := range []string{"alpha", "beta"} {
		data, err := os.ReadFile(filepath.Join(project, ".agents", "skills", name, "SKILL.md"))
		if err != nil || !strings.Contains(string(data), "name: "+name) {
			t.Fatalf("installed %s changed before collision failure: %q, %v", name, data, err)
		}
		if got := readUpdateLock(t, filepath.Join(project, "skills-lock.json"), name).Ref; got != initial {
			t.Fatalf("lock ref for %s = %q, want %q", name, got, initial)
		}
	}
}

func updateTestDirectories(t *testing.T) (string, string) {
	t.Helper()
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
	return project, home
}

func installGitFixture(t *testing.T, project, home, repository, commit, agent string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	source := "file://" + filepath.ToSlash(repository) + "#" + commit
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source, "--yes", "--agent", agent}); exit != 0 {
		t.Fatalf("install fixture = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
}

type updateLockEntry struct{ Ref string }

func readUpdateLock(t *testing.T, path, name string) updateLockEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lock struct{ Skills map[string]updateLockEntry }
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	return lock.Skills[name]
}
