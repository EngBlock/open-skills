package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	lockTimeoutEnvironment = "OPEN_SKILLS_LOCK_TIMEOUT_MS"
	defaultLockTimeout     = 10 * time.Second
	lockRetryInterval      = 50 * time.Millisecond
)

type advisoryLockMode int

const (
	advisoryLockShared advisoryLockMode = iota
	advisoryLockExclusive
)

type advisoryLockSpec struct {
	path  string
	label string
}

type heldAdvisoryLock struct {
	file  *os.File
	token platformLockToken
}

func invocationContext(invocation Invocation) context.Context {
	if invocation.context == nil {
		return context.Background()
	}
	return invocation.context
}

func withStateAndInstallationLocks(invocation Invocation, lockPath string, installationBases []string, mode advisoryLockMode, operation func() error) error {
	stateSpec, err := stateAdvisoryLockSpec(lockPath)
	if err != nil {
		return err
	}
	if mode == advisoryLockExclusive {
		return withAdvisoryLocks(invocationContext(invocation), invocation.Stderr, []advisoryLockSpec{stateSpec}, mode, func() error {
			recoveryBases, recoveryPending, err := pendingInstallationBases(lockPath)
			if err != nil {
				return err
			}
			bases := append(append([]string(nil), installationBases...), recoveryBases...)
			return withAdvisoryLocks(invocationContext(invocation), invocation.Stderr, installationAdvisoryLockSpecs(bases), mode, func() error {
				if recoveryPending {
					if err := recoverInstallationTransactions(lockPath); err != nil {
						return err
					}
				}
				return operation()
			})
		})
	}

	for {
		pendingRecovery := false
		err := withAdvisoryLocks(invocationContext(invocation), invocation.Stderr, []advisoryLockSpec{stateSpec}, advisoryLockShared, func() error {
			pending, err := hasPendingInstallationRecovery(lockPath)
			if err != nil {
				return err
			}
			if pending {
				pendingRecovery = true
				return nil
			}
			return withAdvisoryLocks(invocationContext(invocation), invocation.Stderr, installationAdvisoryLockSpecs(installationBases), advisoryLockShared, operation)
		})
		if err != nil {
			return err
		}
		if !pendingRecovery {
			return nil
		}
		if err := recoverInstallationState(invocation, lockPath); err != nil {
			return err
		}
	}
}

func runWithStateAndInstallationLocks(invocation Invocation, lockPath string, installationBases []string, mode advisoryLockMode, operation func() int) int {
	result := 1
	err := withStateAndInstallationLocks(invocation, lockPath, installationBases, mode, func() error {
		result = operation()
		return nil
	})
	if err != nil {
		_, _ = fmt.Fprintf(invocation.Stderr, "Acquire advisory locks: %v\n", err)
		return 1
	}
	return result
}

func withAdvisoryLocks(ctx context.Context, stderr io.Writer, specs []advisoryLockSpec, mode advisoryLockMode, operation func() error) error {
	locks, err := acquireAdvisoryLocks(ctx, stderr, specs, mode)
	if err != nil {
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		releaseErr := releaseAdvisoryLocks(locks)
		if releaseErr != nil {
			return fmt.Errorf("%w; release advisory locks: %v", ctx.Err(), releaseErr)
		}
		return ctx.Err()
	}
	operationErr := operation()
	releaseErr := releaseAdvisoryLocks(locks)
	if operationErr != nil && releaseErr != nil {
		return fmt.Errorf("%w; release advisory locks: %v", operationErr, releaseErr)
	}
	if operationErr != nil {
		return operationErr
	}
	if releaseErr != nil {
		return fmt.Errorf("release advisory locks: %w", releaseErr)
	}
	return nil
}

func acquireAdvisoryLocks(ctx context.Context, stderr io.Writer, specs []advisoryLockSpec, mode advisoryLockMode) ([]heldAdvisoryLock, error) {
	timeout, err := configuredLockTimeout()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	specs = normalizedAdvisoryLockSpecs(specs)
	locks := make([]heldAdvisoryLock, 0, len(specs))
	deadline := time.Now().Add(timeout)
	waitingVisible := false
	for _, spec := range specs {
		if err := ctx.Err(); err != nil {
			_ = releaseAdvisoryLocks(locks)
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(spec.path), 0o700); err != nil {
			_ = releaseAdvisoryLocks(locks)
			return nil, fmt.Errorf("create advisory lock directory for %s: %w", spec.label, err)
		}
		file, err := os.OpenFile(spec.path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			_ = releaseAdvisoryLocks(locks)
			return nil, fmt.Errorf("open advisory lock for %s: %w", spec.label, err)
		}
		for {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				_ = releaseAdvisoryLocks(locks)
				return nil, err
			}
			token, acquired, lockErr := tryPlatformLock(file, mode)
			if lockErr != nil {
				_ = file.Close()
				_ = releaseAdvisoryLocks(locks)
				return nil, fmt.Errorf("acquire advisory lock for %s: %w", spec.label, lockErr)
			}
			if acquired {
				locks = append(locks, heldAdvisoryLock{file: file, token: token})
				break
			}
			if !waitingVisible {
				_, _ = fmt.Fprintf(stderr, "Waiting for another open-skills process to release %s (up to %s; configure with %s)…\n", spec.label, formatLockTimeout(timeout), lockTimeoutEnvironment)
				waitingVisible = true
			}
			remaining := time.Until(deadline)
			if remaining <= 0 {
				_ = file.Close()
				_ = releaseAdvisoryLocks(locks)
				return nil, lockTimeoutError(spec, timeout)
			}
			wait := lockRetryInterval
			if remaining < wait {
				wait = remaining
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				_ = file.Close()
				_ = releaseAdvisoryLocks(locks)
				return nil, fmt.Errorf("waiting for %s: %w", spec.label, ctx.Err())
			case <-timer.C:
			}
		}
	}
	return locks, nil
}

func releaseAdvisoryLocks(locks []heldAdvisoryLock) error {
	var failures []string
	for index := len(locks) - 1; index >= 0; index-- {
		lock := locks[index]
		if err := unlockPlatformFile(lock.file, lock.token); err != nil {
			failures = append(failures, err.Error())
		}
		if err := lock.file.Close(); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func configuredLockTimeout() (time.Duration, error) {
	raw, exists := os.LookupEnv(lockTimeoutEnvironment)
	if !exists || strings.TrimSpace(raw) == "" {
		return defaultLockTimeout, nil
	}
	raw = strings.TrimSpace(raw)
	milliseconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || milliseconds < 0 {
		return 0, fmt.Errorf("%s must be a non-negative decimal number of milliseconds; managed state was not touched", lockTimeoutEnvironment)
	}
	maximum := int64((time.Duration(1<<63 - 1)) / time.Millisecond)
	if milliseconds > maximum {
		return 0, fmt.Errorf("%s is too large; managed state was not touched", lockTimeoutEnvironment)
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func normalizedAdvisoryLockSpecs(specs []advisoryLockSpec) []advisoryLockSpec {
	byPath := make(map[string]advisoryLockSpec, len(specs))
	for _, spec := range specs {
		if spec.path == "" {
			continue
		}
		spec.path = cleanAbsolutePath(spec.path)
		if spec.label == "" {
			spec.label = "advisory lock"
		}
		if _, exists := byPath[spec.path]; !exists {
			byPath[spec.path] = spec
		}
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]advisoryLockSpec, 0, len(paths))
	for _, path := range paths {
		result = append(result, byPath[path])
	}
	return result
}

func stateAdvisoryLockSpec(lockPath string) (advisoryLockSpec, error) {
	return advisoryLockSpec{path: stableAdvisoryLockPath("state", lockPath), label: "state mutation lock"}, nil
}

func trustAdvisoryLockSpec(path string) advisoryLockSpec {
	return advisoryLockSpec{path: stableAdvisoryLockPath("trust", path), label: "trust mutation lock"}
}

func stableAdvisoryLockPath(kind, resource string) string {
	hash := sha256.Sum256([]byte(kind + "\x00" + canonicalAdvisoryResource(resource)))
	return filepath.Join(platformAdvisoryLockRoot(), hex.EncodeToString(hash[:12])+".lock")
}

func canonicalAdvisoryResource(resource string) string {
	resource = cleanAbsolutePath(resource)
	if resolved, err := filepath.EvalSymlinks(resource); err == nil {
		resource = cleanAbsolutePath(resolved)
	}
	return canonicalAdvisoryResourceCase(resource, runtime.GOOS)
}

func canonicalAdvisoryResourceCase(resource, goos string) string {
	if goos == "windows" {
		return strings.ToLower(resource)
	}
	return resource
}

func installationAdvisoryLockSpecs(bases []string) []advisoryLockSpec {
	result := make([]advisoryLockSpec, 0, len(bases))
	for _, base := range bases {
		base = canonicalAdvisoryResource(base)
		result = append(result, advisoryLockSpec{
			path:  stableAdvisoryLockPath("installation", base),
			label: "installation mutation lock",
		})
	}
	return result
}

func formatLockTimeout(timeout time.Duration) string {
	if timeout%time.Second == 0 {
		return timeout.String()
	}
	return fmt.Sprintf("%dms", timeout.Milliseconds())
}

func lockTimeoutError(spec advisoryLockSpec, timeout time.Duration) error {
	return fmt.Errorf("timed out after %s waiting for another open-skills process to release %s at %s; wait for the other command to finish or increase %s", formatLockTimeout(timeout), spec.label, spec.path, lockTimeoutEnvironment)
}
