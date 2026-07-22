package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/EngBlock/open-skills/internal/application"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	exitCode := application.Run(ctx, application.Invocation{
		Args:   os.Args[1:],
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	stop()
	os.Exit(exitCode)
}
