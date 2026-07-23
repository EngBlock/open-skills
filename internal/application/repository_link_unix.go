//go:build !windows

package application

import (
	"io/fs"
	"os"
)

func isRepositoryLink(_ string, mode fs.FileMode) (bool, error) {
	return mode&os.ModeSymlink != 0, nil
}
