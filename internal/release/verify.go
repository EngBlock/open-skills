package release

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	checksumsFilename  = "checksums.txt"
	provenanceFilename = "provenance.sigstore.json"
)

type VerifyOptions struct {
	Output string
	Tag    string
}

func expectedArtifacts(version string) []Artifact {
	artifacts := make([]Artifact, 0, len(previewTargets))
	for _, selected := range previewTargets {
		artifacts = append(artifacts, Artifact{
			Filename: fmt.Sprintf("open-skills_%s_%s_%s.%s", version, selected.goos, selected.goarch, selected.archiveType),
			GOOS:     selected.goos,
			GOARCH:   selected.goarch,
			Support:  selected.support,
		})
	}
	return artifacts
}

func WriteChecksums(output string, artifacts []Artifact) error {
	var content strings.Builder
	for _, artifact := range artifacts {
		digest, err := fileSHA256(filepath.Join(output, artifact.Filename))
		if err != nil {
			return fmt.Errorf("checksum %s: %w", artifact.Filename, err)
		}
		fmt.Fprintf(&content, "%s  %s\n", digest, artifact.Filename)
	}
	if err := os.WriteFile(filepath.Join(output, checksumsFilename), []byte(content.String()), 0o644); err != nil {
		return fmt.Errorf("write checksums: %w", err)
	}
	return nil
}

func VerifyReleaseBundle(options VerifyOptions) error {
	if options.Output == "" {
		return fmt.Errorf("release artifact directory is required")
	}
	version, ok := strings.CutPrefix(options.Tag, "v")
	if !ok || validateVersion(version) != nil {
		return fmt.Errorf("release tag %q must name a canonical native release version", options.Tag)
	}

	expected := expectedArtifacts(version)
	allowedFiles := map[string]struct{}{
		checksumsFilename:                    {},
		checksumsFilename + ".sigstore.json": {},
		provenanceFilename:                   {},
	}
	for _, artifact := range expected {
		allowedFiles[artifact.Filename] = struct{}{}
		allowedFiles[artifact.Filename+".sigstore.json"] = struct{}{}
	}
	entries, err := os.ReadDir(options.Output)
	if err != nil {
		return fmt.Errorf("read release artifact directory: %w", err)
	}
	for _, entry := range entries {
		if _, ok := allowedFiles[entry.Name()]; !ok {
			if isReleaseArchive(entry.Name()) {
				return fmt.Errorf("artifact %q does not correspond to release tag %s", entry.Name(), options.Tag)
			}
			return fmt.Errorf("unexpected release asset %q", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect release asset %q: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release asset %q is not a regular file", entry.Name())
		}
	}

	for _, artifact := range expected {
		archivePath := filepath.Join(options.Output, artifact.Filename)
		if _, err := os.Stat(archivePath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("missing release target %s/%s: %s", artifact.GOOS, artifact.GOARCH, artifact.Filename)
			}
			return fmt.Errorf("inspect release target %s: %w", artifact.Filename, err)
		}
		canonical := "open-skills"
		if artifact.GOOS == "windows" {
			canonical = "open-skills.exe"
		}
		if err := verifyArchiveExecutable(archivePath, canonical); err != nil {
			return fmt.Errorf("verify %s: %w", artifact.Filename, err)
		}
	}
	if err := verifyChecksums(options.Output, expected); err != nil {
		return err
	}
	for _, artifact := range expected {
		if err := requireNonempty(filepath.Join(options.Output, artifact.Filename+".sigstore.json")); err != nil {
			return fmt.Errorf("missing keyless signature for %s: %w", artifact.Filename, err)
		}
	}
	if err := requireNonempty(filepath.Join(options.Output, checksumsFilename+".sigstore.json")); err != nil {
		return fmt.Errorf("missing keyless signature for %s: %w", checksumsFilename, err)
	}
	if err := requireNonempty(filepath.Join(options.Output, provenanceFilename)); err != nil {
		return fmt.Errorf("missing build provenance: %w", err)
	}
	return nil
}

func verifyChecksums(output string, expected []Artifact) error {
	file, err := os.Open(filepath.Join(output, checksumsFilename))
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}
	defer file.Close()

	checksums := make(map[string]string, len(expected))
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			return fmt.Errorf("invalid checksum entry %q", scanner.Text())
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return fmt.Errorf("invalid checksum for %q", fields[1])
		}
		if filepath.Base(fields[1]) != fields[1] {
			return fmt.Errorf("checksum target %q is not a release filename", fields[1])
		}
		if _, duplicate := checksums[fields[1]]; duplicate {
			return fmt.Errorf("duplicate checksum target %q", fields[1])
		}
		checksums[fields[1]] = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}
	if len(checksums) != len(expected) {
		return fmt.Errorf("checksums cover %d archives; want %d", len(checksums), len(expected))
	}
	for _, artifact := range expected {
		want, ok := checksums[artifact.Filename]
		if !ok {
			return fmt.Errorf("checksums do not cover release target %s", artifact.Filename)
		}
		got, err := fileSHA256(filepath.Join(output, artifact.Filename))
		if err != nil {
			return fmt.Errorf("checksum %s: %w", artifact.Filename, err)
		}
		if got != want {
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", artifact.Filename, got, want)
		}
	}
	return nil
}

func verifyArchiveExecutable(path string, canonical string) error {
	if strings.HasSuffix(path, ".zip") {
		reader, err := zip.OpenReader(path)
		if err != nil {
			return fmt.Errorf("open zip archive: %w", err)
		}
		defer reader.Close()
		if len(reader.File) != 1 || reader.File[0].Name != canonical || !reader.File[0].Mode().IsRegular() || reader.File[0].UncompressedSize64 == 0 {
			return fmt.Errorf("archive must contain exactly one non-empty canonical executable named %s", canonical)
		}
		payload, err := reader.File[0].Open()
		if err != nil {
			return fmt.Errorf("open canonical executable: %w", err)
		}
		_, readErr := io.Copy(io.Discard, payload)
		closeErr := payload.Close()
		if readErr != nil {
			return fmt.Errorf("read canonical executable: %w", readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close canonical executable: %w", closeErr)
		}
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open tar archive: %w", err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	header, err := reader.Next()
	if err != nil {
		return fmt.Errorf("read tar archive: %w", err)
	}
	if header.Name != canonical || header.Typeflag != tar.TypeReg || header.Size == 0 {
		return fmt.Errorf("archive must contain exactly one non-empty canonical executable named %s", canonical)
	}
	if header.Mode&0o111 == 0 {
		return fmt.Errorf("canonical executable %s must be executable", canonical)
	}
	if _, err := reader.Next(); err != io.EOF {
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}
		return fmt.Errorf("archive must contain exactly one non-empty canonical executable named %s", canonical)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func requireNonempty(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("%s is empty or not a regular file", filepath.Base(path))
	}
	return nil
}

func isReleaseArchive(name string) bool {
	return strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".zip")
}
