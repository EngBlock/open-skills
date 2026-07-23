package application

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	defaultMaxFileBytes      int64 = 10 << 20
	defaultMaxTotalBytes     int64 = 100 << 20
	defaultMaxFiles                = 5_000
	defaultMaxDepth                = 20
	acquisitionMaxFileBytes  int64 = 512 << 20
	acquisitionMaxTotalBytes int64 = 512 << 20
	acquisitionMaxFiles            = 20_000
	acquisitionMaxDepth            = 100
)

// resourceLimits is the finite selected-content and traversal policy shared by
// remote commands. Acquisition derives a larger, still-finite staging policy so
// unrelated repository content does not consume the selected-content budget.
// Exactly-at-limit is allowed; only exceeding a limit fails.
type resourceLimits struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxFiles      int
	MaxDepth      int
	overrides     resourceLimitOverrides
}

type resourceLimitOverrides struct {
	fileBytes  bool
	totalBytes bool
	files      bool
	depth      bool
}

func defaultResourceLimits() resourceLimits {
	return resourceLimits{
		MaxFileBytes:  defaultMaxFileBytes,
		MaxTotalBytes: defaultMaxTotalBytes,
		MaxFiles:      defaultMaxFiles,
		MaxDepth:      defaultMaxDepth,
	}
}

func acquisitionResourceLimits(selected resourceLimits) resourceLimits {
	limits := selected
	if limits.MaxFileBytes < acquisitionMaxFileBytes {
		limits.MaxFileBytes = acquisitionMaxFileBytes
	}
	if limits.MaxTotalBytes < acquisitionMaxTotalBytes {
		limits.MaxTotalBytes = acquisitionMaxTotalBytes
	}
	if limits.MaxFiles < acquisitionMaxFiles {
		limits.MaxFiles = acquisitionMaxFiles
	}
	if limits.MaxDepth < acquisitionMaxDepth {
		limits.MaxDepth = acquisitionMaxDepth
	}
	return limits
}

func unlimitedContentResourceLimits() resourceLimits {
	return resourceLimits{
		MaxFileBytes:  math.MaxInt64 - 1,
		MaxTotalBytes: math.MaxInt64 - 1,
		MaxFiles:      math.MaxInt,
		MaxDepth:      math.MaxInt,
	}
}

func (limits resourceLimits) hasRemoteOverrides() bool {
	return limits.overrides.fileBytes || limits.overrides.totalBytes || limits.overrides.files
}

type resourceLimitError struct {
	message string
}

func (err *resourceLimitError) Error() string { return err.message }

func isResourceLimitError(err error) bool {
	var limitErr *resourceLimitError
	return errors.As(err, &limitErr)
}

func resourceError(format string, arguments ...any) error {
	return &resourceLimitError{message: fmt.Sprintf(format, arguments...)}
}

type resourceBudget struct {
	limits resourceLimits
	files  int
	bytes  int64
}

func newResourceBudget(limits resourceLimits) *resourceBudget {
	return &resourceBudget{limits: limits}
}

func (budget *resourceBudget) addFile(relativePath string, size int64) error {
	if size < 0 {
		return resourceError("resource limit check received a negative size for %q", relativePath)
	}
	if size > budget.limits.MaxFileBytes {
		return resourceError("file %q exceeds the %d-byte per-file limit (%d bytes); raise it with --max-file-bytes", relativePath, budget.limits.MaxFileBytes, size)
	}
	if budget.files >= budget.limits.MaxFiles {
		return resourceError("content exceeds the %d-file limit at %q; raise it with --max-files", budget.limits.MaxFiles, relativePath)
	}
	if size > budget.limits.MaxTotalBytes-budget.bytes {
		return resourceError("content exceeds the %d-byte total limit at %q; raise it with --max-total-bytes", budget.limits.MaxTotalBytes, relativePath)
	}
	budget.files++
	budget.bytes += size
	return nil
}

func pathDirectoryDepth(relativePath string) int {
	trimmed := strings.Trim(strings.ReplaceAll(relativePath, "\\", "/"), "/")
	if trimmed == "" {
		return 0
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) <= 1 {
		return 0
	}
	return len(parts) - 1
}

func (limits resourceLimits) checkDepth(relativePath string, depth int) error {
	if depth > limits.MaxDepth {
		return resourceError("content traversal exceeds the maximum depth of %d at %q; raise it with --max-depth", limits.MaxDepth, relativePath)
	}
	return nil
}

// parseResourceLimitOption parses one dedicated finite override. Byte values are
// positive decimal bytes so scripts and exact-boundary tests have no unit
// ambiguity.
func parseResourceLimitOption(arguments []string, index int, limits *resourceLimits) (bool, int, error) {
	if index < 0 || index >= len(arguments) {
		return false, index, nil
	}
	argument := arguments[index]
	var seen *bool
	var assign func(int64) error
	switch argument {
	case "--max-file-bytes":
		seen = &limits.overrides.fileBytes
		assign = func(value int64) error {
			if value > math.MaxInt64-1 {
				return fmt.Errorf("%s value is too large", argument)
			}
			limits.MaxFileBytes = value
			return nil
		}
	case "--max-total-bytes":
		seen = &limits.overrides.totalBytes
		assign = func(value int64) error {
			if value > math.MaxInt64-1 {
				return fmt.Errorf("%s value is too large", argument)
			}
			limits.MaxTotalBytes = value
			return nil
		}
	case "--max-files":
		seen = &limits.overrides.files
		assign = func(value int64) error {
			if value > int64(math.MaxInt) {
				return fmt.Errorf("%s value is too large", argument)
			}
			limits.MaxFiles = int(value)
			return nil
		}
	case "--max-depth":
		seen = &limits.overrides.depth
		assign = func(value int64) error {
			if value > int64(math.MaxInt) {
				return fmt.Errorf("%s value is too large", argument)
			}
			limits.MaxDepth = int(value)
			return nil
		}
	default:
		return false, index, nil
	}
	if *seen {
		return true, index, fmt.Errorf("%s may be provided only once", argument)
	}
	if index+1 >= len(arguments) || strings.HasPrefix(arguments[index+1], "-") {
		return true, index, fmt.Errorf("%s requires a positive decimal value", argument)
	}
	value, err := strconv.ParseInt(arguments[index+1], 10, 64)
	if err != nil || value <= 0 {
		return true, index, fmt.Errorf("%s requires a positive decimal value", argument)
	}
	if err := assign(value); err != nil {
		return true, index, err
	}
	*seen = true
	return true, index + 1, nil
}
