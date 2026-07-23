package compatibility

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultScenarioTimeout = 30 * time.Second

func (h Harness) RunBoth(ctx context.Context, scenario Scenario) (Pair, error) {
	oracle, err := h.Run(ctx, h.Oracle, scenario)
	if err != nil {
		return Pair{}, fmt.Errorf("run oracle: %w", err)
	}
	native, err := h.Run(ctx, h.Native, scenario)
	if err != nil {
		return Pair{}, fmt.Errorf("run native: %w", err)
	}
	return Pair{Oracle: oracle, Native: native}, nil
}

// Run materializes an isolated sandbox and invokes target directly, never
// through a shell. A nonzero target exit is an observation, not a harness error.
func (h Harness) Run(ctx context.Context, target Target, scenario Scenario) (Observation, error) {
	if target.Command == "" {
		return Observation{}, errors.New("target command is required")
	}

	root, err := os.MkdirTemp("", "open-skills-compat-")
	if err != nil {
		return Observation{}, err
	}
	defer os.RemoveAll(root)

	paths := SandboxPaths{
		Root:    root,
		Home:    filepath.Join(root, "home"),
		Project: filepath.Join(root, "project"),
		Temp:    filepath.Join(root, "tmp"),
	}
	control := filepath.Join(root, "control")
	for _, directory := range []string{paths.Home, paths.Project, paths.Temp, filepath.Join(root, "repositories"), control} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return Observation{}, err
		}
	}

	repositories, err := materializeRepositories(ctx, root, scenario.Repositories)
	if err != nil {
		return Observation{}, err
	}

	var requestMu sync.Mutex
	requests := make([]HTTPRequest, 0)
	server := newFixtureServer(scenario.HTTPRoutes, &requestMu, &requests)
	defer server.Close()
	paths.FixtureURL = server.URL

	expand := func(value string) string {
		value = strings.ReplaceAll(value, "{{root:json}}", filepath.ToSlash(paths.Root))
		value = strings.ReplaceAll(value, "{{home:json}}", filepath.ToSlash(paths.Home))
		value = strings.ReplaceAll(value, "{{project:json}}", filepath.ToSlash(paths.Project))
		value = strings.ReplaceAll(value, "{{temp:json}}", filepath.ToSlash(paths.Temp))
		value = strings.ReplaceAll(value, "{{root}}", paths.Root)
		value = strings.ReplaceAll(value, "{{home}}", paths.Home)
		value = strings.ReplaceAll(value, "{{project}}", paths.Project)
		value = strings.ReplaceAll(value, "{{temp}}", paths.Temp)
		value = strings.ReplaceAll(value, "{{http:url}}", server.URL)
		for name, path := range repositories {
			value = strings.ReplaceAll(value, "{{repo:"+name+"}}", path)
		}
		return value
	}

	if err := materializeFiles(paths, scenario.Files, expand); err != nil {
		return Observation{}, err
	}

	environment := isolatedEnvironment(paths)
	if err := mergeEnvironment(environment, target.Env, expand); err != nil {
		return Observation{}, fmt.Errorf("target environment: %w", err)
	}
	if err := mergeEnvironment(environment, scenario.Env, expand); err != nil {
		return Observation{}, fmt.Errorf("scenario environment: %w", err)
	}
	if scenario.Offline {
		// Standard HTTP clients are forced through the recorder. Direct child
		// tools are replaced below with recording stubs, while every other
		// executable remains unavailable through the empty PATH.
		environment["HTTP_PROXY"] = server.URL
		environment["HTTPS_PROXY"] = server.URL
		environment["ALL_PROXY"] = server.URL
		environment["NO_PROXY"] = ""
	}

	fixtures := append([]CommandFixture{}, scenario.Commands...)
	if scenario.Offline {
		fixtures = appendOfflineCommandFixtures(fixtures)
	}
	spawnLog := filepath.Join(control, "spawned.jsonl")
	environment["PATH"] = ""
	if len(fixtures) > 0 {
		fixtureBin, behaviors, err := installCommandFixtures(ctx, control, fixtures)
		if err != nil {
			return Observation{}, err
		}
		// A fixture-only PATH makes undeclared subprocesses fail closed instead of
		// escaping into the developer or CI host environment.
		environment["PATH"] = fixtureBin
		environment["OPEN_SKILLS_HARNESS_COMMAND_LOG"] = spawnLog
		environment["OPEN_SKILLS_HARNESS_COMMAND_BEHAVIORS"] = behaviors
	}

	args := append([]string{}, target.Args...)
	for _, arg := range scenario.Args {
		args = append(args, expand(arg))
	}
	timeout := scenario.Timeout
	if timeout <= 0 {
		timeout = defaultScenarioTimeout
	}
	runContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	command := exec.Command(target.Command, args...)
	command.Dir = paths.Project
	command.Env = environmentList(environment)
	command.Stdin = bytes.NewReader(scenario.Stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := runProcess(runContext, command)

	observation := Observation{
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		ExitCode:        0,
		Files:           map[string]FileState{},
		Locks:           map[LockLocation][]byte{},
		ParsedLocks:     map[LockLocation]any{},
		LockParseErrors: map[LockLocation]string{},
		HTTPRequests:    []HTTPRequest{},
		SpawnedCommands: []SpawnedCommand{},
		Paths:           paths,
	}
	if runErr != nil {
		var exitError *exec.ExitError
		switch {
		case runContext.Err() != nil:
			observation.TimedOut = errors.Is(runContext.Err(), context.DeadlineExceeded)
			observation.ProcessError = runErr.Error()
			observation.ExitCode = -1
		case errors.As(runErr, &exitError):
			observation.ExitCode = exitError.ExitCode()
		default:
			observation.ProcessError = runErr.Error()
			observation.ExitCode = -1
		}
	}

	observation.Files, err = snapshotSandbox(root, "control")
	if err != nil {
		return Observation{}, err
	}
	captureLocks(&observation, environment)
	requestMu.Lock()
	observation.HTTPRequests = append(observation.HTTPRequests, requests...)
	requestMu.Unlock()
	observation.SpawnedCommands, err = readSpawnLog(spawnLog)
	if err != nil {
		return Observation{}, err
	}
	if scenario.Offline && (len(observation.HTTPRequests) > 0 || len(observation.SpawnedCommands) > 0) {
		return observation, fmt.Errorf("offline scenario captured a network attempt: %d HTTP request(s), %d child command(s)", len(observation.HTTPRequests), len(observation.SpawnedCommands))
	}
	return observation, nil
}

func appendOfflineCommandFixtures(fixtures []CommandFixture) []CommandFixture {
	configured := make(map[string]struct{}, len(fixtures))
	for _, fixture := range fixtures {
		configured[fixture.Name] = struct{}{}
	}
	for _, name := range []string{"curl", "wget", "git", "gh", "node", "npm", "npx", "powershell", "pwsh"} {
		if _, ok := configured[name]; !ok {
			fixtures = append(fixtures, CommandFixture{Name: name, ExitCode: 125})
		}
	}
	return fixtures
}

func isolatedEnvironment(paths SandboxPaths) map[string]string {
	// Deliberately do not inherit tokens, proxies, Git configuration, agent
	// detection, or runtime injection variables from the host.
	environment := make(map[string]string)
	for _, name := range []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value := os.Getenv(name); value != "" {
			environment[environmentName(name)] = value
		}
	}
	environment["HOME"] = paths.Home
	environment["USERPROFILE"] = paths.Home
	environment["XDG_CONFIG_HOME"] = filepath.Join(paths.Home, ".config")
	environment["XDG_DATA_HOME"] = filepath.Join(paths.Home, ".local", "share")
	environment["XDG_STATE_HOME"] = filepath.Join(paths.Home, ".local", "state")
	environment["XDG_CACHE_HOME"] = filepath.Join(paths.Home, ".cache")
	environment["APPDATA"] = filepath.Join(paths.Home, "AppData", "Roaming")
	environment["LOCALAPPDATA"] = filepath.Join(paths.Home, "AppData", "Local")
	environment["TMPDIR"] = paths.Temp
	environment["TMP"] = paths.Temp
	environment["TEMP"] = paths.Temp
	environment["OPEN_SKILLS_PROJECT"] = paths.Project
	environment["GIT_CONFIG_GLOBAL"] = os.DevNull
	environment["GIT_CONFIG_NOSYSTEM"] = "1"
	environment["GIT_TERMINAL_PROMPT"] = "0"
	environment["LC_ALL"] = "C"
	environment["LANG"] = "C"
	environment["TZ"] = "UTC"
	return environment
}

func mergeEnvironment(destination map[string]string, source map[string]string, expand func(string) string) error {
	for name, value := range source {
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return fmt.Errorf("invalid environment variable name %q", name)
		}
		destination[environmentName(name)] = expand(value)
	}
	return nil
}

func environmentName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func environmentList(environment map[string]string) []string {
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+environment[name])
	}
	return result
}

func materializeFiles(paths SandboxPaths, fixtures []FileFixture, expand func(string) string) error {
	roots := map[FixtureRoot]string{ProjectRoot: paths.Project, HomeRoot: paths.Home, TempRoot: paths.Temp}
	for _, fixture := range fixtures {
		root, ok := roots[fixture.Root]
		if !ok {
			return fmt.Errorf("unknown fixture root %q", fixture.Root)
		}
		path, err := confinedPath(root, fixture.Path)
		if err != nil {
			return fmt.Errorf("fixture %q: %w", fixture.Path, err)
		}
		mode := fixture.Mode
		if mode == 0 {
			if fixture.Directory {
				mode = 0o755
			} else {
				mode = 0o644
			}
		}
		if err := rejectSymlinkComponents(root, path); err != nil {
			return fmt.Errorf("fixture %q: %w", fixture.Path, err)
		}
		if fixture.Directory {
			if err := os.MkdirAll(path, mode.Perm()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if fixture.Symlink != "" || fixture.Junction != "" {
			if fixture.Symlink != "" && fixture.Junction != "" {
				return fmt.Errorf("fixture %q: choose either symlink or junction", fixture.Path)
			}
			target := fixture.Symlink
			if fixture.Junction != "" {
				target = fixture.Junction
			}
			target = expand(target)
			resolved := target
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(path), resolved)
			}
			if !pathWithin(paths.Root, resolved) {
				return fmt.Errorf("fixture %q: symlink target escapes sandbox", fixture.Path)
			}
			if fixture.Junction != "" {
				if err := createFixtureJunction(path, resolved); err != nil {
					return err
				}
			} else if err := os.Symlink(target, path); err != nil {
				return err
			}
			continue
		}
		if err := os.WriteFile(path, []byte(expand(string(fixture.Data))), mode.Perm()); err != nil {
			return err
		}
	}
	return nil
}

func createFixtureJunction(path, target string) error {
	if runtime.GOOS != "windows" {
		return os.Symlink(target, path)
	}
	commandLine := fmt.Sprintf(`mklink /J "%s" "%s"`, strings.ReplaceAll(path, `"`, `""`), strings.ReplaceAll(target, `"`, `""`))
	command := exec.Command("cmd.exe", "/d", "/s", "/c", commandLine)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("create fixture junction: %w: %s", err, output)
	}
	return nil
}

func materializeRepositories(ctx context.Context, root string, fixtures []RepositoryFixture) (map[string]string, error) {
	result := make(map[string]string, len(fixtures))
	for _, fixture := range fixtures {
		if fixture.Name == "" || fixture.Name == "." || fixture.Name == ".." || filepath.Base(fixture.Name) != fixture.Name {
			return nil, fmt.Errorf("invalid repository fixture name %q", fixture.Name)
		}
		path := filepath.Join(root, "repositories", fixture.Name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, err
		}
		for relative, data := range fixture.Files {
			if containsGitMetadataPath(relative) {
				return nil, fmt.Errorf("repository %q: fixture path %q contains reserved .git metadata", fixture.Name, relative)
			}
			file, err := confinedPath(path, relative)
			if err != nil {
				return nil, fmt.Errorf("repository %q: %w", fixture.Name, err)
			}
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(file, data, 0o644); err != nil {
				return nil, err
			}
		}
		gitHome := filepath.Join(root, "git-home")
		if err := os.MkdirAll(gitHome, 0o755); err != nil {
			return nil, err
		}
		commands := [][]string{{"init", "-q", "-b", "main"}, {"-c", "core.hooksPath=" + os.DevNull, "add", "."}, {"-c", "core.hooksPath=" + os.DevNull, "commit", "-q", "-m", "fixture"}}
		for _, args := range commands {
			commandArgs := append([]string{"-c", "core.autocrlf=false"}, args...)
			command := exec.CommandContext(ctx, "git", commandArgs...)
			command.Dir = path
			command.Env = append(gitHostEnvironment(),
				"HOME="+gitHome, "XDG_CONFIG_HOME="+filepath.Join(gitHome, ".config"),
				"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
				"GIT_AUTHOR_NAME=Open Skills Harness", "GIT_AUTHOR_EMAIL=harness@example.invalid",
				"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
				"GIT_COMMITTER_NAME=Open Skills Harness", "GIT_COMMITTER_EMAIL=harness@example.invalid",
				"GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
			)
			if output, err := command.CombinedOutput(); err != nil {
				return nil, fmt.Errorf("initialize repository %q with git %s: %w: %s", fixture.Name, args[0], err, output)
			}
		}
		result[fixture.Name] = path
	}
	return result, nil
}

const maxFixtureRequestBody = 1 << 20

func newFixtureServer(routes []HTTPRoute, mu *sync.Mutex, requests *[]HTTPRequest) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(io.LimitReader(request.Body, maxFixtureRequestBody+1))
		_ = request.Body.Close()
		mu.Lock()
		*requests = append(*requests, HTTPRequest{
			Method: request.Method,
			Path:   request.URL.RequestURI(),
			Host:   request.Host,
			Header: request.Header.Clone(),
			Body:   body,
		})
		mu.Unlock()
		if err != nil || len(body) > maxFixtureRequestBody {
			http.Error(response, "fixture request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		for _, route := range routes {
			if request.Method == route.Method && request.URL.Path == route.Path && (route.Query == "" || request.URL.RawQuery == route.Query) {
				for name, values := range route.Header {
					for _, value := range values {
						response.Header().Add(name, value)
					}
				}
				status := route.Status
				if status == 0 {
					status = http.StatusOK
				}
				response.WriteHeader(status)
				_, _ = response.Write(route.Body)
				return
			}
		}
		http.NotFound(response, request)
	}))
}

func captureLocks(observation *Observation, environment map[string]string) {
	home := environment[environmentName("HOME")]
	xdgState := environment[environmentName("XDG_STATE_HOME")]
	locations := map[LockLocation]string{
		ProjectLock:      filepath.Join(observation.Paths.Project, "skills-lock.json"),
		XDGGlobalLock:    filepath.Join(xdgState, "skills", ".skill-lock.json"),
		LegacyGlobalLock: filepath.Join(home, ".agents", ".skill-lock.json"),
	}
	for location, path := range locations {
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			observation.LockParseErrors[location] = "lock path is not a regular file"
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			observation.LockParseErrors[location] = err.Error()
			continue
		}
		observation.Locks[location] = data
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			observation.LockParseErrors[location] = err.Error()
		} else {
			observation.ParsedLocks[location] = value
		}
	}
}

func readSpawnLog(path string) ([]SpawnedCommand, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []SpawnedCommand{}, nil
	}
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	result := make([]SpawnedCommand, 0, len(lines))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var event SpawnedCommand
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("decode spawned command: %w", err)
		}
		result = append(result, event)
	}
	return result, nil
}

func rejectSymlinkComponents(root, parent string) error {
	relative, err := filepath.Rel(root, parent)
	if err != nil {
		return err
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("parent path contains a symlink")
		}
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func containsGitMetadataPath(relative string) bool {
	for _, component := range strings.FieldsFunc(filepath.ToSlash(relative), func(r rune) bool { return r == '/' }) {
		if strings.EqualFold(component, ".git") {
			return true
		}
	}
	return false
}

func gitHostEnvironment() []string {
	result := []string{}
	for _, name := range []string{"PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value := os.Getenv(name); value != "" {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func confinedPath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", errors.New("path must be nonempty and relative")
	}
	clean := filepath.Clean(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes fixture root")
	}
	return filepath.Join(root, clean), nil
}

func moduleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
