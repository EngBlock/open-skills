package compatibility

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeShellShowsBannerOnlyOutsideAgentEnvironments(t *testing.T) {
	target := buildShellTarget(t)
	harness := Harness{}

	ordinary, err := harness.Run(context.Background(), target, Scenario{Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.ExitCode != 0 || ordinary.Stderr != "" {
		t.Fatalf("ordinary startup = exit %d, stderr %q", ordinary.ExitCode, ordinary.Stderr)
	}
	for _, text := range []string{
		"The open agent skills ecosystem",
		"open-skills add",
		"open-skills use",
		"open-skills update",
		"open-skills init",
		"EngBlock/open-skills@find-skills",
	} {
		if !strings.Contains(ordinary.Stdout, text) {
			t.Errorf("ordinary startup stdout does not contain %q: %q", text, ordinary.Stdout)
		}
	}
	assertOfflineShellObservation(t, ordinary)

	agent, err := harness.Run(context.Background(), target, Scenario{Offline: true, Env: map[string]string{"AI_AGENT": "test-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	if agent.ExitCode != 0 || agent.Stdout != "" || agent.Stderr != "" {
		t.Fatalf("agent startup = exit %d, stdout %q, stderr %q", agent.ExitCode, agent.Stdout, agent.Stderr)
	}
	assertOfflineShellObservation(t, agent)

	for _, test := range []struct {
		name string
		env  map[string]string
	}{
		{name: "Cursor", env: map[string]string{"CURSOR_AGENT": "1"}},
		{name: "Cursor extension", env: map[string]string{"CURSOR_EXTENSION_HOST_ROLE": "agent-exec"}},
		{name: "Gemini", env: map[string]string{"GEMINI_CLI": "1"}},
		{name: "Codex", env: map[string]string{"CODEX_SANDBOX": "1"}},
		{name: "Antigravity", env: map[string]string{"ANTIGRAVITY_AGENT": "1"}},
		{name: "Augment", env: map[string]string{"AUGMENT_AGENT": "1"}},
		{name: "OpenCode", env: map[string]string{"OPENCODE_CLIENT": "1"}},
		{name: "Claude", env: map[string]string{"CLAUDE_CODE": "1"}},
		{name: "Replit", env: map[string]string{"REPL_ID": "1"}},
		{name: "Copilot", env: map[string]string{"COPILOT_MODEL": "test"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation, err := harness.Run(context.Background(), target, Scenario{Offline: true, Env: test.env})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != 0 || observation.Stdout != "" || observation.Stderr != "" {
				t.Fatalf("agent startup = exit %d, stdout %q, stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
			}
		})
	}

	for _, env := range []map[string]string{{"AI_AGENT": "   "}, {"CURSOR_AGENT": "   "}, {"CURSOR_TRACE_ID": "terminal-only"}} {
		observation, err := harness.Run(context.Background(), target, Scenario{Offline: true, Env: env})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(observation.Stdout, "The open agent skills ecosystem") {
			t.Fatalf("weak agent signal suppressed banner: env=%v stdout=%q", env, observation.Stdout)
		}
	}
}

func TestNativeShellHelpIsCanonicalAndOffline(t *testing.T) {
	target := buildShellTarget(t)
	harness := Harness{}

	long, err := harness.Run(context.Background(), target, Scenario{Offline: true, Args: []string{"--help"}})
	if err != nil {
		t.Fatal(err)
	}
	short, err := harness.Run(context.Background(), target, Scenario{Offline: true, Args: []string{"-h"}})
	if err != nil {
		t.Fatal(err)
	}
	if long.ExitCode != 0 || long.Stderr != "" || short.ExitCode != 0 || short.Stderr != "" {
		t.Fatalf("help failed: long=%#v short=%#v", long, short)
	}
	if long.Stdout != short.Stdout {
		t.Fatalf("help aliases differ: --help %q, -h %q", long.Stdout, short.Stdout)
	}
	for _, text := range []string{
		"Usage: open-skills <command> [options]",
		"add <package>",
		"use <package>@<skill>",
		"remove [skills]",
		"list, ls",
		"find, search, f, s",
		"update [skills...]",
		"init [name]",
		"--help, -h",
		"--version, -v",
	} {
		if !strings.Contains(long.Stdout, text) {
			t.Errorf("help does not contain %q: %q", text, long.Stdout)
		}
	}
	for _, forbidden := range []string{"npx skills", "skills.sh", "self-update", "add-skill"} {
		if strings.Contains(long.Stdout, forbidden) {
			t.Errorf("help advertises forbidden %q: %q", forbidden, long.Stdout)
		}
	}
	assertOfflineShellObservation(t, long)
	assertOfflineShellObservation(t, short)
}

func TestNativeShellVersionAliasesAreStableAndOffline(t *testing.T) {
	target := buildShellTarget(t)
	harness := Harness{}

	for _, flag := range []string{"--version", "-v"} {
		observation, err := harness.Run(context.Background(), target, Scenario{Offline: true, Args: []string{flag}})
		if err != nil {
			t.Fatal(err)
		}
		if observation.ExitCode != 0 || observation.Stdout != "0.1.2\n" || observation.Stderr != "" {
			t.Errorf("%s = exit %d, stdout %q, stderr %q", flag, observation.ExitCode, observation.Stdout, observation.Stderr)
		}
		assertOfflineShellObservation(t, observation)
	}
}

func TestNativeShellFindAliasesReturnOfflineMigrationGuidance(t *testing.T) {
	target := buildShellTarget(t)
	harness := Harness{}
	want := "Hosted skill search is no longer available.\n" +
		"Discover skills by searching GitHub and the web for SKILL.md files, then install one with:\n" +
		"  open-skills add <owner>/<repo>@<skill>\n"

	for _, command := range []string{"find", "search", "f", "s"} {
		for _, arguments := range [][]string{{command, "react", "--owner", "example"}, {command, "--help"}, {command, "-h"}} {
			observation, err := harness.Run(context.Background(), target, Scenario{Offline: true, Args: arguments})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != 1 || observation.Stdout != want || observation.Stderr != "" {
				t.Errorf("%v = exit %d, stdout %q, stderr %q", arguments, observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			assertOfflineShellObservation(t, observation)
		}
	}
}

func TestNativeShellSubcommandHelpShortCircuitsBeforeDispatch(t *testing.T) {
	target := buildShellTarget(t)
	harness := Harness{}
	topLevelCommands := []string{
		"add", "a", "install", "i", "use", "list", "ls", "check", "update", "upgrade",
		"init", "experimental_install", "experimental_sync", "unknown-command",
	}

	for _, command := range topLevelCommands {
		for _, flag := range []string{"--help", "-h"} {
			observation, err := harness.Run(context.Background(), target, Scenario{Offline: true, Args: []string{command, flag}})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != 0 || !strings.Contains(observation.Stdout, "Usage: open-skills <command> [options]") || observation.Stderr != "" {
				t.Errorf("%s %s did not return top-level help: exit %d, stdout %q, stderr %q", command, flag, observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			if _, created := observation.Files["project/SKILL.md"]; created {
				t.Errorf("%s %s created SKILL.md", command, flag)
			}
			assertOfflineShellObservation(t, observation)
		}
	}

	for _, command := range []string{"remove", "rm", "r"} {
		observation, err := harness.Run(context.Background(), target, Scenario{Offline: true, Args: []string{command, "--help"}})
		if err != nil {
			t.Fatal(err)
		}
		if observation.ExitCode != 0 || !strings.Contains(observation.Stdout, "Usage: open-skills remove [skills...] [options]") || observation.Stderr != "" {
			t.Errorf("%s --help did not return remove help: exit %d, stdout %q, stderr %q", command, observation.ExitCode, observation.Stdout, observation.Stderr)
		}
		assertOfflineShellObservation(t, observation)
	}
}

func TestNativeShellRejectsUnknownCommands(t *testing.T) {
	target := buildShellTarget(t)
	observation, err := (Harness{}).Run(context.Background(), target, Scenario{Offline: true, Args: []string{"unknown-command"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "Unknown command: unknown-command\nRun open-skills --help for usage.\n"
	if observation.ExitCode != 1 || observation.Stdout != want || observation.Stderr != "" {
		t.Fatalf("unknown command = exit %d, stdout %q, stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeShellInitCreatesBaselineTemplateOffline(t *testing.T) {
	target := buildShellTarget(t)
	observation, err := (Harness{}).Run(context.Background(), target, Scenario{Offline: true, Args: []string{"init", "demo", "ignored"}})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("init = exit %d, stderr %q", observation.ExitCode, observation.Stderr)
	}
	for _, text := range []string{"Initialized skill: demo", "Created:", "demo/SKILL.md", "open-skills add <owner>/<repo>"} {
		if !strings.Contains(observation.Stdout, text) {
			t.Errorf("init output does not contain %q: %q", text, observation.Stdout)
		}
	}
	want := `---
name: demo
description: A brief description of what this skill does
---

# demo

Instructions for the agent to follow when this skill is activated.

## When to use

Describe when this skill should be used.

## Instructions

1. First step
2. Second step
3. Additional steps as needed
`
	assertFile(t, observation, "project/demo/SKILL.md", FileState{Kind: FileKindRegular, Data: []byte(want)})
	assertOfflineShellObservation(t, observation)
}

func TestNativeShellInitUsesCurrentDirectoryAndPreservesExistingSkill(t *testing.T) {
	target := buildShellTarget(t)
	harness := Harness{}

	bare, err := harness.Run(context.Background(), target, Scenario{Offline: true, Args: []string{"init"}})
	if err != nil {
		t.Fatal(err)
	}
	if bare.ExitCode != 0 || !strings.Contains(bare.Stdout, "Initialized skill: project") {
		t.Fatalf("bare init = exit %d, stdout %q, stderr %q", bare.ExitCode, bare.Stdout, bare.Stderr)
	}
	state, ok := bare.Files["project/SKILL.md"]
	if !ok || !strings.Contains(string(state.Data), "name: project\n") {
		t.Fatalf("bare init file = %#v", state)
	}
	assertOfflineShellObservation(t, bare)

	emptyName, err := harness.Run(context.Background(), target, Scenario{Offline: true, Args: []string{"init", ""}})
	if err != nil {
		t.Fatal(err)
	}
	if emptyName.ExitCode != 0 || !strings.Contains(emptyName.Stdout, "Initialized skill: project") {
		t.Fatalf("empty-name init = exit %d, stdout %q, stderr %q", emptyName.ExitCode, emptyName.Stdout, emptyName.Stderr)
	}
	emptyState, ok := emptyName.Files["project/project/SKILL.md"]
	if !ok || !strings.Contains(string(emptyState.Data), "name: project\n") {
		t.Fatalf("empty-name init file = %#v", emptyState)
	}
	assertOfflineShellObservation(t, emptyName)

	existing := []byte("keep this skill\n")
	preserved, err := harness.Run(context.Background(), target, Scenario{
		Args:    []string{"init", "demo"},
		Offline: true,
		Files:   []FileFixture{{Root: ProjectRoot, Path: "demo/SKILL.md", Data: existing}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if preserved.ExitCode != 0 || !strings.Contains(preserved.Stdout, "Skill already exists at demo/SKILL.md") || preserved.Stderr != "" {
		t.Fatalf("existing init = exit %d, stdout %q, stderr %q", preserved.ExitCode, preserved.Stdout, preserved.Stderr)
	}
	assertFile(t, preserved, "project/demo/SKILL.md", FileState{Kind: FileKindRegular, Data: existing})
	assertOfflineShellObservation(t, preserved)
}

// D02 keeps offline shell commands inert. Git source acquisition is deliberately
// implemented by the add command (issue #18), so os/exec and net/url are
// permitted dependencies; process observations above ensure shell routes never
// invoke them.
func TestD02OfflineShellHasNoNetworkDependencies(t *testing.T) {
	command := exec.Command("go", "list", "-json", "./internal/application")
	command.Dir = testModuleRoot(t)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Imports []string
		Deps    []string
	}
	if err := json.Unmarshal(output, &metadata); err != nil {
		t.Fatal(err)
	}
	for _, imported := range metadata.Imports {
		if imported == "syscall" || imported == "unsafe" {
			t.Errorf("offline shell directly imports low-level capability %q", imported)
		}
	}
	for _, dependency := range append(metadata.Imports, metadata.Deps...) {
		if dependency == "net" || dependency == "net/http" || dependency == "net/rpc" {
			t.Errorf("offline shell depends on network transport capability %q", dependency)
		}
	}
}

func TestD02PinnedNPMOracleMatchesNativeOfflineShellWhenEnabled(t *testing.T) {
	if os.Getenv("OPEN_SKILLS_TEST_PINNED_ORACLE") != "1" {
		t.Skip("set OPEN_SKILLS_TEST_PINNED_ORACLE=1 to compare against the integrity-pinned npm 0.1.2 oracle")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root := testModuleRoot(t)
	oracle, err := PrepareNPMOracle(context.Background(), OracleOptions{
		ManifestPath:   filepath.Join(root, "compatibility", "npm-0.1.2", "oracle.json"),
		Destination:    filepath.Join(t.TempDir(), "oracle"),
		NodeExecutable: node,
	})
	if err != nil {
		t.Fatal(err)
	}
	oracle.Env = map[string]string{"NODE_DISABLE_COMPILE_CACHE": "1"}
	native := buildShellTarget(t)
	harness := Harness{Oracle: oracle, Native: native}
	scenarios := []struct {
		name     string
		scenario Scenario
	}{
		{name: "banner", scenario: Scenario{Offline: true}},
		{name: "agent banner suppression", scenario: Scenario{Offline: true, Env: map[string]string{"AI_AGENT": "test"}}},
		{name: "whitespace Cursor signal", scenario: Scenario{Offline: true, Env: map[string]string{"CURSOR_AGENT": "   "}}},
		{name: "help", scenario: Scenario{Offline: true, Args: []string{"--help"}}},
		{name: "short help", scenario: Scenario{Offline: true, Args: []string{"-h"}}},
		{name: "version", scenario: Scenario{Offline: true, Args: []string{"--version"}}},
		{name: "unknown command", scenario: Scenario{Offline: true, Args: []string{"unknown-command"}}},
		{name: "unknown command help", scenario: Scenario{Offline: true, Args: []string{"unknown-command", "--help"}}},
		{name: "remove help", scenario: Scenario{Offline: true, Args: []string{"rm", "-h"}}},
		{name: "find migration", scenario: Scenario{Offline: true, Args: []string{"search", "react", "--owner", "example"}}},
		{name: "find help migration", scenario: Scenario{Offline: true, Args: []string{"f", "--help"}}},
		{name: "init", scenario: Scenario{Offline: true, Args: []string{"init", "demo"}}},
		{name: "agent init", scenario: Scenario{Offline: true, Args: []string{"init", "demo"}, Env: map[string]string{"AI_AGENT": "test"}}},
		{name: "empty-name init", scenario: Scenario{Offline: true, Args: []string{"init", ""}, Env: map[string]string{"AI_AGENT": "test"}}},
		{name: "existing init", scenario: Scenario{
			Offline: true,
			Args:    []string{"init", "demo"},
			Env:     map[string]string{"AI_AGENT": "test"},
			Files:   []FileFixture{{Root: ProjectRoot, Path: "demo/SKILL.md", Data: []byte("keep\n")}},
		}},
	}
	for _, test := range scenarios {
		t.Run(test.name, func(t *testing.T) {
			pair, err := harness.RunBoth(context.Background(), test.scenario)
			if err != nil {
				t.Fatal(err)
			}
			if differences := CompareObservations(pair.Oracle, pair.Native, Normalization{}); len(differences) != 0 {
				t.Fatalf("native shell differs from npm 0.1.2: %#v", differences)
			}
		})
	}
}

func buildShellTarget(t *testing.T) Target {
	t.Helper()
	target, err := BuildNative(context.Background(), testModuleRoot(t), t.TempDir()+"/native")
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func assertOfflineShellObservation(t *testing.T, observation Observation) {
	t.Helper()
	if len(observation.HTTPRequests) != 0 {
		t.Fatalf("offline shell made HTTP requests: %#v", observation.HTTPRequests)
	}
	if len(observation.SpawnedCommands) != 0 {
		t.Fatalf("offline shell spawned commands: %#v", observation.SpawnedCommands)
	}
}
