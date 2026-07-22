//go:build windows

package compatibility

import (
	"context"
	"os/exec"
	"time"
)

func runProcess(ctx context.Context, command *exec.Cmd) error {
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = command.Process.Kill()
		return <-done
	}
}
