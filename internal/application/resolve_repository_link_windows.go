package application

import (
	"os"
	"path/filepath"
)

func resolveRepositoryLink(path string) (string, error) {
	// Since Go 1.23, EvalSymlinks intentionally does not traverse Windows
	// junctions because they are mount-point reparse records rather than
	// ModeSymlink entries. Resolve the selected reparse record explicitly, then
	// canonicalize any ordinary links in its target.
	target, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.EvalSymlinks(target)
}
