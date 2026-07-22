package compatibility

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func snapshotSandbox(root string, excludedTopLevel string) (map[string]FileState, error) {
	result := make(map[string]FileState)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		topLevel := strings.Split(relative, string(filepath.Separator))[0]
		if topLevel == excludedTopLevel || (topLevel != "home" && topLevel != "project" && topLevel != "tmp") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		key := filepath.ToSlash(relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state := FileState{Mode: info.Mode()}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			state.Kind = FileKindSymlink
			state.LinkTarget, err = os.Readlink(path)
		case info.IsDir():
			state.Kind = FileKindDirectory
		case info.Mode().IsRegular():
			state.Kind = FileKindRegular
			state.Data, err = os.ReadFile(path)
		default:
			return nil
		}
		if err != nil {
			return err
		}
		result[key] = state
		return nil
	})
	return result, err
}
