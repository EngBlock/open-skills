//go:build windows

package compatibility

func lockScenarioUmask() func() {
	return func() {}
}
