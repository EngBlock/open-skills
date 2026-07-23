package application

import (
	"errors"
	"os"
)

func syncRegularFile(path string) (resultErr error) {
	// FlushFileBuffers requires a write-capable Windows handle. os.Open uses a
	// read-only handle, which makes Sync fail with ERROR_ACCESS_DENIED.
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode&0o200 == 0 {
		if err := os.Chmod(path, mode|0o200); err != nil {
			return err
		}
		defer func() {
			resultErr = errors.Join(resultErr, os.Chmod(path, mode))
		}()
	}

	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = file.Sync()
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
