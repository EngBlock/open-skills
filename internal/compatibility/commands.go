package compatibility

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type commandBehavior struct {
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	ExitCode    int    `json:"exitCode"`
	Passthrough string `json:"passthrough,omitempty"`
}

func installCommandFixtures(ctx context.Context, control string, fixtures []CommandFixture) (string, string, error) {
	bin := filepath.Join(control, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return "", "", err
	}
	helperName := "fixture-command"
	if runtime.GOOS == "windows" {
		helperName += ".exe"
	}
	helper := filepath.Join(control, helperName)
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", helper, "./internal/compatibility/fixturecmd")
	command.Dir = moduleRoot()
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build command fixture helper: %w: %s", err, output)
	}

	behaviors := make(map[string]commandBehavior, len(fixtures))
	for _, fixture := range fixtures {
		if fixture.Name == "" || filepath.Base(fixture.Name) != fixture.Name || strings.ContainsAny(fixture.Name, `/\\`) {
			return "", "", fmt.Errorf("invalid command fixture name %q", fixture.Name)
		}
		behaviorName := fixture.Name
		name := fixture.Name
		if runtime.GOOS == "windows" {
			behaviorName = strings.ToLower(behaviorName)
			name += ".exe"
		}
		destination := filepath.Join(bin, name)
		if err := copyExecutable(helper, destination); err != nil {
			return "", "", err
		}
		passthrough := ""
		if fixture.Passthrough {
			if !strings.EqualFold(fixture.Name, "git") {
				return "", "", fmt.Errorf("passthrough command fixture %q is not allowed", fixture.Name)
			}
			var err error
			passthrough, err = exec.LookPath("git")
			if err != nil {
				return "", "", fmt.Errorf("find Git passthrough fixture: %w", err)
			}
			passthrough, err = filepath.Abs(passthrough)
			if err != nil {
				return "", "", fmt.Errorf("resolve Git passthrough fixture: %w", err)
			}
		}
		behaviors[behaviorName] = commandBehavior{Stdout: fixture.Stdout, Stderr: fixture.Stderr, ExitCode: fixture.ExitCode, Passthrough: passthrough}
	}
	encoded, err := json.Marshal(behaviors)
	if err != nil {
		return "", "", err
	}
	behaviorPath := filepath.Join(control, "command-behaviors.json")
	if err := os.WriteFile(behaviorPath, encoded, 0o600); err != nil {
		return "", "", err
	}
	return bin, behaviorPath, nil
}

func copyExecutable(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0o755)
}
