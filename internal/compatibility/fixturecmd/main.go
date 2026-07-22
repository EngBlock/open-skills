// fixturecmd is an internal test executable built on demand by the process
// harness. It is never part of native distribution staging.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type behavior struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

type event struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
}

func main() {
	name := filepath.Base(os.Args[0])
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	}
	cwd, _ := os.Getwd()
	encoded, err := json.Marshal(event{Name: name, Args: os.Args[1:], Cwd: cwd})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	logPath := os.Getenv("OPEN_SKILLS_HARNESS_COMMAND_LOG")
	log, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	_, writeErr := log.Write(append(encoded, '\n'))
	closeErr := log.Close()
	if writeErr != nil || closeErr != nil {
		fmt.Fprintln(os.Stderr, "could not record command fixture")
		os.Exit(125)
	}

	configuration, err := os.ReadFile(os.Getenv("OPEN_SKILLS_HARNESS_COMMAND_BEHAVIORS"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	behaviors := map[string]behavior{}
	if err := json.Unmarshal(configuration, &behaviors); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	selected, ok := behaviors[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "no command fixture configured for %s\n", name)
		os.Exit(125)
	}
	_, _ = os.Stdout.WriteString(selected.Stdout)
	_, _ = os.Stderr.WriteString(selected.Stderr)
	os.Exit(selected.ExitCode)
}
