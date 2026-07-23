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
	"sync/atomic"
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
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &listed, Stderr: &listErrors}, []string{server.URL, "--list", "--allow-insecure-transport"}); exit != 0 {
		t.Fatalf("list exit = %d, stdout %q, stderr %q", exit, listed.String(), listErrors.String())
	}
	if !strings.Contains(listed.String(), "single-file") || !strings.Contains(listed.String(), "multi-file") {
		t.Fatalf("list output = %q", listed.String())
	}

	var stdout, stderr bytes.Buffer
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{server.URL, "--skill", "multi-file", "--agent", "universal", "--copy", "--yes", "--allow-insecure-transport"}); exit != 0 {
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
			Ref        string `json:"ref"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	entry := lock.Skills["multi-file"]
	if entry.Source != "127.0.0.1" || entry.SourceType != "well-known" || entry.SourceURL != server.URL+"/.well-known/agent-skills/multi-file/SKILL.md" || !strings.HasPrefix(entry.Ref, "sha256:") {
		t.Fatalf("unsafe or incomplete well-known provenance: %#v", entry)
	}
	assertOnlyFixtureRequests(t, server, requests)
}

func TestWellKnownHTTPRequiresDedicatedAuthorizationAndWarnsBeforeAcquisition(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		switch request.URL.Path {
		case "/.well-known/agent-skills/index.json":
			_, _ = response.Write([]byte(`{"skills":[{"name":"insecure","description":"insecure fixture","files":["SKILL.md"]}]}`))
		case "/.well-known/agent-skills/insecure/SKILL.md":
			_, _ = response.Write([]byte("---\nname: insecure\ndescription: insecure fixture\n---\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	project := t.TempDir()
	withWorkingDirectory(t, project)
	var stdout, stderr bytes.Buffer
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{server.URL, "--agent", "universal", "--yes"}); exit != 1 || !strings.Contains(stderr.String(), "--allow-insecure-transport") {
		t.Fatalf("unauthorized HTTP source = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if requests.Load() != 0 {
		t.Fatalf("unauthorized HTTP source made %d requests", requests.Load())
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{server.URL, "--agent", "universal", "--yes", "--allow-insecure-transport"}); exit != 0 || !strings.Contains(stderr.String(), "Warning: allowing insecure HTTP well-known source") {
		t.Fatalf("authorized HTTP source = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
}

func TestWellKnownCrossHostRedirectReportsAndRecordsRedactedFinalHost(t *testing.T) {
	const querySecret = "redirect-query-secret"
	destination := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/agent-skills/index.json":
			_, _ = response.Write([]byte(`{"skills":[{"name":"redirected","description":"redirect fixture","files":["SKILL.md"]}]}`))
		case "/.well-known/agent-skills/redirected/SKILL.md":
			_, _ = response.Write([]byte("---\nname: redirected\ndescription: redirect fixture\n---\n"))
		default:
			http.NotFound(response, request)
		}
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, destination.URL+request.URL.Path+"?access_token="+querySecret, http.StatusFound)
	}))
	defer source.Close()

	project := t.TempDir()
	withWorkingDirectory(t, project)
	var stdout, stderr bytes.Buffer
	arguments := []string{source.URL, "--agent", "universal", "--yes", "--allow-insecure-transport"}
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, arguments); exit != 0 {
		t.Fatalf("redirected install = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	destinationHost := strings.TrimPrefix(destination.URL, "http://")
	if !strings.Contains(stderr.String(), "final host "+destinationHost) {
		t.Fatalf("redirect diagnostics = %q", stderr.String())
	}
	lock, err := os.ReadFile(filepath.Join(project, "skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var listJSON, listErrors bytes.Buffer
	if exit := runList(Invocation{Stdout: &listJSON, Stderr: &listErrors}, []string{"--json"}); exit != 0 {
		t.Fatalf("list JSON = %d stdout %q stderr %q", exit, listJSON.String(), listErrors.String())
	}
	combined := stdout.String() + stderr.String() + string(lock) + listJSON.String() + listErrors.String()
	if strings.Contains(combined, querySecret) || strings.Contains(combined, "access_token") {
		t.Fatalf("redirect token leaked into diagnostics, JSON, or provenance: %q", combined)
	}
	if !strings.Contains(string(lock), destination.URL+"/.well-known/agent-skills/redirected/SKILL.md") {
		t.Fatalf("lock omits redirected final URL: %s", lock)
	}
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
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{server.URL, "--agent", "universal", "--yes", "--allow-insecure-transport"}); exit != 0 {
		t.Fatalf("install exit = %d, stdout %q, stderr %q", exit, stdout.String(), stderr.String())
	}
	if data, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "v2-archive", "references", "guide.md")); err != nil || string(data) != "v2 reference\n" {
		t.Fatalf("v2 multifile payload = %q, %v", data, err)
	}
}

func TestWellKnownSelectedContentLimitsIgnoreUnselectedSkillSize(t *testing.T) {
	var largeRequested atomic.Bool
	small := []byte("---\nname: small\ndescription: small skill\n---\n")
	large := append([]byte("---\nname: large\ndescription: unselected large skill\n---\n"), bytes.Repeat([]byte{'x'}, int(defaultMaxFileBytes))...)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/agent-skills/index.json":
			_, _ = response.Write([]byte(`{"skills":[{"name":"small","description":"small skill","files":["SKILL.md"]},{"name":"large","description":"unselected large skill","files":["SKILL.md"]}]}`))
		case "/.well-known/agent-skills/small/SKILL.md":
			_, _ = response.Write(small)
		case "/.well-known/agent-skills/large/SKILL.md":
			largeRequested.Store(true)
			_, _ = response.Write(large)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	project := t.TempDir()
	withWorkingDirectory(t, project)
	var stdout, stderr bytes.Buffer
	arguments := []string{server.URL, "--skill", "small", "--agent", "universal", "--yes", "--allow-insecure-transport"}
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, arguments); exit != 0 {
		t.Fatalf("selected install = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "small", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "large")); !os.IsNotExist(err) {
		t.Fatalf("unselected large skill was installed: %v", err)
	}
	if largeRequested.Load() {
		t.Fatal("unselected large skill content was fetched")
	}
}

func TestWellKnownResourceFailureIsActionableAndLeavesNoState(t *testing.T) {
	skillMD := []byte("---\nname: bounded\ndescription: bounded skill\n---\n")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/agent-skills/index.json":
			_, _ = response.Write([]byte(`{"skills":[{"name":"bounded","description":"bounded skill","files":["SKILL.md"]}]}`))
		case "/.well-known/agent-skills/bounded/SKILL.md":
			_, _ = response.Write(skillMD)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	project := t.TempDir()
	withWorkingDirectory(t, project)
	var stdout, stderr bytes.Buffer
	tooSmall := fmt.Sprintf("%d", len(skillMD)-1)
	arguments := []string{server.URL, "--agent", "universal", "--yes", "--allow-insecure-transport", "--max-file-bytes", tooSmall}
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, arguments); exit != 1 || !strings.Contains(stderr.String(), "--max-file-bytes") {
		t.Fatalf("bounded install = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "bounded")); !os.IsNotExist(err) {
		t.Fatalf("resource failure left installed content: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "skills-lock.json")); !os.IsNotExist(err) {
		t.Fatalf("resource failure left a lock: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	exact := fmt.Sprintf("%d", len(skillMD))
	arguments = []string{server.URL, "--agent", "universal", "--yes", "--allow-insecure-transport", "--max-file-bytes", exact, "--max-total-bytes", exact, "--max-files", "1"}
	if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, arguments); exit != 0 {
		t.Fatalf("exact-boundary install = %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
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
			if exit := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source, "--agent", "universal", "--yes", "--allow-insecure-transport"}); exit != 0 {
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
