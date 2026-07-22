// Package application owns native command orchestration. It is internal so the
// supported contract remains the open-skills process, not a Go library.
package application

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

type Invocation struct {
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Version is the native preview version. Release builds may replace it with
// -ldflags without requiring package metadata at runtime.
var Version = "0.1.2"

func Run(ctx context.Context, invocation Invocation) int {
	_ = ctx
	if len(invocation.Args) == 0 {
		if !runningInAgent() {
			_, _ = fmt.Fprint(invocation.Stdout, banner)
		}
		return 0
	}

	command := invocation.Args[0]
	if command == "find" || command == "search" || command == "f" || command == "s" {
		_, _ = fmt.Fprint(invocation.Stdout, findMigrationGuidance)
		return 1
	}

	if hasHelpFlag(invocation.Args[1:]) && command != "--help" && command != "-h" && command != "--version" && command != "-v" {
		if command == "remove" || command == "rm" || command == "r" {
			_, _ = fmt.Fprint(invocation.Stdout, removeHelp)
		} else {
			_, _ = fmt.Fprint(invocation.Stdout, help)
		}
		return 0
	}

	switch command {
	case "--help", "-h":
		_, _ = fmt.Fprint(invocation.Stdout, help)
		return 0
	case "--version", "-v":
		_, _ = fmt.Fprintln(invocation.Stdout, Version)
		return 0
	case "init":
		if !runningInAgent() {
			_, _ = fmt.Fprint(invocation.Stdout, logo)
		}
		_, _ = fmt.Fprintln(invocation.Stdout)
		return runInit(invocation, invocation.Args[1:])
	case "list", "ls":
		return runList(invocation, invocation.Args[1:])
	case "add", "a", "install", "i":
		return runAddLocal(invocation, invocation.Args[1:])
	default:
		_, _ = fmt.Fprintf(invocation.Stdout, "Unknown command: %s\nRun open-skills --help for usage.\n", command)
		return 1
	}
}

func hasHelpFlag(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "--help" || argument == "-h" {
			return true
		}
	}
	return false
}

func runningInAgent() bool {
	if strings.TrimSpace(os.Getenv("AI_AGENT")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("CURSOR_AGENT")) != "" || os.Getenv("CURSOR_EXTENSION_HOST_ROLE") == "agent-exec" {
		return true
	}
	for _, name := range []string{
		"GEMINI_CLI",
		"CODEX_SANDBOX",
		"CODEX_CI",
		"CODEX_THREAD_ID",
		"ANTIGRAVITY_AGENT",
		"AUGMENT_AGENT",
		"OPENCODE_CLIENT",
		"CLAUDECODE",
		"CLAUDE_CODE",
		"REPL_ID",
		"COPILOT_MODEL",
		"COPILOT_ALLOW_ALL",
		"COPILOT_GITHUB_TOKEN",
	} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	_, err := os.Stat("/opt/.devin")
	return err == nil
}
