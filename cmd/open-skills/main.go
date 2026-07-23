package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/EngBlock/open-skills/internal/application"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	stdinInfo, _ := os.Stdin.Stat()
	exitCode := application.Run(ctx, application.Invocation{
		Args:        os.Args[1:],
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Interactive: stdinInfo != nil && stdinInfo.Mode()&os.ModeCharDevice != 0,
	})
	stop()
	os.Exit(exitCode)
}
