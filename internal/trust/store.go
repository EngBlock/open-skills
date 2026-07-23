// Package trust owns persistent authorization for injecting remote skill
// instructions into an agent. Approvals are scoped to one sanitized source
// identity and one immutable commit.
package trust

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
)

const storeVersion = 1

// Approval is the complete persisted trust record. It intentionally contains
// no source URL, credentials, skill content, or agent details.
type Approval struct {
	Source     string `json:"source"`
	Commit     string `json:"commit"`
	ApprovedAt string `json:"approvedAt"`
}

type document struct {
	Version   int        `json:"version"`
	Approvals []Approval `json:"approvals"`
}

// Store hides the platform configuration path and durable file format from
// command orchestration.
type Store struct {
	path      string
	approvals []Approval
}

// Open reads the Open Skills-owned trust file in the platform user
// configuration directory. A missing file is an empty store.
func Open() (*Store, error) {
	path, err := storePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Store{path: path, approvals: []Approval{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read trust store: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var contents document
	if err := decoder.Decode(&contents); err != nil {
		return nil, fmt.Errorf("decode trust store: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode trust store: unexpected trailing JSON")
	}
	if contents.Version != storeVersion {
		return nil, fmt.Errorf("unsupported trust store version %d", contents.Version)
	}
	seen := make(map[string]bool, len(contents.Approvals))
	for _, approval := range contents.Approvals {
		if !validTrustValue(approval.Source) || !validTrustValue(approval.Commit) {
			return nil, errors.New("decode trust store: approval source and commit must be non-empty and contain no control characters")
		}
		if _, err := time.Parse(time.RFC3339Nano, approval.ApprovedAt); err != nil {
			return nil, fmt.Errorf("decode trust store: invalid approval time: %w", err)
		}
		key := approval.Source + "\x00" + approval.Commit
		if seen[key] {
			return nil, errors.New("decode trust store: duplicate approval")
		}
		seen[key] = true
	}
	return &Store{path: path, approvals: append([]Approval(nil), contents.Approvals...)}, nil
}

// Approvals returns a deterministic copy for human or machine inspection.
func (store *Store) Approvals() []Approval {
	result := make([]Approval, len(store.approvals))
	copy(result, store.approvals)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].Commit < result[j].Commit
	})
	return result
}

// Contains reports whether this exact source commit has been approved.
func (store *Store) Contains(source, commit string) bool {
	for _, approval := range store.approvals {
		if approval.Source == source && approval.Commit == commit {
			return true
		}
	}
	return false
}

// Approve durably records one exact source commit. Existing approvals retain
// their original approval time.
func (store *Store) Approve(source, commit string, approvedAt time.Time) error {
	if !validTrustValue(source) || !validTrustValue(commit) {
		return errors.New("trust approval source and commit must be non-empty and contain no control characters")
	}
	if store.Contains(source, commit) {
		return nil
	}
	store.approvals = append(store.approvals, Approval{
		Source: source, Commit: commit, ApprovedAt: approvedAt.UTC().Format(time.RFC3339Nano),
	})
	return store.write()
}

func validTrustValue(value string) bool {
	return strings.TrimSpace(value) != "" && strings.IndexFunc(value, unicode.IsControl) < 0
}

// Revoke removes either one exact approval or every commit for a source.
func (store *Store) Revoke(source, commit string) (int, error) {
	retained := make([]Approval, 0, len(store.approvals))
	removed := 0
	for _, approval := range store.approvals {
		matches := approval.Source == source && (commit == "" || approval.Commit == commit)
		if matches {
			removed++
			continue
		}
		retained = append(retained, approval)
	}
	if removed == 0 {
		return 0, nil
	}
	store.approvals = retained
	if len(retained) == 0 {
		return removed, store.clearFile()
	}
	return removed, store.write()
}

// ClearAll removes persistent trust without decoding it. Revocation must remain
// possible even when a damaged trust file cannot be inspected.
func ClearAll() error {
	path, err := storePath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Path returns the stable trust store location so command orchestration can
// protect mutations with an advisory lock without locking the replaceable file.
func Path() (string, error) {
	return storePath()
}

func storePath() (string, error) {
	config := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if config == "" {
		var err error
		config, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("determine user configuration directory: %w", err)
		}
	}
	return filepath.Join(config, "open-skills", "trust.json"), nil
}

func (store *Store) clearFile() error {
	err := os.Remove(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (store *Store) write() error {
	contents := document{Version: storeVersion, Approvals: store.Approvals()}
	data, err := json.MarshalIndent(contents, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(store.path), ".trust-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(store.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(name, store.path)
}
