package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EngBlock/open-skills/internal/state"
)

func TestGitBackedCommandsRequireDedicatedInsecureTransportAuthorization(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture command uses a POSIX script")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "git-ran")
	script := "#!/bin/sh\necho ran >> \"$GIT_MARKER\"\necho clone failed >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GIT_MARKER", marker)

	project, home := t.TempDir(), t.TempDir()
	withSecurityWorkingDirectory(t, project)
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	writeProjectGitLock(t, project, "http://example.test/repository.git")

	tests := []struct {
		name string
		run  func(Invocation, []string) int
		args []string
	}{
		{"add-http", runAdd, []string{"http://example.test/repository.git", "--yes", "--agent", "universal"}},
		{"add-git-protocol", runAdd, []string{"git://example.test/repository.git", "--yes", "--agent", "universal"}},
		{"use", func(inv Invocation, args []string) int { return runUse(inv, args) }, []string{"http://example.test/repository.git"}},
		{"check", runCheck, []string{"--project"}},
		{"update", runUpgrade, []string{"--project", "--yes"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_ = os.Remove(marker)
			var stdout, stderr bytes.Buffer
			invocation := Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}
			if exit := test.run(invocation, test.args); exit != 1 || !strings.Contains(stderr.String(), "--allow-insecure-transport") {
				t.Fatalf("without authorization = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("Git ran before authorization: %v", err)
			}

			stdout.Reset()
			stderr.Reset()
			args := append(append([]string(nil), test.args...), "--allow-insecure-transport")
			if exit := test.run(invocation, args); exit != 1 || !strings.Contains(stderr.String(), "Warning: allowing insecure") {
				t.Fatalf("with authorization = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("authorized Git did not run: %v", err)
			}
		})
	}
}

func TestMaliciousGitRefsAndCommandTransportsRunNoSubprocess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture command uses a POSIX script")
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "ran")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf 'ran\\n' > \"$GIT_MARKER\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GIT_MARKER", marker)

	for _, ref := range []string{"--upload-pack=sh", "refs/heads/main..evil", "@{upstream}"} {
		_, err := materializeGitSource(gitSource{Identity: "owner/repository", URL: "https://github.com/owner/repository.git", RequestedRef: ref}, defaultResourceLimits())
		if err == nil {
			t.Errorf("malicious ref %q succeeded", ref)
		}
	}
	for _, source := range []string{"ext::sh -c id", "helper::payload", "fd::3"} {
		if _, err := parseGitSource(source); err == nil {
			t.Errorf("command transport %q parsed", source)
		}
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("subprocess ran for rejected input: %v", err)
	}
}

func TestGitValidRefShellMetacharactersRemainOneInertArgument(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture command uses a POSIX script")
	}
	working := t.TempDir()
	withSecurityWorkingDirectory(t, working)
	bin := filepath.Join(working, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	argumentLog := filepath.Join(working, "arguments")
	script := "#!/bin/sh\n: > \"$ARGUMENT_LOG\"\nfor argument in \"$@\"; do printf '%s\\n' \"$argument\" >> \"$ARGUMENT_LOG\"; done\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("ARGUMENT_LOG", argumentLog)
	const ref = "main;>git-ref-injection-sentinel"
	_, err := materializeGitSource(gitSource{Identity: "owner/repository", URL: "https://github.com/owner/repository.git", RequestedRef: ref}, defaultResourceLimits())
	if err == nil {
		t.Fatal("fixture clone unexpectedly succeeded")
	}
	arguments, readErr := os.ReadFile(argumentLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	lines := strings.Split(strings.TrimSpace(string(arguments)), "\n")
	found := false
	for index, argument := range lines {
		if argument == ref {
			found = index > 0 && lines[index-1] == "--branch"
		}
	}
	if !found {
		t.Fatalf("valid ref was not one --branch argv value: %q", lines)
	}
	if _, statErr := os.Stat(filepath.Join(working, "git-ref-injection-sentinel")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("shell metacharacters executed: %v", statErr)
	}
}

func TestConfiguredCredentialHelperIsAttemptedByInitialGitClone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("credential-helper fixture uses a POSIX script")
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
		http.Error(response, "authentication required", http.StatusUnauthorized)
	}))
	defer server.Close()
	root := t.TempDir()
	marker := filepath.Join(root, "helper-ran")
	helper := filepath.Join(root, "credential-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\necho helper >> \"$HELPER_MARKER\"\nprintf 'username=fixture\\npassword=credential-secret\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(config, []byte("[credential]\n\thelper = "+helper+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HELPER_MARKER", marker)
	var notice bytes.Buffer
	_, err := materializeGitSourceWithPolicy(
		gitSource{Identity: server.URL + "/repository.git", URL: server.URL + "/repository.git"},
		defaultResourceLimits(),
		gitAcquisitionPolicy{AllowInsecureTransport: true, Notice: &notice},
	)
	if err == nil {
		t.Fatal("authenticated clone unexpectedly succeeded")
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("configured credential helper was not attempted: %v", statErr)
	}
	if strings.Contains(err.Error(), "credential-secret") || strings.Contains(notice.String(), "credential-secret") {
		t.Fatalf("credential helper output leaked: error %q notice %q", err, notice.String())
	}
}

func TestGitEnvironmentPreservesCredentialConfigurationAndOverridesTransport(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/tmp/credential-config")
	t.Setenv("GIT_ASKPASS", "/tmp/askpass")
	t.Setenv("GIT_ALLOW_PROTOCOL", "ext:http")
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	environment := strings.Join(gitEnvironment(false), "\n")
	for _, expected := range []string{"GIT_CONFIG_GLOBAL=/tmp/credential-config", "GIT_ASKPASS=/tmp/askpass", "GIT_ALLOW_PROTOCOL=file:https:ssh", "GIT_TERMINAL_PROMPT=0"} {
		if !strings.Contains(environment, expected) {
			t.Errorf("Git environment missing %q: %s", expected, environment)
		}
	}
	if strings.Contains(environment, "GIT_ALLOW_PROTOCOL=ext:http") {
		t.Fatal("inherited transport override survived")
	}
	insecure := strings.Join(gitEnvironment(true), "\n")
	if !strings.Contains(insecure, "GIT_ALLOW_PROTOCOL=file:https:ssh:http:git") {
		t.Fatalf("authorized protocol policy = %s", insecure)
	}
}

func TestGitHubAuthenticationFallbackIsAnnouncedAfterGitAndRedactsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture commands use POSIX scripts")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "order")
	gitScript := `#!/bin/sh
case " $* " in
  *" clone "*)
    echo git >> "$ORDER_LOG"
    echo "fatal: Authentication failed for https://token-user:super-secret@github.com/owner/repository.git" >&2
    exit 1
    ;;
esac
exec "$REAL_GIT" "$@"
`
	ghScript := `#!/bin/sh
echo gh >> "$ORDER_LOG"
repository="$4"
mkdir -p "$repository"
cd "$repository" || exit 1
"$REAL_GIT" init -q -b main
mkdir -p skills/fixture
printf '%s\n' '---' 'name: fixture' 'description: fixture' '---' > skills/fixture/SKILL.md
"$REAL_GIT" -c core.hooksPath="$DEV_NULL" add .
"$REAL_GIT" -c core.hooksPath="$DEV_NULL" -c commit.gpgsign=false -c user.name=Fixture -c user.email=fixture@example.test commit -q -m fixture
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(gitScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(ghScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Dir(realGit)+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("REAL_GIT", realGit)
	t.Setenv("ORDER_LOG", log)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("DEV_NULL", os.DevNull)
	var notice bytes.Buffer
	workspace, err := materializeGitSourceWithPolicy(
		gitSource{Identity: "owner/repository", URL: "https://github.com/owner/repository.git", CloneURL: "https://github.com/owner/repository.git", Type: "github"},
		defaultResourceLimits(),
		gitAcquisitionPolicy{Notice: &notice},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.remove()
	order, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(order) != "git\ngh\n" || !strings.Contains(notice.String(), "invoking gh") {
		t.Fatalf("fallback order %q notice %q", order, notice.String())
	}
	if strings.Contains(notice.String(), "super-secret") || strings.Contains(notice.String(), "token-user") {
		t.Fatalf("fallback notice leaked subprocess output: %q", notice.String())
	}
}

func TestGitAcquisitionDoesNotInitializeSubmodules(t *testing.T) {
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(filepath.Join(repository, "skills", "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "skills", "fixture", "SKILL.md"), []byte("---\nname: fixture\ndescription: fixture\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "submodule-ran")
	modules := "[submodule \"payload\"]\n\tpath = skills/fixture/payload\n\turl = file:///missing\n\tupdate = !touch " + marker + "\n"
	if err := os.WriteFile(filepath.Join(repository, ".gitmodules"), []byte(modules), 0o644); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, repository, "init", "-q", "-b", "main")
	runFixtureGit(t, repository, "add", ".")
	runFixtureGit(t, repository, "commit", "-q", "-m", "fixture")
	commit := strings.TrimSpace(runFixtureGit(t, repository, "rev-parse", "HEAD"))
	runFixtureGit(t, repository, "update-index", "--add", "--cacheinfo", "160000,"+commit+",skills/fixture/payload")
	runFixtureGit(t, repository, "commit", "-q", "-m", "add submodule gitlink")
	workspace, err := materializeGitSource(gitSource{Identity: "fixture", URL: "file://" + filepath.ToSlash(repository)}, defaultResourceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.remove()
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("submodule command ran: %v", err)
	}
}

func TestSelectedGitLFSPointersFailAddUseAndUpdateWithoutMutation(t *testing.T) {
	repository, _, _ := createGitFixture(t)
	project, home := t.TempDir(), t.TempDir()
	withSecurityWorkingDirectory(t, project)
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	source := "file://" + filepath.ToSlash(repository)

	var stdout, stderr bytes.Buffer
	invocation := Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}
	if exit := runAdd(invocation, []string{source, "--yes", "--agent", "universal"}); exit != 0 {
		t.Fatalf("initial add = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	installedPath := filepath.Join(project, ".agents", "skills", "fixture", "payload.txt")
	beforeInstalled, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeLock, err := os.ReadFile(filepath.Join(project, "skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	pointer := "version https://git-lfs.github.com/spec/v1\noid sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nsize 42\n"
	if err := os.WriteFile(filepath.Join(repository, "skills", "fixture", "payload.txt"), []byte(pointer), 0o644); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, repository, "add", ".")
	runFixtureGit(t, repository, "commit", "-q", "-m", "lfs pointer")

	stdout.Reset()
	stderr.Reset()
	if exit := runUse(invocation, []string{source}); exit != 1 || !strings.Contains(stderr.String(), "Git LFS pointer") {
		t.Fatalf("use pointer = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if exit := runUpgrade(invocation, []string{"--project", "--yes"}); exit != 1 || !strings.Contains(stderr.String(), "Git LFS pointer") {
		t.Fatalf("update pointer = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if after, _ := os.ReadFile(installedPath); !bytes.Equal(after, beforeInstalled) {
		t.Fatalf("failed update changed installed content: %q", after)
	}
	if after, _ := os.ReadFile(filepath.Join(project, "skills-lock.json")); !bytes.Equal(after, beforeLock) {
		t.Fatal("failed update changed lock state")
	}

	freshProject := t.TempDir()
	withSecurityWorkingDirectory(t, freshProject)
	stdout.Reset()
	stderr.Reset()
	if exit := runAdd(invocation, []string{source, "--yes", "--agent", "universal"}); exit != 1 || !strings.Contains(stderr.String(), "Git LFS pointer") {
		t.Fatalf("add pointer = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(freshProject, "skills-lock.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed add wrote a lock: %v", err)
	}
}

func TestRemoteInstallationPersistenceStripsCredentialMaterial(t *testing.T) {
	project, home, sourceRoot := t.TempDir(), t.TempDir(), t.TempDir()
	withSecurityWorkingDirectory(t, project)
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	if err := os.WriteFile(filepath.Join(sourceRoot, "SKILL.md"), []byte("---\nname: fixture\ndescription: fixture\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skill := localSkill{Name: "fixture", Path: sourceRoot, RelativePath: "."}
	provenance := installationProvenance{
		Identity: "https://token-user:credential-secret@example.test/repository.git?access_token=query-secret",
		URL:      "https://token-user:credential-secret@example.test/repository.git?access_token=query-secret",
		Type:     "git",
		Ref:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if err := installLocalSkill(skill, provenance, state.Project, project, project, home, []string{"universal"}, false, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(project, "skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"token-user", "credential-secret", "query-secret", "access_token"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("persisted lock contains %q: %s", secret, data)
		}
	}
}

func TestListJSONAndUpdateDiagnosticsRedactLegacySourceCredentials(t *testing.T) {
	project, home := t.TempDir(), t.TempDir()
	withSecurityWorkingDirectory(t, project)
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	if err := os.MkdirAll(filepath.Join(project, ".agents", "skills", "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".agents", "skills", "fixture", "SKILL.md"), []byte("---\nname: fixture\ndescription: fixture\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secretSource := "https://token-user:super-secret@example.test/repository%zz.git?access_token=also-secret#fragment-secret"
	if display := credentialFreeSource(secretSource); display != "https://example.test/repository%zz.git" {
		t.Fatalf("credentialFreeSource = %q", display)
	}
	writeProjectGitLock(t, project, secretSource)

	var stdout, stderr bytes.Buffer
	invocation := Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}
	if exit := runList(invocation, []string{"--json"}); exit != 0 {
		t.Fatalf("list = %d stderr %q", exit, stderr.String())
	}
	for _, secret := range []string{"super-secret", "also-secret", "fragment-secret", "token-user"} {
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("list JSON leaked %q: %s", secret, stdout.String())
		}
	}
	var document map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runList(invocation, nil); exit != 0 {
		t.Fatalf("human list = %d stderr %q", exit, stderr.String())
	}
	for _, secret := range []string{"super-secret", "also-secret", "fragment-secret", "token-user"} {
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("human list leaked %q: %s", secret, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runCheck(invocation, []string{"--project"}); exit != 1 {
		t.Fatalf("check = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	for _, secret := range []string{"super-secret", "also-secret", "fragment-secret", "token-user"} {
		if strings.Contains(stderr.String(), secret) {
			t.Fatalf("update diagnostic leaked %q: %q", secret, stderr.String())
		}
	}
}

func TestLimitedBufferCapsGitDiagnosticOutput(t *testing.T) {
	var output limitedBuffer
	output.limit = 4
	if written, err := output.Write([]byte("123456")); err != nil || written != 6 {
		t.Fatalf("Write = %d, %v", written, err)
	}
	if got := output.String(); got != "1234" || !output.exceeded {
		t.Fatalf("limited output = %q exceeded=%v", got, output.exceeded)
	}
}

func writeProjectGitLock(t *testing.T, project, source string) {
	t.Helper()
	contents := map[string]any{
		"version": 1,
		"skills": map[string]any{
			"fixture": map[string]any{
				"source": source, "sourceUrl": source, "sourceType": "git", "ref": "deadbeef", "skillPath": "skills/fixture/SKILL.md", "computedHash": "old",
			},
		},
	}
	data, err := json.Marshal(contents)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "skills-lock.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func withSecurityWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}
