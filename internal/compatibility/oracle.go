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
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxOracleCompressedSize = 8 << 20
	maxOracleEntrySize      = 16 << 20
	maxOracleFileCount      = 10_000
	maxOracleUnpackedSize   = 128 << 20
)

type OracleOptions struct {
	ManifestPath          string
	RuntimeManifestPath   string
	CachedTarball         string
	CachedRuntimeTarballs map[string]string
	Destination           string
	NodeExecutable        string
	HTTPClient            *http.Client
}

type artifactManifest struct {
	URL          string `json:"url"`
	Integrity    string `json:"integrity"`
	SHA1         string `json:"sha1"`
	SHA256       string `json:"sha256"`
	SHA512       string `json:"sha512"`
	Size         int64  `json:"size"`
	FileCount    int    `json:"fileCount"`
	UnpackedSize int64  `json:"unpackedSize"`
}

type oracleManifest struct {
	SchemaVersion int `json:"schemaVersion"`
	Package       struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"package"`
	Artifact artifactManifest `json:"artifact"`
}

type runtimeManifest struct {
	SchemaVersion int `json:"schemaVersion"`
	Dependencies  []struct {
		Name     string           `json:"name"`
		Version  string           `json:"version"`
		Artifact artifactManifest `json:"artifact"`
	} `json:"dependencies"`
}

// PrepareNPMOracle materializes only the exact CLI and runtime dependency
// artifacts described by checked-in manifests. It never consults a dist-tag or
// invokes npm/npx.
func PrepareNPMOracle(ctx context.Context, options OracleOptions) (Target, error) {
	if options.ManifestPath == "" || options.Destination == "" || options.NodeExecutable == "" {
		return Target{}, errors.New("manifest path, destination, and explicit Node executable are required")
	}
	manifestData, err := os.ReadFile(options.ManifestPath)
	if err != nil {
		return Target{}, err
	}
	var manifest oracleManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return Target{}, fmt.Errorf("decode oracle manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || manifest.Package.Name != "@engblock/open-skills" || manifest.Package.Version != "0.1.2" {
		return Target{}, fmt.Errorf("unsupported oracle identity %s@%s (schema %d)", manifest.Package.Name, manifest.Package.Version, manifest.SchemaVersion)
	}
	if err := validateArtifact(manifest.Artifact, "open-skills-0.1.2.tgz"); err != nil {
		return Target{}, err
	}

	runtimePath := options.RuntimeManifestPath
	if runtimePath == "" {
		runtimePath = filepath.Join(filepath.Dir(options.ManifestPath), "runtime-dependencies.json")
	}
	runtimeData, err := os.ReadFile(runtimePath)
	if err != nil {
		return Target{}, fmt.Errorf("read oracle runtime manifest: %w", err)
	}
	var runtime runtimeManifest
	if err := json.Unmarshal(runtimeData, &runtime); err != nil {
		return Target{}, fmt.Errorf("decode oracle runtime manifest: %w", err)
	}
	if runtime.SchemaVersion != 1 || len(runtime.Dependencies) != 1 || runtime.Dependencies[0].Name != "yaml" || runtime.Dependencies[0].Version != "2.9.0" {
		return Target{}, errors.New("oracle runtime manifest must pin exactly yaml@2.9.0")
	}
	dependency := runtime.Dependencies[0]
	if err := validateArtifact(dependency.Artifact, "yaml-2.9.0.tgz"); err != nil {
		return Target{}, fmt.Errorf("yaml runtime artifact: %w", err)
	}

	if err := os.Mkdir(options.Destination, 0o755); err != nil {
		return Target{}, fmt.Errorf("create oracle destination (must not already exist): %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(options.Destination)
		}
	}()

	artifact, err := loadArtifact(ctx, manifest.Artifact, options.CachedTarball, options.HTTPClient)
	if err != nil {
		return Target{}, err
	}
	if err := extractPackageTarball(ctx, artifact, options.Destination, manifest.Artifact, false); err != nil {
		return Target{}, err
	}

	cachedDependency := ""
	if options.CachedRuntimeTarballs != nil {
		cachedDependency = options.CachedRuntimeTarballs[dependency.Name]
	}
	dependencyArtifact, err := loadArtifact(ctx, dependency.Artifact, cachedDependency, options.HTTPClient)
	if err != nil {
		return Target{}, fmt.Errorf("prepare yaml runtime: %w", err)
	}
	dependencyDestination := filepath.Join(options.Destination, "package", "node_modules", dependency.Name)
	if err := os.MkdirAll(dependencyDestination, 0o755); err != nil {
		return Target{}, err
	}
	if err := extractPackageTarball(ctx, dependencyArtifact, dependencyDestination, dependency.Artifact, true); err != nil {
		return Target{}, fmt.Errorf("extract yaml runtime: %w", err)
	}

	entrypoint := filepath.Join(options.Destination, "package", "bin", "cli.mjs")
	info, err := os.Stat(entrypoint)
	if err != nil || !info.Mode().IsRegular() {
		return Target{}, errors.New("oracle entrypoint package/bin/cli.mjs is missing")
	}
	complete = true
	return Target{Name: "npm-0.1.2", Command: options.NodeExecutable, Args: []string{entrypoint}}, nil
}

func validateArtifact(artifact artifactManifest, filename string) error {
	if artifact.Size <= 0 || artifact.FileCount <= 0 || artifact.UnpackedSize <= 0 || artifact.URL == "" {
		return errors.New("oracle manifest is missing pinned artifact metadata")
	}
	if artifact.Size > maxOracleCompressedSize || artifact.FileCount > maxOracleFileCount || artifact.UnpackedSize > maxOracleUnpackedSize {
		return errors.New("oracle manifest exceeds fixed artifact safety limits")
	}
	artifactURL, err := url.Parse(artifact.URL)
	if err != nil || path.Base(artifactURL.Path) != filename {
		return fmt.Errorf("oracle artifact URL is not the pinned %s tarball: %q", filename, artifact.URL)
	}
	return nil
}

func loadArtifact(ctx context.Context, manifest artifactManifest, cached string, client *http.Client) ([]byte, error) {
	var artifact []byte
	var err error
	if cached != "" {
		file, openErr := os.Open(cached)
		if openErr != nil {
			return nil, openErr
		}
		artifact, err = io.ReadAll(io.LimitReader(file, manifest.Size+1))
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	} else {
		if client == nil {
			client = http.DefaultClient
		}
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, manifest.URL, nil)
		if requestErr != nil {
			return nil, requestErr
		}
		response, requestErr := client.Do(request)
		if requestErr != nil {
			return nil, fmt.Errorf("fetch pinned npm artifact: %w", requestErr)
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("fetch pinned npm artifact: %s", response.Status)
		}
		artifact, err = io.ReadAll(io.LimitReader(response.Body, manifest.Size+1))
	}
	if err != nil {
		return nil, err
	}
	if int64(len(artifact)) != manifest.Size {
		return nil, fmt.Errorf("oracle artifact size mismatch: got %d, want %d", len(artifact), manifest.Size)
	}
	if err := verifyOracleDigests(artifact, manifest); err != nil {
		return nil, err
	}
	return artifact, nil
}

func verifyOracleDigests(artifact []byte, manifest artifactManifest) error {
	one, two, five := sha1.Sum(artifact), sha256.Sum256(artifact), sha512.Sum512(artifact)
	checks := []struct{ name, actual, expected string }{
		{"sha1", hex.EncodeToString(one[:]), manifest.SHA1},
		{"sha256", hex.EncodeToString(two[:]), manifest.SHA256},
		{"sha512", hex.EncodeToString(five[:]), manifest.SHA512},
		{"integrity", "sha512-" + base64.StdEncoding.EncodeToString(five[:]), manifest.Integrity},
	}
	for _, check := range checks {
		if check.actual != check.expected {
			return fmt.Errorf("oracle artifact %s mismatch: got %s, want %s", check.name, check.actual, check.expected)
		}
	}
	return nil
}

func extractPackageTarball(ctx context.Context, artifact []byte, destination string, manifest artifactManifest, stripPackage bool) error {
	compressed, err := gzip.NewReader(bytes.NewReader(artifact))
	if err != nil {
		return fmt.Errorf("open oracle gzip: %w", err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	files := 0
	var unpacked int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read oracle tar: %w", err)
		}
		name := strings.ReplaceAll(header.Name, `\`, "/")
		clean := path.Clean(name)
		windowsAbsolute := len(clean) >= 3 && ((clean[0] >= 'a' && clean[0] <= 'z') || (clean[0] >= 'A' && clean[0] <= 'Z')) && clean[1] == ':' && clean[2] == '/'
		traversal := false
		for _, segment := range strings.Split(name, "/") {
			if segment == ".." {
				traversal = true
			}
		}
		if clean == "." || path.IsAbs(clean) || windowsAbsolute || traversal || filepath.IsAbs(header.Name) {
			return fmt.Errorf("unsafe oracle archive path %q", header.Name)
		}
		if stripPackage {
			if clean == "package" {
				continue
			}
			if !strings.HasPrefix(clean, "package/") {
				return fmt.Errorf("unsafe oracle archive path %q", header.Name)
			}
			clean = strings.TrimPrefix(clean, "package/")
		}
		destinationPath := filepath.Join(destination, filepath.FromSlash(clean))
		if !pathWithin(destination, destinationPath) {
			return fmt.Errorf("unsafe oracle archive path %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxOracleEntrySize || unpacked+header.Size > manifest.UnpackedSize || files+1 > manifest.FileCount {
				return fmt.Errorf("oracle archive exceeds recorded file or size limits")
			}
			files++
			unpacked += header.Size
			if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode).Perm())
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, archive, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsafe oracle archive entry %q has type %d", header.Name, header.Typeflag)
		}
	}
	if files != manifest.FileCount || unpacked != manifest.UnpackedSize {
		return fmt.Errorf("oracle archive content mismatch: got %d files/%d bytes, want %d/%d", files, unpacked, manifest.FileCount, manifest.UnpackedSize)
	}
	return nil
}
