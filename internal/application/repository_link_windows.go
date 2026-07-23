package application

import (
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

func isRepositoryLink(path string, mode fs.FileMode) (bool, error) {
	if mode&os.ModeSymlink != 0 {
		return true, nil
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return false, err
	}
	return attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}
