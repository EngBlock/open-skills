//go:build !windows

package application

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type platformLockToken struct{}

func platformAdvisoryLockRoot() string {
	return filepath.Join("/tmp", fmt.Sprintf("open-skills-locks-%d", os.Geteuid()))
}

func tryPlatformLock(file *os.File, mode advisoryLockMode) (platformLockToken, bool, error) {
	operation := unix.LOCK_SH | unix.LOCK_NB
	if mode == advisoryLockExclusive {
		operation = unix.LOCK_EX | unix.LOCK_NB
	}
	err := unix.Flock(int(file.Fd()), operation)
	if err == nil {
		return platformLockToken{}, true, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return platformLockToken{}, false, nil
	}
	return platformLockToken{}, false, err
}

func unlockPlatformFile(file *os.File, _ platformLockToken) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
