package application

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// remoteContentRevision identifies an exact non-Git remote skill tree. Length
// framing prevents different path/content boundaries from hashing to the same
// byte stream.
func remoteContentRevision(directory string) (string, error) {
	files := []string{}
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported remote skill file: %s", path)
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	hash := sha256.New()
	_, _ = hash.Write([]byte("open-skills-remote-content-v1\x00"))
	for _, relative := range files {
		contents, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(relative)))
		if err != nil {
			return "", err
		}
		if err := binary.Write(hash, binary.BigEndian, uint64(len(relative))); err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte(relative))
		if err := binary.Write(hash, binary.BigEndian, uint64(len(contents))); err != nil {
			return "", err
		}
		_, _ = hash.Write(contents)
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}
