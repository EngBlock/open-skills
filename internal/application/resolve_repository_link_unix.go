//go:build !windows

package application

import "path/filepath"

func resolveRepositoryLink(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
