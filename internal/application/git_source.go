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
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
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
	Root          string
	Repository    string
	Commit        string
	FallbackLinks []archiveSymlink
	remove        func() error
}

type gitAcquisitionPolicy struct {
	AllowInsecureTransport bool
	Notice                 io.Writer
}

type archiveSymlink struct {
	path   string
	target string
}

const (
	gitArchiveFramingAllowance    = 1 << 20
	maxGitArchiveFramingAllowance = 256 << 20
	maxGitDiagnosticBytes         = 64 << 10
)

var githubShorthand = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*(?:/.*)?$`)
var scpGitURL = regexp.MustCompile(`^[^@/:\s]+@[^:/\s]+:.+$`)
var gitObjectID = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
var resolvedGitCommit = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

func validateGitRef(ref string) error {
	if ref == "" || gitObjectID.MatchString(ref) {
		return nil
	}
	if ref == "@" || strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, ".") || strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, "/") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.Contains(ref, "//") || strings.IndexFunc(ref, unicode.IsSpace) >= 0 || strings.ContainsAny(ref, `~^:?*[\\`) {
		return errors.New("Git ref is invalid or could be interpreted as subprocess syntax")
	}
	for _, component := range strings.Split(ref, "/") {
		if component == "" || component == "." || component == ".." || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return errors.New("Git ref is invalid or could be interpreted as subprocess syntax")
		}
	}
	return nil
}

// parseGitSource accepts the Git forms supported by the compatibility
// baseline. It only classifies and normalizes a source; transport policy is
// enforced immediately before Git is invoked.
func parseGitSource(raw string) (gitSource, error) {
	if strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return gitSource{}, errors.New("Git source must not contain control characters")
	}
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
		if strings.IndexFunc(parsed.Path, unicode.IsControl) >= 0 {
			return gitSource{}, errors.New("Git source path must not contain control characters")
		}
		if parsed.User != nil {
			if parsed.Scheme == "http" || parsed.Scheme == "https" {
				return gitSource{}, errors.New("HTTP Git source URLs must not contain user credentials")
			}
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
			safe.RawQuery = ""
			safe.ForceQuery = false
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
	return gitSource{}, errors.New("unsupported Git source")
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

func credentialFreeSource(raw string) string {
	base, _, _ := strings.Cut(raw, "#")
	if parsed, err := url.Parse(base); err == nil && parsed.Scheme != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		if sanitized := sanitizeHuman(parsed.String()); sanitized != "" {
			return sanitized
		}
		return "[redacted source]"
	}

	// url.Parse rejects malformed percent escapes before it can expose the
	// authority. Legacy locks can still contain such values, so redact query,
	// fragment, and authority user-info from the raw form rather than returning
	// it unchanged. Keep a useful host/path only when an authority is present.
	base, _, _ = strings.Cut(base, "?")
	if schemeEnd := strings.Index(base, "://"); schemeEnd >= 0 {
		prefix := base[:schemeEnd+3]
		remainder := base[schemeEnd+3:]
		authority, path, hasPath := strings.Cut(remainder, "/")
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			authority = authority[at+1:]
		}
		if authority == "" {
			return "[redacted source]"
		}
		candidate := prefix + authority
		if hasPath {
			candidate += "/" + path
		}
		if sanitized := sanitizeHuman(candidate); sanitized != "" {
			return sanitized
		}
		return "[redacted source]"
	}
	if scpGitURL.MatchString(base) {
		if _, remainder, found := strings.Cut(base, "@"); found {
			base = remainder
		}
	}
	if sanitized := sanitizeHuman(base); sanitized != "" {
		return sanitized
	}
	return "[redacted source]"
}

func materializeGitSource(source gitSource, selectedLimits resourceLimits) (gitWorkspace, error) {
	return materializeGitSourceWithPolicy(source, selectedLimits, gitAcquisitionPolicy{})
}

func materializeGitSourceWithPolicy(source gitSource, selectedLimits resourceLimits, policy gitAcquisitionPolicy) (gitWorkspace, error) {
	limits := acquisitionResourceLimits(selectedLimits)
	cloneURL := source.CloneURL
	if cloneURL == "" {
		cloneURL = source.URL
	}
	if err := validateGitRef(source.RequestedRef); err != nil {
		return gitWorkspace{}, err
	}
	transport := ""
	if scpGitURL.MatchString(cloneURL) {
		transport = "ssh"
	} else if parsed, parseErr := url.Parse(cloneURL); parseErr == nil {
		transport = parsed.Scheme
	}
	if strings.HasPrefix(cloneURL, "ext::") || (transport != "file" && transport != "https" && transport != "ssh" && transport != "http" && transport != "git") {
		return gitWorkspace{}, errors.New("unsupported or command-capable Git transport")
	}
	insecure := transport == "http" || transport == "git"
	if insecure && !policy.AllowInsecureTransport {
		return gitWorkspace{}, fmt.Errorf("plaintext %s Git sources require --allow-insecure-transport", transport)
	}
	if insecure && policy.Notice != nil {
		_, _ = fmt.Fprintf(policy.Notice, "Warning: allowing insecure %s Git transport for %s; source contents may be intercepted or modified.\n", transport, credentialFreeSource(source.Identity))
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
	arguments := hardenedCloneArguments(source.RequestedRef, cloneURL, workspace.Repository)
	output, cloneErr := runGitWithPolicy(policy.AllowInsecureTransport, arguments...)
	if cloneErr != nil && gitAuthenticationFailure(output) && isGitHubHTTPSClone(source) {
		if policy.Notice != nil {
			_, _ = fmt.Fprintln(policy.Notice, "Git authentication failed; invoking gh as an optional GitHub authentication fallback.")
		}
		_ = os.RemoveAll(workspace.Repository)
		cloneErr = runGHClone(source, workspace.Repository, policy.AllowInsecureTransport)
		if cloneErr != nil {
			return fail(errors.New("clone Git source: authentication failed after Git credentials and the optional gh fallback"))
		}
	}
	if cloneErr != nil {
		return fail(fmt.Errorf("clone Git source: %w", cloneErr))
	}
	requestedObjectID := gitObjectID.MatchString(source.RequestedRef)
	resolvedRef := "HEAD^{commit}"
	if requestedObjectID {
		if _, err := runGitWithPolicy(policy.AllowInsecureTransport, "-C", workspace.Repository, "fetch", "--depth", "1", "origin", source.RequestedRef); err != nil {
			return fail(fmt.Errorf("fetch requested Git commit: %w", err))
		}
		resolvedRef = "FETCH_HEAD^{commit}"
	}
	output, err = runGitWithPolicy(policy.AllowInsecureTransport, "-C", workspace.Repository, "rev-parse", resolvedRef)
	if err != nil {
		return fail(fmt.Errorf("resolve Git commit: %w", err))
	}
	workspace.Commit = strings.TrimSpace(string(output))
	if !resolvedGitCommit.MatchString(workspace.Commit) {
		return fail(errors.New("resolve Git commit: Git returned an invalid object ID"))
	}
	if output, err = runGitArchiveWithPolicy(limits, policy.AllowInsecureTransport, "-C", workspace.Repository, "archive", "--format=tar", workspace.Commit); err != nil {
		return fail(fmt.Errorf("archive resolved Git commit: %w", err))
	}
	if err := extractGitArchiveWithLimits(output, workspace.Root, limits, &workspace.FallbackLinks); err != nil {
		return fail(fmt.Errorf("extract resolved Git commit: %w", err))
	}
	return workspace, nil
}

func hardenedCloneArguments(ref, cloneURL, repository string) []string {
	arguments := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "submodule.recurse=false", "-c", "filter.lfs.required=false", "-c", "filter.lfs.clean=", "-c", "filter.lfs.smudge=", "-c", "filter.lfs.process=", "clone", "--no-checkout", "--depth", "1"}
	if ref != "" && !gitObjectID.MatchString(ref) {
		arguments = append(arguments, "--branch", ref)
	}
	return append(arguments, "--", cloneURL, repository)
}

func gitAuthenticationFailure(output []byte) bool {
	message := strings.ToLower(string(output))
	for _, marker := range []string{"authentication failed", "could not read username", "permission denied", "repository not found", "returned error: 401", "returned error: 403", "saml sso"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isGitHubHTTPSClone(source gitSource) bool {
	cloneURL := firstNonempty(source.CloneURL, source.URL)
	parsed, err := url.Parse(cloneURL)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "github.com" || host == strings.ToLower(strings.TrimSpace(os.Getenv("GH_HOST")))
}

func githubCloneTarget(source gitSource) string {
	parsed, err := url.Parse(firstNonempty(source.CloneURL, source.URL))
	if err != nil {
		return source.Identity
	}
	path := strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git")
	if strings.EqualFold(parsed.Hostname(), "github.com") {
		return path
	}
	return parsed.Hostname() + "/" + path
}

func runGHClone(source gitSource, repository string, allowInsecure bool) error {
	context, cancel := context.WithTimeout(context.Background(), gitCloneTimeout())
	defer cancel()
	arguments := []string{"repo", "clone", githubCloneTarget(source), repository, "--", "--no-checkout", "--depth", "1", "--config", "core.hooksPath=" + os.DevNull, "--config", "submodule.recurse=false", "--config", "filter.lfs.required=false", "--config", "filter.lfs.clean=", "--config", "filter.lfs.smudge=", "--config", "filter.lfs.process="}
	if source.RequestedRef != "" && !gitObjectID.MatchString(source.RequestedRef) {
		arguments = append(arguments, "--branch", source.RequestedRef)
	}
	command := exec.CommandContext(context, "gh", arguments...)
	command.Env = gitEnvironment(allowInsecure)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	err := command.Run()
	if context.Err() != nil {
		return fmt.Errorf("gh fallback timed out after %s", gitCloneTimeout())
	}
	return err
}

func runGit(arguments ...string) ([]byte, error) {
	return runGitWithPolicy(false, arguments...)
}

func runGitWithPolicy(allowInsecure bool, arguments ...string) ([]byte, error) {
	context, cancel := context.WithTimeout(context.Background(), gitCloneTimeout())
	defer cancel()
	command := exec.CommandContext(context, "git", arguments...)
	command.Env = gitEnvironment(allowInsecure)
	var output limitedBuffer
	output.limit = maxGitDiagnosticBytes
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if context.Err() != nil {
		return output.Bytes(), fmt.Errorf("Git command timed out after %s", gitCloneTimeout())
	}
	return output.Bytes(), err
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

func runGitArchive(limits resourceLimits, arguments ...string) ([]byte, error) {
	return runGitArchiveWithPolicy(limits, false, arguments...)
}

func runGitArchiveWithPolicy(limits resourceLimits, allowInsecure bool, arguments ...string) ([]byte, error) {
	context, cancel := context.WithTimeout(context.Background(), gitCloneTimeout())
	defer cancel()
	command := exec.CommandContext(context, "git", arguments...)
	command.Env = gitEnvironment(allowInsecure)
	var stdout limitedBuffer
	stdout.limit = gitArchiveTransportLimit(limits)
	command.Stdout = &stdout
	command.Stderr = io.Discard
	err := command.Run()
	if context.Err() != nil {
		return stdout.Bytes(), fmt.Errorf("Git command timed out after %s", gitCloneTimeout())
	}
	if stdout.exceeded {
		return stdout.Bytes(), resourceError("Git archive exceeds its bounded transport limit; raise content limits with --max-file-bytes, --max-total-bytes, or --max-files")
	}
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("Git archive command failed: %w", err)
	}
	return stdout.Bytes(), nil
}

func gitArchiveTransportLimit(limits resourceLimits) int {
	maximum := int64(^uint(0) >> 1)
	// Git's uncompressed tar adds headers, padding, and directory entries. Keep
	// that framing allowance finite even when a user deliberately raises depth
	// or file-count limits; extraction enforces the exact content budget.
	overhead := int64(maxGitArchiveFramingAllowance)
	if limits.MaxFiles <= maxGitArchiveFramingAllowance/2048 {
		overhead = int64(limits.MaxFiles) * 2048
	}
	overhead += gitArchiveFramingAllowance
	if limits.MaxTotalBytes > maximum-overhead {
		return int(maximum)
	}
	return int(limits.MaxTotalBytes + overhead)
}

func gitCloneTimeout() time.Duration {
	if milliseconds, err := time.ParseDuration(strings.TrimSpace(os.Getenv("SKILLS_CLONE_TIMEOUT_MS")) + "ms"); err == nil && milliseconds > 0 {
		return milliseconds
	}
	return 5 * time.Minute
}

func gitEnvironment(allowInsecure bool) []string {
	overridden := map[string]bool{
		"GIT_ALLOW_PROTOCOL": true, "GIT_TERMINAL_PROMPT": true, "GIT_LFS_SKIP_SMUDGE": true,
	}
	environment := make([]string, 0, len(os.Environ())+4)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if !overridden[name] {
			environment = append(environment, value)
		}
	}
	protocols := "file:https:ssh"
	if allowInsecure {
		protocols += ":http:git"
	}
	return append(environment,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL="+protocols,
		"GIT_LFS_SKIP_SMUDGE=1",
	)
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

func extractGitArchive(data []byte, destination string, fallbackOutput ...*[]archiveSymlink) error {
	return extractGitArchiveWithLimits(data, destination, defaultResourceLimits(), fallbackOutput...)
}

func extractGitArchiveWithLimits(data []byte, destination string, limits resourceLimits, fallbackOutput ...*[]archiveSymlink) error {
	reader := tar.NewReader(bytes.NewReader(data))
	budget := newResourceBudget(limits)
	links := []archiveSymlink{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if header.Size < 0 {
			return fmt.Errorf("Git archive entry %q has a negative size", header.Name)
		}
		if header.Name == "" || filepath.IsAbs(header.Name) {
			return fmt.Errorf("invalid archive path %q", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(header.Name))
		relative, err := filepath.Rel(destination, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive path escapes checkout: %q", header.Name)
		}
		depth := len(strings.Split(filepath.ToSlash(filepath.Dir(relative)), "/"))
		if filepath.Dir(relative) == "." {
			depth = 0
		}
		if header.Typeflag == tar.TypeDir {
			depth = len(strings.Split(strings.Trim(filepath.ToSlash(relative), "/"), "/"))
		}
		if err := limits.checkDepth(header.Name, depth); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := budget.addFile(header.Name, header.Size); err != nil {
				return err
			}
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
		case tar.TypeSymlink:
			if err := budget.addFile(header.Name, int64(len(header.Linkname))); err != nil {
				return err
			}
			links = append(links, archiveSymlink{path: target, target: header.Linkname})
		default:
			return fmt.Errorf("unsupported archive entry %q", header.Name)
		}
	}
	// Materialize repository links only after every regular archive entry. A
	// repository-controlled link can therefore never redirect extraction writes.
	fallbacks := []archiveSymlink{}
	for _, link := range links {
		if err := rejectArchiveSymlinkParents(destination, filepath.Dir(link.path)); err != nil {
			return err
		}
		if err := os.Symlink(link.target, link.path); err != nil {
			if runtime.GOOS != "windows" {
				return fmt.Errorf("create repository symlink %q: %w", link.path, err)
			}
			fallbacks = append(fallbacks, link)
		}
	}
	if len(fallbackOutput) > 0 {
		*fallbackOutput[0] = fallbacks
		return nil
	}
	return materializeArchiveLinkFallbacks(destination, fallbacks)
}

func rejectArchiveSymlinkParents(root, parent string) error {
	relative, err := filepath.Rel(root, parent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive symlink parent escapes checkout: %s", parent)
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect archive symlink parent %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive symlink parent contains repository symlink: %s", current)
		}
	}
	return nil
}

func materializeGitLinkFallbacksForSkills(workspace *gitWorkspace, skills []localSkill) error {
	if workspace == nil || len(workspace.FallbackLinks) == 0 {
		return nil
	}
	selected := make([]archiveSymlink, 0, len(workspace.FallbackLinks))
	remaining := make([]archiveSymlink, 0, len(workspace.FallbackLinks))
	for _, link := range workspace.FallbackLinks {
		included := false
		for _, skill := range skills {
			if archivePathWithin(skill.Path, link.path) {
				included = true
				break
			}
		}
		if included {
			selected = append(selected, link)
		} else {
			remaining = append(remaining, link)
		}
	}
	if err := materializeArchiveLinkFallbacks(workspace.Root, selected); err != nil {
		return err
	}
	workspace.FallbackLinks = remaining
	return nil
}

func materializeArchiveLinkFallbacks(root string, links []archiveSymlink) error {
	pending := append([]archiveSymlink(nil), links...)
	for len(pending) > 0 {
		progress := false
		next := make([]archiveSymlink, 0, len(pending))
		for _, link := range pending {
			if !filepath.IsAbs(link.target) && hasParentPathComponent(link.target) {
				return fmt.Errorf("repository symlink %q has parent-directory symlink target %q", link.path, link.target)
			}
			target := link.target
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(link.path), target)
			}
			target = filepath.Clean(target)
			if !archivePathWithin(root, target) {
				return fmt.Errorf("repository symlink target escapes checkout at %q: %s", link.path, link.target)
			}
			info, err := os.Lstat(target)
			if errors.Is(err, os.ErrNotExist) {
				next = append(next, link)
				continue
			}
			if err != nil {
				return fmt.Errorf("inspect repository symlink fallback target %q: %w", link.target, err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("repository symlink fallback target %q contains another symbolic link", link.target)
			}
			if info.IsDir() && archiveLinkPendingInside(target, pending) {
				next = append(next, link)
				continue
			}
			switch {
			case info.Mode().IsRegular():
				if err := os.Link(target, link.path); err != nil {
					data, readErr := os.ReadFile(target)
					if readErr != nil {
						return readErr
					}
					if writeErr := os.WriteFile(link.path, data, info.Mode().Perm()); writeErr != nil {
						return writeErr
					}
				}
			case info.IsDir():
				if err := copyArchiveFallbackDirectory(target, link.path); err != nil {
					return err
				}
			default:
				return fmt.Errorf("repository symlink fallback target %q is not regular content", link.target)
			}
			progress = true
		}
		if !progress {
			paths := make([]string, 0, len(next))
			for _, link := range next {
				paths = append(paths, link.path)
			}
			sort.Strings(paths)
			return fmt.Errorf("broken or cyclic repository symlink fallback: %s", strings.Join(paths, ", "))
		}
		pending = next
	}
	return nil
}

func archiveLinkPendingInside(directory string, links []archiveSymlink) bool {
	for _, link := range links {
		if archivePathWithin(directory, link.path) {
			return true
		}
	}
	return false
}

func archivePathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func copyArchiveFallbackDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("repository directory link fallback contains symbolic link: %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("repository directory link fallback contains non-regular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
