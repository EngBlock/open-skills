//go:build windows

package application

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type platformLockToken struct {
	overlapped windows.Overlapped
}

func platformAdvisoryLockRoot() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "open-skills", "locks")
}

func tryPlatformLock(file *os.File, mode advisoryLockMode) (platformLockToken, bool, error) {
	token := platformLockToken{}
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if mode == advisoryLockExclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &token.overlapped)
	if err == nil {
		return token, true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return platformLockToken{}, false, nil
	}
	return platformLockToken{}, false, err
}

func unlockPlatformFile(file *os.File, token platformLockToken) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &token.overlapped)
}
