// Package application owns native command orchestration. It is internal so the
// supported contract remains the open-skills process, not a Go library.
package application

import (
	"context"
	"io"
)

type Invocation struct {
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run is deliberately a quiet bootstrap. User-facing command behavior starts
// with issue #13; this seam only proves the standalone process can start.
func Run(ctx context.Context, invocation Invocation) int {
	_ = ctx
	_ = invocation
	return 0
}
