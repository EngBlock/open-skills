package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/EngBlock/open-skills/internal/state"
)

const (
	installationJournalName    = "journal.json"
	orderedRenameCommitModel   = "ordered per-destination renames; crash-recoverable, not atomic across filesystems"
	maximumInstallationTargets = 1_000
	maximumInstallationJournal = 1 << 20
)

var (
	installationFault                    func(string) error
	errSimulatedInstallationInterruption = errors.New("simulated installation interruption")
)

type installationJournal struct {
	Version     int                         `json:"version"`
	State       string                      `json:"state"`
	CommitModel string                      `json:"commitModel"`
	LockPath    string                      `json:"lockPath"`
	Current     int                         `json:"current"`
	Targets     []installationJournalTarget `json:"targets"`
}

type installationJournalTarget struct {
	Destination string `json:"destination"`
	Stage       string `json:"stage"`
	Backup      string `json:"backup"`
	Existed     bool   `json:"existed"`
	Staged      bool   `json:"staged"`
	Deleted     bool   `json:"deleted,omitempty"`
	Committed   bool   `json:"committed"`
}

type installationTransaction struct {
	root           string
	journal        installationJournal
	byPath         map[string]int
	stageCount     int
	lockWriteCount int
}

func withInstallationTransaction(lockPath string, destinations []string, operation func(*installationTransaction) error) error {
	if err := recoverInstallationTransactions(lockPath); err != nil {
		return err
	}
	if err := injectInstallationFault("before-staging"); err != nil {
		return err
	}
	transaction, err := prepareInstallationTransaction(lockPath, destinations)
	if err != nil {
		return err
	}

	if err := operation(transaction); err != nil {
		if errors.Is(err, errSimulatedInstallationInterruption) {
			return err
		}
		if cleanupErr := transaction.cleanupUncommitted(); cleanupErr != nil {
			return fmt.Errorf("%w; clean staged installation: %v", err, cleanupErr)
		}
		return err
	}
	for _, target := range transaction.journal.Targets {
		if !target.Staged || target.Deleted {
			continue
		}
		if _, err := os.Lstat(target.Stage); err != nil {
			_ = transaction.cleanupUncommitted()
			return fmt.Errorf("verify staged installation for %s: %w", target.Destination, err)
		}
		if err := syncReplacementPath(target.Stage); err != nil {
			_ = transaction.cleanupUncommitted()
			return fmt.Errorf("persist staged installation for %s: %w", target.Destination, err)
		}
		if err := syncDirectory(filepath.Dir(target.Stage)); err != nil {
			_ = transaction.cleanupUncommitted()
			return fmt.Errorf("persist staged installation directory for %s: %w", target.Destination, err)
		}
	}
	transaction.journal.State = "prepared"
	if err := transaction.writeJournal(); err != nil {
		_ = transaction.cleanupUncommitted()
		return fmt.Errorf("write prepared installation journal: %w", err)
	}

	transaction.journal.State = "committing"
	for index := range transaction.journal.Targets {
		target := &transaction.journal.Targets[index]
		if !target.Staged {
			continue
		}
		if err := injectInstallationFault("commit:" + strconv.Itoa(index)); err != nil {
			if errors.Is(err, errSimulatedInstallationInterruption) {
				return err
			}
			return transaction.rollback(err)
		}
		transaction.journal.Current = index
		if err := transaction.writeJournal(); err != nil {
			// The destination for this step is still untouched, and the durable
			// journal describes every earlier completed step. Exclude the current
			// step from rollback so an interruption during restoration remains
			// consistent with the last durable journal.
			transaction.journal.Current = -1
			return transaction.rollback(fmt.Errorf("journal commit step %d: %w", index, err))
		}
		if err := commitInstallationTarget(*target); err != nil {
			return transaction.rollback(fmt.Errorf("commit installation destination %s: %w", target.Destination, err))
		}
		target.Committed = true
		transaction.journal.Current = -1
		if err := transaction.writeJournal(); err != nil {
			return transaction.rollback(fmt.Errorf("journal completed commit step %d: %w", index, err))
		}
	}
	if err := injectInstallationFault("after-commit"); err != nil {
		if errors.Is(err, errSimulatedInstallationInterruption) {
			return err
		}
		return transaction.rollback(err)
	}
	transaction.journal.State = "committed"
	if err := transaction.writeJournal(); err != nil {
		return transaction.rollback(fmt.Errorf("journal committed installation: %w", err))
	}
	if err := transaction.cleanupCommitted(); err != nil {
		return fmt.Errorf("installation committed but journal cleanup failed at %s: %w", transaction.root, err)
	}
	return nil
}

func prepareInstallationTransaction(lockPath string, destinations []string) (*installationTransaction, error) {
	unique, err := uniqueReplacementPaths(append(destinations, lockPath))
	if err != nil {
		return nil, fmt.Errorf("preflight installation destinations: %w", err)
	}
	if len(unique) > maximumInstallationTargets {
		return nil, fmt.Errorf("preflight installation destinations: %d targets exceed limit %d", len(unique), maximumInstallationTargets)
	}
	canonical := make([]string, 0, len(unique))
	seen := make(map[string]bool, len(unique))
	for _, destination := range unique {
		destination = cleanAbsolutePath(destination)
		if seen[destination] {
			return nil, fmt.Errorf("preflight installation destinations: multiple targets resolve to %s", destination)
		}
		for _, previous := range canonical {
			if installationPathsOverlap(previous, destination) {
				return nil, fmt.Errorf("preflight installation destinations overlap: %s and %s", previous, destination)
			}
		}
		seen[destination] = true
		canonical = append(canonical, destination)
	}
	unique = canonical
	for _, destination := range unique {
		if err := preflightInstallationDestination(destination); err != nil {
			return nil, fmt.Errorf("preflight installation destination %s: %w", destination, err)
		}
	}
	base, err := installationTransactionScope(lockPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, fmt.Errorf("create installation journal directory: %w", err)
	}
	root, err := os.MkdirTemp(base, "transaction-")
	if err != nil {
		return nil, fmt.Errorf("create installation journal: %w", err)
	}
	if err := syncDirectory(base); err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("persist installation journal directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	transaction := &installationTransaction{
		root: root, byPath: make(map[string]int),
		journal: installationJournal{Version: 1, State: "staging", CommitModel: orderedRenameCommitModel, LockPath: canonicalAdvisoryResource(lockPath), Current: -1},
	}
	for index, destination := range unique {
		destination = cleanAbsolutePath(destination)
		target := installationJournalTarget{
			Destination: destination,
			Stage:       filepath.Join(filepath.Dir(destination), fmt.Sprintf(".open-skills-stage-%s-%d", filepath.Base(root), index)),
			Backup:      filepath.Join(root, "backups", strconv.Itoa(index)),
		}
		if _, err := os.Lstat(destination); err == nil {
			target.Existed = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			_ = os.RemoveAll(root)
			return nil, fmt.Errorf("inspect installation destination %s: %w", destination, err)
		}
		transaction.byPath[destination] = index
		transaction.journal.Targets = append(transaction.journal.Targets, target)
	}
	if err := transaction.writeJournal(); err != nil {
		_ = transaction.cleanupUncommitted()
		return nil, fmt.Errorf("write staging installation journal: %w", err)
	}
	for index := range transaction.journal.Targets {
		target := &transaction.journal.Targets[index]
		if !target.Existed {
			continue
		}
		if err := copyReplacementPath(target.Destination, target.Backup); err != nil {
			_ = transaction.cleanupUncommitted()
			return nil, fmt.Errorf("snapshot installation destination %s: %w", target.Destination, err)
		}
		if err := syncReplacementPath(target.Backup); err != nil {
			_ = transaction.cleanupUncommitted()
			return nil, fmt.Errorf("persist installation snapshot %s: %w", target.Destination, err)
		}
	}
	if err := syncDirectory(filepath.Join(root, "backups")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = transaction.cleanupUncommitted()
		return nil, fmt.Errorf("persist installation backup directory: %w", err)
	}
	if err := syncDirectory(root); err != nil {
		_ = transaction.cleanupUncommitted()
		return nil, fmt.Errorf("persist installation backups: %w", err)
	}
	if target, err := transaction.target(lockPath); err == nil && target.Existed {
		if err := os.MkdirAll(filepath.Dir(target.Stage), 0o755); err != nil {
			_ = transaction.cleanupUncommitted()
			return nil, err
		}
		if err := copyReplacementPath(target.Destination, target.Stage); err != nil {
			_ = transaction.cleanupUncommitted()
			return nil, fmt.Errorf("stage existing installation state: %w", err)
		}
	}
	return transaction, nil
}

func installationPathsOverlap(left, right string) bool {
	relative, err := filepath.Rel(left, right)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return true
	}
	relative, err = filepath.Rel(right, left)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func preflightInstallationDestination(destination string) error {
	absolute := cleanAbsolutePath(destination)
	if absolute == filepath.Dir(absolute) {
		return errors.New("filesystem root cannot be an installation destination")
	}
	if _, err := os.Lstat(absolute); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if hasNonDirectoryAncestor(absolute) {
		return errors.New("parent path is not a directory")
	}
	return nil
}

func (transaction *installationTransaction) stageDirectory(content *skillContent, destination string) (string, error) {
	target, err := transaction.target(destination)
	if err != nil {
		return "", err
	}
	if err := transaction.injectStageFault(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target.Stage), 0o755); err != nil {
		return "", err
	}
	if err := os.RemoveAll(target.Stage); err != nil {
		return "", err
	}
	if err := content.replaceDirectory(target.Stage); err != nil {
		return "", err
	}
	target.Deleted = false
	target.Staged = true
	return target.Stage, nil
}

func (transaction *installationTransaction) stageDelete(destination string) error {
	target, err := transaction.target(destination)
	if err != nil {
		return err
	}
	if err := transaction.injectStageFault(); err != nil {
		return err
	}
	if err := os.RemoveAll(target.Stage); err != nil {
		return err
	}
	target.Deleted = true
	target.Staged = true
	return nil
}

func (transaction *installationTransaction) stageSymlink(source, destination string) error {
	target, err := transaction.target(destination)
	if err != nil {
		return err
	}
	if err := transaction.injectStageFault(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target.Stage), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(target.Stage); err != nil {
		return err
	}
	relative, err := filepath.Rel(filepath.Dir(target.Destination), source)
	if err != nil {
		return err
	}
	if err := os.Symlink(relative, target.Stage); err != nil {
		return err
	}
	target.Deleted = false
	target.Staged = true
	return nil
}

func (transaction *installationTransaction) readPath(destination string) string {
	target, err := transaction.target(destination)
	if err == nil && target.Staged {
		return target.Stage
	}
	return destination
}

func (transaction *installationTransaction) readState(path string, version int) (*state.Document, error) {
	target, err := transaction.target(path)
	if err != nil {
		return nil, err
	}
	return state.Read(target.Stage, version)
}

func (transaction *installationTransaction) stagePreserve(path string) error {
	target, err := transaction.target(path)
	if err != nil {
		return err
	}
	if err := transaction.injectLockWriteFault(); err != nil {
		return err
	}
	if !target.Existed {
		target.Deleted = true
	}
	target.Staged = true
	return nil
}

func (transaction *installationTransaction) writeState(document *state.Document, path string) error {
	target, err := transaction.target(path)
	if err != nil {
		return err
	}
	if err := transaction.injectLockWriteFault(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target.Stage), 0o755); err != nil {
		return err
	}
	if err := document.Write(target.Stage); err != nil {
		return err
	}
	target.Deleted = false
	target.Staged = true
	return nil
}

func (transaction *installationTransaction) target(path string) (*installationJournalTarget, error) {
	absolute := cleanAbsolutePath(path)
	index, exists := transaction.byPath[absolute]
	if !exists {
		return nil, fmt.Errorf("installation attempted undeclared destination %s", path)
	}
	return &transaction.journal.Targets[index], nil
}

func (transaction *installationTransaction) injectStageFault() error {
	point := "stage:" + strconv.Itoa(transaction.stageCount)
	transaction.stageCount++
	return injectInstallationFault(point)
}

func (transaction *installationTransaction) injectLockWriteFault() error {
	point := "lock-write:" + strconv.Itoa(transaction.lockWriteCount)
	transaction.lockWriteCount++
	if err := injectInstallationFault("lock-write"); err != nil {
		return err
	}
	return injectInstallationFault(point)
}

func injectInstallationFault(point string) error {
	if installationFault == nil {
		return nil
	}
	return installationFault(point)
}

func commitInstallationTarget(target installationJournalTarget) error {
	if err := os.RemoveAll(target.Destination); err != nil {
		return err
	}
	if target.Deleted {
		err := syncDirectory(filepath.Dir(target.Destination))
		if !target.Existed && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.Rename(target.Stage, target.Destination); err != nil {
		return fmt.Errorf("ordered rename failed (the transaction is not atomic across filesystems): %w", err)
	}
	if err := syncReplacementPath(target.Destination); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(target.Destination))
}

func (transaction *installationTransaction) rollback(cause error) error {
	if err := restoreInstallationJournal(transaction.root, &transaction.journal); err != nil {
		return unresolvedInstallationRecoveryError(transaction.root, err, cause)
	}
	if err := transaction.markRolledBack(); err != nil {
		return unresolvedInstallationRecoveryError(transaction.root, err, cause)
	}
	if err := transaction.cleanupCommitted(); err != nil {
		return unresolvedInstallationRecoveryError(transaction.root, err, cause)
	}
	return cause
}

func (transaction *installationTransaction) markRolledBack() error {
	transaction.journal.State = "rolled-back"
	transaction.journal.Current = -1
	return transaction.writeJournal()
}

func (transaction *installationTransaction) cleanupUncommitted() error {
	return transaction.cleanupTransactionArtifacts()
}

func (transaction *installationTransaction) cleanupCommitted() error {
	return transaction.cleanupTransactionArtifacts()
}

func (transaction *installationTransaction) cleanupTransactionArtifacts() error {
	var failures []string
	for _, target := range transaction.journal.Targets {
		if err := os.RemoveAll(target.Stage); err != nil {
			failures = append(failures, fmt.Sprintf("remove stage %s: %v", target.Stage, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	base := filepath.Dir(transaction.root)
	retired := filepath.Join(base, "cleanup-"+strings.TrimPrefix(filepath.Base(transaction.root), "transaction-"))
	if err := os.Rename(transaction.root, retired); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("retire journal directory: %w", err)
	}
	if err := syncDirectory(base); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("persist retired journal: %w", err)
	}
	if err := os.RemoveAll(retired); err != nil {
		return fmt.Errorf("remove retired journal: %w", err)
	}
	if err := syncDirectory(base); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("persist journal cleanup: %w", err)
	}
	return nil
}

func (transaction *installationTransaction) writeJournal() error {
	data, err := json.MarshalIndent(transaction.journal, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maximumInstallationJournal {
		return fmt.Errorf("installation journal exceeds %d bytes", maximumInstallationJournal)
	}
	path := filepath.Join(transaction.root, installationJournalName)
	temporary, err := os.CreateTemp(transaction.root, ".journal-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDirectory(transaction.root)
}

func recoverPendingInstallations(invocation Invocation) error {
	project, err := os.Getwd()
	if err != nil {
		return err
	}
	home, homeErr := os.UserHomeDir()
	if os.Getenv("XDG_STATE_HOME") == "" && homeErr != nil {
		// Without either state location no prior durable journal could have been
		// created. Mutating commands will still fail closed when they try to start
		// a transaction, while help/version/bootstrap remain usable.
		return nil
	}
	paths := []string{filepath.Join(project, "skills-lock.json")}
	if homeErr == nil {
		global, _ := installationLockLocation(state.Global, project, home)
		paths = append(paths, global)
	}
	seen := map[string]bool{}
	for _, path := range paths {
		path = cleanAbsolutePath(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		pending, err := hasPendingInstallationRecovery(path)
		if err != nil {
			return err
		}
		if !pending {
			continue
		}
		if err := recoverInstallationState(invocation, path); err != nil {
			return err
		}
	}
	return nil
}

func recoverInstallationState(invocation Invocation, lockPath string) error {
	stateSpec, err := stateAdvisoryLockSpec(lockPath)
	if err != nil {
		return err
	}
	return withAdvisoryLocks(invocationContext(invocation), invocation.Stderr, []advisoryLockSpec{stateSpec}, advisoryLockExclusive, func() error {
		bases, _, err := pendingInstallationBases(lockPath)
		if err != nil {
			return err
		}
		return withAdvisoryLocks(invocationContext(invocation), invocation.Stderr, installationAdvisoryLockSpecs(bases), advisoryLockExclusive, func() error {
			// Re-enumerate only after every lease is held. A live writer may have
			// completed and retired its journal while recovery waited.
			return recoverInstallationTransactions(lockPath)
		})
	})
}

func pendingInstallationBases(lockPath string) ([]string, bool, error) {
	directories, err := installationTransactionDirectories(lockPath)
	if err != nil {
		return nil, false, err
	}
	bases := []string{}
	for _, directory := range directories {
		journal, err := readInstallationJournal(directory, lockPath)
		if err != nil {
			return nil, false, invalidInstallationJournalError(directory, err)
		}
		for _, target := range journal.Targets {
			if canonicalAdvisoryResource(target.Destination) != canonicalAdvisoryResource(lockPath) {
				bases = append(bases, filepath.Dir(target.Destination))
			}
		}
	}
	return bases, len(directories) > 0, nil
}

func hasPendingInstallationRecovery(lockPath string) (bool, error) {
	base, err := installationTransactionScope(lockPath)
	if err != nil {
		return false, err
	}
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() && (strings.HasPrefix(entry.Name(), "transaction-") || strings.HasPrefix(entry.Name(), "cleanup-")) {
			return true, nil
		}
	}
	return false, nil
}

func recoverInstallationTransactions(lockPath string) error {
	directories, err := installationTransactionDirectories(lockPath)
	if err != nil {
		return err
	}
	for _, directory := range directories {
		journal, err := readInstallationJournal(directory, lockPath)
		if err != nil {
			return invalidInstallationJournalError(directory, err)
		}
		switch journal.State {
		case "staging", "prepared":
			transaction := &installationTransaction{root: directory, journal: *journal}
			if err := transaction.cleanupUncommitted(); err != nil {
				return unresolvedInstallationRecoveryError(directory, err, nil)
			}
		case "committing":
			if err := restoreInstallationJournal(directory, journal); err != nil {
				return unresolvedInstallationRecoveryError(directory, err, nil)
			}
			transaction := &installationTransaction{root: directory, journal: *journal}
			if err := transaction.markRolledBack(); err != nil {
				return unresolvedInstallationRecoveryError(directory, err, nil)
			}
			if err := transaction.cleanupCommitted(); err != nil {
				return unresolvedInstallationRecoveryError(directory, err, nil)
			}
		case "rolled-back", "committed":
			transaction := &installationTransaction{root: directory, journal: *journal}
			if err := transaction.cleanupCommitted(); err != nil {
				return unresolvedInstallationRecoveryError(directory, err, nil)
			}
		default:
			return unresolvedInstallationRecoveryError(directory, fmt.Errorf("unknown journal state %q", journal.State), nil)
		}
	}
	return nil
}

func restoreInstallationJournal(_ string, journal *installationJournal) error {
	var failures []string
	for index, target := range journal.Targets {
		if (!target.Committed && journal.Current != index) || !target.Existed {
			continue
		}
		if _, err := os.Lstat(target.Backup); err != nil {
			failures = append(failures, fmt.Sprintf("inspect backup for %s at %s: %v", target.Destination, target.Backup, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	for index := len(journal.Targets) - 1; index >= 0; index-- {
		target := journal.Targets[index]
		if !target.Committed && journal.Current != index {
			continue
		}
		if err := os.RemoveAll(target.Destination); err != nil {
			failures = append(failures, fmt.Sprintf("remove %s: %v", target.Destination, err))
			continue
		}
		if target.Existed {
			if err := copyReplacementPath(target.Backup, target.Destination); err != nil {
				failures = append(failures, fmt.Sprintf("restore %s from %s: %v", target.Destination, target.Backup, err))
				continue
			}
			if err := syncReplacementPath(target.Destination); err != nil {
				failures = append(failures, fmt.Sprintf("persist restored destination %s: %v", target.Destination, err))
				continue
			}
		}
		if err := syncDirectory(filepath.Dir(target.Destination)); err != nil {
			failures = append(failures, fmt.Sprintf("persist restored parent for %s: %v", target.Destination, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func readInstallationJournal(directory, expectedLockPath string) (*installationJournal, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("transaction path is not a directory")
	}
	journalPath := filepath.Join(directory, installationJournalName)
	file, err := os.Open(journalPath)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maximumInstallationJournal+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > maximumInstallationJournal {
		return nil, fmt.Errorf("installation journal exceeds %d bytes", maximumInstallationJournal)
	}
	var journal installationJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, err
	}
	expectedLockPath = canonicalAdvisoryResource(expectedLockPath)
	if journal.Version != 1 || journal.CommitModel != orderedRenameCommitModel || canonicalAdvisoryResource(journal.LockPath) != expectedLockPath || journal.Current < -1 || len(journal.Targets) == 0 || len(journal.Targets) > maximumInstallationTargets {
		return nil, errors.New("unsupported or malformed installation journal")
	}
	seen := map[string]bool{}
	for index, target := range journal.Targets {
		destination := cleanAbsolutePath(target.Destination)
		if destination != target.Destination || seen[destination] {
			return nil, errors.New("installation journal has invalid destinations")
		}
		seen[destination] = true
		expectedStage := filepath.Join(filepath.Dir(destination), fmt.Sprintf(".open-skills-stage-%s-%d", filepath.Base(directory), index))
		if target.Stage != expectedStage {
			return nil, errors.New("installation journal has invalid stage path")
		}
		expectedBackup := filepath.Join(directory, "backups", strconv.Itoa(index))
		if target.Backup != expectedBackup {
			return nil, errors.New("installation journal has invalid backup path")
		}
	}
	if canonicalAdvisoryResource(journal.Targets[len(journal.Targets)-1].Destination) != expectedLockPath || journal.Current >= len(journal.Targets) {
		return nil, errors.New("installation journal has invalid lock or current step")
	}
	if err := validateInstallationJournalState(journal); err != nil {
		return nil, err
	}
	return &journal, nil
}

func validateInstallationJournalState(journal installationJournal) error {
	if journal.Current >= 0 {
		current := journal.Targets[journal.Current]
		if !current.Staged || current.Committed {
			return errors.New("installation journal has inconsistent current step")
		}
	}
	seenPending := false
	for index, target := range journal.Targets {
		if target.Deleted && !target.Staged {
			return errors.New("installation journal has an unstaged deletion")
		}
		if target.Committed && !target.Staged {
			return errors.New("installation journal committed an unstaged target")
		}
		if target.Staged && !target.Committed {
			seenPending = true
		}
		if target.Committed && seenPending && journal.Current != index {
			return errors.New("installation journal has non-prefix commits")
		}
	}
	switch journal.State {
	case "staging":
		for _, target := range journal.Targets {
			if target.Committed {
				return errors.New("staging journal contains commits")
			}
		}
		if journal.Current != -1 {
			return errors.New("staging journal has a current commit")
		}
	case "prepared":
		for _, target := range journal.Targets {
			if target.Committed {
				return errors.New("prepared journal contains commits")
			}
		}
		if journal.Current != -1 || !journal.Targets[len(journal.Targets)-1].Staged {
			return errors.New("prepared journal has invalid lock state")
		}
	case "committing":
		if !journal.Targets[len(journal.Targets)-1].Staged {
			return errors.New("committing journal has no staged lock")
		}
	case "committed":
		if journal.Current != -1 {
			return errors.New("committed journal has a current step")
		}
		for _, target := range journal.Targets {
			if target.Staged && !target.Committed {
				return errors.New("committed journal has pending targets")
			}
		}
	case "rolled-back":
		if journal.Current != -1 {
			return errors.New("rolled-back journal has a current step")
		}
	default:
		return fmt.Errorf("unknown journal state %q", journal.State)
	}
	return nil
}

func invalidInstallationJournalError(directory string, cause error) error {
	return fmt.Errorf("installation journal at %s is invalid: %v. Recovery stopped without following any journal destination. deterministic cleanup: move %s aside for inspection, then restore affected skills from a trusted backup or reinstall them before removing the quarantined directory", filepath.Join(directory, installationJournalName), cause, directory)
}

func unresolvedInstallationRecoveryError(directory string, recoveryErr, operationErr error) error {
	prefix := "recover interrupted installation"
	if operationErr != nil {
		prefix = operationErr.Error() + "; rollback is unresolved"
	}
	return fmt.Errorf("%s: %v. This ordered commit is crash-recoverable but not atomic across filesystems. deterministic cleanup: inspect %s; for each committed/current target in %s, remove its destination and restore its backup when existed=true; then remove %s", prefix, recoveryErr, filepath.Join(directory, installationJournalName), filepath.Join(directory, installationJournalName), directory)
}

func installationTransactionDirectories(lockPath string) ([]string, error) {
	base, err := installationTransactionScope(lockPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	removedRetired := false
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "cleanup-") {
			if err := os.RemoveAll(filepath.Join(base, entry.Name())); err != nil {
				return nil, fmt.Errorf("remove retired installation journal %s: %w", entry.Name(), err)
			}
			removedRetired = true
		}
	}
	if removedRetired {
		if err := syncDirectory(base); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("persist retired installation journal cleanup: %w", err)
		}
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "transaction-") {
			result = append(result, filepath.Join(base, entry.Name()))
		}
	}
	sort.Strings(result)
	return result, nil
}

func installationTransactionScope(lockPath string) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determine installation journal location: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	hash := sha256.Sum256([]byte(canonicalAdvisoryResource(lockPath)))
	return filepath.Join(base, "open-skills", "transactions", hex.EncodeToString(hash[:12])), nil
}

func cleanAbsolutePath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	absolute = filepath.Clean(absolute)
	parent := filepath.Dir(absolute)
	missing := []string{}
	for current := parent; ; current = filepath.Dir(current) {
		if resolved, resolveErr := filepath.EvalSymlinks(current); resolveErr == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(filepath.Join(resolved, filepath.Base(absolute)))
		}
		next := filepath.Dir(current)
		if next == current {
			return absolute
		}
		missing = append(missing, filepath.Base(current))
	}
}

func syncReplacementPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		err = file.Sync()
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := syncReplacementPath(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
		return syncDirectory(path)
	}
	return nil
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		// Windows does not support opening directories for FlushFileBuffers.
		// Regular staged files are still flushed before their ordered rename.
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	return closeErr
}
