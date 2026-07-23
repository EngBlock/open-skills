package release

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestCheckedHomebrewFormulaIsCanonical(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	formula := readRepositoryFile(t, root, "Formula", "open-skills.rb")
	version := requireFormulaValue(t, formula, "version", `[^\"]+`)
	checksum := requireFormulaValue(t, formula, "sha256", `[0-9a-f]{64}`)
	filename := "open-skills_" + version + "_darwin_arm64.tar.gz"
	generated, err := HomebrewFormula(version, strings.NewReader(checksum+"  "+filename+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if formula != generated {
		t.Fatal("checked Homebrew formula differs from the canonical generated formula")
	}
}

func TestHomebrewDocumentationUsesTheRepositoryTapWithoutCurlPipeShell(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	readme := readRepositoryFile(t, root, "README.md")
	for _, command := range []string{
		"brew tap EngBlock/open-skills https://github.com/EngBlock/open-skills",
		"brew install EngBlock/open-skills/open-skills",
		"brew upgrade EngBlock/open-skills/open-skills",
	} {
		if !strings.Contains(readme, command) {
			t.Errorf("README does not contain %q", command)
		}
	}
	if regexp.MustCompile(`(?i)curl[^\n|]*\|[^\n]*(?:sh|bash)`).MatchString(readme) {
		t.Fatal("README recommends curl-pipe-shell installation")
	}

	smokeScript := filepath.Join(root, "scripts", "homebrew-smoke.sh")
	info, err := os.Stat(smokeScript)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		t.Fatal("Homebrew smoke script is not executable")
	}
}

func TestNativePreviewWorkflowGatesPublicationOnHomebrewSmoke(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	workflow := readRepositoryFile(t, root, ".github", "workflows", "native-preview.yml")
	for _, text := range []string{
		"tags:\n      - 'v0.2.0-*'",
		"permissions:\n  contents: read\n  id-token: write\n  attestations: write",
		`go run ./internal/release/cmd/native-preview`,
		`--version "${GITHUB_REF_NAME#v}"`,
		"--homebrew-formula homebrew/open-skills.rb",
		"cmp Formula/open-skills.rb homebrew/open-skills.rb",
		"subject-checksums: native-dist/checksums.txt",
		"cosign sign-blob --yes --bundle",
		"native-dist/provenance.sigstore.json",
		"go run ./internal/release/cmd/verify-native-release",
		"sha256sum --check checksums.txt",
		"cosign verify-blob",
		"gh attestation verify",
		`@refs/tags/${GITHUB_REF_NAME}`,
		"runs-on: macos-15",
		`run: test "$(uname -m)" = arm64`,
		`OPEN_SKILLS_HOMEBREW_ARTIFACT="$artifact" scripts/homebrew-smoke.sh homebrew/open-skills.rb`,
		"needs: [build, homebrew]",
		`scripts/verify-native-release-tag.sh`,
		`gh release create "${GITHUB_REF_NAME}"`,
		"native-dist/*",
		"--prerelease",
		"--verify-tag",
		`--target "${GITHUB_SHA}"`,
		`--signer-workflow "${workflow}"`,
	} {
		if !strings.Contains(workflow, text) {
			t.Errorf("native preview workflow does not contain %q", text)
		}
	}
	if strings.Contains(workflow, "--skip-linux-smoke") {
		t.Fatal("canonical release workflow skips the required Linux smoke test")
	}

	attestationBlock := strings.SplitN(workflow, "gh attestation verify", 2)
	if len(attestationBlock) != 2 {
		t.Fatal("native preview workflow does not verify GitHub attestation")
	}
	attestationFlags := strings.SplitN(attestationBlock[1], "done", 2)[0]
	if strings.Contains(attestationFlags, "--cert-identity") {
		t.Fatal("gh attestation verify combines mutually exclusive signer and certificate identity flags")
	}

	homebrewIndex := strings.Index(workflow, "  homebrew:")
	releaseIndex := strings.Index(workflow, "  release:")
	if homebrewIndex < 0 || releaseIndex < 0 || homebrewIndex >= releaseIndex {
		t.Fatal("Homebrew verification must precede release publication")
	}
}

func TestRepositoryWorkflowActionsArePinnedToCommits(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	entries, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	pinned := regexp.MustCompile(`(?m)^\s*uses:\s*[^@\s]+@[0-9a-f]{40}(?:\s+#.*)?$`)
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".yml") && !strings.HasSuffix(entry.Name(), ".yaml")) {
			continue
		}
		contents := readRepositoryFile(t, root, ".github", "workflows", entry.Name())
		for _, line := range strings.Split(contents, "\n") {
			if strings.Contains(line, "uses:") && !pinned.MatchString(line) {
				t.Errorf("%s contains an action that is not pinned to a commit: %s", entry.Name(), strings.TrimSpace(line))
			}
		}
	}
}

func requireFormulaValue(t *testing.T, formula string, name string, valuePattern string) string {
	t.Helper()
	match := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(name) + ` \"(` + valuePattern + `)\"$`).FindStringSubmatch(formula)
	if len(match) != 2 {
		t.Fatalf("Homebrew formula is missing %s", name)
	}
	return match[1]
}

func readRepositoryFile(t *testing.T, root string, elements ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, elements...)...)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
