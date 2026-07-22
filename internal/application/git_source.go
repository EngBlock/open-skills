package application

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type gitSource struct {
	Identity     string
	URL          string
	CloneURL     string
	Type         string
	RequestedRef string
	Subpath      string
	SkillFilter  string
}

type gitWorkspace struct {
	Root       string
	Repository string
	Commit     string
	remove     func() error
}

const (
	maxGitArchiveBytes   = 32 << 20
	maxGitArchiveEntries = 10_000
	maxGitArchiveDepth   = 20
)

var githubShorthand = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*(?:/.*)?$`)
var scpGitURL = regexp.MustCompile(`^[^@/:\s]+@[^:/\s]+:.+$`)
var gitObjectID = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// parseGitSource accepts the Git forms supported by the compatibility
// baseline. It only classifies and normalizes a source; transport policy is
// enforced immediately before Git is invoked.
func parseGitSource(raw string) (gitSource, error) {
	base, fragment, hasFragment := strings.Cut(raw, "#")
	if hasFragment && fragment == "" {
		return gitSource{}, errors.New("Git ref fragment must not be empty")
	}
	ref, filter := "", ""
	if hasFragment {
		ref, filter, _ = strings.Cut(fragment, "@")
		if ref == "" {
			return gitSource{}, errors.New("Git ref fragment must not be empty")
		}
	}
	parseSelector := func(value string) (string, string) {
		if strings.Contains(value, "@") {
			left, right, _ := strings.Cut(value, "@")
			return left, right
		}
		return value, ""
	}
	validateSubpath := func(path string) (string, error) {
		path = strings.Trim(path, "/")
		if path == "" {
			return "", nil
		}
		for _, part := range strings.FieldsFunc(strings.ReplaceAll(path, "\\", "/"), func(r rune) bool { return r == '/' }) {
			if part == ".." {
				return "", errors.New("Git source subpath must not contain '..'")
			}
		}
		return path, nil
	}
	github := func(path string, cloneHost string) (gitSource, error) {
		path, directFilter := parseSelector(strings.Trim(path, "/"))
		parts := strings.Split(path, "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return gitSource{}, errors.New("GitHub source requires owner/repository")
		}
		repository := strings.TrimSuffix(parts[1], ".git")
		subpath := strings.Join(parts[2:], "/")
		if len(parts) > 2 && parts[2] == "tree" {
			if len(parts) < 4 || parts[3] == "" {
				return gitSource{}, errors.New("GitHub tree URL requires a ref")
			}
			ref = parts[3]
			subpath = strings.Join(parts[4:], "/")
		}
		subpath, err := validateSubpath(subpath)
		if err != nil {
			return gitSource{}, err
		}
		identity := parts[0] + "/" + repository
		cloneURL := "https://" + cloneHost + "/" + identity + ".git"
		return gitSource{Identity: identity, URL: cloneURL, CloneURL: cloneURL, Type: "github", RequestedRef: ref, Subpath: subpath, SkillFilter: firstNonempty(filter, directFilter)}, nil
	}

	if strings.HasPrefix(base, "github:") {
		result, err := github(strings.TrimPrefix(base, "github:"), githubHost())
		if strings.TrimSpace(os.Getenv("GH_HOST")) != "" {
			result.Type, result.Identity = "git", result.URL
		}
		return result, err
	}
	if shorthand, _ := parseSelector(base); githubShorthand.MatchString(shorthand) {
		result, err := github(base, githubHost())
		if strings.TrimSpace(os.Getenv("GH_HOST")) != "" {
			result.Type, result.Identity = "git", result.URL
		}
		return result, err
	}

	if parsed, err := url.Parse(base); err == nil && parsed.Scheme != "" {
		if parsed.User != nil {
			if _, present := parsed.User.Password(); present {
				return gitSource{}, errors.New("Git source URLs must not contain passwords")
			}
		}
		host := strings.ToLower(parsed.Hostname())
		path := strings.Trim(parsed.Path, "/")
		if host == "github.com" {
			return github(path, "github.com")
		}
		if configured := strings.ToLower(strings.TrimSpace(os.Getenv("GH_HOST"))); configured != "" && host == configured {
			result, sourceErr := github(path, parsed.Host)
			result.Type, result.Identity = "git", result.URL
			return result, sourceErr
		}
		if strings.HasPrefix(base, "gitlab:") { // url.Parse treats this as an opaque URL.
			return parseGitLab(strings.TrimPrefix(base, "gitlab:"), "gitlab.com", ref, filter, validateSubpath)
		}
		if host == "gitlab.com" || strings.Contains(path, "/-/tree/") {
			return parseGitLab(path, parsed.Host, ref, filter, validateSubpath)
		}
		if parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "ssh" || parsed.Scheme == "git" || parsed.Scheme == "file" {
			safe := *parsed
			safe.User = nil
			safe.Fragment = ""
			if (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User != nil {
				return gitSource{}, errors.New("HTTP Git source URLs must not contain user credentials")
			}
			return gitSource{Identity: safe.String(), URL: safe.String(), CloneURL: base, Type: "git", RequestedRef: ref, SkillFilter: filter}, nil
		}
	}
	if strings.HasPrefix(base, "gitlab:") {
		return parseGitLab(strings.TrimPrefix(base, "gitlab:"), "gitlab.com", ref, filter, validateSubpath)
	}
	if scpGitURL.MatchString(base) {
		identity := base
		if _, remainder, found := strings.Cut(base, "@"); found {
			identity = remainder
		}
		return gitSource{Identity: identity, URL: identity, CloneURL: base, Type: "git", RequestedRef: ref, SkillFilter: filter}, nil
	}
	return gitSource{}, fmt.Errorf("unsupported Git source: %s", raw)
}

func parseGitLab(path, host, ref, filter string, validateSubpath func(string) (string, error)) (gitSource, error) {
	path = strings.Trim(path, "/")
	if before, after, found := strings.Cut(path, "/-/tree/"); found {
		path = before
		parts := strings.Split(after, "/")
		if len(parts) == 0 || parts[0] == "" {
			return gitSource{}, errors.New("GitLab tree URL requires a ref")
		}
		ref = parts[0]
		var err error
		path, err = validateSubpath(strings.Join(parts[1:], "/"))
		if err != nil {
			return gitSource{}, err
		}
		// Keep the repository path separately: path below is now the subpath.
		cloneURL := "https://" + host + "/" + strings.TrimSuffix(before, ".git") + ".git"
		return gitSource{Identity: before, URL: cloneURL, CloneURL: cloneURL, Type: "gitlab", RequestedRef: ref, Subpath: path, SkillFilter: filter}, nil
	}
	if strings.Count(path, "/") < 1 {
		return gitSource{}, errors.New("GitLab source requires group/repository")
	}
	identity := strings.TrimSuffix(path, ".git")
	cloneURL := "https://" + host + "/" + identity + ".git"
	return gitSource{Identity: identity, URL: cloneURL, CloneURL: cloneURL, Type: "gitlab", RequestedRef: ref, SkillFilter: filter}, nil
}

func githubHost() string {
	if host := strings.TrimSpace(os.Getenv("GH_HOST")); host != "" {
		return host
	}
	return "github.com"
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func materializeGitSource(source gitSource) (gitWorkspace, error) {
	cloneURL := source.CloneURL
	if cloneURL == "" {
		cloneURL = source.URL
	}
	if strings.HasPrefix(cloneURL, "ext::") {
		return gitWorkspace{}, errors.New("unsupported Git transport ext::")
	}
	if parsed, err := url.Parse(cloneURL); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "git") {
		return gitWorkspace{}, fmt.Errorf("plaintext %s Git sources require explicit authorization", parsed.Scheme)
	}
	root, err := os.MkdirTemp("", "open-skills-git-")
	if err != nil {
		return gitWorkspace{}, fmt.Errorf("create Git workspace: %w", err)
	}
	workspace := gitWorkspace{Root: filepath.Join(root, "checkout"), Repository: filepath.Join(root, "repository"), remove: func() error { return os.RemoveAll(root) }}
	fail := func(cause error) (gitWorkspace, error) {
		if removeErr := workspace.remove(); removeErr != nil {
			return gitWorkspace{}, fmt.Errorf("%w (remove Git workspace: %v)", cause, removeErr)
		}
		return gitWorkspace{}, cause
	}
	arguments := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "submodule.recurse=false", "-c", "filter.lfs.required=false", "-c", "filter.lfs.clean=", "-c", "filter.lfs.smudge=", "-c", "filter.lfs.process=", "clone", "--no-checkout", "--depth", "1"}
	requestedObjectID := gitObjectID.MatchString(source.RequestedRef)
	if source.RequestedRef != "" && !requestedObjectID {
		arguments = append(arguments, "--branch", source.RequestedRef)
	}
	arguments = append(arguments, cloneURL, workspace.Repository)
	if output, err := runGit(arguments...); err != nil {
		return fail(fmt.Errorf("clone Git source: %w: %s", err, strings.TrimSpace(string(output))))
	}
	resolvedRef := "HEAD^{commit}"
	if requestedObjectID {
		if output, err := runGit("-C", workspace.Repository, "fetch", "--depth", "1", "origin", source.RequestedRef); err != nil {
			return fail(fmt.Errorf("fetch requested Git commit: %w: %s", err, strings.TrimSpace(string(output))))
		}
		resolvedRef = "FETCH_HEAD^{commit}"
	}
	output, err := runGit("-C", workspace.Repository, "rev-parse", resolvedRef)
	if err != nil {
		return fail(fmt.Errorf("resolve Git commit: %w: %s", err, strings.TrimSpace(string(output))))
	}
	workspace.Commit = strings.TrimSpace(string(output))
	if output, err = runGitArchive("-C", workspace.Repository, "archive", "--format=tar", workspace.Commit); err != nil {
		return fail(fmt.Errorf("archive resolved Git commit: %w: %s", err, strings.TrimSpace(string(output))))
	}
	if err := extractGitArchive(output, workspace.Root); err != nil {
		return fail(fmt.Errorf("extract resolved Git commit: %w", err))
	}
	return workspace, nil
}

func runGit(arguments ...string) ([]byte, error) {
	context, cancel := context.WithTimeout(context.Background(), gitCloneTimeout())
	defer cancel()
	command := exec.CommandContext(context, "git", arguments...)
	command.Env = gitEnvironment()
	output, err := command.CombinedOutput()
	if context.Err() != nil {
		return output, fmt.Errorf("Git command timed out after %s", gitCloneTimeout())
	}
	return output, err
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	available := buffer.limit - buffer.Len()
	if available <= 0 {
		buffer.exceeded = true
		return len(data), nil
	}
	if len(data) > available {
		_, _ = buffer.Buffer.Write(data[:available])
		buffer.exceeded = true
		return len(data), nil
	}
	return buffer.Buffer.Write(data)
}

func runGitArchive(arguments ...string) ([]byte, error) {
	context, cancel := context.WithTimeout(context.Background(), gitCloneTimeout())
	defer cancel()
	command := exec.CommandContext(context, "git", arguments...)
	command.Env = gitEnvironment()
	var stdout limitedBuffer
	stdout.limit = maxGitArchiveBytes
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if context.Err() != nil {
		return stdout.Bytes(), fmt.Errorf("Git command timed out after %s", gitCloneTimeout())
	}
	if stdout.exceeded {
		return stdout.Bytes(), fmt.Errorf("Git archive exceeds %d byte limit", maxGitArchiveBytes)
	}
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func gitCloneTimeout() time.Duration {
	if milliseconds, err := time.ParseDuration(strings.TrimSpace(os.Getenv("SKILLS_CLONE_TIMEOUT_MS")) + "ms"); err == nil && milliseconds > 0 {
		return milliseconds
	}
	return 5 * time.Minute
}

func gitEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+8)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(name, "GIT_") && name != "GIT_SSH_COMMAND" {
			continue
		}
		environment = append(environment, value)
	}
	environment = append(environment,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=file:https:ssh",
		"GIT_LFS_SKIP_SMUDGE=1",
	)
	return environment
}

func gitTreeHash(workspace gitWorkspace, skillDirectory string) (string, error) {
	relative, err := filepath.Rel(workspace.Root, skillDirectory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("skill directory escapes Git workspace")
	}
	output, err := runGit("-C", workspace.Repository, "rev-parse", workspace.Commit+":"+filepath.ToSlash(relative))
	if err != nil {
		return "", fmt.Errorf("resolve Git skill tree: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func extractGitArchive(data []byte, destination string) error {
	reader := tar.NewReader(bytes.NewReader(data))
	entries := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		entries++
		if entries > maxGitArchiveEntries {
			return fmt.Errorf("Git archive exceeds %d entry limit", maxGitArchiveEntries)
		}
		if header.Size < 0 || header.Size > maxGitArchiveBytes {
			return fmt.Errorf("Git archive entry %q exceeds byte limit", header.Name)
		}
		if header.Name == "" || filepath.IsAbs(header.Name) {
			return fmt.Errorf("invalid archive path %q", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(header.Name))
		relative, err := filepath.Rel(destination, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive path escapes checkout: %q", header.Name)
		}
		if len(strings.Split(filepath.ToSlash(relative), "/")) > maxGitArchiveDepth {
			return fmt.Errorf("Git archive entry %q exceeds depth limit", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported archive entry %q", header.Name)
		}
	}
}

func rejectLFSPointers(directory string) error {
	return filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !entry.Type().IsRegular() {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 128))
		if err != nil {
			return err
		}
		if strings.HasPrefix(string(data), "version https://git-lfs.github.com/spec/v1\n") {
			return fmt.Errorf("Git LFS pointer content is not allowed: %s", path)
		}
		return nil
	})
}
