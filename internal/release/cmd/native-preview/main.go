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
	version := flag.String("version", "", "0.2.0 release or prerelease version without the v prefix")
	output := flag.String("output", "dist/native-preview", "artifact output directory")
	notes := flag.String("notes", "", "optional release-notes output path")
	homebrewFormula := flag.String("homebrew-formula", "", "optional Homebrew formula output path")
	scoopManifest := flag.String("scoop-manifest", "", "optional Scoop manifest output path")
	skipLinuxSmoke := flag.Bool("skip-linux-smoke", false, "allow local cross-packaging without the Linux x86-64 built-binary smoke test")
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
		RequireSmoke: !*skipLinuxSmoke,
	})
	if err != nil {
		fail("package native release: %v", err)
	}
	if *homebrewFormula != "" {
		checksums, err := os.Open(filepath.Join(*output, "checksums.txt"))
		if err != nil {
			fail("open release checksums for Homebrew formula: %v", err)
		}
		content, formulaErr := nativerelease.HomebrewFormula(*version, checksums)
		closeErr := checksums.Close()
		if formulaErr != nil {
			fail("generate Homebrew formula: %v", formulaErr)
		}
		if closeErr != nil {
			fail("close release checksums: %v", closeErr)
		}
		if err := os.MkdirAll(filepath.Dir(*homebrewFormula), 0o755); err != nil {
			fail("create Homebrew formula directory: %v", err)
		}
		if err := os.WriteFile(*homebrewFormula, []byte(content), 0o644); err != nil {
			fail("write Homebrew formula: %v", err)
		}
	}
	if *scoopManifest != "" {
		checksums, err := os.Open(filepath.Join(*output, "checksums.txt"))
		if err != nil {
			fail("open release checksums for Scoop manifest: %v", err)
		}
		content, manifestErr := nativerelease.ScoopManifest(*version, checksums)
		closeErr := checksums.Close()
		if manifestErr != nil {
			fail("generate Scoop manifest: %v", manifestErr)
		}
		if closeErr != nil {
			fail("close release checksums: %v", closeErr)
		}
		if err := os.MkdirAll(filepath.Dir(*scoopManifest), 0o755); err != nil {
			fail("create Scoop manifest directory: %v", err)
		}
		if err := os.WriteFile(*scoopManifest, []byte(content), 0o644); err != nil {
			fail("write Scoop manifest: %v", err)
		}
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
