// Package release builds the self-contained archives used for native previews.
package release

import (
	"archive/tar"
	"archive/zip"
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

var previewVersion = regexp.MustCompile(`^0\.2\.0-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*$`)

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
	return fmt.Sprintf(
		"# open-skills v%s native preview\n\n"+
			"This prerelease makes self-contained native archives available for early testing. Important: prerelease availability does not satisfy the production cutover gate; the npm implementation remains the production distribution until that gate is approved.\n\n"+
			"## Target support\n\n"+
			"| Archive suffix | Target | Status |\n"+
			"| --- | --- | --- |\n"+
			"| `darwin_arm64.tar.gz` | macOS ARM64 | Supported |\n"+
			"| `linux_amd64.tar.gz` | Linux x86-64 | Supported |\n"+
			"| `darwin_amd64.tar.gz` | macOS x86-64 | Experimental |\n"+
			"| `linux_arm64.tar.gz` | Linux ARM64 | Experimental |\n"+
			"| `windows_amd64.zip` | Windows x86-64 | Experimental |\n\n"+
			"Supported targets are maintainer-supported preview platforms. Experimental targets are compile-checked but are not yet represented as fully tested.\n\n"+
			"Each archive contains only `open-skills` (`open-skills.exe` on Windows). The only runtime dependency is system Git; Node.js and npm are not required.\n\n"+
			"## Verification\n\n"+
			"`checksums.txt` covers every release archive. Each archive and the checksum file has an adjacent keyless signature bundle named `<file>.sigstore.json`; `provenance.sigstore.json` covers every archive.\n\n"+
			"After downloading the archive and verification files, verify the checksum, keyless signature, repository, release tag, and producing workflow identity:\n\n"+
			"```sh\n"+
			"sha256sum --check checksums.txt\n"+
			"cosign verify-blob --bundle <archive>.sigstore.json --certificate-identity 'https://github.com/EngBlock/open-skills/.github/workflows/native-preview.yml@refs/tags/v%s' --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' <archive>\n"+
			"gh attestation verify <archive> --bundle provenance.sigstore.json --repo EngBlock/open-skills --signer-workflow EngBlock/open-skills/.github/workflows/native-preview.yml --cert-identity 'https://github.com/EngBlock/open-skills/.github/workflows/native-preview.yml@refs/tags/v%s'\n"+
			"```\n",
		version,
		version,
		version,
	), nil
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
	if !previewVersion.MatchString(version) {
		return fmt.Errorf("native preview version %q must be a 0.2.0 prerelease such as 0.2.0-preview.1", version)
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
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", "-s -w -X github.com/EngBlock/open-skills/internal/application.Version="+options.Version, "-o", executablePath, "./cmd/open-skills")
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
