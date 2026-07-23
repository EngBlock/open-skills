package application

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EngBlock/open-skills/internal/state"
)

func TestNormalizedSkillNameCombinesUnicodeCaseAndInstallSanitization(t *testing.T) {
	for _, names := range [][]string{
		{"Case", "case"},
		{"white space", "white/space"},
		{"../safe", "safe"},
		{"café", "cafe\u0301"},
		{"Ｆｕｌｌ", "full"},
		{"STRASSE", "straße"},
	} {
		left, right := normalizedSkillName(names[0]), normalizedSkillName(names[1])
		if left != right {
			t.Errorf("normalizedSkillName(%q) = %q, normalizedSkillName(%q) = %q", names[0], left, names[1], right)
		}
	}
}

func TestParseAddOptionsAcceptsDeterministicResourceOverrides(t *testing.T) {
	source, options, err := parseAddOptions([]string{"owner/repository", "--yes", "--max-file-bytes", "11534336", "--max-total-bytes", "209715200", "--max-files", "6000", "--max-depth", "25"})
	if err != nil {
		t.Fatal(err)
	}
	if source != "owner/repository" || !options.Yes || options.Limits.MaxFileBytes != 11534336 || options.Limits.MaxTotalBytes != 209715200 || options.Limits.MaxFiles != 6000 || options.Limits.MaxDepth != 25 {
		t.Fatalf("parsed add options = source %q options %#v", source, options)
	}
}

func TestSameInstallationSourceUsesSanitizedStableIdentity(t *testing.T) {
	project := t.TempDir()
	local := filepath.Join(project, "source#one")
	tests := []struct {
		name     string
		existing state.LockEntry
		incoming installationProvenance
		want     bool
	}{
		{
			name:     "relative and absolute local aliases",
			existing: state.LockEntry{Source: "source#one", SourceType: "local"},
			incoming: installationProvenance{Identity: local, URL: local, Type: "local"},
			want:     true,
		},
		{
			name:     "local fragment characters remain identity",
			existing: state.LockEntry{Source: local, SourceType: "local"},
			incoming: installationProvenance{Identity: filepath.Join(project, "source#two"), Type: "local"},
			want:     false,
		},
		{
			name:     "source type remains identity",
			existing: state.LockEntry{Source: "owner/repository", SourceType: "github"},
			incoming: installationProvenance{Identity: "owner/repository", Type: "local"},
			want:     false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sameInstallationSource(test.existing, test.incoming, project); got != test.want {
				t.Fatalf("sameInstallationSource = %v; want %v", got, test.want)
			}
		})
	}
}

func TestFullDepthDiscoveryResourceBoundary(t *testing.T) {
	for _, test := range []struct {
		name      string
		depth     int
		wantError bool
	}{
		{"immediately below", 4, false},
		{"exactly at", 5, false},
		{"above", 6, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			parts := []string{root}
			for depth := 0; depth < test.depth; depth++ {
				parts = append(parts, "nested")
			}
			directory := filepath.Join(parts...)
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: depth\ndescription: depth\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			limits := defaultResourceLimits()
			limits.MaxDepth = 5
			skills, err := discoverLocalSkillsWithLimits(root, true, limits)
			if test.wantError {
				if !isResourceLimitError(err) {
					t.Fatalf("discovery error = %v; want resource limit error", err)
				}
				return
			}
			if err != nil || len(skills) != 1 {
				t.Fatalf("discovery = %#v, %v", skills, err)
			}
		})
	}
}

func TestRepositoryRelativePathsRemainRootedAboveASearchSubpath(t *testing.T) {
	repository := t.TempDir()
	directory := filepath.Join(repository, "catalog", "selected")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: selected\ndescription: selected\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := discoverLocalSkills(filepath.Join(repository, "catalog"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := assignRepositoryRelativePaths(skills, repository); err != nil {
		t.Fatal(err)
	}
	selected, err := selectSkillsByPath(skills, []string{"catalog/selected"})
	if err != nil || len(selected) != 1 || selected[0].Path != directory {
		t.Fatalf("repository-rooted selection = %#v, %v", selected, err)
	}
}

func TestCollisionDisplaysQuoteUntrustedControlCharacters(t *testing.T) {
	if got := displaySkillName("safe\nforged"); got != `"safe\nforged"` {
		t.Fatalf("displaySkillName = %q", got)
	}
	if got := displaySkillPath("skills/\x1b[31mforged"); got != `"skills/\x1b[31mforged"` {
		t.Fatalf("displaySkillPath = %q", got)
	}
}

func TestPathsOverlap(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", "topology-skill")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if !pathsOverlap(source, filepath.Join(root, "skills", "topology-skill")) {
		t.Fatal("identical paths do not overlap")
	}
	if !pathsOverlap(filepath.Join(root, "skills"), filepath.Join(root, "skills", "topology-skill")) {
		t.Fatal("ancestor paths do not overlap")
	}
}

func TestRunAddSkipsOverlappingAgentDestination(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(project, "skills", "topology-skill")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: topology-skill\ndescription: test\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runAddLocal(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{"skills", "--agent", "openclaw", "--copy", "--yes"}); code != 0 {
		t.Fatalf("runAddLocal = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
		t.Fatalf("source was removed: %v", err)
	}
}

func TestRunAddLetsInteractiveUserResolveAmbiguousNameByRepositoryPath(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	for _, fixture := range []struct {
		path, name, marker string
	}{
		{"skills/first", "same", "first"},
		{"skills/second", "SAME", "second"},
	} {
		directory := filepath.Join(source, filepath.FromSlash(fixture.path))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		contents := "---\nname: " + fixture.name + "\ndescription: collision\n---\n# " + fixture.marker + "\n"
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runAdd(Invocation{
		Stdin: bytes.NewBufferString("skills/second\n"), Stdout: &stdout, Stderr: &stderr, Interactive: true,
	}, []string{source, "--skill", "same", "--agent", "universal", "--yes"})
	if code != 0 || stderr.String() != "" {
		t.Fatalf("runAdd = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	installed, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "same", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installed), "# second") {
		t.Fatalf("wrong collision candidate installed: %q", installed)
	}
}

func TestRunAddInteractiveReplacementDisplaysSanitizedSourcesAndCanReject(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(t.TempDir(), "replacement")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: provenance-skill\ndescription: replacement\n---\n# replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(project, ".agents", "skills", "provenance-skill")
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	prior := []byte("---\nname: provenance-skill\ndescription: prior\n---\n# prior\n")
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), prior, 0o644); err != nil {
		t.Fatal(err)
	}
	lock := []byte(`{"version":1,"skills":{"provenance-skill":{"source":"old-source\u001b[31m","sourceType":"local","computedHash":"prior"}}}`)
	lockPath := filepath.Join(project, "skills-lock.json")
	if err := os.WriteFile(lockPath, lock, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	var stdout, stderr bytes.Buffer
	code := runAdd(Invocation{Stdin: bytes.NewBufferString("no\n"), Stdout: &stdout, Stderr: &stderr, Interactive: true}, []string{source, "--agent", "universal", "--yes"})
	if code != 1 || !strings.Contains(stderr.String(), "replacement cancelled") {
		t.Fatalf("rejected replacement = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Installed source (local): old-source\n") || !strings.Contains(stdout.String(), "Replacement source (local): "+source+"\n") || !strings.Contains(stdout.String(), "Replace provenance-skill? [y/N]") {
		t.Fatalf("replacement prompt omitted sanitized provenance: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "\x1b") {
		t.Fatalf("replacement prompt contains terminal escapes: %q", stdout.String())
	}
	if installed, err := os.ReadFile(filepath.Join(canonical, "SKILL.md")); err != nil || !bytes.Equal(installed, prior) {
		t.Fatalf("rejected replacement changed prior content: %q, %v", installed, err)
	}
	if installedLock, err := os.ReadFile(lockPath); err != nil || !bytes.Equal(installedLock, lock) {
		t.Fatalf("rejected replacement changed prior lock: %q, %v", installedLock, err)
	}
}

func writeAddOutputSkill(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "---\nname: " + name + "\ndescription: output fixture\n---\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func isolateAddOutputEnvironment(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	for _, name := range []string{"AUTOHAND_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "GROK_HOME", "HERMES_HOME", "VIBE_HOME", "APPDATA", "FLATPAK_XDG_CONFIG_HOME"} {
		t.Setenv(name, "")
	}
	return home
}

func TestRunAddInteractiveOutputReportsActualAgents(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(t.TempDir(), "output-skill")
	writeAddOutputSkill(t, source, "output-skill")
	isolateAddOutputEnvironment(t)
	t.Chdir(project)

	var stdout, stderr bytes.Buffer
	code := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, Interactive: true}, []string{source, "--agent", "claude-code", "pi", "--copy", "--yes"})
	if code != 0 || stderr.String() != "" {
		t.Fatalf("runAdd = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "Installed output-skill\n  Agents: Claude Code, Pi\n"; got != want {
		t.Fatalf("stdout = %q; want %q", got, want)
	}
}

func TestRunAddInteractiveOutputDistinguishesSkippedDetectedAgentsAndCompactsLargeLists(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(t.TempDir(), "find-skills")
	writeAddOutputSkill(t, source, "find-skills")
	home := isolateAddOutputEnvironment(t)
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)

	var stdout, stderr bytes.Buffer
	code := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, Interactive: true}, []string{source, "--yes"})
	if code != 0 || stderr.String() != "" {
		t.Fatalf("runAdd = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	prefix := "Installed find-skills\n  Agents: Amp, Antigravity, Antigravity CLI, Cline, Codex +14 more\n  Skipped: "
	if got := stdout.String(); !strings.HasPrefix(got, prefix) {
		t.Fatalf("stdout = %q; want prefix %q", got, prefix)
	}
	// ZCode has a system-wide detector outside the isolated test home.
	skipped := strings.TrimSuffix(strings.TrimPrefix(stdout.String(), prefix), "\n")
	if skipped != "Pi" && skipped != "Pi, ZCode" {
		t.Fatalf("skipped agents = %q; want Pi with only an optional system ZCode detection", skipped)
	}
}

func TestRunAddInteractiveOutputReportsNoneWhenEverySelectedAgentIsSkipped(t *testing.T) {
	project := t.TempDir()
	source := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		writeAddOutputSkill(t, filepath.Join(source, name), name)
	}
	isolateAddOutputEnvironment(t)
	t.Chdir(project)

	var stdout, stderr bytes.Buffer
	code := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, Interactive: true}, []string{source, "--agent", "pi", "--yes"})
	if code != 0 || stderr.String() != "" {
		t.Fatalf("runAdd = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	want := "Installed alpha\n  Agents: none\n  Skipped: Pi\n" +
		"Installed beta\n  Agents: none\n  Skipped: Pi\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q; want %q", got, want)
	}
}

func TestRunAddNonInteractiveOutputRemainsOneLine(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(t.TempDir(), "legacy-output")
	writeAddOutputSkill(t, source, "legacy-output")
	isolateAddOutputEnvironment(t)
	t.Chdir(project)

	var stdout, stderr bytes.Buffer
	code := runAdd(Invocation{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr}, []string{source, "--agent", "universal", "--yes"})
	if code != 0 || stderr.String() != "" {
		t.Fatalf("runAdd = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if got, want := stdout.String(), "Installed legacy-output\n"; got != want {
		t.Fatalf("stdout = %q; want %q", got, want)
	}
}

func TestRunAddJSONOutputRemainsUnchanged(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(t.TempDir(), "json-output")
	writeAddOutputSkill(t, source, "json-output")
	isolateAddOutputEnvironment(t)
	t.Chdir(project)

	var stdout, stderr bytes.Buffer
	code := Run(nil, Invocation{Args: []string{"add", source, "--agent", "universal", "--json"}, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, Interactive: true})
	if code != 0 || stderr.String() != "" {
		t.Fatalf("Run = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	got := strings.ReplaceAll(stdout.String(), project, "{{project}}")
	got = strings.ReplaceAll(got, source, "{{source}}")
	want := "{\n" +
		"  \"schemaVersion\": 1,\n" +
		"  \"scope\": \"project\",\n" +
		"  \"installed\": [\n" +
		"    {\n" +
		"      \"name\": \"json-output\",\n" +
		"      \"path\": \"{{project}}/.agents/skills/json-output\",\n" +
		"      \"agents\": [\n" +
		"        \"universal\"\n" +
		"      ],\n" +
		"      \"source\": \"{{source}}\",\n" +
		"      \"sourceType\": \"local\",\n" +
		"      \"revision\": null\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"
	if got != want {
		t.Fatalf("stdout = %q; want %q", got, want)
	}
}

func TestInstallLocalSkillSkipsOverlappingAgentDestination(t *testing.T) {
	project := t.TempDir()
	source := filepath.Join(project, "skills", "topology-skill")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: topology-skill\ndescription: test\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installLocalSkill(localSkill{Name: "topology-skill", Path: source}, installationProvenance{Identity: filepath.Join(project, "skills"), URL: filepath.Join(project, "skills"), Type: "local"}, state.Project, project, project, project, []string{"openclaw"}, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
		t.Fatalf("source was removed: %v", err)
	}
}
