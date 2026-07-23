package compatibility

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestNativeD03RedirectReportsFinalHostAndRedactsPersistedURL(t *testing.T) {
	const querySecret = "redirect-query-secret"
	skillMD := []byte("---\nname: redirected\ndescription: redirected fixture\n---\n")
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{http:url}}", "--agent", "universal", "--yes", "--allow-insecure-transport"},
		HTTPRoutes: []HTTPRoute{
			{Method: http.MethodGet, Path: "/.well-known/agent-skills/index.json", Status: http.StatusFound, Header: http.Header{"Location": []string{"/redirected/index.json?access_token=" + querySecret}}},
			{Method: http.MethodGet, Path: "/redirected/index.json", Body: []byte(`{"skills":[{"name":"redirected","description":"redirected fixture","files":["SKILL.md"]}]}`)},
			{Method: http.MethodGet, Path: "/redirected/redirected/SKILL.md", Body: skillMD},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixtureHost := strings.TrimPrefix(observation.Paths.FixtureURL, "http://")
	if observation.ExitCode != 0 || observation.Stdout != "Installed redirected\n" || !strings.Contains(observation.Stderr, "final host "+fixtureHost) {
		t.Fatalf("redirected HTTP source = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	lock := observation.Locks[ProjectLock]
	combined := observation.Stdout + observation.Stderr + string(lock)
	if strings.Contains(combined, querySecret) || strings.Contains(combined, "access_token") {
		t.Fatalf("redirect token leaked into diagnostics or provenance: %q", combined)
	}
	if !strings.Contains(string(lock), observation.Paths.FixtureURL+"/redirected/redirected/SKILL.md") {
		t.Fatalf("redirected final URL missing from lock: %s", lock)
	}
}

func TestNativeHTTPSourceRequiresDedicatedInsecureTransportAuthorization(t *testing.T) {
	skillMD := []byte("---\nname: insecure\ndescription: insecure fixture\n---\n")
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{http:url}}", "--agent", "universal", "--yes"},
		HTTPRoutes: []HTTPRoute{
			{Method: http.MethodGet, Path: "/.well-known/agent-skills/index.json", Body: []byte(`{"skills":[{"name":"insecure","description":"insecure fixture","files":["SKILL.md"]}]}`)},
			{Method: http.MethodGet, Path: "/.well-known/agent-skills/insecure/SKILL.md", Body: skillMD},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || observation.Stdout != "" || !strings.Contains(observation.Stderr, "--allow-insecure-transport") {
		t.Fatalf("unauthorized HTTP source = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	if len(observation.HTTPRequests) != 0 {
		t.Fatalf("unauthorized HTTP source made requests: %#v", observation.HTTPRequests)
	}
	if _, found := observation.Locks[ProjectLock]; found {
		t.Fatal("unauthorized HTTP source wrote a project lock")
	}
}
