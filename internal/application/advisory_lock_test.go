package application

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EngBlock/open-skills/internal/state"
	truststore "github.com/EngBlock/open-skills/internal/trust"
)

func TestAdvisoryCommandHelperProcess(t *testing.T) {
	if os.Getenv("OPEN_SKILLS_COMMAND_HELPER") != "1" {
		return
	}
	if directory := os.Getenv("OPEN_SKILLS_COMMAND_CWD"); directory != "" {
		if err := os.Chdir(directory); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	var arguments []string
	if err := json.Unmarshal([]byte(os.Getenv("OPEN_SKILLS_COMMAND_ARGS")), &arguments); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	input := bufio.NewReader(os.Stdin)
	pausePoints := map[string]bool{}
	for _, point := range strings.Split(os.Getenv("OPEN_SKILLS_COMMAND_PAUSE_POINTS"), ",") {
		if point = strings.TrimSpace(point); point != "" {
			pausePoints[point] = true
		}
	}
	if len(pausePoints) > 0 {
		installationFault = func(point string) error {
			if !pausePoints[point] {
				return nil
			}
			fmt.Fprintf(os.Stderr, "paused:%s\n", point)
			_, err := input.ReadString('\n')
			return err
		}
	}
	code := Run(context.Background(), Invocation{Args: arguments, Stdin: input, Stdout: os.Stdout, Stderr: os.Stderr})
	os.Exit(code)
}

func TestAdvisoryLockHelperProcess(t *testing.T) {
	if os.Getenv("OPEN_SKILLS_LOCK_HELPER") != "1" {
		return
	}
	mode := advisoryLockExclusive
	if os.Getenv("OPEN_SKILLS_LOCK_MODE") == "shared" {
		mode = advisoryLockShared
	}
	specs := []advisoryLockSpec{}
	if path := os.Getenv("OPEN_SKILLS_LOCK_PATH"); path != "" {
		specs = append(specs, advisoryLockSpec{path: path, label: "test lock"})
	}
	if statePath := os.Getenv("OPEN_SKILLS_LOCK_STATE"); statePath != "" {
		spec, err := stateAdvisoryLockSpec(statePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		specs = append(specs, spec)
	}
	if base := os.Getenv("OPEN_SKILLS_LOCK_BASE"); base != "" {
		specs = append(specs, installationAdvisoryLockSpecs([]string{base})...)
	}
	locks, err := acquireAdvisoryLocks(context.Background(), os.Stderr, specs, mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	fmt.Fprintln(os.Stdout, "acquired")
	if approval := os.Getenv("OPEN_SKILLS_LOCK_APPROVAL"); approval != "" {
		parts := strings.SplitN(approval, "\t", 2)
		store, openErr := truststore.Open()
		if openErr != nil || len(parts) != 2 {
			fmt.Fprintln(os.Stderr, openErr)
			os.Exit(4)
		}
		if approveErr := store.Approve(parts[0], parts[1], time.Unix(1, 0)); approveErr != nil {
			fmt.Fprintln(os.Stderr, approveErr)
			os.Exit(5)
		}
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if cleanup := os.Getenv("OPEN_SKILLS_LOCK_CLEANUP"); cleanup != "" {
		_ = os.RemoveAll(cleanup)
	}
	if err := releaseAdvisoryLocks(locks); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(6)
	}
}

type synchronizedBuffer struct {
	mu      sync.Mutex
	target  string
	reached chan struct{}
	once    sync.Once
	bytes.Buffer
}

func newSignalingBuffer(target string) *synchronizedBuffer {
	return &synchronizedBuffer{target: target, reached: make(chan struct{})}
}

func (buffer *synchronizedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	written, err := buffer.Buffer.Write(data)
	if buffer.target != "" && strings.Contains(buffer.Buffer.String(), buffer.target) {
		buffer.once.Do(func() { close(buffer.reached) })
	}
	return written, err
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.String()
}

type lockHelper struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stderr  *bytes.Buffer
}

func startLockHelper(t *testing.T, environment map[string]string) *lockHelper {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestAdvisoryLockHelperProcess$")
	command.Env = append(os.Environ(), "OPEN_SKILLS_LOCK_HELPER=1", "OPEN_SKILLS_LOCK_TIMEOUT_MS=5000")
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "acquired" {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("helper did not acquire lock: output %q stderr %q", scanner.Text(), stderr.String())
	}
	return &lockHelper{command: command, stdin: stdin, stderr: stderr}
}

func (helper *lockHelper) release(t *testing.T) {
	t.Helper()
	_ = helper.stdin.Close()
	if err := helper.command.Wait(); err != nil {
		t.Fatalf("lock helper failed: %v stderr %q", err, helper.stderr.String())
	}
}

func runLockContender(t *testing.T, environment map[string]string) (string, error) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestAdvisoryLockHelperProcess$")
	command.Env = append(os.Environ(), "OPEN_SKILLS_LOCK_HELPER=1", "OPEN_SKILLS_LOCK_TIMEOUT_MS=150")
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	command.Stdin = strings.NewReader("")
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestSharedAdvisoryLocksCoexistAndExclusiveConflicts(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "resource.lock")
	first := startLockHelper(t, map[string]string{"OPEN_SKILLS_LOCK_PATH": lockPath, "OPEN_SKILLS_LOCK_MODE": "shared"})
	second := startLockHelper(t, map[string]string{"OPEN_SKILLS_LOCK_PATH": lockPath, "OPEN_SKILLS_LOCK_MODE": "shared"})
	second.release(t)
	first.release(t)

	for _, contenderMode := range []string{"shared", "exclusive"} {
		holder := startLockHelper(t, map[string]string{"OPEN_SKILLS_LOCK_PATH": lockPath, "OPEN_SKILLS_LOCK_MODE": "exclusive"})
		output, err := runLockContender(t, map[string]string{"OPEN_SKILLS_LOCK_PATH": lockPath, "OPEN_SKILLS_LOCK_MODE": contenderMode})
		holder.release(t)
		if err == nil || !strings.Contains(output, "timed out after 150ms") || !strings.Contains(output, lockTimeoutEnvironment) {
			t.Fatalf("%s contender = %v output %q", contenderMode, err, output)
		}
		if count := strings.Count(output, "Waiting for another open-skills process"); count != 1 {
			t.Fatalf("waiting diagnostics = %d in %q", count, output)
		}
	}
}

func TestAdvisoryLockReleasesAfterFailureAndContextCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resource.lock")
	specs := []advisoryLockSpec{{path: path, label: "test lock"}}
	injected := errors.New("injected callback failure")
	if err := withAdvisoryLocks(context.Background(), io.Discard, specs, advisoryLockExclusive, func() error { return injected }); !errors.Is(err, injected) {
		t.Fatalf("callback error = %v", err)
	}
	if err := withAdvisoryLocks(context.Background(), io.Discard, specs, advisoryLockExclusive, func() error { return nil }); err != nil {
		t.Fatalf("reacquire after failure: %v", err)
	}

	holder := startLockHelper(t, map[string]string{"OPEN_SKILLS_LOCK_PATH": path, "OPEN_SKILLS_LOCK_MODE": "exclusive"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operationCalled := false
	err := withAdvisoryLocks(ctx, io.Discard, specs, advisoryLockShared, func() error {
		operationCalled = true
		return nil
	})
	holder.release(t)
	if !errors.Is(err, context.Canceled) || operationCalled {
		t.Fatalf("cancelled wait = %v operationCalled=%v", err, operationCalled)
	}

	unopened := filepath.Join(t.TempDir(), "cancelled.lock")
	operationCalled = false
	err = withAdvisoryLocks(ctx, io.Discard, []advisoryLockSpec{{path: unopened, label: "cancelled lock"}}, advisoryLockExclusive, func() error {
		operationCalled = true
		return nil
	})
	if !errors.Is(err, context.Canceled) || operationCalled {
		t.Fatalf("pre-cancelled acquire = %v operationCalled=%v", err, operationCalled)
	}
	if _, statErr := os.Stat(unopened); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("pre-cancelled acquisition touched lock file: %v", statErr)
	}
}

func TestListAndTrustListUseConcurrentSharedLocks(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	home := filepath.Join(root, "home")
	stateHome := filepath.Join(root, "state")
	configHome := filepath.Join(root, "config")
	for _, directory := range []string{project, home, stateHome, configHome} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)

	statePath := filepath.Join(project, "skills-lock.json")
	stateHolder := startLockHelper(t, map[string]string{
		"OPEN_SKILLS_LOCK_STATE": statePath,
		"OPEN_SKILLS_LOCK_MODE":  "shared",
	})
	var listOut, listErr bytes.Buffer
	if code := Run(context.Background(), Invocation{Args: []string{"list"}, Stdin: strings.NewReader(""), Stdout: &listOut, Stderr: &listErr}); code != 0 {
		t.Fatalf("concurrent list = %d stderr %q", code, listErr.String())
	}
	stateHolder.release(t)

	trustPath, err := truststore.Path()
	if err != nil {
		t.Fatal(err)
	}
	trustHolder := startLockHelper(t, map[string]string{
		"OPEN_SKILLS_LOCK_PATH": trustAdvisoryLockSpec(trustPath).path,
		"OPEN_SKILLS_LOCK_MODE": "shared",
	})
	var trustOut, trustErr bytes.Buffer
	if code := Run(context.Background(), Invocation{Args: []string{"trust", "list"}, Stdin: strings.NewReader(""), Stdout: &trustOut, Stderr: &trustErr}); code != 0 {
		t.Fatalf("concurrent trust list = %d stderr %q", code, trustErr.String())
	}
	trustHolder.release(t)
}

func TestListLocksAbsentInstallationResources(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	home := filepath.Join(root, "home")
	for _, directory := range []string{project, home} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv(lockTimeoutEnvironment, "150")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)

	absentBase := filepath.Join(project, ".agents", "skills")
	holder := startLockHelper(t, map[string]string{"OPEN_SKILLS_LOCK_BASE": absentBase})
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Invocation{Args: []string{"list"}, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
	holder.release(t)
	if code != 1 || !strings.Contains(stderr.String(), "timed out after 150ms") || !strings.Contains(stderr.String(), "installation mutation lock") {
		t.Fatalf("list without installation directory = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, ".agents")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only lock created managed directory: %v", err)
	}
}

func TestKilledProcessReleasesAdvisoryLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resource.lock")
	holder := startLockHelper(t, map[string]string{"OPEN_SKILLS_LOCK_PATH": path})
	if err := holder.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.command.Wait()
	contender := startLockHelper(t, map[string]string{"OPEN_SKILLS_LOCK_PATH": path})
	contender.release(t)
}

func TestInvalidLockTimeoutFailsBeforeManagedStateIsTouched(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv(lockTimeoutEnvironment, "-1")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Invocation{Args: []string{"list"}, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
	if code != 1 || !strings.Contains(stderr.String(), lockTimeoutEnvironment) || !strings.Contains(stderr.String(), "managed state was not touched") {
		t.Fatalf("invalid timeout = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("negative timeout created state: %v", err)
	}
	t.Setenv(lockTimeoutEnvironment, "not-a-number")
	stdout.Reset()
	stderr.Reset()
	code = Run(context.Background(), Invocation{Args: []string{"list"}, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
	if code != 1 || !strings.Contains(stderr.String(), lockTimeoutEnvironment) {
		t.Fatalf("invalid timeout = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid timeout created state: %v", err)
	}
}

func TestWindowsAdvisoryResourceIdentityIsCaseInsensitive(t *testing.T) {
	upper := canonicalAdvisoryResourceCase(`C:\\Users\\Alice\\.agents\\skills`, "windows")
	lower := canonicalAdvisoryResourceCase(`c:\\users\\alice\\.AGENTS\\SKILLS`, "windows")
	if upper != lower {
		t.Fatalf("Windows resource aliases differ: %q != %q", upper, lower)
	}
	if got := canonicalAdvisoryResourceCase("/Case/Sensitive", "linux"); got != "/Case/Sensitive" {
		t.Fatalf("Unix resource case changed: %q", got)
	}
}

func TestInstallationLocksConvergeAcrossDifferentStateLocations(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "home", ".agents", "skills")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "aliased-skills")
	if err := os.Symlink(base, alias); err == nil {
		baseSpec := installationAdvisoryLockSpecs([]string{base})[0]
		aliasSpec := installationAdvisoryLockSpecs([]string{alias})[0]
		if baseSpec.path != aliasSpec.path {
			t.Fatalf("symlinked installation aliases use different locks: %s != %s", baseSpec.path, aliasSpec.path)
		}
	}
	firstState := filepath.Join(root, "state-a", "skills-lock.json")
	secondState := filepath.Join(root, "state-b", "skills-lock.json")
	holder := startLockHelper(t, map[string]string{"OPEN_SKILLS_LOCK_STATE": firstState, "OPEN_SKILLS_LOCK_BASE": base})
	output, err := runLockContender(t, map[string]string{"OPEN_SKILLS_LOCK_STATE": secondState, "OPEN_SKILLS_LOCK_BASE": base})
	holder.release(t)
	if err == nil || !strings.Contains(output, "installation mutation lock") {
		t.Fatalf("cross-state contender = %v output %q", err, output)
	}
}

func TestRecoveryRechecksJournalAfterWaitingForLease(t *testing.T) {
	project, _ := transactionFixture(t, "first")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	lockPath := filepath.Join(project, "skills-lock.json")
	transaction, err := prepareInstallationTransaction(lockPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	journalRoot := transaction.root
	stateSpec, err := stateAdvisoryLockSpec(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	holder := startLockHelper(t, map[string]string{
		"OPEN_SKILLS_LOCK_PATH":    stateSpec.path,
		"OPEN_SKILLS_LOCK_CLEANUP": journalRoot,
	})
	var stdout synchronizedBuffer
	stderr := newSignalingBuffer("Waiting for another open-skills process")
	done := make(chan int, 1)
	go func() {
		done <- Run(context.Background(), Invocation{Args: []string{"list"}, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: stderr})
	}()
	select {
	case <-stderr.reached:
	case <-time.After(2 * time.Second):
		t.Fatalf("recovery did not reach lock barrier: %q", stderr.String())
	}
	holder.release(t)
	if code := <-done; code != 0 {
		t.Fatalf("recovery recheck = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "invalid") || strings.Contains(stderr.String(), "Recover installation state") {
		t.Fatalf("cleaned live journal was mis-recovered: %q", stderr.String())
	}
}

func TestReaderRecoversJournalAfterWriterDiesWhileHoldingLocks(t *testing.T) {
	project, source := transactionFixture(t, "first")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)

	var initialStdout, initialStderr bytes.Buffer
	addArguments := []string{"add", source, "--agent", "universal", "--yes"}
	if code := Run(context.Background(), Invocation{Args: addArguments, Stdin: strings.NewReader(""), Stdout: &initialStdout, Stderr: &initialStderr}); code != 0 {
		t.Fatalf("initial add = %d stdout %q stderr %q", code, initialStdout.String(), initialStderr.String())
	}
	lockPath := filepath.Join(project, "skills-lock.json")
	priorLock, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	writeTransactionSkill(t, source, "second")

	arguments, err := json.Marshal(addArguments)
	if err != nil {
		t.Fatal(err)
	}
	writer := exec.Command(os.Args[0], "-test.run=^TestAdvisoryCommandHelperProcess$")
	writer.Env = append(os.Environ(),
		"OPEN_SKILLS_COMMAND_HELPER=1",
		"OPEN_SKILLS_COMMAND_CWD="+project,
		"OPEN_SKILLS_COMMAND_ARGS="+string(arguments),
		"OPEN_SKILLS_COMMAND_PAUSE_POINTS=before-staging,commit:1",
		"OPEN_SKILLS_LOCK_TIMEOUT_MS=5000",
	)
	writer.Stdout = io.Discard
	writerInput, err := writer.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	writerStderr, err := writer.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(writerStderr)
	waitForHelperBarrier(t, scanner, "paused:before-staging")

	var listStdout synchronizedBuffer
	listStderr := newSignalingBuffer("Waiting for another open-skills process")
	listDone := make(chan int, 1)
	go func() {
		listDone <- Run(context.Background(), Invocation{Args: []string{"list"}, Stdin: strings.NewReader(""), Stdout: &listStdout, Stderr: listStderr})
	}()
	select {
	case <-listStderr.reached:
	case <-time.After(5 * time.Second):
		_ = writer.Process.Kill()
		_ = writer.Wait()
		t.Fatalf("reader did not wait on live writer: %q", listStderr.String())
	}

	if _, err := io.WriteString(writerInput, "continue\n"); err != nil {
		t.Fatal(err)
	}
	waitForHelperBarrier(t, scanner, "paused:commit:1")
	journals, err := installationTransactionDirectories(lockPath)
	if err != nil || len(journals) != 1 {
		t.Fatalf("durable partial-commit journal = %v, %v", journals, err)
	}
	installed := filepath.Join(project, ".agents", "skills", "transaction-skill")
	committedContent, err := os.ReadFile(filepath.Join(installed, "SKILL.md"))
	if err != nil || !strings.Contains(string(committedContent), "# second") {
		t.Fatalf("first commit step was not visible before death: %q, %v", committedContent, err)
	}

	if err := writer.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = writerInput.Close()
	_ = writer.Wait()
	if code := <-listDone; code != 0 {
		t.Fatalf("reader recovery = %d stdout %q stderr %q", code, listStdout.String(), listStderr.String())
	}
	restoredContent, err := os.ReadFile(filepath.Join(installed, "SKILL.md"))
	if err != nil || !strings.Contains(string(restoredContent), "# first") {
		t.Fatalf("recovery did not restore prior content: %q, %v", restoredContent, err)
	}
	restoredLock, err := os.ReadFile(lockPath)
	if err != nil || !bytes.Equal(restoredLock, priorLock) {
		t.Fatalf("recovery did not restore prior state: %q, %v; want %q", restoredLock, err, priorLock)
	}
	journals, err = installationTransactionDirectories(lockPath)
	if err != nil || len(journals) != 0 {
		t.Fatalf("recovery left journals = %v, %v", journals, err)
	}
}

func waitForHelperBarrier(t *testing.T, scanner *bufio.Scanner, expected string) {
	t.Helper()
	for scanner.Scan() {
		if scanner.Text() == expected {
			return
		}
	}
	t.Fatalf("helper exited before %s: %v", expected, scanner.Err())
}

func TestConcurrentDistinctAddsPreserveBothStateEntries(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	home := filepath.Join(root, "home")
	stateHome := filepath.Join(root, "state")
	for _, directory := range []string{project, home, stateHome} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sources := []string{filepath.Join(root, "source-a"), filepath.Join(root, "source-b")}
	for index, source := range sources {
		if err := os.MkdirAll(source, 0o755); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("concurrent-%c", 'a'+index)
		contents := fmt.Sprintf("---\nname: %s\ndescription: concurrent fixture\n---\n", name)
		if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lockPath := filepath.Join(project, "skills-lock.json")
	t.Setenv("XDG_STATE_HOME", stateHome)
	stateSpec, err := stateAdvisoryLockSpec(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	gate := startLockHelper(t, map[string]string{"OPEN_SKILLS_LOCK_PATH": stateSpec.path})
	commands := make([]*exec.Cmd, 0, 2)
	for _, source := range sources {
		arguments, _ := json.Marshal([]string{"add", source, "--agent", "universal", "--yes"})
		command := exec.Command(os.Args[0], "-test.run=^TestAdvisoryCommandHelperProcess$")
		command.Env = append(os.Environ(),
			"OPEN_SKILLS_COMMAND_HELPER=1",
			"OPEN_SKILLS_COMMAND_CWD="+project,
			"OPEN_SKILLS_COMMAND_ARGS="+string(arguments),
			"OPEN_SKILLS_LOCK_TIMEOUT_MS=5000",
			"HOME="+home,
			"XDG_STATE_HOME="+stateHome,
			"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		)
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		command.Stdout, command.Stderr = io.Discard, writer
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		_ = writer.Close()
		scanner := bufio.NewScanner(reader)
		if !scanner.Scan() || !strings.Contains(scanner.Text(), "Waiting for another open-skills process") {
			t.Fatalf("add helper did not reach lock barrier: %q", scanner.Text())
		}
		go func() {
			for scanner.Scan() {
			}
			_ = reader.Close()
		}()
		commands = append(commands, command)
	}
	gate.release(t)
	for index, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("concurrent add %d: %v", index, err)
		}
	}
	document, err := state.Read(lockPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	if document.Entry("concurrent-a") == nil || document.Entry("concurrent-b") == nil || len(document.Skills) != 2 {
		t.Fatalf("concurrent state = %#v", document.Skills)
	}
}

func TestConcurrentTrustApprovalsPreserveBothRecords(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	path, err := truststore.Path()
	if err != nil {
		t.Fatal(err)
	}
	lockPath := trustAdvisoryLockSpec(path).path
	gate := startLockHelper(t, map[string]string{"OPEN_SKILLS_LOCK_PATH": lockPath})

	commands := make([]*exec.Cmd, 0, 2)
	for _, approval := range []string{"source-a\tcommit-a", "source-b\tcommit-b"} {
		command := exec.Command(os.Args[0], "-test.run=^TestAdvisoryLockHelperProcess$")
		command.Env = append(os.Environ(),
			"OPEN_SKILLS_LOCK_HELPER=1",
			"OPEN_SKILLS_LOCK_TIMEOUT_MS=5000",
			"OPEN_SKILLS_LOCK_PATH="+lockPath,
			"OPEN_SKILLS_LOCK_APPROVAL="+approval,
		)
		command.Stdin = strings.NewReader("")
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		command.Stdout, command.Stderr = writer, writer
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		_ = writer.Close()
		scanner := bufio.NewScanner(reader)
		if !scanner.Scan() || !strings.Contains(scanner.Text(), "Waiting for another open-skills process") {
			t.Fatalf("approval helper did not reach lock barrier: %q", scanner.Text())
		}
		go func() {
			for scanner.Scan() {
			}
			_ = reader.Close()
		}()
		commands = append(commands, command)
	}
	gate.release(t)
	for index, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("approval helper %d: %v", index, err)
		}
	}
	store, err := truststore.Open()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(store.Approvals())
	if len(store.Approvals()) != 2 || !strings.Contains(string(data), "source-a") || !strings.Contains(string(data), "source-b") {
		t.Fatalf("concurrent approvals = %s", data)
	}
}
