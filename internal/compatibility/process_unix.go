//go:build !windows

package compatibility

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

func runProcess(ctx context.Context, command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		return err
	}
	pid := command.Process.Pid
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		// Tear down descendants even when the direct process exits normally.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		return err
	case <-ctx.Done():
		// Kill the whole process group so a timed-out oracle cannot leave a child
		// holding stdout/stderr pipes or mutating the sandbox.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		return <-done
	}
}
