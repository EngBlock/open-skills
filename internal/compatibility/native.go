package compatibility

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// BuildNative stages the sole distributable native executable. Compatibility
// helpers live under internal/ and are intentionally not staged.
func BuildNative(ctx context.Context, repositoryRoot, stage string) (Target, error) {
	if repositoryRoot == "" || stage == "" {
		return Target{}, fmt.Errorf("repository root and stage are required")
	}
	if err := os.Mkdir(stage, 0o755); err != nil {
		return Target{}, fmt.Errorf("create native stage (must not already exist): %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(stage)
		}
	}()
	name := "open-skills"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(stage, name)
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", output, "./cmd/open-skills")
	command.Dir = repositoryRoot
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	if data, err := command.CombinedOutput(); err != nil {
		return Target{}, fmt.Errorf("build native open-skills: %w: %s", err, data)
	}
	complete = true
	return Target{Name: "native", Command: output}, nil
}
