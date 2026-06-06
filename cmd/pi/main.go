package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"pi-go/pkg/ai"
)

func main() {
	// Register builtin provider
	if err := ai.RegisterBuiltinProviders(); err != nil {
		fmt.Fprintf(os.Stderr, "Error registering provider: %v\n", err)
		os.Exit(1)
	}

	args := os.Args[1:]
	if os.Getenv("BE_PI_CLI") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				args = os.Args[i+1:]
				break
			}
		}
	}
	// Parse CLI options
	opts, err := ParseFlags(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	// Handle version
	if opts.Version {
		printVersion()
		os.Exit(0)
	}

	// Handle list-models
	if opts.ListModels {
		printSupportedModels()
		os.Exit(0)
	}

	// Handle config subcommand if passed as first positional arg
	if len(opts.PositionalArgs) > 0 && opts.PositionalArgs[0] == "config" {
		fmt.Fprintln(os.Stderr, "Subcommand 'config' is planned but not yet implemented.")
		os.Exit(0)
	}

	// Check if we have a prompt to run in print mode
	if opts.PrintPrompt != "" {
		// Set up cancelable context on interrupts
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		if err := RunPrintMode(ctx, opts); err != nil {
			// Return non-zero only for runtime failure, not normal conversation errors
			os.Exit(1)
		}
		os.Exit(0)
	}

	// No prompt or known command specified
	fmt.Fprintln(os.Stderr, "No prompt specified. Use -p <prompt> or run with a prompt directly.")
	fmt.Fprintln(os.Stderr, "Run with --help or -h for usage information.")
	os.Exit(1)
}

func printVersion() {
	fmt.Println("pi version 0.1.0-go (OpenAI Codex only)")
}

func printSupportedModels() {
	models := ai.GetModels()
	fmt.Println("Supported models (OpenAI Codex only):")
	for _, m := range models {
		fmt.Printf("  - %s (%s)\n", m.ID, m.Name)
	}
}
