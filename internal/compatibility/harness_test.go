package compatibility

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHarnessRunBothUsesEquivalentIndependentSandboxesAndCapturesProcessEffects(t *testing.T) {
	target := fixtureTarget()
	harness := Harness{Oracle: target, Native: target}
	scenario := Scenario{
		Args:    []string{"probe"},
		Stdin:   []byte("from stdin"),
		Timeout: 20 * time.Second,
		Env: map[string]string{
			"PROBE_VALUE": "from environment",
			"SERVER_URL":  "{{http:url}}",
			"REPO_PATH":   "{{repo:origin}}",
		},
		Files: []FileFixture{
			{Root: ProjectRoot, Path: "fixture.txt", Data: []byte("project fixture")},
			{Root: HomeRoot, Path: "home.txt", Data: []byte("home fixture")},
			{Root: TempRoot, Path: "temp.txt", Data: []byte("temp fixture")},
		},
		Repositories: []RepositoryFixture{{
			Name: "origin",
			Files: map[string][]byte{
				"README.md": []byte("repository fixture\n"),
			},
		}},
		HTTPRoutes: []HTTPRoute{{
			Method: http.MethodPost,
			Path:   "/fixture",
			Status: http.StatusCreated,
			Header: http.Header{"X-Fixture": []string{"yes"}},
			Body:   []byte("network fixture"),
		}},
		Commands: []CommandFixture{{
			Name:     "fixture-child",
			Stdout:   "child stdout",
			Stderr:   "child stderr",
			ExitCode: 7,
		}},
	}

	pair, err := harness.RunBoth(context.Background(), scenario)
	if err != nil {
		t.Fatalf("RunBoth: %v", err)
	}

	for name, observation := range map[string]Observation{
		"oracle": pair.Oracle,
		"native": pair.Native,
	} {
		if observation.ExitCode != 23 {
			t.Errorf("%s exit code = %d, want 23", name, observation.ExitCode)
		}
		if observation.ProcessError != "" || observation.TimedOut {
			t.Errorf("%s process failure = %q, timed out = %v", name, observation.ProcessError, observation.TimedOut)
		}
		for _, want := range []string{
			"stdin=from stdin",
			"env=from environment",
			"project fixture",
			"home fixture",
			"temp fixture",
			"http=201:network fixture",
			"git=repository fixture",
			"child=7:child stdout:child stderr",
		} {
			if !strings.Contains(observation.Stdout, want) {
				t.Errorf("%s stdout %q does not contain %q", name, observation.Stdout, want)
			}
		}
		if observation.Stderr != "probe stderr\n" {
			t.Errorf("%s stderr = %q", name, observation.Stderr)
		}

		assertFile(t, observation, "project/generated.txt", FileState{Kind: FileKindRegular, Data: []byte("generated")})
		if runtime.GOOS != "windows" {
			assertFile(t, observation, "project/generated-link", FileState{Kind: FileKindSymlink, LinkTarget: "generated.txt"})
		}
		if got := string(observation.Locks[ProjectLock]); got != `{"version":1}` {
			t.Errorf("%s project lock = %q", name, got)
		}
		if got := string(observation.Locks[XDGGlobalLock]); got != `{"version":3}` {
			t.Errorf("%s global lock = %q", name, got)
		}
		if len(observation.HTTPRequests) != 1 {
			t.Fatalf("%s requests = %d, want 1", name, len(observation.HTTPRequests))
		}
		request := observation.HTTPRequests[0]
		if request.Method != http.MethodPost || request.Path != "/fixture" || string(request.Body) != "request body" {
			t.Errorf("%s request = %#v", name, request)
		}
		if len(observation.SpawnedCommands) != 1 {
			t.Fatalf("%s spawned commands = %d, want 1", name, len(observation.SpawnedCommands))
		}
		spawn := observation.SpawnedCommands[0]
		if spawn.Name != "fixture-child" || strings.Join(spawn.Args, "|") != "one|two words" {
			t.Errorf("%s spawn = %#v", name, spawn)
		}
		if spawn.Cwd != observation.Paths.Project {
			t.Errorf("%s spawn cwd = %q, want %q", name, spawn.Cwd, observation.Paths.Project)
		}
	}

	if pair.Oracle.Paths.Root == pair.Native.Paths.Root {
		t.Fatal("oracle and native reused a sandbox")
	}
	if differences := CompareObservations(pair.Oracle, pair.Native, Normalization{}); len(differences) != 0 {
		t.Fatalf("equivalent observations differ after path normalization: %v", differences)
	}
}

func TestHarnessTimesOutAndPreservesRawFailure(t *testing.T) {
	harness := Harness{}
	observation, err := harness.Run(context.Background(), fixtureTarget(), Scenario{
		Args:    []string{"sleep"},
		Timeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !observation.TimedOut {
		t.Fatal("expected timeout")
	}
	if observation.ProcessError == "" {
		t.Fatal("expected a raw process error")
	}
}

func TestHarnessUsesMinimalEnvironmentAndFixtureOnlyPath(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "must-not-leak")
	t.Setenv("HTTPS_PROXY", "http://must-not-leak.invalid")
	harness := Harness{}
	observation, err := harness.Run(context.Background(), fixtureTarget(), Scenario{Args: []string{"environment"}})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Stdout != "token=\nproxy=\npath=\n" {
		t.Fatalf("isolated environment leaked host state: %q", observation.Stdout)
	}

	observation, err = harness.Run(context.Background(), fixtureTarget(), Scenario{Args: []string{"undeclared"}, Commands: []CommandFixture{{Name: "declared"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(observation.Stdout, "undeclared=true") || len(observation.SpawnedCommands) != 0 {
		t.Fatalf("undeclared command escaped fixture PATH: %#v", observation)
	}
}

func TestD02HarnessOfflineScenariosFailOnCapturedNetworkAttempts(t *testing.T) {
	t.Run("HTTP", func(t *testing.T) {
		observation, err := (Harness{}).Run(context.Background(), fixtureTarget(), Scenario{
			Args:    []string{"network-http"},
			Env:     map[string]string{"NETWORK_URL": "{{http:url}}/unexpected"},
			Offline: true,
		})
		if err == nil || !strings.Contains(err.Error(), "offline scenario captured a network attempt") {
			t.Fatalf("HTTP attempt did not fail closed: observation=%#v err=%v", observation, err)
		}
		if len(observation.HTTPRequests) != 1 {
			t.Fatalf("HTTP attempt was not recorded: %#v", observation.HTTPRequests)
		}
	})

	t.Run("child command", func(t *testing.T) {
		observation, err := (Harness{}).Run(context.Background(), fixtureTarget(), Scenario{
			Args:    []string{"network-command"},
			Offline: true,
		})
		if err == nil || !strings.Contains(err.Error(), "offline scenario captured a network attempt") {
			t.Fatalf("child attempt did not fail closed: observation=%#v err=%v", observation, err)
		}
		if len(observation.SpawnedCommands) != 1 || observation.SpawnedCommands[0].Name != "curl" {
			t.Fatalf("child attempt was not recorded: %#v", observation.SpawnedCommands)
		}
	})
}

func TestHarnessRestrictsHostCommandPassthroughToGit(t *testing.T) {
	_, err := (Harness{}).Run(context.Background(), fixtureTarget(), Scenario{
		Commands: []CommandFixture{{Name: "node", Passthrough: true}},
	})
	if err == nil || !strings.Contains(err.Error(), `passthrough command fixture "node" is not allowed`) {
		t.Fatalf("arbitrary host passthrough error = %v", err)
	}
}

func TestHarnessRejectsMalformedEnvironmentNames(t *testing.T) {
	for _, test := range []struct {
		name     string
		target   map[string]string
		scenario map[string]string
	}{
		{name: "target", target: map[string]string{"PATH=/host": "bad"}},
		{name: "scenario", scenario: map[string]string{"BAD=NAME": "bad"}},
		{name: "empty", scenario: map[string]string{"": "bad"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := fixtureTarget()
			target.Env = test.target
			_, err := (Harness{}).Run(context.Background(), target, Scenario{Env: test.scenario})
			if err == nil || !strings.Contains(err.Error(), "invalid environment variable name") {
				t.Fatalf("expected malformed environment rejection, got %v", err)
			}
		})
	}
}

func TestHarnessRejectsRepositoryGitMetadataFixtures(t *testing.T) {
	for _, path := range []string{".git/config", "nested/.GIT/hooks/pre-commit"} {
		t.Run(path, func(t *testing.T) {
			_, err := (Harness{}).Run(context.Background(), fixtureTarget(), Scenario{
				Repositories: []RepositoryFixture{{Name: "origin", Files: map[string][]byte{path: []byte("unsafe")}}},
			})
			if err == nil || !strings.Contains(err.Error(), "reserved .git metadata") {
				t.Fatalf("expected .git fixture rejection, got %v", err)
			}
		})
	}
}

func TestHarnessCapturesEffectiveOverriddenLockLocation(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), fixtureTarget(), Scenario{
		Args: []string{"custom-lock"},
		Env:  map[string]string{"XDG_STATE_HOME": "{{home}}/custom-state"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(observation.Locks[XDGGlobalLock]); got != `{"custom":true}` {
		t.Fatalf("effective XDG lock = %q", got)
	}
}

func TestHarnessTimeoutKillsUnixDescendants(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows process-tree containment is not yet implemented")
	}
	started := time.Now()
	observation, err := (Harness{}).Run(context.Background(), fixtureTarget(), Scenario{
		Args: []string{"spawn-sleeper"}, Timeout: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !observation.TimedOut || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("timeout did not terminate the process group promptly: timedOut=%v elapsed=%s", observation.TimedOut, time.Since(started))
	}
}

func TestHarnessKillsUnixDescendantsAfterNormalExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows process-tree containment is not yet implemented")
	}
	marker := filepath.Join(t.TempDir(), "descendant-marker")
	observation, err := (Harness{}).Run(context.Background(), fixtureTarget(), Scenario{
		Args: []string{"spawn-background"}, Env: map[string]string{"DESCENDANT_MARKER": marker},
	})
	if err != nil || observation.ExitCode != 0 {
		t.Fatalf("normal parent run failed: observation=%#v err=%v", observation, err)
	}
	time.Sleep(1250 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("normal-exit descendant survived process-group teardown: %v", err)
	}
}

func TestHarnessCapturesProcessStartFailure(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), Target{Command: filepath.Join(t.TempDir(), "missing")}, Scenario{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if observation.ExitCode != -1 || observation.ProcessError == "" || observation.TimedOut {
		t.Fatalf("start failure = %#v", observation)
	}
}

func TestNormalizerUsesRecordedResolvedSandboxPathsAfterCleanup(t *testing.T) {
	observation := Observation{
		Stdout:        "/private/var/sandbox/project/skills-lock.json\n",
		Paths:         SandboxPaths{Root: "/var/sandbox", Project: "/var/sandbox/project"},
		ResolvedPaths: SandboxPaths{Root: "/private/var/sandbox", Project: "/private/var/sandbox/project"},
	}
	normalized := normalizedObservation(observation, Normalization{})
	if normalized.Stdout != "<project>/skills-lock.json\n" {
		t.Fatalf("resolved sandbox path was not normalized: %q", normalized.Stdout)
	}
}

func TestNormalizerOnlyIgnoresPresentationDifferences(t *testing.T) {
	oracle := Observation{
		Stdout:   "\x1b[32mcreated /private/oracle/project/a/b\x1b[0m\r\nclock=<time>\r\n",
		Stderr:   "warning at /private/oracle/home\r\n",
		ExitCode: 0,
		Paths:    SandboxPaths{Root: "/private/oracle", Home: "/private/oracle/home", Project: "/private/oracle/project", Temp: "/private/oracle/tmp"},
	}
	native := Observation{
		Stdout:   "created <project>/a/b\nclock=ignored\n",
		Stderr:   "warning at <home>\n",
		ExitCode: 0,
		Paths:    SandboxPaths{Root: "/private/native", Home: "/private/native/home", Project: "/private/native/project", Temp: "/private/native/tmp"},
	}
	normalization := Normalization{Replacements: []Replacement{{Pattern: "clock=<time>", With: "clock=ignored"}}}
	if differences := CompareObservations(oracle, native, normalization); len(differences) != 0 {
		t.Fatalf("presentation-only differences were not normalized: %v", differences)
	}

	native.ExitCode = 1
	if differences := CompareObservations(oracle, native, normalization); !containsDifference(differences, "exit code") {
		t.Fatalf("exit status was normalized away: %v", differences)
	}
	native.ExitCode = 0
	native.Stdout, native.Stderr = native.Stderr, native.Stdout
	if differences := CompareObservations(oracle, native, normalization); !containsDifference(differences, "stdout") || !containsDifference(differences, "stderr") {
		t.Fatalf("stream roles were normalized away: %v", differences)
	}
	native = oracle
	native.SpawnedCommands = []SpawnedCommand{{Name: "git", Args: []string{"clone", "different"}}}
	if differences := CompareObservations(oracle, native, normalization); !containsDifference(differences, "spawned commands") {
		t.Fatalf("spawn arguments were normalized away: %v", differences)
	}

	oracle = Observation{Files: map[string]FileState{"project/binary": {Kind: FileKindRegular, Data: []byte{'\\'}}}}
	native = Observation{Files: map[string]FileState{"project/binary": {Kind: FileKindRegular, Data: []byte{'/'}}}}
	if differences := CompareObservations(oracle, native, Normalization{}); !containsDifference(differences, "filesystem") {
		t.Fatal("binary slash difference was normalized away")
	}
	oracle = Observation{Locks: map[LockLocation][]byte{ProjectLock: []byte(`{"path":"a\\b"}`)}}
	native = Observation{Locks: map[LockLocation][]byte{ProjectLock: []byte(`{"path":"a/b"}`)}}
	if differences := CompareObservations(oracle, native, Normalization{}); !containsDifference(differences, "locks") {
		t.Fatal("semantic lock difference was normalized away")
	}
	oracle = Observation{Paths: SandboxPaths{FixtureURL: "http://127.0.0.1:1"}, HTTPRequests: []HTTPRequest{{Host: "127.0.0.1:1"}}}
	native = Observation{Paths: SandboxPaths{FixtureURL: "http://127.0.0.1:2"}, HTTPRequests: []HTTPRequest{{Host: "other.invalid"}}}
	if differences := CompareObservations(oracle, native, Normalization{}); !containsDifference(differences, "HTTP requests") {
		t.Fatal("non-fixture HTTP host difference was normalized away")
	}
}

func TestPinnedNPMOracleRunsWhenExplicitlyEnabled(t *testing.T) {
	if os.Getenv("OPEN_SKILLS_TEST_PINNED_ORACLE") != "1" {
		t.Skip("set OPEN_SKILLS_TEST_PINNED_ORACLE=1 for the online pinned-artifact smoke test")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := testModuleRoot(t)
	target, err := PrepareNPMOracle(context.Background(), OracleOptions{
		ManifestPath:   filepath.Join(repositoryRoot, "compatibility", "npm-0.1.2", "oracle.json"),
		Destination:    filepath.Join(t.TempDir(), "oracle"),
		NodeExecutable: node,
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := (Harness{}).Run(context.Background(), target, Scenario{Args: []string{"--version"}})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || strings.TrimSpace(observation.Stdout) != "0.1.2" {
		t.Fatalf("pinned oracle did not run: exit=%d stdout=%q stderr=%q error=%q", observation.ExitCode, observation.Stdout, observation.Stderr, observation.ProcessError)
	}
}

func TestPrepareNPMOracleVerifiesPinnedArtifactAndExtractsSafely(t *testing.T) {
	archive := makeTarball(t, []tarEntry{
		{Name: "package/bin/cli.mjs", Data: []byte("console.log('oracle')\n"), Mode: 0o755},
		{Name: "package/package.json", Data: []byte(`{"name":"@engblock/open-skills"}`), Mode: 0o644},
	})
	manifestPath, archivePath := writeOracleFixture(t, archive)
	destination := filepath.Join(t.TempDir(), "oracle")

	target, err := PrepareNPMOracle(context.Background(), OracleOptions{
		ManifestPath:          manifestPath,
		CachedTarball:         archivePath,
		CachedRuntimeTarballs: runtimeCache(archivePath),
		Destination:           destination,
		NodeExecutable:        "/explicit/node",
	})
	if err != nil {
		t.Fatalf("PrepareNPMOracle: %v", err)
	}
	if target.Command != "/explicit/node" || len(target.Args) != 1 || target.Args[0] != filepath.Join(destination, "package", "bin", "cli.mjs") {
		t.Fatalf("target = %#v", target)
	}
	content, err := os.ReadFile(target.Args[0])
	if err != nil || string(content) != "console.log('oracle')\n" {
		t.Fatalf("extracted entry = %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(destination, "package", "node_modules", "yaml", "index.js")); err != nil {
		t.Fatalf("pinned yaml runtime was not materialized: %v", err)
	}
}

func TestPrepareNPMOracleDoesNotDeletePreexistingDestination(t *testing.T) {
	archive := makeTarball(t, []tarEntry{{Name: "package/bin/cli.mjs", Data: []byte("oracle"), Mode: 0o755}})
	manifestPath, archivePath := writeOracleFixture(t, archive)
	destination := t.TempDir()
	sentinel := filepath.Join(destination, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := PrepareNPMOracle(context.Background(), OracleOptions{
		ManifestPath: manifestPath, CachedTarball: archivePath, CachedRuntimeTarballs: runtimeCache(archivePath), Destination: destination, NodeExecutable: "node",
	})
	if err == nil || !strings.Contains(err.Error(), "must not already exist") {
		t.Fatalf("expected existing destination rejection, got %v", err)
	}
	if content, readErr := os.ReadFile(sentinel); readErr != nil || string(content) != "keep" {
		t.Fatalf("preexisting destination was modified: %q, %v", content, readErr)
	}
}

func TestPrepareNPMOracleRejectsRecordedArchiveCountDrift(t *testing.T) {
	archive := makeTarball(t, []tarEntry{{Name: "package/bin/cli.mjs", Data: []byte("oracle"), Mode: 0o755}})
	manifestPath, archivePath := writeOracleFixture(t, archive)
	data, _ := os.ReadFile(manifestPath)
	var manifest map[string]any
	_ = json.Unmarshal(data, &manifest)
	manifest["artifact"].(map[string]any)["fileCount"] = float64(2)
	changed, _ := json.Marshal(manifest)
	_ = os.WriteFile(manifestPath, changed, 0o600)
	_, err := PrepareNPMOracle(context.Background(), OracleOptions{
		ManifestPath: manifestPath, CachedTarball: archivePath, CachedRuntimeTarballs: runtimeCache(archivePath), Destination: filepath.Join(t.TempDir(), "oracle"), NodeExecutable: "node",
	})
	if err == nil || !strings.Contains(err.Error(), "content mismatch") {
		t.Fatalf("expected count mismatch, got %v", err)
	}
}

func TestPrepareNPMOracleRejectsManifestSafetyLimitDrift(t *testing.T) {
	archive := makeTarball(t, []tarEntry{{Name: "package/bin/cli.mjs", Data: []byte("oracle"), Mode: 0o755}})
	manifestPath, archivePath := writeOracleFixture(t, archive)
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["artifact"].(map[string]any)["size"] = maxOracleCompressedSize + 1
	changed, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = PrepareNPMOracle(context.Background(), OracleOptions{
		ManifestPath: manifestPath, CachedTarball: archivePath, CachedRuntimeTarballs: runtimeCache(archivePath), Destination: filepath.Join(t.TempDir(), "oracle"), NodeExecutable: "node",
	})
	if err == nil || !strings.Contains(err.Error(), "fixed artifact safety limits") {
		t.Fatalf("expected fixed safety-limit rejection, got %v", err)
	}
}

func TestPrepareNPMOracleFailsClosedOnSizeAndDigestDrift(t *testing.T) {
	archive := makeTarball(t, []tarEntry{{Name: "package/bin/cli.mjs", Data: []byte("oracle"), Mode: 0o755}})

	t.Run("size", func(t *testing.T) {
		manifestPath, archivePath := writeOracleFixture(t, archive)
		if err := os.WriteFile(archivePath, append(archive, 'x'), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := PrepareNPMOracle(context.Background(), OracleOptions{
			ManifestPath: manifestPath, CachedTarball: archivePath, CachedRuntimeTarballs: runtimeCache(archivePath), Destination: filepath.Join(t.TempDir(), "oracle"), NodeExecutable: "node",
		})
		if err == nil || !strings.Contains(err.Error(), "size") {
			t.Fatalf("expected size failure, got %v", err)
		}
	})

	for _, field := range []string{"sha1", "sha256", "sha512", "integrity"} {
		t.Run(field, func(t *testing.T) {
			manifestPath, archivePath := writeOracleFixture(t, archive)
			manifestBytes, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var manifest map[string]any
			if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
				t.Fatal(err)
			}
			manifest["artifact"].(map[string]any)[field] = "drifted"
			changed, _ := json.Marshal(manifest)
			if err := os.WriteFile(manifestPath, changed, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = PrepareNPMOracle(context.Background(), OracleOptions{
				ManifestPath: manifestPath, CachedTarball: archivePath, CachedRuntimeTarballs: runtimeCache(archivePath), Destination: filepath.Join(t.TempDir(), "oracle"), NodeExecutable: "node",
			})
			if err == nil || !strings.Contains(err.Error(), field+" mismatch") {
				t.Fatalf("expected %s failure, got %v", field, err)
			}
		})
	}
}

func TestPrepareNPMOracleRejectsMutableArtifactURLs(t *testing.T) {
	archive := makeTarball(t, []tarEntry{{Name: "package/bin/cli.mjs", Data: []byte("oracle"), Mode: 0o755}})
	manifestPath, archivePath := writeOracleFixture(t, archive)
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["artifact"].(map[string]any)["url"] = "https://registry.invalid/latest"
	changed, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = PrepareNPMOracle(context.Background(), OracleOptions{
		ManifestPath: manifestPath, CachedTarball: archivePath, CachedRuntimeTarballs: runtimeCache(archivePath), Destination: filepath.Join(t.TempDir(), "oracle"), NodeExecutable: "node",
	})
	if err == nil || !strings.Contains(err.Error(), "not the pinned open-skills-0.1.2.tgz tarball") {
		t.Fatalf("expected mutable URL failure, got %v", err)
	}
}

func TestPrepareNPMOracleCanFetchPinnedURLWithoutUsingLatest(t *testing.T) {
	archive := makeTarball(t, []tarEntry{{Name: "package/bin/cli.mjs", Data: []byte("oracle"), Mode: 0o755}})
	requested := make(chan string, 1)
	server := http.Server{}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested <- r.URL.Path
		_, _ = w.Write(archive)
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	manifestPath, _ := writeOracleFixture(t, archive)
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["artifact"].(map[string]any)["url"] = "http://" + listener.Addr().String() + "/pinned/open-skills-0.1.2.tgz"
	changed, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, changed, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = PrepareNPMOracle(context.Background(), OracleOptions{
		ManifestPath: manifestPath, CachedRuntimeTarballs: runtimeCache(filepath.Join(filepath.Dir(manifestPath), "oracle.tgz")), Destination: filepath.Join(t.TempDir(), "oracle"), NodeExecutable: "node",
	})
	if err != nil {
		t.Fatalf("PrepareNPMOracle fetch: %v", err)
	}
	select {
	case path := <-requested:
		if path != "/pinned/open-skills-0.1.2.tgz" {
			t.Fatalf("requested %q", path)
		}
	default:
		t.Fatal("artifact was not requested")
	}
}

func TestPrepareNPMOracleRejectsUnsafeArchiveEntries(t *testing.T) {
	for _, entry := range []tarEntry{
		{Name: "../escape", Data: []byte("bad"), Mode: 0o644},
		{Name: "package/../escape", Data: []byte("bad"), Mode: 0o644},
		{Name: "/absolute", Data: []byte("bad"), Mode: 0o644},
		{Name: `C:\absolute`, Data: []byte("bad"), Mode: 0o644},
		{Name: "package/link", Linkname: "../../escape", Typeflag: tar.TypeSymlink, Mode: 0o777},
		{Name: "package/directory", Typeflag: tar.TypeDir, Mode: 0o755},
	} {
		t.Run(strings.ReplaceAll(entry.Name, "/", "_"), func(t *testing.T) {
			archive := makeTarball(t, []tarEntry{entry})
			manifestPath, archivePath := writeOracleFixture(t, archive)
			destination := filepath.Join(t.TempDir(), "extract")
			_, err := PrepareNPMOracle(context.Background(), OracleOptions{
				ManifestPath: manifestPath, CachedTarball: archivePath, CachedRuntimeTarballs: runtimeCache(archivePath), Destination: destination, NodeExecutable: "node",
			})
			if err == nil || !strings.Contains(err.Error(), "unsafe oracle archive") {
				t.Fatalf("expected unsafe archive failure, got %v", err)
			}
			if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("unsafe extraction left destination behind: %v", statErr)
			}
		})
	}
}

func TestFileFixturesRejectSymlinkParentsAndSandboxEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unprivileged Windows symlink creation is unavailable")
	}
	root := t.TempDir()
	paths := SandboxPaths{Root: root, Project: filepath.Join(root, "project"), Home: filepath.Join(root, "home"), Temp: filepath.Join(root, "tmp")}
	for _, directory := range []string{paths.Project, paths.Home, paths.Temp} {
		_ = os.MkdirAll(directory, 0o755)
	}
	_ = os.Mkdir(filepath.Join(root, "outside"), 0o755)
	_ = os.Symlink(filepath.Join(root, "outside"), filepath.Join(paths.Project, "linked"))
	err := materializeFiles(paths, []FileFixture{{Root: ProjectRoot, Path: "linked/escape", Data: []byte("bad")}}, func(value string) string { return value })
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink-parent rejection, got %v", err)
	}
	err = materializeFiles(paths, []FileFixture{{Root: ProjectRoot, Path: "escape-link", Symlink: filepath.Join(filepath.Dir(root), "host")}}, func(value string) string { return value })
	if err == nil || !strings.Contains(err.Error(), "escapes sandbox") {
		t.Fatalf("expected escaping-target rejection, got %v", err)
	}
}

func TestLockCaptureRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unprivileged Windows symlink creation is unavailable")
	}
	root := t.TempDir()
	project, home := filepath.Join(root, "project"), filepath.Join(root, "home")
	_ = os.MkdirAll(project, 0o755)
	_ = os.MkdirAll(home, 0o755)
	target := filepath.Join(root, "target-lock")
	_ = os.WriteFile(target, []byte(`{"secret":true}`), 0o600)
	_ = os.Symlink(target, filepath.Join(project, "skills-lock.json"))
	observation := Observation{Paths: SandboxPaths{Project: project}, Locks: map[LockLocation][]byte{}, ParsedLocks: map[LockLocation]any{}, LockParseErrors: map[LockLocation]string{}}
	captureLocks(&observation, map[string]string{"HOME": home, "XDG_STATE_HOME": filepath.Join(home, ".state")})
	if _, ok := observation.Locks[ProjectLock]; ok || observation.LockParseErrors[ProjectLock] != "lock path is not a regular file" {
		t.Fatalf("symlinked lock was followed: %#v", observation)
	}
}

func TestHTTPFixtureRoutesIncludeQueryAndBoundBodies(t *testing.T) {
	var mu sync.Mutex
	requests := []HTTPRequest{}
	server := newFixtureServer([]HTTPRoute{{Method: http.MethodPost, Path: "/route", Query: "kind=one", Body: []byte("ok")}}, &mu, &requests)
	defer server.Close()
	response, err := http.Post(server.URL+"/route?kind=two", "text/plain", strings.NewReader("small"))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("query mismatch status = %d", response.StatusCode)
	}
	_ = response.Body.Close()
	response, err = http.Post(server.URL+"/route?kind=one", "text/plain", bytes.NewReader(make([]byte, maxFixtureRequestBody+1)))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status = %d", response.StatusCode)
	}
	_ = response.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0].Path != "/route?kind=two" || len(requests[1].Body) != maxFixtureRequestBody+1 {
		t.Fatalf("captured requests = %#v", requests)
	}
}

func TestD01BuildNativeProducesOnlyOpenSkillsAndRunsWithoutNode(t *testing.T) {
	repositoryRoot := testModuleRoot(t)
	preexisting := t.TempDir()
	sentinel := filepath.Join(preexisting, "sentinel")
	_ = os.WriteFile(sentinel, []byte("keep"), 0o600)
	if _, err := BuildNative(context.Background(), repositoryRoot, preexisting); err == nil {
		t.Fatal("BuildNative accepted a preexisting stage")
	}
	if content, err := os.ReadFile(sentinel); err != nil || string(content) != "keep" {
		t.Fatalf("BuildNative modified preexisting stage: %q, %v", content, err)
	}

	stage := filepath.Join(t.TempDir(), "stage")
	target, err := BuildNative(context.Background(), repositoryRoot, stage)
	if err != nil {
		t.Fatalf("BuildNative: %v", err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		t.Fatal(err)
	}
	wantName := "open-skills"
	if runtime.GOOS == "windows" {
		wantName += ".exe"
	}
	if len(entries) != 1 || entries[0].Name() != wantName {
		t.Fatalf("staged executables = %v, want only %s", entryNames(entries), wantName)
	}
	command := exec.Command(target.Command)
	command.Env = []string{"PATH="}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native bootstrap failed without Node in PATH: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "The open agent skills ecosystem") || !strings.Contains(string(output), "open-skills add") {
		t.Fatalf("native command shell did not start without Node, got %q", output)
	}
}

func TestFixtureProcess(t *testing.T) {
	if os.Getenv("OPEN_SKILLS_FIXTURE_PROCESS") != "1" {
		return
	}
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(90)
	}
	switch os.Args[separator+1] {
	case "sleep":
		time.Sleep(time.Second)
		os.Exit(0)
	case "probe":
		runFixtureProbe()
	case "environment":
		fmt.Printf("token=%s\nproxy=%s\npath=%s\n", os.Getenv("GITHUB_TOKEN"), os.Getenv("HTTPS_PROXY"), os.Getenv("PATH"))
		os.Exit(0)
	case "network-http":
		_, _ = http.Get(os.Getenv("NETWORK_URL"))
		os.Exit(0)
	case "network-command":
		_ = exec.Command("curl", "https://example.invalid").Run()
		os.Exit(0)
	case "custom-lock":
		lock := filepath.Join(os.Getenv("XDG_STATE_HOME"), "skills", ".skill-lock.json")
		_ = os.MkdirAll(filepath.Dir(lock), 0o755)
		_ = os.WriteFile(lock, []byte(`{"custom":true}`), 0o600)
		os.Exit(0)
	case "undeclared":
		err := exec.Command("not-configured").Run()
		fmt.Printf("undeclared=%v\n", err != nil)
		os.Exit(0)
	case "spawn-sleeper":
		child := exec.Command(os.Args[0], "-test.run=TestFixtureProcess", "--", "descendant")
		child.Env = append(os.Environ(), "OPEN_SKILLS_FIXTURE_PROCESS=1")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		_ = child.Start()
		time.Sleep(time.Second)
		os.Exit(0)
	case "spawn-background":
		child := exec.Command(os.Args[0], "-test.run=TestFixtureProcess", "--", "delayed-marker")
		child.Env = append(os.Environ(), "OPEN_SKILLS_FIXTURE_PROCESS=1")
		_ = child.Start()
		os.Exit(0)
	case "delayed-marker":
		time.Sleep(time.Second)
		_ = os.WriteFile(os.Getenv("DESCENDANT_MARKER"), []byte("survived"), 0o600)
		os.Exit(0)
	case "descendant":
		time.Sleep(time.Second)
		os.Exit(0)
	default:
		os.Exit(91)
	}
}

func runFixtureProbe() {
	input, _ := io.ReadAll(os.Stdin)
	project, home, temp := os.Getenv("OPEN_SKILLS_PROJECT"), os.Getenv("HOME"), os.Getenv("TMPDIR")
	projectFixture, _ := os.ReadFile(filepath.Join(project, "fixture.txt"))
	homeFixture, _ := os.ReadFile(filepath.Join(home, "home.txt"))
	tempFixture, _ := os.ReadFile(filepath.Join(temp, "temp.txt"))
	fmt.Printf("stdin=%s\nenv=%s\n%s\n%s\n%s\n", input, os.Getenv("PROBE_VALUE"), projectFixture, homeFixture, tempFixture)

	_ = os.WriteFile(filepath.Join(project, "generated.txt"), []byte("generated"), 0o640)
	_ = os.Symlink("generated.txt", filepath.Join(project, "generated-link"))
	_ = os.WriteFile(filepath.Join(project, "skills-lock.json"), []byte(`{"version":1}`), 0o600)
	global := filepath.Join(os.Getenv("XDG_STATE_HOME"), "skills", ".skill-lock.json")
	_ = os.MkdirAll(filepath.Dir(global), 0o755)
	_ = os.WriteFile(global, []byte(`{"version":3}`), 0o600)

	request, _ := http.NewRequest(http.MethodPost, os.Getenv("SERVER_URL")+"/fixture", strings.NewReader("request body"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		fmt.Printf("http-error=%v\n", err)
	} else {
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		fmt.Printf("http=%d:%s\n", response.StatusCode, body)
	}

	repositoryOutput, _ := os.ReadFile(filepath.Join(os.Getenv("REPO_PATH"), "README.md"))
	fmt.Printf("git=%s", repositoryOutput)
	child := exec.Command("fixture-child", "one", "two words")
	child.Dir = project
	var childOut, childErr bytes.Buffer
	child.Stdout, child.Stderr = &childOut, &childErr
	err = child.Run()
	code := 0
	if err != nil {
		code = err.(*exec.ExitError).ExitCode()
	}
	fmt.Printf("child=%d:%s:%s\n", code, childOut.String(), childErr.String())
	_, _ = os.Stderr.WriteString("probe stderr\n")
	os.Exit(23)
}

func fixtureTarget() Target {
	return Target{
		Name:    "fixture",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestFixtureProcess", "--"},
		Env:     map[string]string{"OPEN_SKILLS_FIXTURE_PROCESS": "1"},
	}
}

func assertFile(t *testing.T, observation Observation, path string, expected FileState) {
	t.Helper()
	actual, ok := observation.Files[path]
	if !ok {
		t.Fatalf("missing snapshot path %s", path)
	}
	if actual.Kind != expected.Kind || string(actual.Data) != string(expected.Data) || actual.LinkTarget != expected.LinkTarget {
		t.Errorf("file %s = %#v, want %#v", path, actual, expected)
	}
}

func containsDifference(differences []Difference, field string) bool {
	for _, difference := range differences {
		if difference.Field == field {
			return true
		}
	}
	return false
}

type tarEntry struct {
	Name     string
	Data     []byte
	Mode     int64
	Typeflag byte
	Linkname string
}

func makeTarball(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		typeflag := entry.Typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{Name: entry.Name, Size: int64(len(entry.Data)), Mode: entry.Mode, Typeflag: typeflag, Linkname: entry.Linkname}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := tarWriter.Write(entry.Data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func writeOracleFixture(t *testing.T, archive []byte) (string, string) {
	t.Helper()
	directory := t.TempDir()
	archivePath := filepath.Join(directory, "oracle.tgz")
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"schemaVersion": 1,
		"package":       map[string]any{"name": "@engblock/open-skills", "version": "0.1.2"},
		"artifact":      artifactFixture("https://registry.invalid/open-skills-0.1.2.tgz", archive, countTarballFiles(t, archive)),
	}
	manifestBytes, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(directory, "oracle.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	yamlArchive := makeTarball(t, []tarEntry{{Name: "package/index.js", Data: []byte("export const yaml = true;\n"), Mode: 0o644}})
	yamlPath := filepath.Join(directory, "yaml.tgz")
	if err := os.WriteFile(yamlPath, yamlArchive, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := map[string]any{
		"schemaVersion": 1,
		"dependencies": []any{map[string]any{
			"name": "yaml", "version": "2.9.0",
			"artifact": artifactFixture("https://registry.invalid/yaml-2.9.0.tgz", yamlArchive, countTarballFiles(t, yamlArchive)),
		}},
	}
	runtimeBytes, _ := json.Marshal(runtime)
	if err := os.WriteFile(filepath.Join(directory, "runtime-dependencies.json"), runtimeBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return manifestPath, archivePath
}

func artifactFixture(url string, archive []byte, fileCount int) map[string]any {
	one, two, five := sha1.Sum(archive), sha256.Sum256(archive), sha512.Sum512(archive)
	unpackedSize := tarballUnpackedSize(archive)
	if fileCount == 0 {
		fileCount = 1
	}
	if unpackedSize == 0 {
		unpackedSize = 1
	}
	return map[string]any{
		"url": url, "size": len(archive), "fileCount": fileCount, "unpackedSize": unpackedSize,
		"sha1": hex.EncodeToString(one[:]), "sha256": hex.EncodeToString(two[:]), "sha512": hex.EncodeToString(five[:]),
		"integrity": "sha512-" + base64.StdEncoding.EncodeToString(five[:]),
	}
}

func runtimeCache(archivePath string) map[string]string {
	return map[string]string{"yaml": filepath.Join(filepath.Dir(archivePath), "yaml.tgz")}
}

func countTarballFiles(t *testing.T, archive []byte) int {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tarReader := tar.NewReader(reader)
	count := 0
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			count++
		}
	}
	return count
}

func tarballUnpackedSize(archive []byte) int64 {
	reader, _ := gzip.NewReader(bytes.NewReader(archive))
	tarReader := tar.NewReader(reader)
	var size int64
	for {
		header, err := tarReader.Next()
		if err != nil {
			break
		}
		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			size += header.Size
		}
	}
	return size
}

func testModuleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}
