package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const usage = `usage: loghub serve | migrate | seed | healthcheck | help`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "loghub: %v\n", err)
		os.Exit(1)
	}
}

// dispatcher
func run(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf(usage)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := args[0]
	switch cmd {
	case "serve":
		return serve(ctx)
	case "migrate":
		return migrate(ctx)
	case "seed":
		return seed(ctx)
	case "healthcheck":
		return healthCheck(ctx)
	case "help":
		fmt.Println(usage)
		return nil
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}
