//go:build !windows

package compatibility

import (
	"context"
	"syscall"
	"testing"
)

func TestHarnessUsesDeterministicUmask(t *testing.T) {
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)

	observation, err := (Harness{}).Run(context.Background(), fixtureTarget(), Scenario{Args: []string{"custom-lock"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := observation.Files["home/.local/state/skills"].Mode.Perm(); got != 0o755 {
		t.Fatalf("managed directory permissions = %o, want 755", got)
	}
	if got := observation.Files["home/.local/state/skills/.skill-lock.json"].Mode.Perm(); got != 0o600 {
		t.Fatalf("managed lock permissions = %o, want 600", got)
	}
}
