package compatibility

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

const goldenCorpusRoot = "testdata/golden/v1"

var (
	goldenIDPattern       = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
	rfc3339Field          = regexp.MustCompile(`("(?:installedAt|updatedAt|approvedAt)"\s*:\s*")\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z(")`)
	goldenTemporaryName   = regexp.MustCompile(`\b(open-skills-git|open-skills-well-known|skills-use)-[A-Za-z0-9]+\b`)
	goldenTransactionPath = regexp.MustCompile(`(transactions/)[0-9a-f]{24}\b`)
	presentationSpacing   = regexp.MustCompile(`[\t ]{2,}`)
)

type goldenManifest struct {
	SchemaVersion        int                `json:"schemaVersion"`
	Baseline             string             `json:"baseline"`
	OracleManifestSHA256 string             `json:"oracleManifestSha256"`
	NormalizerVersion    int                `json:"normalizerVersion"`
	Normalization        []string           `json:"normalization"`
	Scenarios            []goldenScenario   `json:"scenarios"`
	Divergences          []goldenDivergence `json:"divergences"`
}

type goldenScenario struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Expectation   string      `json:"expectation"`
	Covers        []string    `json:"covers"`
	Divergences   []string    `json:"divergences,omitempty"`
	Normalization []string    `json:"normalization,omitempty"`
	Input         goldenInput `json:"input"`
}

type goldenDivergence struct {
	ID           string   `json:"id"`
	Expectation  string   `json:"expectation"`
	Rationale    string   `json:"rationale"`
	MigrationDoc string   `json:"migrationDoc"`
	Scenarios    []string `json:"scenarios"`
	Verification []string `json:"verification"`
}

type goldenInput struct {
	Offline      bool               `json:"offline,omitempty"`
	Args         []string           `json:"args,omitempty"`
	Stdin        string             `json:"stdin,omitempty"`
	Env          map[string]string  `json:"env,omitempty"`
	Files        []goldenFileInput  `json:"files,omitempty"`
	Repositories []goldenRepository `json:"repositories,omitempty"`
	HTTPRoutes   []goldenHTTPRoute  `json:"httpRoutes,omitempty"`
	Commands     []goldenCommand    `json:"commands,omitempty"`
}

type goldenFileInput struct {
	Root      FixtureRoot `json:"root"`
	Path      string      `json:"path"`
	Text      *string     `json:"text,omitempty"`
	Fixture   string      `json:"fixture,omitempty"`
	Mode      uint32      `json:"mode,omitempty"`
	Symlink   string      `json:"symlink,omitempty"`
	Junction  string      `json:"junction,omitempty"`
	Directory bool        `json:"directory,omitempty"`
}

type goldenRepository struct {
	Name  string            `json:"name"`
	Files map[string]string `json:"files"`
}

type goldenHTTPRoute struct {
	Method string              `json:"method"`
	Path   string              `json:"path"`
	Query  string              `json:"query,omitempty"`
	Status int                 `json:"status,omitempty"`
	Header map[string][]string `json:"header,omitempty"`
	Body   string              `json:"body,omitempty"`
}

type goldenCommand struct {
	Name        string `json:"name"`
	Stdout      string `json:"stdout,omitempty"`
	Stderr      string `json:"stderr,omitempty"`
	ExitCode    int    `json:"exitCode,omitempty"`
	Passthrough bool   `json:"passthrough,omitempty"`
}

type goldenObservation struct {
	Stdout          string                     `json:"stdout"`
	Stderr          string                     `json:"stderr"`
	ExitCode        int                        `json:"exitCode"`
	TimedOut        bool                       `json:"timedOut"`
	ProcessError    string                     `json:"processError"`
	Files           map[string]goldenFileState `json:"files"`
	Locks           map[LockLocation]string    `json:"locks"`
	LockParseErrors map[LockLocation]string    `json:"lockParseErrors"`
	HTTPRequests    []goldenHTTPRequest        `json:"httpRequests"`
	SpawnedCommands []SpawnedCommand           `json:"spawnedCommands"`
}

type goldenFileState struct {
	Kind       FileKind `json:"kind"`
	Mode       uint32   `json:"mode"`
	Text       *string  `json:"text,omitempty"`
	Base64     string   `json:"base64,omitempty"`
	LinkTarget string   `json:"linkTarget,omitempty"`
}

type goldenHTTPRequest struct {
	Method string      `json:"method"`
	Path   string      `json:"path"`
	Host   string      `json:"host"`
	Header http.Header `json:"header"`
	Text   *string     `json:"text,omitempty"`
	Base64 string      `json:"base64,omitempty"`
}

// TestNativeGoldenCorpus is the issue #40 acceptance seam. It builds (or uses)
// one native executable, runs every reviewed scenario, and compares complete
// process observations without preparing or executing the Node oracle.
func TestNativeGoldenCorpus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the v1 reviewed corpus records Unix file modes; Windows remains covered by native process tests")
	}
	manifest := readGoldenManifest(t)
	validateGoldenManifest(t, manifest)
	target := nativeGoldenTarget(t)
	outcomeDirectory := filepath.Join(goldenCorpusRoot, "outcomes")
	assertGoldenOutcomeInventory(t, manifest, outcomeDirectory)

	for _, reviewed := range manifest.Scenarios {
		reviewed := reviewed
		t.Run(reviewed.ID, func(t *testing.T) {
			scenario := materializeGoldenScenario(t, reviewed.Input)
			observation, err := (Harness{}).Run(context.Background(), target, scenario)
			if err != nil {
				t.Fatalf("run native scenario: %v", err)
			}
			assertNoNodeOrNPM(t, observation)
			assertJSONScenarioObservation(t, reviewed.Input, observation)
			actual := encodeGoldenObservation(t, reviewed, observation)
			expectedPath := filepath.Join(outcomeDirectory, reviewed.ID+".json")
			expected, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read reviewed outcome: %v", err)
			}
			if !bytes.Equal(expected, actual) {
				t.Fatalf("native outcome differs from reviewed golden %s:\n%s", expectedPath, firstGoldenDifference(expected, actual))
			}
		})
	}
}

// TestRecordGoldenCorpus is an opt-in maintainer recorder. Compatibility cases
// run the integrity-pinned npm 0.1.2 CLI; approved divergence cases run native.
// It refuses to overwrite the reviewed v1 directory: record elsewhere, inspect
// the diff, then copy the accepted outcomes into the corpus in a separate step.
func TestRecordGoldenCorpus(t *testing.T) {
	destination := os.Getenv("OPEN_SKILLS_RECORD_GOLDEN_DIR")
	if destination == "" {
		t.Skip("set OPEN_SKILLS_RECORD_GOLDEN_DIR to record candidate outcomes")
	}
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		t.Fatal(err)
	}
	absoluteCorpus, err := filepath.Abs(goldenCorpusRoot)
	if err != nil {
		t.Fatal(err)
	}
	if pathWithin(absoluteCorpus, absoluteDestination) || pathWithin(absoluteDestination, absoluteCorpus) {
		t.Fatalf("candidate directory must be outside reviewed corpus: %s", absoluteDestination)
	}
	manifest := readGoldenManifest(t)
	validateGoldenManifest(t, manifest)
	if err := os.MkdirAll(absoluteDestination, 0o755); err != nil {
		t.Fatal(err)
	}

	native := nativeGoldenTarget(t)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("find Node for opt-in oracle recording: %v", err)
	}
	oracle, err := PrepareNPMOracle(context.Background(), OracleOptions{
		ManifestPath:   filepath.Join(testModuleRoot(t), "compatibility", "npm-0.1.2", "oracle.json"),
		Destination:    filepath.Join(t.TempDir(), "oracle"),
		NodeExecutable: node,
	})
	if err != nil {
		t.Fatalf("prepare pinned npm oracle: %v", err)
	}
	oracle.Env = map[string]string{"NODE_DISABLE_COMPILE_CACHE": "1"}

	for _, reviewed := range manifest.Scenarios {
		target := oracle
		if reviewed.Expectation == "native-divergence" {
			target = native
		}
		observation, err := (Harness{}).Run(context.Background(), target, materializeGoldenScenario(t, reviewed.Input))
		if err != nil {
			t.Fatalf("record %s: %v", reviewed.ID, err)
		}
		assertJSONScenarioObservation(t, reviewed.Input, observation)
		encoded := encodeGoldenObservation(t, reviewed, observation)
		if err := os.WriteFile(filepath.Join(absoluteDestination, reviewed.ID+".json"), encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readGoldenManifest(t *testing.T) goldenManifest {
	t.Helper()
	path := filepath.Join(goldenCorpusRoot, "manifest.json")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open golden manifest: %v", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReader(file))
	decoder.DisallowUnknownFields()
	var manifest goldenManifest
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("decode golden manifest: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("golden manifest has trailing JSON: %v", err)
	}
	return manifest
}

func validateGoldenManifest(t *testing.T, manifest goldenManifest) {
	t.Helper()
	if manifest.SchemaVersion != 1 || manifest.NormalizerVersion != 1 || manifest.Baseline != "@engblock/open-skills@0.1.2" {
		t.Fatalf("unsupported corpus identity: schema=%d normalizer=%d baseline=%q", manifest.SchemaVersion, manifest.NormalizerVersion, manifest.Baseline)
	}
	oracle, err := os.ReadFile(filepath.Join(testModuleRoot(t), "compatibility", "npm-0.1.2", "oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(oracle)
	if manifest.OracleManifestSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("corpus oracle manifest digest = %q, want %x", manifest.OracleManifestSHA256, digest)
	}
	allowedNormalization := []string{
		"ansi:presentation-streams",
		"crlf:presentation-and-lock-text",
		"filesystem:file-kind-and-portable-permissions",
		"network:fixture-host-and-port",
		"path:generated-workspace-and-transaction-identifiers",
		"path:platform-separators-in-path-fields",
		"path:sandbox-home-project-temp-and-fixture-url",
		"symlink:path-kind-and-resolved-sandbox-target",
		"terminal-layout:repeated-horizontal-spacing",
		"timestamp:installedAt-updatedAt-approvedAt-json-fields",
	}
	if !reflect.DeepEqual(manifest.Normalization, allowedNormalization) {
		t.Fatalf("golden normalization must be the reviewed typed policy:\n got %#v\nwant %#v", manifest.Normalization, allowedNormalization)
	}

	requiredCoverage := []string{
		"command:root", "command:add", "command:use", "command:remove", "command:list", "command:trust", "command:find", "command:check", "command:update", "command:experimental_install", "command:init", "command:experimental_sync",
		"alias:-h", "alias:-v", "alias:a", "alias:f", "alias:i", "alias:i-add", "alias:install", "alias:install-add", "alias:ls", "alias:r", "alias:rm", "alias:s", "alias:search", "alias:upgrade",
		"source:local", "source:github", "source:gitlab", "source:generic-git", "source:well-known",
		"scope:project", "scope:global",
		"topology:canonical-symlink", "topology:copy", "topology:eve-root-and-subagents", "topology:all-retained-global-adapters", "topology:openclaw-legacy-global",
		"agent-launch:claude-code", "agent-launch:codex",
		"decision:yes", "decision:selection", "decision:replace", "decision:force", "decision:trust", "decision:allow-insecure-transport",
		"shell:agent-banner-suppression", "shell:subcommand-help",
		"lock:project-valid", "lock:project-malformed", "lock:project-older", "lock:project-newer", "lock:project-unknown-fields",
		"lock:global-valid", "lock:global-malformed", "lock:global-older", "lock:global-newer", "lock:global-unknown-fields",
	}
	aliasCommands := map[string]string{
		"alias:-h": "-h", "alias:-v": "-v", "alias:a": "a",
		"alias:f": "f", "alias:i": "i", "alias:i-add": "i",
		"alias:install": "install", "alias:install-add": "install",
		"alias:ls": "ls", "alias:r": "r", "alias:rm": "rm",
		"alias:s": "s", "alias:search": "search", "alias:upgrade": "upgrade",
	}
	covered := map[string]bool{}
	scenarioIDs := map[string]bool{}
	for index, scenario := range manifest.Scenarios {
		if !goldenIDPattern.MatchString(scenario.ID) || scenarioIDs[scenario.ID] {
			t.Fatalf("scenario %d has invalid or duplicate id %q", index, scenario.ID)
		}
		scenarioIDs[scenario.ID] = true
		if strings.TrimSpace(scenario.Title) == "" {
			t.Fatalf("scenario %s has no reviewed title", scenario.ID)
		}
		if scenario.Expectation != "npm-0.1.2" && scenario.Expectation != "native-divergence" {
			t.Fatalf("scenario %s has invalid expectation %q", scenario.ID, scenario.Expectation)
		}
		if scenario.Expectation == "npm-0.1.2" && len(scenario.Divergences) != 0 || scenario.Expectation == "native-divergence" && len(scenario.Divergences) == 0 {
			t.Fatalf("scenario %s does not name divergences consistently", scenario.ID)
		}
		for _, normalization := range scenario.Normalization {
			if normalization != "terminal-layout:repeated-horizontal-spacing" {
				t.Errorf("scenario %s requests unsupported normalization %q", scenario.ID, normalization)
			}
			if goldenInputUsesJSON(scenario.Input) {
				t.Errorf("JSON scenario %s may not normalize terminal layout", scenario.ID)
			}
		}
		for _, tag := range scenario.Covers {
			if command, isAlias := aliasCommands[tag]; isAlias && (len(scenario.Input.Args) == 0 || scenario.Input.Args[0] != command) {
				t.Errorf("scenario %s claims %s but invokes %#v", scenario.ID, tag, scenario.Input.Args)
				continue
			}
			covered[tag] = true
		}
	}
	for _, requirement := range requiredCoverage {
		if !covered[requirement] {
			t.Errorf("reviewed corpus does not cover %s", requirement)
		}
	}

	divergenceIDs := map[string]bool{}
	indexedDivergenceScenarios := map[string]bool{}
	for _, divergence := range manifest.Divergences {
		if divergenceIDs[divergence.ID] {
			t.Fatalf("duplicate divergence %s", divergence.ID)
		}
		divergenceIDs[divergence.ID] = true
		if strings.TrimSpace(divergence.Expectation) == "" || strings.TrimSpace(divergence.Rationale) == "" || strings.TrimSpace(divergence.MigrationDoc) == "" {
			t.Errorf("divergence %s lacks a named expectation, rationale, or migration reference", divergence.ID)
		}
		if len(divergence.Scenarios) == 0 && len(divergence.Verification) == 0 {
			t.Errorf("divergence %s has no observable verification", divergence.ID)
		}
		for _, scenario := range divergence.Scenarios {
			if !scenarioIDs[scenario] {
				t.Errorf("divergence %s references unknown scenario %s", divergence.ID, scenario)
			}
			indexedDivergenceScenarios[divergence.ID+"\x00"+scenario] = true
		}
	}
	for number := 1; number <= 13; number++ {
		id := fmt.Sprintf("D%02d", number)
		if !divergenceIDs[id] {
			t.Errorf("reviewed corpus is missing divergence %s", id)
		}
	}
	for _, scenario := range manifest.Scenarios {
		for _, divergence := range scenario.Divergences {
			if !divergenceIDs[divergence] {
				t.Errorf("scenario %s references unknown divergence %s", scenario.ID, divergence)
			}
			if !indexedDivergenceScenarios[divergence+"\x00"+scenario.ID] {
				t.Errorf("scenario %s names %s but the divergence index does not link it back", scenario.ID, divergence)
			}
		}
	}
}

func assertGoldenOutcomeInventory(t *testing.T, manifest goldenManifest, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read golden outcomes: %v", err)
	}
	want := map[string]bool{}
	for _, scenario := range manifest.Scenarios {
		want[scenario.ID+".json"] = true
	}
	for _, entry := range entries {
		if entry.IsDir() || !want[entry.Name()] {
			t.Errorf("unreviewed extra golden outcome %s", entry.Name())
		}
		delete(want, entry.Name())
	}
	for missing := range want {
		t.Errorf("missing reviewed golden outcome %s", missing)
	}
}

func materializeGoldenScenario(t *testing.T, input goldenInput) Scenario {
	t.Helper()
	result := Scenario{Offline: input.Offline, Args: input.Args, Stdin: []byte(input.Stdin), Env: input.Env}
	for _, file := range input.Files {
		if file.Text != nil && file.Fixture != "" {
			t.Fatalf("fixture %s specifies both text and fixture data", file.Path)
		}
		data := []byte{}
		if file.Text != nil {
			data = []byte(*file.Text)
		}
		if file.Fixture != "" {
			path := filepath.Join(goldenCorpusRoot, filepath.FromSlash(file.Fixture))
			absoluteRoot, _ := filepath.Abs(goldenCorpusRoot)
			absolutePath, _ := filepath.Abs(path)
			if !pathWithin(absoluteRoot, absolutePath) {
				t.Fatalf("fixture path escapes corpus: %s", file.Fixture)
			}
			var err error
			data, err = os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture %s: %v", file.Fixture, err)
			}
		}
		result.Files = append(result.Files, FileFixture{
			Root: file.Root, Path: file.Path, Data: data, Mode: fs.FileMode(file.Mode),
			Symlink: file.Symlink, Junction: file.Junction, Directory: file.Directory,
		})
	}
	for _, repository := range input.Repositories {
		files := make(map[string][]byte, len(repository.Files))
		for path, text := range repository.Files {
			files[path] = []byte(text)
		}
		result.Repositories = append(result.Repositories, RepositoryFixture{Name: repository.Name, Files: files})
	}
	for _, route := range input.HTTPRoutes {
		result.HTTPRoutes = append(result.HTTPRoutes, HTTPRoute{
			Method: route.Method, Path: route.Path, Query: route.Query, Status: route.Status,
			Header: http.Header(route.Header), Body: []byte(route.Body),
		})
	}
	for _, command := range input.Commands {
		result.Commands = append(result.Commands, CommandFixture{
			Name: command.Name, Stdout: command.Stdout, Stderr: command.Stderr,
			ExitCode: command.ExitCode, Passthrough: command.Passthrough,
		})
	}
	return result
}

func nativeGoldenTarget(t *testing.T) Target {
	t.Helper()
	if configured := os.Getenv("OPEN_SKILLS_NATIVE_UNDER_TEST"); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatalf("OPEN_SKILLS_NATIVE_UNDER_TEST must be absolute: %s", configured)
		}
		return Target{Name: "open-skills", Command: configured}
	}
	target, err := BuildNative(context.Background(), testModuleRoot(t), filepath.Join(t.TempDir(), "native"))
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func encodeGoldenObservation(t *testing.T, reviewed goldenScenario, observation Observation) []byte {
	t.Helper()
	textFiles := make([]string, 0)
	for path := range observation.Files {
		if isTimestampedJSONPath(path) {
			textFiles = append(textFiles, path)
		}
	}
	fixtureHost := strings.TrimPrefix(observation.Paths.FixtureURL, "http://")
	normalized := normalizedObservation(observation, Normalization{
		TextFiles: textFiles,
		Replacements: []Replacement{
			{Pattern: fixtureHost, With: "<fixture-host>"},
		},
	})
	linkTargets := make(map[string]string)
	for path, state := range observation.Files {
		if state.Kind == FileKindSymlink {
			linkTargets[path] = goldenLinkTarget(observation, path, state.LinkTarget)
		}
	}
	normalizeLayout := scenarioUsesLayoutNormalization(reviewed)
	stdout := normalizeGoldenPresentation(normalized.Stdout, normalizeLayout)
	if goldenInputUsesJSON(reviewed.Input) {
		stdout = normalizeGoldenPathFields(normalized.Stdout)
	}
	result := goldenObservation{
		Stdout: stdout, Stderr: normalizeGoldenPresentation(normalized.Stderr, normalizeLayout),
		ExitCode: normalized.ExitCode, TimedOut: normalized.TimedOut,
		ProcessError:    normalizeGoldenPathFields(normalized.ProcessError),
		Files:           make(map[string]goldenFileState, len(normalized.Files)),
		Locks:           make(map[LockLocation]string, len(normalized.Locks)),
		LockParseErrors: normalized.LockParseErrors,
		HTTPRequests:    make([]goldenHTTPRequest, 0, len(normalized.HTTPRequests)),
		SpawnedCommands: normalized.SpawnedCommands,
	}
	for path, state := range normalized.Files {
		mode := state.Mode.Perm()
		if state.Kind == FileKindSymlink {
			mode = 0
		}
		file := goldenFileState{Kind: state.Kind, Mode: uint32(mode), LinkTarget: linkTargets[path]}
		data := state.Data
		if isTimestampedJSONPath(path) {
			data = []byte(normalizeGoldenPathFields(string(normalizeGoldenJSONTimestamps(data))))
		}
		file.Text, file.Base64 = goldenBytes(data)
		path = normalizeGoldenPathFields(path)
		if _, exists := result.Files[path]; exists {
			t.Fatalf("normalization collapsed distinct filesystem entries at %s", path)
		}
		result.Files[path] = file
	}
	for location, data := range normalized.Locks {
		result.Locks[location] = normalizeGoldenPathFields(string(normalizeGoldenJSONTimestamps(data)))
	}
	for _, request := range normalized.HTTPRequests {
		text, encoded := goldenBytes(request.Body)
		result.HTTPRequests = append(result.HTTPRequests, goldenHTTPRequest{
			Method: request.Method, Path: request.Path, Host: request.Host,
			Header: request.Header, Text: text, Base64: encoded,
		})
	}
	for index := range result.SpawnedCommands {
		result.SpawnedCommands[index].Cwd = normalizeGoldenPathFields(result.SpawnedCommands[index].Cwd)
		for argument := range result.SpawnedCommands[index].Args {
			result.SpawnedCommands[index].Args[argument] = normalizeGoldenPathFields(result.SpawnedCommands[index].Args[argument])
		}
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}

func goldenBytes(data []byte) (*string, string) {
	if len(data) == 0 {
		return nil, ""
	}
	if utf8.Valid(data) {
		text := string(data)
		return &text, ""
	}
	return nil, base64.StdEncoding.EncodeToString(data)
}

func normalizeGoldenJSONTimestamps(data []byte) []byte {
	return rfc3339Field.ReplaceAll(data, []byte(`${1}<timestamp>${2}`))
}

func isTimestampedJSONPath(path string) bool {
	return strings.HasSuffix(path, "skills-lock.json") || strings.HasSuffix(path, ".skill-lock.json") || strings.HasSuffix(path, "trust.json")
}

func goldenLinkTarget(observation Observation, path, target string) string {
	kind := "relative:"
	resolved := target
	if filepath.IsAbs(target) {
		kind = "absolute:"
	} else {
		link := filepath.Join(observation.Paths.Root, filepath.FromSlash(path))
		resolved = filepath.Join(filepath.Dir(link), target)
	}
	resolved = filepath.ToSlash(filepath.Clean(resolved))
	for _, sandboxPath := range []struct {
		raw, resolved, token string
	}{
		{observation.Paths.Project, observation.ResolvedPaths.Project, "<project>"},
		{observation.Paths.Home, observation.ResolvedPaths.Home, "<home>"},
		{observation.Paths.Temp, observation.ResolvedPaths.Temp, "<tmp>"},
		{observation.Paths.Root, observation.ResolvedPaths.Root, "<sandbox>"},
	} {
		for _, candidate := range []string{sandboxPath.raw, sandboxPath.resolved} {
			candidate = filepath.ToSlash(candidate)
			if candidate != "" {
				resolved = strings.ReplaceAll(resolved, candidate, sandboxPath.token)
			}
		}
	}
	return kind + normalizeGoldenPathFields(resolved)
}

func normalizeGoldenPathFields(value string) string {
	for _, token := range []string{"<sandbox>", "<project>", "<home>", "<tmp>"} {
		value = strings.ReplaceAll(value, token+`\`, token+"/")
	}
	value = goldenTemporaryName.ReplaceAllString(value, `$1-<temporary>`)
	return goldenTransactionPath.ReplaceAllString(value, `${1}<transaction>`)
}

func normalizeGoldenPresentation(value string, normalizeLayout bool) string {
	value = normalizeGoldenPathFields(value)
	if !normalizeLayout {
		return value
	}
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(presentationSpacing.ReplaceAllString(line, " "), " \t")
	}
	return strings.Join(lines, "\n")
}

func scenarioUsesLayoutNormalization(scenario goldenScenario) bool {
	for _, normalization := range scenario.Normalization {
		if normalization == "terminal-layout:repeated-horizontal-spacing" {
			return true
		}
	}
	return false
}

func assertJSONScenarioObservation(t *testing.T, input goldenInput, observation Observation) {
	t.Helper()
	if !goldenInputUsesJSON(input) {
		return
	}
	if ansiEscape.MatchString(observation.Stdout) || ansiEscape.MatchString(observation.Stderr) {
		t.Fatal("JSON scenario emitted ANSI presentation escapes")
	}
	decoder := json.NewDecoder(strings.NewReader(observation.Stdout))
	var document any
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("JSON scenario stdout is not one document: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("JSON scenario stdout has trailing content: %v", err)
	}
}

func goldenInputUsesJSON(input goldenInput) bool {
	for _, argument := range input.Args {
		if argument == "--json" {
			return true
		}
	}
	return false
}

func assertNoNodeOrNPM(t *testing.T, observation Observation) {
	t.Helper()
	for _, command := range observation.SpawnedCommands {
		switch strings.ToLower(strings.TrimSuffix(command.Name, ".exe")) {
		case "node", "npm", "npx":
			t.Fatalf("native golden scenario executed forbidden runtime %s: %#v", command.Name, command.Args)
		}
	}
}

func firstGoldenDifference(expected, actual []byte) string {
	expectedLines := strings.Split(string(expected), "\n")
	actualLines := strings.Split(string(actual), "\n")
	limit := len(expectedLines)
	if len(actualLines) < limit {
		limit = len(actualLines)
	}
	for index := 0; index < limit; index++ {
		if expectedLines[index] != actualLines[index] {
			return fmt.Sprintf("line %d\nwant: %s\n got: %s", index+1, expectedLines[index], actualLines[index])
		}
	}
	return fmt.Sprintf("different lengths: want %d bytes, got %d bytes", len(expected), len(actual))
}
