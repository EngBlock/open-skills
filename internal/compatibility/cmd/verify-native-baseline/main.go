package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/EngBlock/open-skills/internal/compatibility"
)

func main() {
	manifestPath := flag.String("manifest", "compatibility/npm-0.1.2/oracle.json", "path to the immutable npm baseline manifest")
	flag.Parse()
	if flag.NArg() != 0 {
		fail("unexpected positional arguments")
	}

	manifest, err := compatibility.ReadBaselineManifest(*manifestPath)
	if err != nil {
		fail("read baseline manifest: %v", err)
	}
	token, err := compatibility.GitHubToken(context.Background())
	if err != nil {
		fail("%v", err)
	}
	if err := compatibility.VerifyBaseline(context.Background(), manifest, nil, token); err != nil {
		fail("%v", err)
	}
	fmt.Printf("Verified %s@%s\n", manifest.Package.Name, manifest.Package.Version)
	fmt.Printf("  artifact sha512: %s\n", manifest.Artifact.SHA512)
	fmt.Printf("  %s: %s -> %s\n", manifest.Source.Tag, manifest.Source.TagObject, manifest.Source.Commit)
}

func fail(format string, arguments ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
