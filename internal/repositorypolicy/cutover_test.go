package repositorypolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestActiveRepositoryIsGoOnly(t *testing.T) {
	root := repositoryRoot(t)
	tracked := trackedExistingFiles(t, root)
	forbiddenExtensions := map[string]bool{
		".ts": true, ".tsx": true, ".mts": true, ".cts": true,
		".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	}
	for _, path := range tracked {
		if forbiddenExtensions[strings.ToLower(filepath.Ext(path))] {
			t.Errorf("retired JavaScript/TypeScript file remains active: %s", path)
		}
	}

	for _, path := range []string{
		"src", "tests", "bin", ".husky", ".oxfmtrc.json", "build.config.mjs",
		"package.json", "package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "pnpm-workspace.yaml",
		"yarn.lock", "bun.lock", "bun.lockb", "deno.json", "deno.jsonc", "tsconfig.json", "jsconfig.json",
		"ThirdPartyNoticeText.txt", ".github/workflows/agents.yml", ".github/workflows/publish.yml",
		".github/RELEASE_TEMPLATE.md",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err == nil {
			t.Errorf("retired active-project path remains: %s", path)
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect %s: %v", path, err)
		}
	}

	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.y*ml"))
	if err != nil {
		t.Fatal(err)
	}
	retiredRuntime := regexp.MustCompile(`(?i)\b(?:node(?:\.js)?|npm|npx|pnpm)\b`)
	for _, workflow := range workflows {
		data, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatal(err)
		}
		if retiredRuntime.Match(data) {
			t.Errorf("active workflow references the retired Node toolchain: %s", filepath.Base(workflow))
		}
	}
}

func TestHistoricalCompatibilityArtifactsRemainIdentityLinked(t *testing.T) {
	root := repositoryRoot(t)
	required := []string{
		"compatibility/npm-0.1.2/oracle.json",
		"compatibility/npm-0.1.2/runtime-dependencies.json",
		"internal/compatibility/testdata/golden/v1/manifest.json",
		"internal/compatibility/testdata/golden/v1/README.md",
	}
	for _, path := range required {
		command := exec.Command("git", "ls-files", "--error-unmatch", "--", path)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("historical artifact is not tracked: %s: %v: %s", path, err, output)
		}
	}

	oracle, err := os.ReadFile(filepath.Join(root, "compatibility", "npm-0.1.2", "oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(oracle)
	if got := hex.EncodeToString(digest[:]); got != "cab36aa81db45f57c2405cd8940546b3610cd7e46c9e8460d55345d0c0ffbe9a" {
		t.Fatalf("reviewed oracle manifest digest = %s", got)
	}
	runtimeManifest, err := os.ReadFile(filepath.Join(root, "compatibility", "npm-0.1.2", "runtime-dependencies.json"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeDigest := sha256.Sum256(runtimeManifest)
	if got := hex.EncodeToString(runtimeDigest[:]); got != "3d48521993dae8341d89a11311d79afc1106176b03f53694fdfbc6771cfc99aa" {
		t.Fatalf("reviewed runtime manifest digest = %s", got)
	}
	var manifest struct {
		Baseline             string `json:"baseline"`
		OracleManifestSHA256 string `json:"oracleManifestSha256"`
		Scenarios            []struct {
			ID string `json:"id"`
		} `json:"scenarios"`
	}
	manifestPath := filepath.Join(root, "internal", "compatibility", "testdata", "golden", "v1", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Baseline != "@engblock/open-skills@0.1.2" {
		t.Fatalf("reviewed corpus baseline = %q", manifest.Baseline)
	}
	if manifest.OracleManifestSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("reviewed corpus oracle digest = %q; want %x", manifest.OracleManifestSHA256, digest)
	}
	if len(manifest.Scenarios) == 0 {
		t.Fatal("reviewed corpus has no scenarios")
	}
	outcomes, err := filepath.Glob(filepath.Join(root, "internal", "compatibility", "testdata", "golden", "v1", "outcomes", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != len(manifest.Scenarios) {
		t.Fatalf("reviewed outcomes = %d; scenarios = %d", len(outcomes), len(manifest.Scenarios))
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	command := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("resolve repository root with system Git: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func trackedExistingFiles(t *testing.T, root string) []string {
	t.Helper()
	command := exec.Command("git", "ls-files", "-z")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list tracked files with system Git: %v", err)
	}
	paths := []string{}
	for _, encoded := range bytes.Split(output, []byte{0}) {
		if len(encoded) == 0 {
			continue
		}
		path := string(encoded)
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err == nil {
			paths = append(paths, path)
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect tracked path %s: %v", path, err)
		}
	}
	sort.Strings(paths)
	return paths
}
