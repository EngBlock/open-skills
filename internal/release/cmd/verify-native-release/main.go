package main

import (
	"flag"
	"fmt"
	"os"

	nativerelease "github.com/EngBlock/open-skills/internal/release"
)

func main() {
	tag := flag.String("tag", "", "signed release tag (for example, v0.2.0 or v0.2.0-preview.1)")
	output := flag.String("output", "native-dist", "release artifact directory")
	flag.Parse()
	if flag.NArg() != 0 {
		fail("unexpected positional arguments")
	}
	if err := nativerelease.VerifyReleaseBundle(nativerelease.VerifyOptions{Output: *output, Tag: *tag}); err != nil {
		fail("verify native release: %v", err)
	}
	fmt.Printf("verified canonical native release bundle structure for %s\n", *tag)
}

func fail(format string, arguments ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
