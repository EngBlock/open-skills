package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	nativerelease "github.com/EngBlock/open-skills/internal/release"
)

func main() {
	version := flag.String("version", "", "0.2.0 prerelease version without the v prefix")
	output := flag.String("output", "dist/native-preview", "artifact output directory")
	notes := flag.String("notes", "", "optional release-notes output path")
	flag.Parse()
	if flag.NArg() != 0 {
		fail("unexpected positional arguments")
	}

	root, err := os.Getwd()
	if err != nil {
		fail("resolve repository root: %v", err)
	}
	artifacts, err := nativerelease.PackageAll(context.Background(), nativerelease.PackageOptions{
		Root:         root,
		Output:       *output,
		Version:      *version,
		RequireSmoke: true,
	})
	if err != nil {
		fail("package native preview: %v", err)
	}
	if *notes != "" {
		content, err := nativerelease.ReleaseNotes(*version)
		if err != nil {
			fail("generate release notes: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(*notes), 0o755); err != nil {
			fail("create release-notes directory: %v", err)
		}
		if err := os.WriteFile(*notes, []byte(content), 0o644); err != nil {
			fail("write release notes: %v", err)
		}
	}
	for _, artifact := range artifacts {
		status := artifact.Support
		if artifact.SmokeTested {
			status += ", smoke-tested"
		}
		fmt.Printf("%s (%s/%s, %s)\n", artifact.Filename, artifact.GOOS, artifact.GOARCH, status)
	}
}

func fail(format string, arguments ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
