package release

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckedScoopManifestIsCanonical(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	manifest := readRepositoryFile(t, root, "bucket", "open-skills.json")
	var values struct {
		Version      string `json:"version"`
		Architecture struct {
			AMD64 struct {
				Hash string `json:"hash"`
			} `json:"64bit"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal([]byte(manifest), &values); err != nil {
		t.Fatal(err)
	}
	filename := "open-skills_" + values.Version + "_windows_amd64.zip"
	generated, err := ScoopManifest(values.Version, strings.NewReader(values.Architecture.AMD64.Hash+"  "+filename+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest != generated {
		t.Fatal("checked Scoop manifest differs from the canonical generated manifest")
	}
}

func TestScoopDocumentationLabelsWindowsX8664Experimental(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	readme := readRepositoryFile(t, root, "README.md")
	for _, text := range []string{
		"Scoop (experimental Windows x86-64 native)",
		"scoop bucket add open-skills https://github.com/EngBlock/open-skills",
		"scoop install open-skills/open-skills",
		"scoop update open-skills",
		"Windows x86-64 support remains experimental",
		"does not require Node.js or npm",
	} {
		if !strings.Contains(readme, text) {
			t.Errorf("README does not contain %q", text)
		}
	}

	development := readRepositoryFile(t, root, "docs", "native-development.md")
	for _, text := range []string{
		"## Scoop native releases",
		"Windows x86-64 target remains experimental",
		"--scoop-manifest bucket/open-skills.json",
		"scripts/scoop-smoke.ps1",
	} {
		if !strings.Contains(development, text) {
			t.Errorf("native development documentation does not contain %q", text)
		}
	}
}

func TestScoopSmokeValidatesManifestInstallCommandsAndUpgradeMetadata(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	scriptPath := filepath.Join(root, "scripts", "scoop-smoke.ps1")
	script := readRepositoryFile(t, scriptPath)
	for _, text := range []string{
		"validator.exe",
		"schema.json",
		"checkver.ps1",
		"-ForceUpdate",
		"releases.json",
		"v0.2.0-preview.2",
		"$olderVersion = '0.1.9'",
		"$isProduction",
		"$currentPrerelease = 'false'",
		"Set-StrictMode -Off",
		"New-Item (Join-Path $env:SCOOP 'shims') -ItemType Directory -Force",
		"apps/scoop/current",
		"-ItemType Junction",
		"scoop.ps1",
		"install",
		"--version",
		"--help",
		"open-skills.exe",
	} {
		if !strings.Contains(script, text) {
			t.Errorf("Scoop smoke script does not contain %q", text)
		}
	}
}

func TestNativePreviewWorkflowGatesPublicationOnScoopSmoke(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	workflow := readRepositoryFile(t, root, ".github", "workflows", "native-preview.yml")
	for _, text := range []string{
		"--scoop-manifest scoop/open-skills.json",
		"cmp bucket/open-skills.json scoop/open-skills.json",
		"scoop/open-skills.json",
		"name: Smoke-test experimental Windows Scoop package",
		"runs-on: windows-2025",
		"repository: ScoopInstaller/Scoop",
		"ref: b588a06e41d920d2123ec70aee682bae14935939",
		"path: scoop-core",
		`$artifact = "native-dist/open-skills_$($env:GITHUB_REF_NAME.Substring(1))_windows_amd64.zip"`,
		"scripts/scoop-smoke.ps1 scoop/open-skills.json $artifact scoop-core",
		"needs: [build, homebrew, scoop]",
	} {
		if !strings.Contains(workflow, text) {
			t.Errorf("native preview workflow does not contain %q", text)
		}
	}

	scoopIndex := strings.Index(workflow, "  scoop:")
	releaseIndex := strings.Index(workflow, "  release:")
	if scoopIndex < 0 || releaseIndex < 0 || scoopIndex >= releaseIndex {
		t.Fatal("Scoop verification must precede release publication")
	}
}
