package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddPreflightsEveryDestinationBeforeStaging(t *testing.T) {
	project, source := transactionFixture(t, "first")
	if err := os.WriteFile(filepath.Join(project, ".pi"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runTransactionCommand(t, project, []string{"add", source, "--agent", "universal", "pi", "--copy", "--yes"})
	if code != 1 || !strings.Contains(stderr, "preflight installation destination") {
		t.Fatalf("obstructed add = %d stderr %q", code, stderr)
	}
	assertTransactionState(t, project, "", nil)
}

func TestAddPreflightIgnoresSkippedProjectAdapter(t *testing.T) {
	project, source := transactionFixture(t, "first")
	if err := os.WriteFile(filepath.Join(project, ".pi"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runTransactionCommand(t, project, []string{"add", source, "--agent", "pi", "--yes"})
	if code != 0 || stderr != "" {
		t.Fatalf("skipped-adapter add = %d stderr %q", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "transaction-skill", "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "# first") {
		t.Fatalf("canonical install = %q, %v", data, err)
	}
}

func TestAddTransactionFaultsLeavePriorContentAndLockUnchanged(t *testing.T) {
	for _, point := range []string{"before-staging", "stage:0", "stage:1", "lock-write", "commit:0", "commit:1", "after-commit"} {
		t.Run(point, func(t *testing.T) {
			project, source := transactionFixture(t, "first")
			code, _, stderr := runTransactionCommand(t, project, []string{"add", source, "--agent", "universal", "--yes"})
			if code != 0 || stderr != "" {
				t.Fatalf("initial add = %d stderr %q", code, stderr)
			}
			priorLock, err := os.ReadFile(filepath.Join(project, "skills-lock.json"))
			if err != nil {
				t.Fatal(err)
			}
			writeTransactionSkill(t, source, "second")

			installationFault = func(actual string) error {
				if actual == point {
					return errors.New("injected " + point)
				}
				return nil
			}
			t.Cleanup(func() { installationFault = nil })
			code, _, stderr = runTransactionCommand(t, project, []string{"add", source, "--agent", "universal", "--yes"})
			installationFault = nil
			if code != 1 || !strings.Contains(stderr, "injected "+point) {
				t.Fatalf("faulted add = %d stderr %q", code, stderr)
			}
			assertTransactionState(t, project, "first", priorLock)
			if directories, err := installationTransactionDirectories(filepath.Join(project, "skills-lock.json")); err != nil || len(directories) != 0 {
				t.Fatalf("completed rollback left journals %v, %v", directories, err)
			}
		})
	}
}

func TestNextInvocationRecoversInterruptedAdd(t *testing.T) {
	project, source := transactionFixture(t, "first")
	code, _, stderr := runTransactionCommand(t, project, []string{"add", source, "--agent", "universal", "--yes"})
	if code != 0 || stderr != "" {
		t.Fatalf("initial add = %d stderr %q", code, stderr)
	}
	priorLock, err := os.ReadFile(filepath.Join(project, "skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeTransactionSkill(t, source, "second")

	installationFault = func(point string) error {
		if point == "commit:1" {
			return errSimulatedInstallationInterruption
		}
		return nil
	}
	t.Cleanup(func() { installationFault = nil })
	code, _, stderr = runTransactionCommand(t, project, []string{"add", source, "--agent", "universal", "--yes"})
	installationFault = nil
	if code != 1 || !strings.Contains(stderr, "simulated installation interruption") {
		t.Fatalf("interrupted add = %d stderr %q", code, stderr)
	}
	assertTransactionState(t, project, "second", priorLock)

	code, _, stderr = runTransactionCommand(t, project, []string{"list"})
	if code != 0 || stderr != "" {
		t.Fatalf("recovery invocation = %d stderr %q", code, stderr)
	}
	assertTransactionState(t, project, "first", priorLock)
	if directories, err := installationTransactionDirectories(filepath.Join(project, "skills-lock.json")); err != nil || len(directories) != 0 {
		t.Fatalf("recovery left journals %v, %v", directories, err)
	}
}

func TestNextInvocationRollsBackWhenEveryTargetWasCommitted(t *testing.T) {
	project, source := transactionFixture(t, "first")
	code, _, stderr := runTransactionCommand(t, project, []string{"add", source, "--agent", "universal", "--yes"})
	if code != 0 || stderr != "" {
		t.Fatalf("initial add = %d stderr %q", code, stderr)
	}
	priorLock, err := os.ReadFile(filepath.Join(project, "skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeTransactionSkill(t, source, "second")
	installationFault = func(point string) error {
		if point == "after-commit" {
			return errSimulatedInstallationInterruption
		}
		return nil
	}
	t.Cleanup(func() { installationFault = nil })
	code, _, stderr = runTransactionCommand(t, project, []string{"add", source, "--agent", "universal", "--yes"})
	installationFault = nil
	if code != 1 || !strings.Contains(stderr, "simulated installation interruption") {
		t.Fatalf("interrupted add = %d stderr %q", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "transaction-skill", "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "# second") {
		t.Fatalf("committed content = %q, %v", data, err)
	}
	committedLock, err := os.ReadFile(filepath.Join(project, "skills-lock.json"))
	if err != nil || bytes.Equal(committedLock, priorLock) {
		t.Fatalf("lock was not committed before interruption: %v", err)
	}

	code, _, stderr = runTransactionCommand(t, project, []string{"list"})
	if code != 0 || stderr != "" {
		t.Fatalf("recovery invocation = %d stderr %q", code, stderr)
	}
	assertTransactionState(t, project, "first", priorLock)
}

func TestSameSourceSymlinkReinstallUsesDistinctTransactionTargets(t *testing.T) {
	project, source := transactionFixture(t, "first")
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"first", "second"} {
		writeTransactionSkill(t, source, marker)
		code, _, stderr := runTransactionCommand(t, project, []string{"add", source, "--agent", "claude-code", "--yes"})
		if code != 0 || stderr != "" {
			t.Fatalf("%s symlink add = %d stderr %q", marker, code, stderr)
		}
	}
	data, err := os.ReadFile(filepath.Join(project, ".claude", "skills", "transaction-skill", "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "# second") {
		t.Fatalf("reinstalled symlink content = %q, %v", data, err)
	}
}

func TestNextInvocationRecoversInterruptedFreshSymlinkInstall(t *testing.T) {
	project, source := transactionFixture(t, "first")
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	installationFault = func(point string) error {
		if point == "commit:2" {
			return errSimulatedInstallationInterruption
		}
		return nil
	}
	t.Cleanup(func() { installationFault = nil })
	code, _, stderr := runTransactionCommand(t, project, []string{"add", source, "--agent", "claude-code", "--yes"})
	installationFault = nil
	if code != 1 || !strings.Contains(stderr, "simulated installation interruption") {
		t.Fatalf("interrupted add = %d stderr %q", code, stderr)
	}
	if info, err := os.Lstat(filepath.Join(project, ".claude", "skills", "transaction-skill")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("agent link was not committed before interruption: %v, %v", info, err)
	}

	code, _, stderr = runTransactionCommand(t, project, []string{"list"})
	if code != 0 || stderr != "" {
		t.Fatalf("recovery invocation = %d stderr %q", code, stderr)
	}
	assertTransactionState(t, project, "", nil)
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "transaction-skill")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left agent link: %v", err)
	}
}

func TestNextInvocationRecoversInterruptedGlobalAdd(t *testing.T) {
	project, source := transactionFixture(t, "first")
	installationFault = func(point string) error {
		if point == "commit:1" {
			return errSimulatedInstallationInterruption
		}
		return nil
	}
	t.Cleanup(func() { installationFault = nil })
	code, _, stderr := runTransactionCommand(t, project, []string{"add", source, "--global", "--agent", "universal", "--yes"})
	installationFault = nil
	if code != 1 || !strings.Contains(stderr, "simulated installation interruption") {
		t.Fatalf("interrupted global add = %d stderr %q", code, stderr)
	}

	code, _, stderr = runTransactionCommand(t, project, []string{"list", "--global"})
	if code != 0 || stderr != "" {
		t.Fatalf("global recovery invocation = %d stderr %q", code, stderr)
	}
	home := os.Getenv("HOME")
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "transaction-skill")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left global content: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "skills", ".skill-lock.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left global lock: %v", err)
	}
}

func TestUnresolvedRecoveryGivesDeterministicCrossFilesystemCleanup(t *testing.T) {
	project, source := transactionFixture(t, "first")
	code, _, stderr := runTransactionCommand(t, project, []string{"add", source, "--agent", "universal", "--yes"})
	if code != 0 || stderr != "" {
		t.Fatalf("initial add = %d stderr %q", code, stderr)
	}
	writeTransactionSkill(t, source, "second")
	installationFault = func(point string) error {
		if point == "commit:1" {
			return errSimulatedInstallationInterruption
		}
		return nil
	}
	t.Cleanup(func() { installationFault = nil })
	code, _, stderr = runTransactionCommand(t, project, []string{"add", source, "--agent", "universal", "--yes"})
	installationFault = nil
	if code != 1 || !strings.Contains(stderr, "simulated installation interruption") {
		t.Fatalf("interrupted add = %d stderr %q", code, stderr)
	}

	directories, err := installationTransactionDirectories(filepath.Join(project, "skills-lock.json"))
	if err != nil || len(directories) != 1 {
		t.Fatalf("pending journals = %v, %v", directories, err)
	}
	data, err := os.ReadFile(filepath.Join(directories[0], installationJournalName))
	if err != nil {
		t.Fatal(err)
	}
	var journal installationJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatal(err)
	}
	if journal.CommitModel != orderedRenameCommitModel {
		t.Fatalf("commit model = %q", journal.CommitModel)
	}
	if err := os.RemoveAll(journal.Targets[0].Backup); err != nil {
		t.Fatal(err)
	}

	code, _, stderr = runTransactionCommand(t, project, []string{"list"})
	if code != 1 || !strings.Contains(stderr, "deterministic cleanup") || !strings.Contains(stderr, "not atomic across filesystems") || !strings.Contains(stderr, directories[0]) {
		t.Fatalf("failed recovery = %d stderr %q", code, stderr)
	}
	if _, err := os.Stat(directories[0]); err != nil {
		t.Fatalf("unresolved journal was not preserved: %v", err)
	}
}

func transactionFixture(t *testing.T, marker string) (string, string) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	source := filepath.Join(root, "source")
	for _, directory := range []string{project, source, filepath.Join(root, "state"), filepath.Join(root, "home")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "home", ".config"))
	writeTransactionSkill(t, source, marker)
	return project, source
}

func writeTransactionSkill(t *testing.T, source, marker string) {
	t.Helper()
	contents := "---\nname: transaction-skill\ndescription: transaction fixture\n---\n# " + marker + "\n"
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runTransactionCommand(t *testing.T, project string, arguments []string) (int, string, string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Invocation{Args: arguments, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr})
	return code, stdout.String(), stderr.String()
}

func assertTransactionState(t *testing.T, project, marker string, lock []byte) {
	t.Helper()
	installed := filepath.Join(project, ".agents", "skills", "transaction-skill", "SKILL.md")
	if marker == "" {
		if _, err := os.Lstat(installed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected installed content: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(project, "skills-lock.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected lock: %v", err)
		}
		return
	}
	data, err := os.ReadFile(installed)
	if err != nil || !strings.Contains(string(data), "# "+marker) {
		t.Fatalf("installed content = %q, %v; want marker %q", data, err, marker)
	}
	actualLock, err := os.ReadFile(filepath.Join(project, "skills-lock.json"))
	if err != nil || !bytes.Equal(actualLock, lock) {
		t.Fatalf("lock = %q, %v; want %q", actualLock, err, lock)
	}
}
