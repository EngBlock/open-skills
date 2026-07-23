package compatibility

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeUseLocalSkillWritesOnlySemanticPromptOffline(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"use", "{{temp}}/source", "--skill", "beta"},
		Files: []FileFixture{
			{Root: TempRoot, Path: "source/alpha/SKILL.md", Data: []byte("---\nname: alpha\ndescription: Alpha local skill\n---\n\n# Alpha\n")},
			{Root: TempRoot, Path: "source/beta/SKILL.md", Data: []byte("---\nname: beta\ndescription: Beta local skill\n---\n\n# Beta\n\nUse the helper.\n")},
			{Root: TempRoot, Path: "source/beta/scripts/helper.sh", Data: []byte("echo helper\n")},
			{Root: TempRoot, Path: "source/beta/metadata.json", Data: []byte(`{"ignored":true}`)},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" {
		t.Fatalf("use = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	for _, want := range []string{
		"You are being given a Skill to execute for the user's next request.",
		"Use the following SKILL.md as your instructions:",
		"<SKILL.md>\n---\nname: beta",
		"# Beta\n\nUse the helper.",
		"</SKILL.md>",
		"Supporting files for this skill were downloaded to:",
		"When the SKILL.md references relative paths, read them from that directory.",
	} {
		if !strings.Contains(observation.Stdout, want) {
			t.Errorf("prompt does not contain %q: %q", want, observation.Stdout)
		}
	}
	if strings.Contains(observation.Stdout, "alpha") || strings.Contains(observation.Stdout, "metadata.json") {
		t.Fatalf("prompt leaked unselected source content: %q", observation.Stdout)
	}
	supportDirectory := supportDirectoryFromPrompt(observation.Stdout)
	if supportDirectory == "" {
		t.Fatal("prompt did not expose the support directory")
	}
	assertFile(t, observation, sandboxRelative(observation, filepath.Join(supportDirectory, "scripts", "helper.sh")), FileState{Kind: FileKindRegular, Data: []byte("echo helper\n")})
	if _, found := observation.Files[sandboxRelative(observation, filepath.Join(supportDirectory, "metadata.json"))]; found {
		t.Fatal("metadata.json was copied into the temporary support directory")
	}
	if _, installed := observation.Files[filepath.Join("project", ".agents", "skills", "beta", "SKILL.md")]; installed || len(observation.Locks) != 0 {
		t.Fatalf("use installed or locked the selected skill: files %#v locks %#v", observation.Files, observation.Locks)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeUseLocalSkillWithoutSupportingFilesOmitsTemporaryPath(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"use", "{{temp}}/source"},
		Files:   []FileFixture{{Root: TempRoot, Path: "source/SKILL.md", Data: []byte("---\nname: plain\ndescription: Plain\n---\n\n# Plain\n")}},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || observation.Stderr != "" || !strings.Contains(observation.Stdout, "# Plain") || strings.Contains(observation.Stdout, "Supporting files for this skill") {
		t.Fatalf("plain use = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeUseBlocksOpenClawRemoteSourceBeforeAcquisition(t *testing.T) {
	target := buildShellTarget(t)
	for _, source := range []string{"openclaw/example@demo", "git@github.com:openclaw/repo.git"} {
		observation, err := (Harness{}).Run(context.Background(), target, Scenario{
			Args:    []string{"use", source},
			Offline: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if observation.ExitCode != 1 || observation.Stdout != "" || !strings.Contains(observation.Stderr, "OpenClaw skills are unverified") {
			t.Fatalf("OpenClaw use = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
		}
		assertOfflineShellObservation(t, observation)
	}
}

func TestNativeUseLocalSkillRequiresSelectorForMultipleSkills(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"use", "{{temp}}/source"},
		Files: []FileFixture{
			{Root: TempRoot, Path: "source/one/SKILL.md", Data: []byte("---\nname: one\ndescription: One\n---\n")},
			{Root: TempRoot, Path: "source/two/SKILL.md", Data: []byte("---\nname: two\ndescription: Two\n---\n")},
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || !strings.Contains(observation.Stderr, "This source contains multiple skills") || !strings.Contains(observation.Stderr, "one") || !strings.Contains(observation.Stderr, "two") {
		t.Fatalf("unselected use = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeUseFullDepthContinuesPastRootSkill(t *testing.T) {
	files := []FileFixture{
		{Root: TempRoot, Path: "source/SKILL.md", Data: []byte("---\nname: root\ndescription: Root\n---\n")},
		{Root: TempRoot, Path: "source/catalog/a/b/c/d/deep/SKILL.md", Data: []byte("---\nname: deep\ndescription: Deep\n---\n\n# Deep\n")},
	}
	target := buildShellTarget(t)
	harness := Harness{}
	shallow, err := harness.Run(context.Background(), target, Scenario{Args: []string{"use", "{{temp}}/source", "--skill", "deep"}, Files: files, Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if shallow.ExitCode != 1 || !strings.Contains(shallow.Stderr, "No matching skill found for: deep") {
		t.Fatalf("shallow use = exit %d stdout %q stderr %q", shallow.ExitCode, shallow.Stdout, shallow.Stderr)
	}
	assertOfflineShellObservation(t, shallow)

	fullDepth, err := harness.Run(context.Background(), target, Scenario{Args: []string{"use", "{{temp}}/source", "--skill", "deep", "--full-depth"}, Files: files, Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if fullDepth.ExitCode != 0 || fullDepth.Stderr != "" || !strings.Contains(fullDepth.Stdout, "# Deep") {
		t.Fatalf("full-depth use = exit %d stdout %q stderr %q", fullDepth.ExitCode, fullDepth.Stdout, fullDepth.Stderr)
	}
	assertOfflineShellObservation(t, fullDepth)
}

func TestNativeUseLaunchesOnlyExplicitSupportedAgentAndPropagatesExitCode(t *testing.T) {
	for _, test := range []struct {
		name       string
		agent      string
		command    string
		exitCode   int
		wantOutput string
	}{
		{name: "claude", agent: "claude-code", command: "claude", wantOutput: "claude output\n"},
		{name: "codex failure", agent: "codex", command: "codex", exitCode: 37, wantOutput: "codex output\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
				Args:     []string{"use", "{{temp}}/source", "--agent", test.agent},
				Files:    []FileFixture{{Root: TempRoot, Path: "source/SKILL.md", Data: []byte("---\nname: launch\ndescription: Launch\n---\n\n# Launch\n")}},
				Commands: []CommandFixture{{Name: test.command, Stdout: test.wantOutput, ExitCode: test.exitCode}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != test.exitCode || observation.Stderr != "" || observation.Stdout != test.wantOutput {
				t.Fatalf("agent use = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			if len(observation.SpawnedCommands) != 1 {
				t.Fatalf("spawned commands = %#v", observation.SpawnedCommands)
			}
			spawn := observation.SpawnedCommands[0]
			if spawn.Name != test.command || len(spawn.Args) != 1 || !strings.Contains(spawn.Args[0], "<SKILL.md>") || !strings.Contains(spawn.Args[0], "# Launch") {
				t.Fatalf("agent was not launched with the skill prompt as one argument: %#v", spawn)
			}
		})
	}
}

func TestNativeUseReportsMissingSupportedAgentExecutable(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"use", "{{temp}}/source", "--agent", "claude-code"},
		Files:   []FileFixture{{Root: TempRoot, Path: "source/SKILL.md", Data: []byte("---\nname: launch\ndescription: Launch\n---\n")}},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || observation.Stdout != "" || !strings.Contains(observation.Stderr, "command not found: claude") {
		t.Fatalf("missing agent use = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	assertOfflineShellObservation(t, observation)
}

func TestNativeUseRejectsUnsupportedAgentBeforeLaunching(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:     []string{"use", "{{temp}}/source", "--agent", "cursor"},
		Files:    []FileFixture{{Root: TempRoot, Path: "source/SKILL.md", Data: []byte("---\nname: launch\ndescription: Launch\n---\n")}},
		Commands: []CommandFixture{{Name: "cursor", ExitCode: 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 1 || observation.Stdout != "" || !strings.Contains(observation.Stderr, "Running Cursor is not supported yet") || len(observation.SpawnedCommands) != 0 {
		t.Fatalf("unsupported agent use = exit %d stdout %q stderr %q commands %#v", observation.ExitCode, observation.Stdout, observation.Stderr, observation.SpawnedCommands)
	}
}

func supportDirectoryFromPrompt(prompt string) string {
	const marker = "Supporting files for this skill were downloaded to:\n"
	parts := strings.SplitN(prompt, marker, 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.SplitN(parts[1], "\n", 2)[0]
}

func sandboxRelative(observation Observation, path string) string {
	return strings.TrimPrefix(path, observation.Paths.Root+string(filepath.Separator))
}
