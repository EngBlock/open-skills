package application

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type recordedWellKnownRequest struct {
	Host string
	Path string
}

func TestWellKnownCurrentIndexListsSelectsInstallsAndRecordsMultifileSkill(t *testing.T) {
	var requests []recordedWellKnownRequest
	var mutex sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		requests = append(requests, recordedWellKnownRequest{Host: request.Host, Path: request.URL.Path})
		mutex.Unlock()
		switch request.URL.Path {
		case "/.well-known/agent-skills/index.json":
			_, _ = response.Write([]byte(`{"skills":[{"name":"single-file","description":"One file","files":["SKILL.md"]},{"name":"multi-file","description":"Many files","files":["SKILL.md","references/guide.md"]}]}`))
		case "/.well-known/agent-skills/single-file/SKILL.md":
			_, _ = response.Write([]byte("---\nname: single-file\ndescription: One file\n---\n"))
		case "/.well-known/agent-skills/multi-file/SKILL.md":
			_, _ = response.Write([]byte("---\nname: multi-file\ndescription: Many files\n---\n"))
		case "/.well-known/agent-skills/multi-file/references/guide.md":
			_, _ = response.Write([]byte("reference material\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	project := t.TempDir()
	withWorkingDirectory(t, project)
	var listed, listErrors bytes.Buffer
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &listed, Stderr: &listErrors}, []string{server.URL, "--list"}); exit != 0 {
		t.Fatalf("list exit = %d, stdout %q, stderr %q", exit, listed.String(), listErrors.String())
	}
	if !strings.Contains(listed.String(), "single-file") || !strings.Contains(listed.String(), "multi-file") {
		t.Fatalf("list output = %q", listed.String())
	}

	var stdout, stderr bytes.Buffer
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{server.URL, "--skill", "multi-file", "--agent", "universal", "--copy", "--yes"}); exit != 0 {
		t.Fatalf("install exit = %d, stdout %q, stderr %q", exit, stdout.String(), stderr.String())
	}
	installed := filepath.Join(project, ".agents", "skills", "multi-file")
	if data, err := os.ReadFile(filepath.Join(installed, "references", "guide.md")); err != nil || string(data) != "reference material\n" {
		t.Fatalf("multifile payload = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "single-file")); !os.IsNotExist(err) {
		t.Fatalf("unselected skill was installed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(project, "skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Skills map[string]struct {
			Source     string `json:"source"`
			SourceURL  string `json:"sourceUrl"`
			SourceType string `json:"sourceType"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	entry := lock.Skills["multi-file"]
	if entry.Source != "127.0.0.1" || entry.SourceType != "well-known" || entry.SourceURL != server.URL+"/.well-known/agent-skills/multi-file/SKILL.md" {
		t.Fatalf("unsafe or incomplete well-known provenance: %#v", entry)
	}
	assertOnlyFixtureRequests(t, server, requests)
}

func TestWellKnownV2ArchiveInstallsMultifileSkill(t *testing.T) {
	archive := createWellKnownZIP(t, map[string]string{
		"SKILL.md":            "---\nname: v2-archive\ndescription: V2 archive\n---\n",
		"references/guide.md": "v2 reference\n",
	})
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/agent-skills/index.json":
			_, _ = response.Write([]byte(`{"$schema":"https://schemas.agentskills.io/discovery/0.2.0/schema.json","skills":[{"name":"v2-archive","description":"V2 archive","type":"archive","url":"/downloads/v2-archive.zip","digest":"` + digest + `"}]}`))
		case "/downloads/v2-archive.zip":
			_, _ = response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	project := t.TempDir()
	withWorkingDirectory(t, project)
	var stdout, stderr bytes.Buffer
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{server.URL, "--agent", "universal", "--yes"}); exit != 0 {
		t.Fatalf("install exit = %d, stdout %q, stderr %q", exit, stdout.String(), stderr.String())
	}
	if data, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "v2-archive", "references", "guide.md")); err != nil || string(data) != "v2 reference\n" {
		t.Fatalf("v2 multifile payload = %q, %v", data, err)
	}
}

func createWellKnownZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, contents := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestWellKnownLegacyIndexAndDirectURLs(t *testing.T) {
	for _, test := range []struct {
		name             string
		sourcePath       string
		indexPath        string
		currentIndexBody string
		skillPath        string
		skillName        string
	}{
		{
			name:      "legacy index",
			indexPath: "/.well-known/skills/index.json",
			skillPath: "/.well-known/skills/legacy-skill/SKILL.md",
			skillName: "legacy-skill",
		},
		{
			name:             "legacy fallback after malformed current index",
			indexPath:        "/.well-known/skills/index.json",
			currentIndexBody: "not JSON",
			skillPath:        "/.well-known/skills/legacy-after-invalid/SKILL.md",
			skillName:        "legacy-after-invalid",
		},
		{
			name:       "current direct directory URL",
			sourcePath: "/.well-known/agent-skills/direct-current",
			skillPath:  "/.well-known/agent-skills/direct-current/SKILL.md",
			skillName:  "direct-current",
		},
		{
			name:       "legacy direct skill URL",
			sourcePath: "/.well-known/skills/direct-legacy/SKILL.md",
			skillPath:  "/.well-known/skills/direct-legacy/SKILL.md",
			skillName:  "direct-legacy",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests []recordedWellKnownRequest
			var mutex sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				mutex.Lock()
				requests = append(requests, recordedWellKnownRequest{Host: request.Host, Path: request.URL.Path})
				mutex.Unlock()
				if request.URL.Path == "/.well-known/agent-skills/index.json" && test.currentIndexBody != "" {
					_, _ = response.Write([]byte(test.currentIndexBody))
					return
				}
				if request.URL.Path == test.indexPath {
					_, _ = response.Write([]byte(`{"skills":[{"name":"` + test.skillName + `","description":"fixture skill","files":["SKILL.md"]}]}`))
					return
				}
				if request.URL.Path == test.skillPath {
					_, _ = response.Write([]byte("---\nname: " + test.skillName + "\ndescription: fixture skill\n---\n"))
					return
				}
				http.NotFound(response, request)
			}))
			defer server.Close()

			project := t.TempDir()
			withWorkingDirectory(t, project)
			source := server.URL + test.sourcePath
			var stdout, stderr bytes.Buffer
			if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source, "--agent", "universal", "--yes"}); exit != 0 {
				t.Fatalf("install exit = %d, stdout %q, stderr %q", exit, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(filepath.Join(project, ".agents", "skills", test.skillName, "SKILL.md")); err != nil {
				t.Fatalf("direct skill was not installed: %v", err)
			}
			assertOnlyFixtureRequests(t, server, requests)
		})
	}
}

func withWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Error(err)
		}
	})
}

func assertOnlyFixtureRequests(t *testing.T, server *httptest.Server, requests []recordedWellKnownRequest) {
	t.Helper()
	if len(requests) == 0 {
		t.Fatal("expected well-known HTTP requests")
	}
	fixtureHost := strings.TrimPrefix(server.URL, "http://")
	for _, request := range requests {
		if request.Host != fixtureHost {
			t.Fatalf("request escaped supplied fixture source: %#v", request)
		}
	}
}
