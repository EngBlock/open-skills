// Package release builds the self-contained native release archives.
package release

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	Supported    = "supported"
	Experimental = "experimental"
)

type Artifact struct {
	Filename    string
	GOOS        string
	GOARCH      string
	Support     string
	SmokeTested bool
}

type PackageOptions struct {
	Root         string
	Output       string
	Version      string
	RequireSmoke bool
}

type target struct {
	goos        string
	goarch      string
	support     string
	archiveType string
	smoke       bool
}

var nativeReleaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
var sha256Checksum = regexp.MustCompile(`^[0-9a-f]{64}$`)

var previewTargets = []target{
	{goos: "darwin", goarch: "arm64", support: Supported, archiveType: "tar.gz"},
	{goos: "darwin", goarch: "amd64", support: Experimental, archiveType: "tar.gz"},
	{goos: "linux", goarch: "amd64", support: Supported, archiveType: "tar.gz", smoke: true},
	{goos: "linux", goarch: "arm64", support: Experimental, archiveType: "tar.gz"},
	{goos: "windows", goarch: "amd64", support: Experimental, archiveType: "zip"},
}

func PackageAll(ctx context.Context, options PackageOptions) ([]Artifact, error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	if options.RequireSmoke && (runtime.GOOS != "linux" || runtime.GOARCH != "amd64") {
		return nil, fmt.Errorf("Linux x86-64 smoke test requires a linux/amd64 runner, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if err := os.MkdirAll(options.Output, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact directory: %w", err)
	}

	artifacts := make([]Artifact, 0, len(previewTargets))
	for _, selected := range previewTargets {
		artifact, err := packageTarget(ctx, options, selected)
		if err != nil {
			return nil, fmt.Errorf("package %s/%s: %w", selected.goos, selected.goarch, err)
		}
		artifacts = append(artifacts, artifact)
	}
	if err := WriteChecksums(options.Output, artifacts); err != nil {
		return nil, err
	}
	return artifacts, nil
}

func ReleaseNotes(version string) (string, error) {
	if err := validateVersion(version); err != nil {
		return "", err
	}
	title := fmt.Sprintf("# open-skills v%s native preview\n\n", version)
	introduction := "This prerelease makes self-contained native archives available for early testing. Prerelease availability does not replace the supported stable native release or its protected publication gate.\n\n"
	supportStatement := "Supported targets are maintainer-supported preview platforms. Experimental targets are compile-checked but are not yet represented as fully tested.\n\n"
	if !strings.Contains(version, "-") {
		title = fmt.Sprintf("# open-skills v%s native release\n\n", version)
		introduction = "This is the production-ready native release of open-skills. It passed the reviewed compatibility, security, state-safety, platform, and release-supply-chain gate. Migration guidance for npm users is available in `docs/native-migration.md`.\n\n"
		supportStatement = "macOS ARM64 and Linux x86-64 are supported production platforms. Experimental targets are compile-checked but are not represented as fully tested.\n\n"
	}
	return fmt.Sprintf(
		title+
			introduction+
			"## Target support\n\n"+
			"| Archive suffix | Target | Status |\n"+
			"| --- | --- | --- |\n"+
			"| `darwin_arm64.tar.gz` | macOS ARM64 | Supported |\n"+
			"| `linux_amd64.tar.gz` | Linux x86-64 | Supported |\n"+
			"| `darwin_amd64.tar.gz` | macOS x86-64 | Experimental |\n"+
			"| `linux_arm64.tar.gz` | Linux ARM64 | Experimental |\n"+
			"| `windows_amd64.zip` | Windows x86-64 | Experimental |\n\n"+
			supportStatement+
			"Each archive contains only `open-skills` (`open-skills.exe` on Windows). The only runtime dependency is system Git; Node.js and npm are not required.\n\n"+
			"## Verification\n\n"+
			"`checksums.txt` covers every release archive. Each archive and the checksum file has an adjacent keyless signature bundle named `<file>.sigstore.json`; `provenance.sigstore.json` covers every archive.\n\n"+
			"After downloading the archive and verification files, verify the checksum, keyless signature, repository, release tag, and producing workflow identity:\n\n"+
			"```sh\n"+
			"sha256sum --check checksums.txt\n"+
			"cosign verify-blob --bundle <archive>.sigstore.json --certificate-identity 'https://github.com/EngBlock/open-skills/.github/workflows/native-preview.yml@refs/tags/v%s' --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' <archive>\n"+
			"gh attestation verify <archive> --bundle provenance.sigstore.json --repo EngBlock/open-skills --signer-workflow EngBlock/open-skills/.github/workflows/native-preview.yml\n"+
			"```\n",
		version,
	), nil
}

func HomebrewFormula(version string, checksums io.Reader) (string, error) {
	if err := validateVersion(version); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("open-skills_%s_darwin_arm64.tar.gz", version)
	checksum, err := checksumForArtifact(checksums, filename, "Homebrew")
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`class OpenSkills < Formula
  desc "CLI for the open agent skills ecosystem"
  homepage "https://github.com/EngBlock/open-skills"
  url "https://github.com/EngBlock/open-skills/releases/download/v%[1]s/%[2]s"
  version "%[1]s"
  sha256 "%[3]s"
  license "MIT"

  depends_on arch: :arm64
  depends_on :macos

  def install
    bin.install "open-skills"
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/open-skills --version").strip
    assert_match "Usage:", shell_output("#{bin}/open-skills --help")
  end
end
`, version, filename, checksum), nil
}

func ScoopManifest(version string, checksums io.Reader) (string, error) {
	if err := validateVersion(version); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("open-skills_%s_windows_amd64.zip", version)
	checksum, err := checksumForArtifact(checksums, filename, "Scoop")
	if err != nil {
		return "", err
	}
	description := "Experimental Windows x86-64 native preview for the open agent skills ecosystem"
	jsonpath := "$[?(@.prerelease == true && @.draft == false)].tag_name"
	baseVersion, _, prerelease := strings.Cut(version, "-")
	versionPattern := strings.ReplaceAll(baseVersion, ".", `\\.`) + `-[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*`
	if !prerelease {
		description = "Experimental Windows x86-64 native release for the open agent skills ecosystem"
		jsonpath = "$[?(@.prerelease == false && @.draft == false)].tag_name"
		versionPattern = `[0-9]+\\.[0-9]+\\.[0-9]+`
	}

	return fmt.Sprintf(`{
  "version": "%[1]s",
  "description": "%[4]s",
  "homepage": "https://github.com/EngBlock/open-skills",
  "license": "MIT",
  "architecture": {
    "64bit": {
      "url": "https://github.com/EngBlock/open-skills/releases/download/v%[1]s/%[2]s",
      "hash": "%[3]s"
    }
  },
  "bin": "open-skills.exe",
  "checkver": {
    "url": "https://api.github.com/repos/EngBlock/open-skills/releases",
    "jsonpath": "%[5]s",
    "regex": "v(?<version>%[6]s)"
  },
  "autoupdate": {
    "architecture": {
      "64bit": {
        "url": "https://github.com/EngBlock/open-skills/releases/download/v$version/open-skills_$version_windows_amd64.zip",
        "hash": {
          "url": "https://github.com/EngBlock/open-skills/releases/download/v$version/checksums.txt"
        }
      }
    }
  }
}
`, version, filename, checksum, description, jsonpath, versionPattern), nil
}

func checksumForArtifact(checksums io.Reader, filename string, distribution string) (string, error) {
	checksum := ""
	scanner := bufio.NewScanner(checksums)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || fields[1] != filename {
			continue
		}
		if checksum != "" {
			return "", fmt.Errorf("checksums contain duplicate entries for %s", filename)
		}
		if !sha256Checksum.MatchString(fields[0]) {
			return "", fmt.Errorf("checksum for %s is not a lowercase SHA-256 digest", filename)
		}
		checksum = fields[0]
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read release checksums: %w", err)
	}
	if checksum == "" {
		return "", fmt.Errorf("checksums do not cover canonical %s artifact %s", distribution, filename)
	}
	return checksum, nil
}

func validateOptions(options PackageOptions) error {
	if err := validateVersion(options.Version); err != nil {
		return err
	}
	if options.Root == "" {
		return errors.New("repository root is required")
	}
	if options.Output == "" {
		return errors.New("artifact output directory is required")
	}
	if _, err := os.Stat(filepath.Join(options.Root, "go.mod")); err != nil {
		return fmt.Errorf("repository root: %w", err)
	}
	return nil
}

func validateVersion(version string) error {
	if !nativeReleaseVersion.MatchString(version) {
		return fmt.Errorf("native release version %q must be a canonical version such as 0.2.1 or 0.2.1-preview.1", version)
	}
	return nil
}

func packageTarget(ctx context.Context, options PackageOptions, selected target) (Artifact, error) {
	stage, err := os.MkdirTemp("", "open-skills-native-preview-")
	if err != nil {
		return Artifact{}, fmt.Errorf("create staging directory: %w", err)
	}
	defer os.RemoveAll(stage)

	executable := "open-skills"
	if selected.goos == "windows" {
		executable += ".exe"
	}
	executablePath := filepath.Join(stage, executable)
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-trimpath", "-ldflags", "-s -w -X github.com/EngBlock/open-skills/internal/application.Version="+options.Version, "-o", executablePath, "./cmd/open-skills")
	build.Dir = options.Root
	build.Env = buildEnvironment(selected)
	if output, err := build.CombinedOutput(); err != nil {
		return Artifact{}, fmt.Errorf("CGO-disabled Go build: %w: %s", err, strings.TrimSpace(string(output)))
	}

	smokeTested := false
	if selected.smoke && runtime.GOOS == selected.goos && runtime.GOARCH == selected.goarch {
		if err := smokeTest(ctx, executablePath, options.Version); err != nil {
			return Artifact{}, err
		}
		smokeTested = true
	}
	if selected.smoke && options.RequireSmoke && !smokeTested {
		return Artifact{}, errors.New("required Linux x86-64 built-binary smoke test did not execute")
	}

	filename := fmt.Sprintf("open-skills_%s_%s_%s.%s", options.Version, selected.goos, selected.goarch, selected.archiveType)
	archivePath := filepath.Join(options.Output, filename)
	if selected.archiveType == "zip" {
		err = writeZip(archivePath, executablePath, executable)
	} else {
		err = writeTarGzip(archivePath, executablePath, executable)
	}
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{
		Filename:    filename,
		GOOS:        selected.goos,
		GOARCH:      selected.goarch,
		Support:     selected.support,
		SmokeTested: smokeTested,
	}, nil
}

func buildEnvironment(selected target) []string {
	environment := make([]string, 0, len(os.Environ())+3)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if name != "CGO_ENABLED" && name != "GOOS" && name != "GOARCH" {
			environment = append(environment, value)
		}
	}
	return append(environment, "CGO_ENABLED=0", "GOOS="+selected.goos, "GOARCH="+selected.goarch)
}

func smokeTest(ctx context.Context, executablePath string, version string) error {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return errors.New("Linux x86-64 smoke test requires system Git on PATH")
	}
	home, err := os.MkdirTemp("", "open-skills-native-smoke-")
	if err != nil {
		return fmt.Errorf("create smoke-test home: %w", err)
	}
	defer os.RemoveAll(home)

	command := exec.CommandContext(ctx, executablePath, "--version")
	command.Env = []string{"HOME=" + home, "PATH=" + filepath.Dir(gitPath)}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Linux x86-64 built-binary smoke test: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if strings.TrimSpace(string(output)) != version {
		return fmt.Errorf("Linux x86-64 built-binary smoke test printed %q; want %q", strings.TrimSpace(string(output)), version)
	}
	return nil
}

func writeTarGzip(archivePath string, executablePath string, executable string) (resultErr error) {
	archive, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer func() {
		if closeErr := archive.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	compressed := gzip.NewWriter(archive)
	defer func() {
		if closeErr := compressed.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	writer := tar.NewWriter(compressed)
	defer func() {
		if closeErr := writer.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()

	info, err := os.Stat(executablePath)
	if err != nil {
		return fmt.Errorf("inspect executable: %w", err)
	}
	header := &tar.Header{Name: executable, Mode: 0o755, Size: info.Size(), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}
	if err := writer.WriteHeader(header); err != nil {
		return fmt.Errorf("write archive header: %w", err)
	}
	return copyExecutable(writer, executablePath)
}

func writeZip(archivePath string, executablePath string, executable string) (resultErr error) {
	archive, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer func() {
		if closeErr := archive.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	writer := zip.NewWriter(archive)
	defer func() {
		if closeErr := writer.Close(); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()

	header := &zip.FileHeader{Name: executable, Method: zip.Deflate}
	header.SetMode(0o755)
	header.SetModTime(time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC))
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("write archive header: %w", err)
	}
	return copyExecutable(entry, executablePath)
}

func copyExecutable(destination io.Writer, executablePath string) error {
	executable, err := os.Open(executablePath)
	if err != nil {
		return fmt.Errorf("open executable: %w", err)
	}
	defer executable.Close()
	if _, err := io.Copy(destination, executable); err != nil {
		return fmt.Errorf("write executable: %w", err)
	}
	return nil
}
