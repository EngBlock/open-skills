//go:build !windows

package compatibility

import (
	"sync"
	"syscall"
)

var scenarioUmask sync.Mutex

// lockScenarioUmask makes captured permission effects independent of the
// developer or CI process umask. Umask is process-global, so harness runs are
// serialized until the original value is restored.
func lockScenarioUmask() func() {
	scenarioUmask.Lock()
	previous := syscall.Umask(0o022)
	return func() {
		syscall.Umask(previous)
		scenarioUmask.Unlock()
	}
}
