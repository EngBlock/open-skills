package compatibility

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeFullDepthCeilingPreservesShallowDiscovery(t *testing.T) {
	target := buildShellTarget(t)
	files := []FileFixture{
		{Root: TempRoot, Path: "source/SKILL.md", Data: []byte("---\nname: root\ndescription: root skill\n---\n")},
		{Root: TempRoot, Path: "source/a/b/SKILL.md", Data: []byte("---\nname: nested\ndescription: nested skill\n---\n")},
	}
	shallow, err := (Harness{}).Run(context.Background(), target, Scenario{
		Args:    []string{"add", "{{temp}}/source", "--list", "--max-depth", "1"},
		Files:   files,
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if shallow.ExitCode != 0 || shallow.Stderr != "" || shallow.Stdout != "root\n" {
		t.Fatalf("shallow discovery = exit %d stdout %q stderr %q", shallow.ExitCode, shallow.Stdout, shallow.Stderr)
	}

	deep, err := (Harness{}).Run(context.Background(), target, Scenario{
		Args:    []string{"add", "{{temp}}/source", "--list", "--full-depth", "--max-depth", "1"},
		Files:   files,
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deep.ExitCode != 1 || deep.Stdout != "" || !strings.Contains(deep.Stderr, "--max-depth") {
		t.Fatalf("full-depth discovery = exit %d stdout %q stderr %q", deep.ExitCode, deep.Stdout, deep.Stderr)
	}
}

func TestNativeRemoteResourceFailureLeavesNoInstalledContentOrLock(t *testing.T) {
	skillMD := []byte("---\nname: bounded\ndescription: bounded skill\n---\n")
	target := buildShellTarget(t)
	failure, err := (Harness{}).Run(context.Background(), target, Scenario{
		Args: []string{
			"add", "{{http:url}}", "--agent", "universal", "--yes",
			"--max-file-bytes", fmt.Sprintf("%d", len(skillMD)-1),
		},
		HTTPRoutes: []HTTPRoute{
			{Method: http.MethodGet, Path: "/.well-known/agent-skills/index.json", Status: http.StatusOK, Body: []byte(`{"skills":[{"name":"bounded","description":"bounded skill","files":["SKILL.md"]}]}`)},
			{Method: http.MethodGet, Path: "/.well-known/agent-skills/bounded/SKILL.md", Status: http.StatusOK, Body: skillMD},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if failure.ExitCode != 1 || failure.Stdout != "" || !strings.Contains(failure.Stderr, "--max-file-bytes") {
		t.Fatalf("failure = exit %d stdout %q stderr %q", failure.ExitCode, failure.Stdout, failure.Stderr)
	}
	if _, found := failure.Files[filepath.Join("project", ".agents", "skills", "bounded")]; found {
		t.Fatal("resource failure left installed content")
	}
	if _, found := failure.Locks[ProjectLock]; found {
		t.Fatal("resource failure wrote a project lock")
	}

	exact := fmt.Sprintf("%d", len(skillMD))
	success, err := (Harness{}).Run(context.Background(), target, Scenario{
		Args: []string{
			"add", "{{http:url}}", "--agent", "universal", "--yes",
			"--max-file-bytes", exact, "--max-total-bytes", exact, "--max-files", "1",
		},
		HTTPRoutes: []HTTPRoute{
			{Method: http.MethodGet, Path: "/.well-known/agent-skills/index.json", Status: http.StatusOK, Body: []byte(`{"skills":[{"name":"bounded","description":"bounded skill","files":["SKILL.md"]}]}`)},
			{Method: http.MethodGet, Path: "/.well-known/agent-skills/bounded/SKILL.md", Status: http.StatusOK, Body: skillMD},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if success.ExitCode != 0 || success.Stderr != "" || success.Stdout != "Installed bounded\n" {
		t.Fatalf("success = exit %d stdout %q stderr %q", success.ExitCode, success.Stdout, success.Stderr)
	}
	installed := success.Files[filepath.Join("project", ".agents", "skills", "bounded", "SKILL.md")]
	if installed.Kind != FileKindRegular || string(installed.Data) != string(skillMD) {
		t.Fatalf("installed skill = %#v", installed)
	}
	if _, found := success.Locks[ProjectLock]; !found {
		t.Fatal("successful exact-boundary install did not write a project lock")
	}
}
