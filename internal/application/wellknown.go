package application

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	wellKnownIndexFile      = "index.json"
	wellKnownSchemaV2       = "https://schemas.agentskills.io/discovery/0.2.0/schema.json"
	maxWellKnownIndexBytes  = 1 << 20
	wellKnownRequestTimeout = 30 * time.Second
)

var wellKnownSkillName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var wellKnownDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

var wellKnownHTTPClient = &http.Client{
	Timeout: wellKnownRequestTimeout,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type wellKnownSource struct {
	baseURL    *url.URL
	directName string
	directURL  *url.URL
	indexes    []*url.URL
	identity   string
}

type wellKnownIndex struct {
	Schema string                `json:"$schema"`
	Skills []wellKnownIndexEntry `json:"skills"`
}

type wellKnownIndexEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
	Type        string   `json:"type"`
	URL         string   `json:"url"`
	Digest      string   `json:"digest"`
}

type wellKnownFetchedSkill struct {
	Name      string
	Files     map[string][]byte
	SourceURL string
}

// parseWellKnownSource recognizes the retained well-known HTTP contract before
// generic HTTP Git sources are considered. HTTPS is required outside loopback,
// which keeps the public transport safe while allowing hermetic HTTP fixtures.
func parseWellKnownSource(raw string) (wellKnownSource, bool, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return wellKnownSource{}, false, nil
	}
	if parsed.User != nil {
		return wellKnownSource{}, true, errors.New("well-known source URLs must not contain user credentials")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return wellKnownSource{}, true, errors.New("well-known sources must use HTTPS")
	}
	if parsed.Hostname() == "github.com" || parsed.Hostname() == "gitlab.com" || strings.HasSuffix(strings.ToLower(parsed.Path), ".git") {
		return wellKnownSource{}, false, nil
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	source := wellKnownSource{baseURL: cloneURL(parsed), identity: wellKnownIdentity(parsed)}
	if source.identity == "" {
		return wellKnownSource{}, true, errors.New("well-known source URL must include a host")
	}

	if base, kind, name, direct, matched := parseWellKnownPath(parsed); matched {
		source.baseURL = base
		source.directName = name
		source.directURL = direct
		source.indexes = appendWellKnownIndex(source.indexes, wellKnownIndexURL(base, kind))
		for _, fallback := range wellKnownIndexCandidates(base) {
			source.indexes = appendWellKnownIndex(source.indexes, fallback)
		}
		return source, true, nil
	}

	if strings.HasSuffix(parsed.Path, "/"+wellKnownIndexFile) {
		return wellKnownSource{}, false, nil
	}
	source.indexes = wellKnownIndexCandidates(parsed)
	return source, true, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func cloneURL(value *url.URL) *url.URL {
	copy := *value
	return &copy
}

func wellKnownIdentity(value *url.URL) string {
	host := strings.ToLower(value.Hostname())
	return strings.TrimPrefix(host, "www.")
}

// parseWellKnownPath accepts both the current agent-skills namespace and the
// legacy skills namespace, including directory and SKILL.md direct URLs.
func parseWellKnownPath(value *url.URL) (*url.URL, string, string, *url.URL, bool) {
	parts := strings.Split(strings.Trim(value.EscapedPath(), "/"), "/")
	for index := 0; index+2 < len(parts); index++ {
		if parts[index] != ".well-known" || (parts[index+1] != "agent-skills" && parts[index+1] != "skills") {
			continue
		}
		kind := parts[index+1]
		remaining := parts[index+2:]
		base := cloneURL(value)
		base.Path = "/" + strings.Join(parts[:index], "/")
		if base.Path == "/" {
			base.Path = ""
		}
		base.RawPath = ""
		if len(remaining) == 1 && remaining[0] == wellKnownIndexFile {
			return base, kind, "", nil, true
		}
		if len(remaining) == 1 || (len(remaining) == 2 && strings.EqualFold(remaining[1], "SKILL.md")) {
			name, err := url.PathUnescape(remaining[0])
			if err != nil || !validWellKnownSkillName(name) {
				return nil, "", "", nil, false
			}
			direct := cloneURL(value)
			direct.Path = strings.TrimRight(value.Path, "/")
			if len(remaining) == 1 {
				direct.Path += "/SKILL.md"
			}
			direct.RawPath = ""
			return base, kind, name, direct, true
		}
	}
	return nil, "", "", nil, false
}

func wellKnownIndexCandidates(base *url.URL) []*url.URL {
	result := []*url.URL{}
	for _, basePath := range []string{strings.TrimRight(base.Path, "/"), ""} {
		for _, kind := range []string{"agent-skills", "skills"} {
			candidate := cloneURL(base)
			candidate.Path = path.Join(basePath, ".well-known", kind, wellKnownIndexFile)
			result = appendWellKnownIndex(result, candidate)
		}
	}
	return result
}

func appendWellKnownIndex(values []*url.URL, value *url.URL) []*url.URL {
	for _, existing := range values {
		if existing.String() == value.String() {
			return values
		}
	}
	return append(values, value)
}

func wellKnownIndexURL(base *url.URL, kind string) *url.URL {
	result := cloneURL(base)
	result.Path = path.Join(strings.TrimRight(base.Path, "/"), ".well-known", kind, wellKnownIndexFile)
	return result
}

func validWellKnownSkillName(name string) bool {
	return len(name) > 0 && len(name) <= 64 && wellKnownSkillName.MatchString(name)
}

func validWellKnownFilePath(file string) bool {
	if file == "" || strings.Contains(file, "\\") || strings.ContainsRune(file, '\x00') || path.IsAbs(file) {
		return false
	}
	for _, part := range strings.Split(file, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func fetchWellKnownSkills(source wellKnownSource, selectedLimits resourceLimits, selectors []string) ([]wellKnownFetchedSkill, error) {
	limits := acquisitionResourceLimits(selectedLimits)
	// A candidate may be stale, malformed, or unavailable while another
	// namespace remains valid. Preserve the provider's current-to-legacy
	// fallback behavior instead of treating an unusable candidate as terminal.
	for _, indexURL := range source.indexes {
		index, found, err := fetchWellKnownIndex(source, indexURL)
		if err != nil || !found {
			if isResourceLimitError(err) {
				return nil, err
			}
			continue
		}
		if index.Schema == wellKnownSchemaV2 {
			entries, err := validWellKnownV2Entries(index, limits)
			if err != nil {
				if isResourceLimitError(err) {
					return nil, err
				}
				continue
			}
			result, fetchErr := fetchWellKnownEntries(source, indexURL, entries, selectors, limits, fetchWellKnownV2Entry)
			if fetchErr != nil {
				return nil, fetchErr
			}
			if len(result) > 0 {
				return result, nil
			}
			continue
		}
		if index.Schema != "" {
			continue
		}
		entries, err := validWellKnownEntries(index, limits)
		if err != nil {
			if isResourceLimitError(err) {
				return nil, err
			}
			continue
		}
		result, fetchErr := fetchWellKnownEntries(source, indexURL, entries, selectors, limits, fetchWellKnownEntry)
		if fetchErr != nil {
			return nil, fetchErr
		}
		if len(result) > 0 {
			return result, nil
		}
	}

	if source.directName != "" && source.directURL != nil {
		data, err := fetchWellKnownURL(source, source.directURL, limits.MaxFileBytes)
		if err != nil {
			return nil, err
		}
		budget := newResourceBudget(limits)
		if err := budget.addFile("SKILL.md", int64(len(data))); err != nil {
			return nil, err
		}
		return []wellKnownFetchedSkill{{Name: source.directName, Files: map[string][]byte{"SKILL.md": data}, SourceURL: source.directURL.String()}}, nil
	}
	return nil, errors.New("no well-known skills found")
}

func fetchWellKnownIndex(source wellKnownSource, indexURL *url.URL) (wellKnownIndex, bool, error) {
	data, status, err := fetchWellKnownURLStatus(source, indexURL, maxWellKnownIndexBytes)
	if err != nil {
		return wellKnownIndex{}, false, err
	}
	if status == http.StatusNotFound {
		return wellKnownIndex{}, false, nil
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return wellKnownIndex{}, false, fmt.Errorf("fetch well-known index %s: HTTP %d", indexURL, status)
	}
	index := wellKnownIndex{}
	if err := jsonUnmarshalStrict(data, &index); err != nil {
		return wellKnownIndex{}, false, fmt.Errorf("decode well-known index %s: %w", indexURL, err)
	}
	return index, true, nil
}

type fetchWellKnownEntryFunc func(wellKnownSource, *url.URL, wellKnownIndexEntry, resourceLimits) (wellKnownFetchedSkill, error)

func fetchWellKnownEntries(source wellKnownSource, indexURL *url.URL, entries []wellKnownIndexEntry, selectors []string, limits resourceLimits, fetchEntry fetchWellKnownEntryFunc) ([]wellKnownFetchedSkill, error) {
	result := make([]wellKnownFetchedSkill, 0, len(entries))
	budget := newResourceBudget(limits)
	for _, entry := range entries {
		if !wellKnownEntryRequested(source, entry.Name, selectors) {
			continue
		}
		skill, err := fetchEntry(source, indexURL, entry, limits)
		if err != nil {
			if isResourceLimitError(err) {
				return nil, err
			}
			continue
		}
		files := make([]string, 0, len(skill.Files))
		for name := range skill.Files {
			files = append(files, name)
		}
		sort.Strings(files)
		for _, name := range files {
			if err := budget.addFile(path.Join(skill.Name, name), int64(len(skill.Files[name]))); err != nil {
				return nil, err
			}
		}
		result = append(result, skill)
	}
	return result, nil
}

func wellKnownEntryRequested(source wellKnownSource, name string, selectors []string) bool {
	if source.directName != "" {
		return name == source.directName
	}
	if len(selectors) == 0 || contains(selectors, "*") {
		return true
	}
	for _, selector := range selectors {
		if strings.EqualFold(selector, name) {
			return true
		}
	}
	return false
}

func validWellKnownEntries(index wellKnownIndex, limits resourceLimits) ([]wellKnownIndexEntry, error) {
	if len(index.Skills) == 0 {
		return nil, errors.New("skills must contain at least one entry")
	}
	if len(index.Skills) > limits.MaxFiles {
		return nil, resourceError("well-known index exceeds the %d-file limit; raise it with --max-files", limits.MaxFiles)
	}
	seen := map[string]bool{}
	result := make([]wellKnownIndexEntry, 0, len(index.Skills))
	for _, entry := range index.Skills {
		if len(entry.Files) > limits.MaxFiles {
			return nil, resourceError("well-known skill %q exceeds the %d-file limit; raise it with --max-files", entry.Name, limits.MaxFiles)
		}
		if !validWellKnownSkillName(entry.Name) || strings.TrimSpace(entry.Description) == "" || len(entry.Files) == 0 || seen[entry.Name] {
			return nil, errors.New("skill entries require unique valid names, descriptions, and files")
		}
		seen[entry.Name] = true
		hasSkill := false
		fileSeen := map[string]bool{}
		for _, file := range entry.Files {
			normalized := strings.ToLower(file)
			if !validWellKnownFilePath(file) || fileSeen[normalized] {
				return nil, fmt.Errorf("skill %q has an unsafe file path", entry.Name)
			}
			if err := limits.checkDepth(file, pathDirectoryDepth(file)); err != nil {
				return nil, err
			}
			fileSeen[normalized] = true
			if strings.EqualFold(file, "SKILL.md") {
				hasSkill = true
			}
		}
		if !hasSkill {
			return nil, fmt.Errorf("skill %q is missing SKILL.md", entry.Name)
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func validWellKnownV2Entries(index wellKnownIndex, limits resourceLimits) ([]wellKnownIndexEntry, error) {
	if len(index.Skills) == 0 {
		return nil, errors.New("skills must contain at least one entry")
	}
	if len(index.Skills) > limits.MaxFiles {
		return nil, resourceError("well-known index exceeds the %d-file limit; raise it with --max-files", limits.MaxFiles)
	}
	seen := map[string]bool{}
	result := make([]wellKnownIndexEntry, 0, len(index.Skills))
	for _, entry := range index.Skills {
		if !validWellKnownSkillName(entry.Name) || strings.TrimSpace(entry.Description) == "" || (entry.Type != "skill-md" && entry.Type != "archive") || entry.URL == "" || !wellKnownDigest.MatchString(entry.Digest) || seen[entry.Name] {
			return nil, errors.New("v0.2.0 skill entries require unique valid names, descriptions, artifacts, and digests")
		}
		if resolved, err := url.Parse(entry.URL); err != nil || resolved.User != nil {
			return nil, fmt.Errorf("skill %q has an invalid artifact URL", entry.Name)
		}
		seen[entry.Name] = true
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func fetchWellKnownV2Entry(source wellKnownSource, indexURL *url.URL, entry wellKnownIndexEntry, limits resourceLimits) (wellKnownFetchedSkill, error) {
	artifact, err := url.Parse(entry.URL)
	if err != nil {
		return wellKnownFetchedSkill{}, err
	}
	artifact = indexURL.ResolveReference(artifact)
	artifact.RawQuery = ""
	artifact.Fragment = ""
	downloadLimit := limits.MaxTotalBytes
	if entry.Type == "skill-md" {
		downloadLimit = limits.MaxFileBytes
	}
	data, err := fetchWellKnownURL(source, artifact, downloadLimit)
	if err != nil {
		return wellKnownFetchedSkill{}, err
	}
	if fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != entry.Digest {
		return wellKnownFetchedSkill{}, errors.New("well-known artifact digest does not match")
	}
	files := map[string][]byte{"SKILL.md": data}
	if entry.Type == "archive" {
		files, err = extractWellKnownArchive(data, artifact.Path, limits)
		if err != nil {
			return wellKnownFetchedSkill{}, err
		}
	}
	return wellKnownFetchedSkill{Name: entry.Name, Files: files, SourceURL: artifact.String()}, nil
}

func extractWellKnownArchive(data []byte, artifactPath string, limits resourceLimits) (map[string][]byte, error) {
	if strings.HasSuffix(strings.ToLower(artifactPath), ".zip") || (len(data) >= 2 && data[0] == 'P' && data[1] == 'K') {
		return extractWellKnownZIP(data, limits)
	}
	if strings.HasSuffix(strings.ToLower(artifactPath), ".tar.gz") || strings.HasSuffix(strings.ToLower(artifactPath), ".tgz") || (len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b) {
		return extractWellKnownTarGz(data, limits)
	}
	return nil, errors.New("unsupported well-known archive format")
}

func addWellKnownArchiveFile(files map[string][]byte, name string, contents []byte, budget *resourceBudget) error {
	if !validWellKnownFilePath(name) {
		return fmt.Errorf("unsafe archive path %q", name)
	}
	if err := budget.limits.checkDepth(name, pathDirectoryDepth(name)); err != nil {
		return err
	}
	for existing := range files {
		if strings.EqualFold(existing, name) {
			return fmt.Errorf("duplicate archive path %q", name)
		}
	}
	if err := budget.addFile(name, int64(len(contents))); err != nil {
		return err
	}
	if strings.EqualFold(name, "SKILL.md") {
		name = "SKILL.md"
	}
	files[name] = contents
	return nil
}

func extractWellKnownTarGz(data []byte, limits resourceLimits) (map[string][]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	files := map[string][]byte{}
	budget := newResourceBudget(limits)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			directory := strings.TrimSuffix(header.Name, "/")
			if !validWellKnownFilePath(directory) {
				return nil, fmt.Errorf("unsafe archive path %q", header.Name)
			}
			depth := len(strings.Split(directory, "/"))
			if err := limits.checkDepth(directory, depth); err != nil {
				return nil, err
			}
			continue
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > limits.MaxFileBytes {
				return nil, resourceError("file %q exceeds the %d-byte per-file limit (%d bytes); raise it with --max-file-bytes", header.Name, limits.MaxFileBytes, header.Size)
			}
			contents, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
			if err != nil {
				return nil, fmt.Errorf("read archive entry %q: %w", header.Name, err)
			}
			if int64(len(contents)) > header.Size {
				return nil, fmt.Errorf("archive entry %q exceeds declared size", header.Name)
			}
			if err := addWellKnownArchiveFile(files, header.Name, contents, budget); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported archive entry %q", header.Name)
		}
	}
	if _, found := files["SKILL.md"]; !found {
		return nil, errors.New("archive is missing root SKILL.md")
	}
	return files, nil
}

func extractWellKnownZIP(data []byte, limits resourceLimits) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	budget := newResourceBudget(limits)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			directory := strings.TrimSuffix(file.Name, "/")
			if !validWellKnownFilePath(directory) {
				return nil, fmt.Errorf("unsafe archive path %q", file.Name)
			}
			depth := len(strings.Split(directory, "/"))
			if err := limits.checkDepth(directory, depth); err != nil {
				return nil, err
			}
			continue
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsupported archive entry %q", file.Name)
		}
		if file.UncompressedSize64 > uint64(limits.MaxFileBytes) {
			return nil, resourceError("file %q exceeds the %d-byte per-file limit (%d bytes); raise it with --max-file-bytes", file.Name, limits.MaxFileBytes, file.UncompressedSize64)
		}
		contents, err := readWellKnownZIPFile(file, limits)
		if err != nil {
			return nil, err
		}
		if err := addWellKnownArchiveFile(files, file.Name, contents, budget); err != nil {
			return nil, err
		}
	}
	if _, found := files["SKILL.md"]; !found {
		return nil, errors.New("archive is missing root SKILL.md")
	}
	return files, nil
}

func readWellKnownZIPFile(file *zip.File, limits resourceLimits) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	contents, err := io.ReadAll(io.LimitReader(reader, limits.MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read archive entry %q: %w", file.Name, err)
	}
	if int64(len(contents)) > limits.MaxFileBytes {
		return nil, resourceError("file %q exceeds the %d-byte per-file limit; raise it with --max-file-bytes", file.Name, limits.MaxFileBytes)
	}
	return contents, nil
}

func fetchWellKnownEntry(source wellKnownSource, indexURL *url.URL, entry wellKnownIndexEntry, limits resourceLimits) (wellKnownFetchedSkill, error) {
	files := make(map[string][]byte, len(entry.Files))
	budget := newResourceBudget(limits)
	var skillURL string
	for _, file := range entry.Files {
		fileURL := cloneURL(indexURL)
		fileURL.Path = path.Join(strings.TrimSuffix(indexURL.Path, "/"+wellKnownIndexFile), entry.Name, file)
		fileURL.RawPath = ""
		data, err := fetchWellKnownURL(source, fileURL, limits.MaxFileBytes)
		if err != nil {
			return wellKnownFetchedSkill{}, err
		}
		if err := limits.checkDepth(file, pathDirectoryDepth(file)); err != nil {
			return wellKnownFetchedSkill{}, err
		}
		if err := budget.addFile(file, int64(len(data))); err != nil {
			return wellKnownFetchedSkill{}, err
		}
		files[file] = data
		if strings.EqualFold(file, "SKILL.md") {
			skillURL = fileURL.String()
		}
	}
	return wellKnownFetchedSkill{Name: entry.Name, Files: files, SourceURL: skillURL}, nil
}

func fetchWellKnownURL(source wellKnownSource, target *url.URL, limit int64) ([]byte, error) {
	data, status, err := fetchWellKnownURLStatus(source, target, limit)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch well-known skill %s: HTTP %d", target, status)
	}
	return data, nil
}

func fetchWellKnownURLStatus(source wellKnownSource, target *url.URL, limit int64) ([]byte, int, error) {
	if target.Scheme != source.baseURL.Scheme || !strings.EqualFold(target.Host, source.baseURL.Host) {
		return nil, 0, errors.New("well-known provider refuses cross-origin requests")
	}
	request, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	response, err := wellKnownHTTPClient.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("request %s: %w", target, err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
		return nil, response.StatusCode, errors.New("well-known provider refuses redirects")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if int64(len(data)) > limit {
		if strings.HasSuffix(target.Path, "/"+wellKnownIndexFile) {
			return nil, response.StatusCode, fmt.Errorf("well-known index response from %s exceeds the fixed %d-byte limit", target, limit)
		}
		return nil, response.StatusCode, resourceError("response from %s exceeds the %d-byte limit; raise it with --max-file-bytes or --max-total-bytes", target, limit)
	}
	return data, response.StatusCode, nil
}

func jsonUnmarshalStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
