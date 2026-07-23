package release

import (
	"archive/tar"
	"archive/zip"
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
