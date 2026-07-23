package application

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skillContent is a confined, dereferenced view of one selected skill. Preparing
// it validates every repository-controlled link before installation mutates a
// destination. Callers then copy and identify exactly the same file set.
type skillContent struct {
	rootIdentity fs.FileInfo
	directories  []string
	files        []skillContentFile
}

type skillContentFile struct {
	relative string
	data     []byte
	mode     fs.FileMode
}

func (content *skillContent) rejectLFSPointers() error {
	for _, file := range content.files {
		if bytes.HasPrefix(file.data, []byte("version https://git-lfs.github.com/spec/v1\n")) {
			return fmt.Errorf("Git LFS pointer content is not allowed: %s", filepath.ToSlash(file.relative))
		}
	}
	return nil
}

func prepareSkillContent(source string) (*skillContent, error) {
	return prepareSkillContentWithBudget(source, newResourceBudget(unlimitedContentResourceLimits()))
}

func prepareSkillContentWithBudget(source string, budget *resourceBudget) (*skillContent, error) {
	absolute, err := filepath.Abs(source)
	if err != nil {
		return nil, fmt.Errorf("resolve selected skill directory: %w", err)
	}
	selectedInfo, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("inspect selected skill directory: %w", err)
	}
	selectedLink, err := isRepositoryLink(absolute, selectedInfo.Mode())
	if err != nil {
		return nil, fmt.Errorf("inspect selected skill directory link: %w", err)
	}
	if selectedLink {
		return nil, fmt.Errorf("selected skill directory is a symbolic link: %s", source)
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve selected skill directory: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect selected skill directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("selected skill path is not a directory: %s", source)
	}
	if !os.SameFile(selectedInfo, info) {
		return nil, fmt.Errorf("selected skill directory changed while resolving: %s", source)
	}
	content := &skillContent{rootIdentity: info}
	if err := content.scanDirectory(root, ".", nil, budget); err != nil {
		return nil, err
	}
	sort.Strings(content.directories)
	sort.Slice(content.files, func(left, right int) bool {
		return content.files[left].relative < content.files[right].relative
	})
	return content, nil
}

func (content *skillContent) scanDirectory(directory, destination string, active []fs.FileInfo, budget *resourceBudget) error {
	depth := 0
	if destination != "." {
		depth = len(strings.Split(filepath.ToSlash(destination), "/"))
	}
	if err := budget.limits.checkDepth(filepath.ToSlash(destination), depth); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return classifySymlinkResolutionError(destination, err)
	}
	resolved = filepath.Clean(resolved)
	if !content.contains(resolved) {
		return fmt.Errorf("repository symlink target escapes selected skill directory at %q", filepath.ToSlash(destination))
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("inspect selected skill directory %q: %w", filepath.ToSlash(destination), err)
	}
	for _, ancestor := range active {
		if os.SameFile(ancestor, resolvedInfo) {
			return fmt.Errorf("cyclic symlink at %q resolves through an ancestor directory", filepath.ToSlash(destination))
		}
	}
	active = append(active, resolvedInfo)

	if destination != "." {
		content.directories = append(content.directories, destination)
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return fmt.Errorf("read selected skill directory %q: %w", filepath.ToSlash(destination), err)
	}
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		sourcePath := filepath.Join(resolved, entry.Name())
		relative := entry.Name()
		if destination != "." {
			relative = filepath.Join(destination, entry.Name())
		}
		link, err := isRepositoryLink(sourcePath, entry.Type())
		if err != nil {
			return fmt.Errorf("inspect repository link %q: %w", filepath.ToSlash(relative), err)
		}
		if link {
			if err := content.scanSymlink(sourcePath, relative, active, budget); err != nil {
				return err
			}
			continue
		}
		if entry.IsDir() {
			if err := content.scanDirectory(sourcePath, relative, active, budget); err != nil {
				return err
			}
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported non-regular source file: %s", sourcePath)
		}
		if err := content.captureFile(sourcePath, relative, budget); err != nil {
			return err
		}
	}
	return nil
}

func (content *skillContent) scanSymlink(linkPath, destination string, active []fs.FileInfo, budget *resourceBudget) error {
	target, err := os.Readlink(linkPath)
	if err != nil {
		return fmt.Errorf("read repository symlink %q: %w", filepath.ToSlash(destination), err)
	}
	if !filepath.IsAbs(target) && hasParentPathComponent(target) {
		return fmt.Errorf("repository symlink %q has parent-directory symlink target %q", filepath.ToSlash(destination), target)
	}
	resolved, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		return classifySymlinkResolutionError(destination, err)
	}
	resolved = filepath.Clean(resolved)
	if !content.contains(resolved) {
		return fmt.Errorf("repository symlink target escapes selected skill directory at %q: %s", filepath.ToSlash(destination), target)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return classifySymlinkResolutionError(destination, err)
	}
	switch {
	case info.IsDir():
		return content.scanDirectory(resolved, destination, active, budget)
	case info.Mode().IsRegular():
		return content.captureFile(linkPath, destination, budget)
	default:
		return fmt.Errorf("repository symlink %q resolves to unsupported non-regular content", filepath.ToSlash(destination))
	}
}

func (content *skillContent) captureFile(source, destination string, budget *resourceBudget) error {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return classifySymlinkResolutionError(destination, err)
	}
	if !content.contains(resolved) {
		return fmt.Errorf("repository symlink target escapes selected skill directory at %q", filepath.ToSlash(destination))
	}
	file, err := os.Open(resolved)
	if err != nil {
		return fmt.Errorf("open confined source file %q: %w", filepath.ToSlash(destination), err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect confined source file %q: %w", filepath.ToSlash(destination), err)
	}
	if !openedInfo.Mode().IsRegular() {
		return fmt.Errorf("confined source path %q is no longer a regular file", filepath.ToSlash(destination))
	}
	if openedInfo.Size() > budget.limits.MaxFileBytes {
		return budget.addFile(filepath.ToSlash(destination), openedInfo.Size())
	}
	remaining := budget.limits.MaxTotalBytes - budget.bytes
	readLimit := budget.limits.MaxFileBytes
	if remaining < readLimit {
		readLimit = remaining
	}
	data, err := io.ReadAll(io.LimitReader(file, readLimit+1))
	if err != nil {
		return fmt.Errorf("read confined source file %q: %w", filepath.ToSlash(destination), err)
	}
	if err := budget.addFile(filepath.ToSlash(destination), int64(len(data))); err != nil {
		return err
	}
	finalResolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("source path changed while reading %q: %w", filepath.ToSlash(destination), err)
	}
	if !content.contains(finalResolved) {
		return fmt.Errorf("source path changed to escape selected skill directory at %q", filepath.ToSlash(destination))
	}
	finalInfo, err := os.Stat(finalResolved)
	if err != nil || !os.SameFile(openedInfo, finalInfo) {
		return fmt.Errorf("source path changed while reading %q", filepath.ToSlash(destination))
	}
	content.files = append(content.files, skillContentFile{relative: destination, data: data, mode: openedInfo.Mode().Perm()})
	return nil
}

func (content *skillContent) replaceDirectory(destination string) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	for _, relative := range content.directories {
		if err := os.MkdirAll(filepath.Join(destination, relative), 0o755); err != nil {
			return err
		}
	}
	for _, file := range content.files {
		target := filepath.Join(destination, file.relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, file.data, file.mode); err != nil {
			return err
		}
	}
	return nil
}

func (content *skillContent) identity() (string, []string, error) {
	files := make([]string, 0, len(content.files))
	contents := make(map[string][]byte, len(content.files))
	for _, file := range content.files {
		relative := filepath.ToSlash(file.relative)
		if containsPathComponent(relative, "node_modules") {
			continue
		}
		files = append(files, relative)
		contents[relative] = file.data
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, file := range files {
		_, _ = hash.Write([]byte(file))
		_, _ = hash.Write(contents[file])
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), files, nil
}

func contentIdentity(directory string) (string, []string, error) {
	content, err := prepareSkillContent(directory)
	if err != nil {
		return "", nil, err
	}
	return content.identity()
}

func classifySymlinkResolutionError(relative string, err error) error {
	path := filepath.ToSlash(relative)
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "too many links") || strings.Contains(message, "symbolic link loop") || strings.Contains(message, "cyclic") {
		return fmt.Errorf("cyclic symlink at %q: %w", path, err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("broken symlink at %q: target does not exist", path)
	}
	return fmt.Errorf("resolve repository symlink %q: %w", path, err)
}

func hasParentPathComponent(target string) bool {
	start := 0
	for index := 0; index <= len(target); index++ {
		if index < len(target) && !os.IsPathSeparator(target[index]) {
			continue
		}
		if target[start:index] == ".." {
			return true
		}
		start = index + 1
	}
	return false
}

func (content *skillContent) contains(candidate string) bool {
	for current := filepath.Clean(candidate); ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err == nil && os.SameFile(content.rootIdentity, info) {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
	}
}

func containsPathComponent(path, component string) bool {
	for _, candidate := range strings.Split(filepath.ToSlash(path), "/") {
		if candidate == component {
			return true
		}
	}
	return false
}
