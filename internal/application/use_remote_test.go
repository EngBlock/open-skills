package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUseOptionsAcceptsResourceOverrides(t *testing.T) {
	sources, options, parseErrors := parseUseOptions([]string{"owner/repository", "--max-file-bytes", "11534336", "--max-total-bytes", "209715200", "--max-files", "6000", "--max-depth", "25"})
	if len(parseErrors) != 0 {
		t.Fatalf("parse errors = %#v", parseErrors)
	}
	if len(sources) != 1 || sources[0] != "owner/repository" || options.Limits.MaxFileBytes != 11534336 || options.Limits.MaxTotalBytes != 209715200 || options.Limits.MaxFiles != 6000 || options.Limits.MaxDepth != 25 {
		t.Fatalf("parsed use options = sources %#v options %#v", sources, options)
	}
}

func TestRemoteUseEnforcesAndOverridesResourceLimits(t *testing.T) {
	skillMD := []byte("---\nname: bounded-use\ndescription: bounded use skill\n---\n")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/agent-skills/index.json":
			_, _ = response.Write([]byte(`{"skills":[{"name":"bounded-use","description":"bounded use skill","files":["SKILL.md"]}]}`))
		case "/.well-known/agent-skills/bounded-use/SKILL.md":
			_, _ = response.Write(skillMD)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	tooSmall := fmt.Sprintf("%d", len(skillMD)-1)
	if exit := runUse(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{server.URL, "--allow-insecure-transport", "--max-file-bytes", tooSmall}); exit != 1 || stdout.String() != "" || !strings.Contains(stderr.String(), "--max-file-bytes") {
		t.Fatalf("limited use = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exact := fmt.Sprintf("%d", len(skillMD))
	arguments := []string{server.URL, "--allow-insecure-transport", "--max-file-bytes", exact, "--max-total-bytes", exact, "--max-files", "1"}
	if exit := runUse(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, arguments); exit != 0 || !strings.Contains(stderr.String(), "Warning: allowing insecure HTTP") || !strings.Contains(stdout.String(), string(skillMD)) {
		t.Fatalf("exact-boundary use = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
}

func TestRemoteUseFollowsBoundedRedirectPolicy(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/agent-skills/index.json":
			_, _ = response.Write([]byte(`{"skills":[{"name":"redirected-use","description":"redirected use","files":["SKILL.md"]}]}`))
		case "/.well-known/agent-skills/redirected-use/SKILL.md":
			_, _ = response.Write([]byte("---\nname: redirected-use\ndescription: redirected use\n---\n\nUse redirected instructions.\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, destination.URL+request.URL.Path, http.StatusFound)
	}))
	defer source.Close()

	var stdout, stderr bytes.Buffer
	arguments := []string{source.URL, "--allow-insecure-transport"}
	if exit := runUse(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, arguments); exit != 0 {
		t.Fatalf("redirected use = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Use redirected instructions.") || !strings.Contains(stderr.String(), "final host "+strings.TrimPrefix(destination.URL, "http://")) {
		t.Fatalf("redirected use stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestUseTreatsExistingAmbiguousRelativeDirectoryAsLocal(t *testing.T) {
	project := t.TempDir()
	directory := filepath.Join(project, "owner", "repository")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\nname: local-ambiguous\ndescription: local ambiguous skill\n---\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exit := runUse(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{"owner/repository"}); exit != 0 || stderr.String() != "" || !strings.Contains(stdout.String(), skillMD) {
		t.Fatalf("ambiguous local use = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
}

func TestRemoteContentRevisionFramesPathsAndContents(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	for _, directory := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: collision\ndescription: Collision\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(first, "A"), []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "AB"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	firstLegacy, _, err := contentIdentity(first)
	if err != nil {
		t.Fatal(err)
	}
	secondLegacy, _, err := contentIdentity(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstLegacy != secondLegacy {
		t.Fatal("fixture does not exercise the unframed content-hash collision")
	}
	firstRevision, err := remoteContentRevision(first)
	if err != nil {
		t.Fatal(err)
	}
	secondRevision, err := remoteContentRevision(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstRevision == secondRevision {
		t.Fatalf("different remote trees share revision %q", firstRevision)
	}
}

func TestRemoteAgentUseRequiresDedicatedNoninteractiveTrustAuthorization(t *testing.T) {
	repository, firstCommit, _ := createGitFixture(t)
	source := "file://" + filepath.ToSlash(repository) + "#" + firstCommit
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	var stdout, stderr bytes.Buffer

	exit := runUse(Invocation{Stdin: bytes.NewReader([]byte("yes\n")), Stdout: &stdout, Stderr: &stderr}, []string{source, "--skill", "fixture", "--agent", "codex", "--yes"})
	if exit != 1 || stdout.String() != "" || !strings.Contains(stderr.String(), "--trust") {
		t.Fatalf("runUse = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(config, "open-skills", "trust.json")); !os.IsNotExist(err) {
		t.Fatalf("--yes recorded remote trust: %v", err)
	}
}

func TestInteractiveRemoteAgentApprovalPersistsOnlySanitizedExactCommit(t *testing.T) {
	repository, firstCommit, secondCommit := createGitFixture(t)
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	original := useAgentConfigs["codex"]
	useAgentConfigs["codex"] = useAgentConfig{command: "open-skills-test-missing-codex"}
	t.Cleanup(func() { useAgentConfigs["codex"] = original })

	run := func(commit string, interactive bool, input string) (int, string) {
		var stdout, stderr bytes.Buffer
		source := "file://" + filepath.ToSlash(repository) + "#" + commit
		exit := runUse(Invocation{Stdin: bytes.NewBufferString(input), Stdout: &stdout, Stderr: &stderr, Interactive: interactive}, []string{source, "--skill", "fixture", "--agent", "codex"})
		if stdout.String() != "" {
			t.Fatalf("agent use wrote unexpected stdout: %q", stdout.String())
		}
		return exit, stderr.String()
	}

	if exit, stderr := run(firstCommit, true, "yes\n"); exit != 1 || !strings.Contains(stderr, "Trust this exact source commit") || !strings.Contains(stderr, "command not found") {
		t.Fatalf("interactive approval = exit %d stderr %q", exit, stderr)
	}
	data, err := os.ReadFile(filepath.Join(config, "open-skills", "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Approvals []map[string]any `json:"approvals"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Approvals) != 1 || len(document.Approvals[0]) != 3 || document.Approvals[0]["source"] != "file://"+filepath.ToSlash(repository) || document.Approvals[0]["commit"] != firstCommit || document.Approvals[0]["approvedAt"] == "" {
		t.Fatalf("persisted trust record = %s", data)
	}

	if exit, stderr := run(firstCommit, false, ""); exit != 1 || strings.Contains(stderr, "--trust") || !strings.Contains(stderr, "command not found") {
		t.Fatalf("repeated exact commit = exit %d stderr %q", exit, stderr)
	}
	if exit, stderr := run(secondCommit, false, "yes\n"); exit != 1 || !strings.Contains(stderr, "--trust") {
		t.Fatalf("changed commit = exit %d stderr %q", exit, stderr)
	}
}

func TestLockRecordWithoutInstalledContentDoesNotCountAsApproval(t *testing.T) {
	repository, firstCommit, _ := createGitFixture(t)
	source := "file://" + filepath.ToSlash(repository)
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
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	lock := fmt.Sprintf(`{"version":1,"skills":{"fixture":{"source":%q,"sourceType":"git","ref":%q,"skillPath":"skills/fixture/SKILL.md","computedHash":"recorded"}}}`, source, firstCommit)
	if err := os.WriteFile(filepath.Join(project, "skills-lock.json"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	original := useAgentConfigs["codex"]
	useAgentConfigs["codex"] = useAgentConfig{command: "open-skills-test-missing-codex"}
	t.Cleanup(func() { useAgentConfigs["codex"] = original })

	var stdout, stderr bytes.Buffer
	exit := runUse(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source + "#" + firstCommit, "--agent", "codex"})
	if exit != 1 || stdout.String() != "" || !strings.Contains(stderr.String(), "--trust") {
		t.Fatalf("lock-only approval = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
}

func TestInstalledApprovalDoesNotApplyToDifferentSkillPathAtSameCommit(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	for _, path := range []string{"skills/a", "skills/b"} {
		if err := os.MkdirAll(filepath.Join(repository, path), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "A"
		if path == "skills/b" {
			body = "B"
		}
		contents := "---\nname: duplicate\ndescription: Duplicate\n---\n\n# " + body + "\n"
		if err := os.WriteFile(filepath.Join(repository, path, "SKILL.md"), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runFixtureGit(t, repository, "init", "-q", "-b", "main")
	runFixtureGit(t, repository, "add", ".")
	runFixtureGit(t, repository, "commit", "-q", "-m", "fixture")
	commit := strings.TrimSpace(runFixtureGit(t, repository, "rev-parse", "HEAD"))
	source := "file://" + filepath.ToSlash(repository)

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
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	lock := fmt.Sprintf(`{"version":1,"skills":{"duplicate":{"source":%q,"sourceType":"git","ref":%q,"skillPath":"skills/b/SKILL.md","computedHash":"recorded"}}}`, source, commit)
	if err := os.WriteFile(filepath.Join(project, "skills-lock.json"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	original := useAgentConfigs["codex"]
	useAgentConfigs["codex"] = useAgentConfig{command: "open-skills-test-missing-codex"}
	t.Cleanup(func() { useAgentConfigs["codex"] = original })

	var stdout, stderr bytes.Buffer
	exit := runUse(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source + "#" + commit, "--skill-path", "skills/a", "--agent", "codex"})
	if exit != 1 || stdout.String() != "" || !strings.Contains(stderr.String(), "--trust") {
		t.Fatalf("different skill path = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
}

func TestInstalledRemoteCommitCountsAsApprovedButChangedCommitDoesNot(t *testing.T) {
	repository, firstCommit, secondCommit := createGitFixture(t)
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
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	original := useAgentConfigs["codex"]
	useAgentConfigs["codex"] = useAgentConfig{command: "open-skills-test-missing-codex"}
	t.Cleanup(func() { useAgentConfigs["codex"] = original })

	firstSource := "file://" + filepath.ToSlash(repository) + "#" + firstCommit
	var addOut, addErr bytes.Buffer
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &addOut, Stderr: &addErr}, []string{firstSource, "--yes", "--agent", "universal"}); exit != 0 {
		t.Fatalf("runAdd = %d stdout %q stderr %q", exit, addOut.String(), addErr.String())
	}
	run := func(commit string) (int, string) {
		var stdout, stderr bytes.Buffer
		source := "file://" + filepath.ToSlash(repository) + "#" + commit
		exit := runUse(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source, "--skill", "fixture", "--agent", "codex"})
		return exit, stderr.String()
	}
	if exit, stderr := run(firstCommit); exit != 1 || strings.Contains(stderr, "--trust") || !strings.Contains(stderr, "command not found") {
		t.Fatalf("installed commit = exit %d stderr %q", exit, stderr)
	}
	if exit, stderr := run(secondCommit); exit != 1 || !strings.Contains(stderr, "--trust") {
		t.Fatalf("changed commit = exit %d stderr %q", exit, stderr)
	}
}

func TestRemoteGitUseDisplaysSanitizedSourceAndExactCommitBeforeInstructions(t *testing.T) {
	repository, firstCommit, _ := createGitFixture(t)
	source := "file://" + filepath.ToSlash(repository) + "#" + firstCommit
	var stdout, stderr bytes.Buffer

	exit := runUse(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source, "--skill", "fixture"})
	if exit != 0 || stderr.String() != "" {
		t.Fatalf("runUse = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	prompt := stdout.String()
	sourceLine := "Remote skill source: file://" + filepath.ToSlash(repository)
	commitLine := "Remote skill commit: " + firstCommit
	if !strings.Contains(prompt, sourceLine) || !strings.Contains(prompt, commitLine) {
		t.Fatalf("prompt omitted remote provenance: %q", prompt)
	}
	if strings.Index(prompt, sourceLine) > strings.Index(prompt, "<SKILL.md>") || strings.Index(prompt, commitLine) > strings.Index(prompt, "<SKILL.md>") {
		t.Fatalf("prompt displayed provenance after instructions: %q", prompt)
	}
	if strings.Contains(prompt, "#"+firstCommit) {
		t.Fatalf("prompt displayed the unsanitized source argument: %q", prompt)
	}
}
