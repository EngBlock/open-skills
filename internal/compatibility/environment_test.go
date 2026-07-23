package compatibility

import (
	"context"
	"strings"
	"testing"
)

func TestD12LegacyInternalSkillsSettingRemainsSupportedWithMigrationWarning(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args:    []string{"add", "{{project}}/catalog", "--list"},
		Env:     map[string]string{"INSTALL_INTERNAL_SKILLS": "1"},
		Files:   environmentSkillFixtures(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || !strings.Contains(observation.Stdout, "internal-skill") {
		t.Fatalf("legacy internal-skills setting = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	if !strings.Contains(observation.Stderr, "INSTALL_INTERNAL_SKILLS is deprecated") || !strings.Contains(observation.Stderr, "OPEN_SKILLS_INSTALL_INTERNAL_SKILLS") {
		t.Fatalf("legacy internal-skills warning = %q", observation.Stderr)
	}
}

func TestD12CanonicalInternalSkillsSettingControlsDiscoveryWithoutWarning(t *testing.T) {
	target := buildShellTarget(t)
	for _, test := range []struct {
		name         string
		environment  map[string]string
		wantInternal bool
	}{
		{name: "default hides internal skills", environment: map[string]string{}, wantInternal: false},
		{name: "canonical setting includes internal skills", environment: map[string]string{"OPEN_SKILLS_INSTALL_INTERNAL_SKILLS": "true"}, wantInternal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation, err := (Harness{}).Run(context.Background(), target, Scenario{
				Args: []string{"add", "{{project}}/catalog", "--list"},
				Env:  test.environment, Files: environmentSkillFixtures(), Offline: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != 0 || !strings.Contains(observation.Stdout, "public-skill") || strings.Contains(observation.Stdout, "internal-skill") != test.wantInternal {
				t.Fatalf("internal discovery = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			if strings.Contains(observation.Stderr, "deprecated") {
				t.Fatalf("canonical internal setting emitted migration warning: %q", observation.Stderr)
			}
		})
	}
}

func TestD12CanonicalInternalSkillsSettingWinsLegacyConflict(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{project}}/catalog", "--list"},
		Env: map[string]string{
			"OPEN_SKILLS_INSTALL_INTERNAL_SKILLS": "0",
			"INSTALL_INTERNAL_SKILLS":             "1",
		},
		Files:   environmentSkillFixtures(),
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || !strings.Contains(observation.Stdout, "public-skill") || strings.Contains(observation.Stdout, "internal-skill") {
		t.Fatalf("conflicting internal-skills settings = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	if !strings.Contains(observation.Stderr, "INSTALL_INTERNAL_SKILLS is deprecated and ignored because OPEN_SKILLS_INSTALL_INTERNAL_SKILLS is set") {
		t.Fatalf("legacy conflict warning = %q", observation.Stderr)
	}
}

func TestD12NamespacedControlSuppressesOnlyLegacyEnvironmentWarnings(t *testing.T) {
	observation, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"add", "{{project}}/catalog", "--list"},
		Env: map[string]string{
			"INSTALL_INTERNAL_SKILLS":                  "1",
			"OPEN_SKILLS_SUPPRESS_LEGACY_ENV_WARNINGS": "true",
		},
		Files: environmentSkillFixtures(), Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExitCode != 0 || !strings.Contains(observation.Stdout, "internal-skill") {
		t.Fatalf("suppressed legacy setting = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
	}
	if observation.Stderr != "" {
		t.Fatalf("suppressed legacy warning = %q", observation.Stderr)
	}

	failure, err := (Harness{}).Run(context.Background(), buildShellTarget(t), Scenario{
		Args: []string{"list"},
		Env: map[string]string{
			"INSTALL_INTERNAL_SKILLS":                  "1",
			"OPEN_SKILLS_SUPPRESS_LEGACY_ENV_WARNINGS": "1",
			"OPEN_SKILLS_LOCK_TIMEOUT_MS":              "invalid",
		},
		Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if failure.ExitCode != 1 || !strings.Contains(failure.Stderr, "OPEN_SKILLS_LOCK_TIMEOUT_MS must be a non-negative decimal number") || strings.Contains(failure.Stderr, "deprecated") {
		t.Fatalf("suppression hid non-migration diagnostic: exit %d stderr %q", failure.ExitCode, failure.Stderr)
	}
}

func TestD12CloneTimeoutLegacyWarningAndCanonicalPrecedence(t *testing.T) {
	target := buildShellTarget(t)
	for _, test := range []struct {
		name        string
		environment map[string]string
		wantWarning string
	}{
		{
			name:        "legacy fallback warns",
			environment: map[string]string{"SKILLS_CLONE_TIMEOUT_MS": "1000"},
			wantWarning: "SKILLS_CLONE_TIMEOUT_MS is deprecated and will be removed in open-skills 2.0; use OPEN_SKILLS_CLONE_TIMEOUT_MS instead",
		},
		{
			name: "canonical conflict wins and warns about ignored legacy value",
			environment: map[string]string{
				"OPEN_SKILLS_CLONE_TIMEOUT_MS": "2000",
				"SKILLS_CLONE_TIMEOUT_MS":      "1000",
			},
			wantWarning: "SKILLS_CLONE_TIMEOUT_MS is deprecated and ignored because OPEN_SKILLS_CLONE_TIMEOUT_MS is set",
		},
		{
			name:        "canonical setting needs no migration warning",
			environment: map[string]string{"OPEN_SKILLS_CLONE_TIMEOUT_MS": "2000"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation, err := (Harness{}).Run(context.Background(), target, Scenario{Args: []string{"--version"}, Env: test.environment, Offline: true})
			if err != nil {
				t.Fatal(err)
			}
			if observation.ExitCode != 0 || strings.TrimSpace(observation.Stdout) == "" {
				t.Fatalf("version with environment = exit %d stdout %q stderr %q", observation.ExitCode, observation.Stdout, observation.Stderr)
			}
			if test.wantWarning == "" && observation.Stderr != "" {
				t.Fatalf("unexpected canonical warning = %q", observation.Stderr)
			}
			if test.wantWarning != "" && !strings.Contains(observation.Stderr, test.wantWarning) {
				t.Fatalf("migration warning = %q; want %q", observation.Stderr, test.wantWarning)
			}
		})
	}
}

func environmentSkillFixtures() []FileFixture {
	return []FileFixture{
		{Root: ProjectRoot, Path: "catalog/public/SKILL.md", Data: []byte("---\nname: public-skill\ndescription: public fixture\n---\n")},
		{Root: ProjectRoot, Path: "catalog/internal/SKILL.md", Data: []byte("---\nname: internal-skill\ndescription: internal fixture\nmetadata:\n  internal: true\n---\n")},
	}
}
