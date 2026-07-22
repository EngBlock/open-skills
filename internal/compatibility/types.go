// Package compatibility provides the private process-level seam used to compare
// the frozen npm oracle with the native executable.
package compatibility

import (
	"io/fs"
	"net/http"
	"time"
)

// Target is an opaque executable invocation. Args are prepended to scenario
// arguments, which lets the npm oracle use an explicit Node entrypoint.
type Target struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

type Harness struct {
	Oracle Target
	Native Target
}

type FixtureRoot string

const (
	ProjectRoot FixtureRoot = "project"
	HomeRoot    FixtureRoot = "home"
	TempRoot    FixtureRoot = "tmp"
)

type FileFixture struct {
	Root      FixtureRoot
	Path      string
	Data      []byte
	Mode      fs.FileMode
	Symlink   string
	Directory bool
}

type RepositoryFixture struct {
	Name  string
	Files map[string][]byte
}

type HTTPRoute struct {
	Method string
	Path   string
	Query  string
	Status int
	Header http.Header
	Body   []byte
}

type CommandFixture struct {
	Name     string
	Stdout   string
	Stderr   string
	ExitCode int
}

type Scenario struct {
	// Offline makes captured HTTP requests and child commands a harness failure.
	// The sandbox routes standard proxy traffic to its recorder and exposes only
	// recording stubs for common network-capable commands. The shell suite pairs
	// this process check with a dependency boundary and network-disabled CI.
	Offline      bool
	Args         []string
	Stdin        []byte
	Env          map[string]string
	Files        []FileFixture
	Repositories []RepositoryFixture
	HTTPRoutes   []HTTPRoute
	Commands     []CommandFixture
	Timeout      time.Duration
}

type Pair struct {
	Oracle Observation
	Native Observation
}

type SandboxPaths struct {
	Root       string
	Home       string
	Project    string
	Temp       string
	FixtureURL string
}

type FileKind string

const (
	FileKindDirectory FileKind = "directory"
	FileKindRegular   FileKind = "file"
	FileKindSymlink   FileKind = "symlink"
)

type FileState struct {
	Kind       FileKind
	Mode       fs.FileMode
	Data       []byte
	LinkTarget string
}

type LockLocation string

const (
	ProjectLock      LockLocation = "project"
	XDGGlobalLock    LockLocation = "global-xdg"
	LegacyGlobalLock LockLocation = "global-legacy"
)

type HTTPRequest struct {
	Method string
	Path   string
	Host   string
	Header http.Header
	Body   []byte
}

type SpawnedCommand struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
}

type Observation struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	TimedOut        bool
	ProcessError    string
	Files           map[string]FileState
	Locks           map[LockLocation][]byte
	ParsedLocks     map[LockLocation]any
	LockParseErrors map[LockLocation]string
	HTTPRequests    []HTTPRequest
	SpawnedCommands []SpawnedCommand
	Paths           SandboxPaths
}

type Replacement struct {
	Pattern string
	With    string
}

type Normalization struct {
	Replacements []Replacement
	// TextFiles identifies filesystem entries whose contents are presentation
	// text. All other file bytes remain exact, including valid-UTF-8 binaries.
	TextFiles []string
}

type Difference struct {
	Field  string
	Oracle any
	Native any
}
