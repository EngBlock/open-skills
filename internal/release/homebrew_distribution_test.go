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
		"tags:\n      - 'v*'",
		"permissions:\n  contents: read\n  id-token: write\n  attestations: write",
		`go run ./internal/release/cmd/native-preview`,
		`--version "${GITHUB_REF_NAME#v}"`,
		"--homebrew-formula homebrew/open-skills.rb",
		"name: Verify checked package metadata",
		"if: ${{ !contains(github.ref_name, '-') }}",
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
		"name: Approve production native release",
		"needs: [build, homebrew, scoop]",
		"environment: native-production",
		"needs: [build, homebrew, scoop, production-approval]",
		`needs['production-approval'].result == 'success' || needs['production-approval'].result == 'skipped'`,
		`scripts/verify-native-release-tag.sh`,
		`gh release create "${GITHUB_REF_NAME}"`,
		"native-dist/*",
		`release_args+=(--prerelease)`,
		"--verify-tag",
		`--signer-workflow "${workflow}"`,
	} {
		if !strings.Contains(workflow, text) {
			t.Errorf("native preview workflow does not contain %q", text)
		}
	}
	if strings.Contains(workflow, "--skip-linux-smoke") {
		t.Fatal("canonical release workflow skips the required Linux smoke test")
	}
	if strings.Contains(workflow, `--target "${GITHUB_SHA}"`) {
		t.Fatal("release creation must not retarget the existing verified annotated tag")
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
	approvalIndex := strings.Index(workflow, "  production-approval:")
	releaseIndex := strings.Index(workflow, "  release:")
	if homebrewIndex < 0 || approvalIndex < 0 || releaseIndex < 0 || homebrewIndex >= approvalIndex || approvalIndex >= releaseIndex {
		t.Fatal("Homebrew verification and human production approval must precede release publication")
	}
}

func TestReleaseTagVerifierAcceptsCanonicalVersionTags(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	verifier := readRepositoryFile(t, root, "scripts", "verify-native-release-tag.sh")
	if !strings.Contains(verifier, `^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`) {
		t.Fatal("release tag verifier does not require a canonical version tag")
	}
	if strings.Contains(verifier, `^v0\.2\.`) {
		t.Fatal("release tag verifier is tied to one release line")
	}
	for _, text := range []string{"git merge-base --is-ancestor origin/main", "scripts/verify-release-rulesets.sh"} {
		if !strings.Contains(verifier, text) {
			t.Errorf("release tag verifier does not contain %q", text)
		}
	}
}

func TestReleaseRulesetsRequireImmutableAdministratorCreatedTags(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	script := readRepositoryFile(t, root, "scripts", "verify-release-rulesets.sh")
	for _, text := range []string{
		`pattern='refs/tags/v*'`,
		`index("update")`,
		`index("deletion")`,
		`.conditions.ref_name.exclude`,
		`length == 0`,
		`index("creation")`,
		`.actor_type == "RepositoryRole"`,
		`.actor_id == 5`,
	} {
		if !strings.Contains(script, text) {
			t.Errorf("release ruleset verifier does not contain %q", text)
		}
	}
}

func TestReleaseScriptBuildsCommitsSignsAndAtomicallyPushesCandidate(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	script := readRepositoryFile(t, root, "scripts", "release.sh")
	for _, text := range []string{
		`: "${TAG:?TAG must be set`,
		`release/v${version}`,
		`scripts/verify-release-rulesets.sh`,
		`export GOTOOLCHAIN="${required_go}"`,
		`go run ./internal/release/cmd/native-preview`,
		`go vet ./...`,
		`go test ./... -count=1`,
		`git commit -m "Prepare ${tag} native release"`,
		`git tag -s "${tag}"`,
		`git push --atomic origin`,
	} {
		if !strings.Contains(script, text) {
			t.Errorf("release script does not contain %q", text)
		}
	}
}

func TestProductionCutoverChecklistCoversEveryApprovalGate(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	checklist := readRepositoryFile(t, root, "docs", "native-production-gate.md")
	for _, text := range []string{
		"Reviewed compatibility corpus",
		"Security and state safety",
		"macOS ARM64 maintainer validation",
		"Linux x86-64 built-binary smoke",
		"Experimental artifacts",
		"Signed canonical artifacts",
		"Homebrew availability",
		"Scoop availability",
		"Migration guidance",
		"native-production",
		"signed `v0.2.0` tag",
	} {
		if !strings.Contains(checklist, text) {
			t.Errorf("production cutover checklist does not contain %q", text)
		}
	}
	migration := readRepositoryFile(t, root, "docs", "native-migration.md")
	if !strings.Contains(migration, "## D13: canonical native release supply chain") {
		t.Error("migration guidance does not document D13")
	}
}

func TestProductionRecoveryWorkflowReusesOnlyTheVerifiedTaggedBundle(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	workflow := readRepositoryFile(t, root, ".github", "workflows", "native-release-recovery.yml")
	for _, text := range []string{
		"workflow_dispatch:",
		"source-run-id:",
		"release-tag:",
		`if [[ "${RELEASE_TAG}" != "v0.2.0" ]]`,
		`if [[ "${SOURCE_RUN_ID}" != "30039890752" ]]`,
		`"$(jq -r .path <<<"${run_json}")" != ".github/workflows/native-preview.yml"`,
		`run-id: ${{ inputs.source-run-id }}`,
		`name: native-preview-${{ inputs.release-tag }}`,
		"go run ./internal/release/cmd/verify-native-release",
		"cosign verify-blob",
		"gh attestation verify",
		`OPEN_SKILLS_HOMEBREW_ARTIFACT="$artifact" scripts/homebrew-smoke.sh homebrew/open-skills.rb`,
		"name: Checkout recovery tooling",
		`$artifact = "native-dist/open-skills_$($env:RELEASE_TAG.Substring(1))_windows_amd64.zip"`,
		"scripts/scoop-smoke.ps1 scoop/open-skills.json $artifact scoop-core",
		"environment: native-production",
		`gh release create "${RELEASE_TAG}"`,
		"--verify-tag",
	} {
		if !strings.Contains(workflow, text) {
			t.Errorf("native production recovery workflow does not contain %q", text)
		}
	}
	homebrewStart := strings.Index(workflow, "  homebrew:")
	scoopStart := strings.Index(workflow, "  scoop:")
	if homebrewStart < 0 || scoopStart <= homebrewStart {
		t.Fatal("recovery workflow does not contain ordered Homebrew and Scoop jobs")
	}
	homebrew := workflow[homebrewStart:scoopStart]
	checkoutIndex := strings.Index(homebrew, "name: Checkout immutable production tag")
	setupGoIndex := strings.Index(homebrew, "name: Setup Go")
	downloadIndex := strings.Index(homebrew, "name: Download the source run's signed bundle")
	if checkoutIndex < 0 || setupGoIndex <= checkoutIndex || downloadIndex <= setupGoIndex {
		t.Fatal("recovery Homebrew job must set up Go between checkout and artifact download")
	}
	for _, forbidden := range []string{"cosign sign-blob", "attest-build-provenance", "--prerelease"} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("native production recovery workflow unexpectedly contains %q", forbidden)
		}
	}
}

func TestOfflineCIPrefetchesGoModulesBeforeDisablingNetwork(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	workflow := readRepositoryFile(t, root, ".github", "workflows", "ci.yml")
	for _, text := range []string{
		`GOMODCACHE="$MOD_CACHE" go mod download`,
		`export GOMODCACHE="$2"`,
		`export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`,
	} {
		if !strings.Contains(workflow, text) {
			t.Errorf("CI offline test setup does not contain %q", text)
		}
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

func TestReadRepositoryFileNormalizesLineEndings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fixture.txt")
	if err := os.WriteFile(path, []byte("first\r\nsecond\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if contents := readRepositoryFile(t, root, "fixture.txt"); contents != "first\nsecond\n" {
		t.Fatalf("normalized contents = %q", contents)
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
	return strings.ReplaceAll(string(contents), "\r\n", "\n")
}
