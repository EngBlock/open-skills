package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPackageAllBuildsTheNativePreviewTargetSet(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	output := t.TempDir()
	result, err := PackageAll(context.Background(), PackageOptions{
		Root:         root,
		Output:       output,
		Version:      "0.2.0-preview.1",
		RequireSmoke: runtime.GOOS == "linux" && runtime.GOARCH == "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []Artifact{
		{Filename: "open-skills_0.2.0-preview.1_darwin_arm64.tar.gz", GOOS: "darwin", GOARCH: "arm64", Support: Supported},
		{Filename: "open-skills_0.2.0-preview.1_darwin_amd64.tar.gz", GOOS: "darwin", GOARCH: "amd64", Support: Experimental},
		{Filename: "open-skills_0.2.0-preview.1_linux_amd64.tar.gz", GOOS: "linux", GOARCH: "amd64", Support: Supported, SmokeTested: runtime.GOOS == "linux" && runtime.GOARCH == "amd64"},
		{Filename: "open-skills_0.2.0-preview.1_linux_arm64.tar.gz", GOOS: "linux", GOARCH: "arm64", Support: Experimental},
		{Filename: "open-skills_0.2.0-preview.1_windows_amd64.zip", GOOS: "windows", GOARCH: "amd64", Support: Experimental},
	}
	if len(result) != len(want) {
		t.Fatalf("PackageAll() returned %d artifacts; want %d", len(result), len(want))
	}
	for index := range want {
		if result[index] != want[index] {
			t.Errorf("artifact %d = %#v; want %#v", index, result[index], want[index])
		}
		path := filepath.Join(output, result[index].Filename)
		entries := archiveEntries(t, path)
		wantEntry := "open-skills"
		if result[index].GOOS == "windows" {
			wantEntry = "open-skills.exe"
		}
		if len(entries) != 1 || entries[0].name != wantEntry || entries[0].size == 0 {
			t.Errorf("%s entries = %#v; want one non-empty %s", result[index].Filename, entries, wantEntry)
		}
	}
	if err := verifyChecksums(output, result); err != nil {
		t.Fatalf("generated checksums do not cover the built archives: %v", err)
	}
}

func TestReleaseNotesDistinguishSupportAndKeepTheCutoverGateClosed(t *testing.T) {
	notes, err := ReleaseNotes("0.2.0-preview.7")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"macOS ARM64 | Supported",
		"Linux x86-64 | Supported",
		"macOS x86-64 | Experimental",
		"Linux ARM64 | Experimental",
		"Windows x86-64 | Experimental",
		"Each archive contains only `open-skills` (`open-skills.exe` on Windows)",
		"The only runtime dependency is system Git",
		"`checksums.txt` covers every release archive",
		"keyless signature bundle",
		"EngBlock/open-skills/.github/workflows/native-preview.yml",
		"prerelease availability does not satisfy the production cutover gate",
	} {
		if !strings.Contains(notes, text) {
			t.Errorf("release notes do not contain %q:\n%s", text, notes)
		}
	}
}

func TestPackageAllRejectsAProductionVersion(t *testing.T) {
	_, err := PackageAll(context.Background(), PackageOptions{
		Root:    filepath.Clean(filepath.Join("..", "..")),
		Output:  t.TempDir(),
		Version: "0.2.0",
	})
	if err == nil || !strings.Contains(err.Error(), "0.2.0 prerelease") {
		t.Fatalf("PackageAll() error = %v; want 0.2.0 prerelease validation", err)
	}
}

func TestVerifyReleaseBundleAcceptsCanonicalArtifacts(t *testing.T) {
	output := releaseBundleFixture(t, "0.2.0-preview.1")
	if err := VerifyReleaseBundle(VerifyOptions{Output: output, Tag: "v0.2.0-preview.1"}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyReleaseBundleRejectsMissingTarget(t *testing.T) {
	output := releaseBundleFixture(t, "0.2.0-preview.1")
	if err := os.Remove(filepath.Join(output, "open-skills_0.2.0-preview.1_linux_arm64.tar.gz")); err != nil {
		t.Fatal(err)
	}

	err := VerifyReleaseBundle(VerifyOptions{Output: output, Tag: "v0.2.0-preview.1"})
	if err == nil || !strings.Contains(err.Error(), "missing release target") {
		t.Fatalf("VerifyReleaseBundle() error = %v; want missing release target", err)
	}
}

func TestVerifyReleaseBundleRejectsExecutableAliases(t *testing.T) {
	output := releaseBundleFixture(t, "0.2.0-preview.1")
	archive := filepath.Join(output, "open-skills_0.2.0-preview.1_darwin_arm64.tar.gz")
	executable := filepath.Join(t.TempDir(), "skills")
	if err := os.WriteFile(executable, []byte("alias"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTarGzip(archive, executable, "skills"); err != nil {
		t.Fatal(err)
	}
	if err := WriteChecksums(output, expectedArtifacts("0.2.0-preview.1")); err != nil {
		t.Fatal(err)
	}

	err := VerifyReleaseBundle(VerifyOptions{Output: output, Tag: "v0.2.0-preview.1"})
	if err == nil || !strings.Contains(err.Error(), "canonical executable") {
		t.Fatalf("VerifyReleaseBundle() error = %v; want canonical executable rejection", err)
	}
}

func TestVerifyReleaseBundleRejectsMismatchedChecksum(t *testing.T) {
	output := releaseBundleFixture(t, "0.2.0-preview.1")
	archive := filepath.Join(output, "open-skills_0.2.0-preview.1_windows_amd64.zip")
	file, err := os.OpenFile(archive, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("tampered"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	err = VerifyReleaseBundle(VerifyOptions{Output: output, Tag: "v0.2.0-preview.1"})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("VerifyReleaseBundle() error = %v; want checksum mismatch", err)
	}
}

func TestVerifyReleaseBundleRejectsArtifactsForAnotherTag(t *testing.T) {
	output := releaseBundleFixture(t, "0.2.0-preview.1")
	err := VerifyReleaseBundle(VerifyOptions{Output: output, Tag: "v0.2.0-preview.2"})
	if err == nil || !strings.Contains(err.Error(), "does not correspond to release tag") {
		t.Fatalf("VerifyReleaseBundle() error = %v; want release tag mismatch", err)
	}
}

func TestVerifyReleaseBundleRejectsUnexpectedArchives(t *testing.T) {
	output := releaseBundleFixture(t, "0.2.0-preview.1")
	if err := os.WriteFile(filepath.Join(output, "unrelated.tar.gz"), []byte("not a release target"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := VerifyReleaseBundle(VerifyOptions{Output: output, Tag: "v0.2.0-preview.1"})
	if err == nil || !strings.Contains(err.Error(), "does not correspond to release tag") {
		t.Fatalf("VerifyReleaseBundle() error = %v; want unexpected archive rejection", err)
	}
}

func TestVerifyReleaseBundleRejectsUnexpectedNonArchiveAssets(t *testing.T) {
	output := releaseBundleFixture(t, "0.2.0-preview.1")
	if err := os.WriteFile(filepath.Join(output, "skills"), []byte("alias executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := VerifyReleaseBundle(VerifyOptions{Output: output, Tag: "v0.2.0-preview.1"})
	if err == nil || !strings.Contains(err.Error(), "unexpected release asset") {
		t.Fatalf("VerifyReleaseBundle() error = %v; want unexpected asset rejection", err)
	}
}

func TestVerifyReleaseBundleReadsZipPayload(t *testing.T) {
	output := releaseBundleFixture(t, "0.2.0-preview.1")
	archive := filepath.Join(output, "open-skills_0.2.0-preview.1_windows_amd64.zip")
	writeCorruptZipFixture(t, archive)
	if err := WriteChecksums(output, expectedArtifacts("0.2.0-preview.1")); err != nil {
		t.Fatal(err)
	}
	err := VerifyReleaseBundle(VerifyOptions{Output: output, Tag: "v0.2.0-preview.1"})
	if err == nil || !strings.Contains(err.Error(), "read canonical executable") {
		t.Fatalf("VerifyReleaseBundle() error = %v; want corrupt ZIP rejection", err)
	}
}

func TestVerifyReleaseBundleRequiresExecutableUnixPayload(t *testing.T) {
	output := releaseBundleFixture(t, "0.2.0-preview.1")
	archive := filepath.Join(output, "open-skills_0.2.0-preview.1_linux_arm64.tar.gz")
	writeTarModeFixture(t, archive, 0o644)
	if err := WriteChecksums(output, expectedArtifacts("0.2.0-preview.1")); err != nil {
		t.Fatal(err)
	}
	err := VerifyReleaseBundle(VerifyOptions{Output: output, Tag: "v0.2.0-preview.1"})
	if err == nil || !strings.Contains(err.Error(), "must be executable") {
		t.Fatalf("VerifyReleaseBundle() error = %v; want non-executable payload rejection", err)
	}
}

func TestVerifyReleaseBundleRequiresSignaturesAndProvenance(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "archive signature", file: "open-skills_0.2.0-preview.1_linux_amd64.tar.gz.sigstore.json", want: "missing keyless signature"},
		{name: "checksum signature", file: "checksums.txt.sigstore.json", want: "missing keyless signature"},
		{name: "provenance", file: "provenance.sigstore.json", want: "missing build provenance"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := releaseBundleFixture(t, "0.2.0-preview.1")
			if err := os.Remove(filepath.Join(output, test.file)); err != nil {
				t.Fatal(err)
			}
			err := VerifyReleaseBundle(VerifyOptions{Output: output, Tag: "v0.2.0-preview.1"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyReleaseBundle() error = %v; want %s", err, test.want)
			}
		})
	}
}

func releaseBundleFixture(t *testing.T, version string) string {
	t.Helper()
	output := t.TempDir()
	executable := filepath.Join(t.TempDir(), "open-skills")
	if err := os.WriteFile(executable, []byte("native executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	artifacts := expectedArtifacts(version)
	for _, artifact := range artifacts {
		path := filepath.Join(output, artifact.Filename)
		entry := "open-skills"
		var err error
		if artifact.GOOS == "windows" {
			entry = "open-skills.exe"
			err = writeZip(path, executable, entry)
		} else {
			err = writeTarGzip(path, executable, entry)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteChecksums(output, artifacts); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range artifacts {
		if err := os.WriteFile(filepath.Join(output, artifact.Filename+".sigstore.json"), []byte("signature"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"checksums.txt.sigstore.json", "provenance.sigstore.json"} {
		if err := os.WriteFile(filepath.Join(output, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return output
}

func writeCorruptZipFixture(t *testing.T, path string) {
	t.Helper()
	var content bytes.Buffer
	writer := zip.NewWriter(&content)
	header := &zip.FileHeader{Name: "open-skills.exe", Method: zip.Store}
	header.SetMode(0o755)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("windows executable payload")
	if _, err := entry.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data := content.Bytes()
	index := bytes.Index(data, payload)
	if index < 0 {
		t.Fatal("ZIP fixture payload was not stored verbatim")
	}
	data[index] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTarModeFixture(t *testing.T, path string, mode int64) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	writer := tar.NewWriter(compressed)
	payload := []byte("unix executable payload")
	if err := writer.WriteHeader(&tar.Header{Name: "open-skills", Mode: mode, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

type archiveEntry struct {
	name string
	size int64
}

func archiveEntries(t *testing.T, path string) []archiveEntry {
	t.Helper()
	if strings.HasSuffix(path, ".zip") {
		reader, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		entries := make([]archiveEntry, 0, len(reader.File))
		for _, file := range reader.File {
			entries = append(entries, archiveEntry{name: file.Name, size: int64(file.UncompressedSize64)})
		}
		return entries
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	var entries []archiveEntry
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, archiveEntry{name: header.Name, size: header.Size})
	}
	return entries
}
