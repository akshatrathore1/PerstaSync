package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"perstasync/internal/load"
	"perstasync/internal/protocol"
	"perstasync/internal/report"
)

func run(ctx context.Context, arguments []string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("perstasync", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	root := flags.String("root", "", "Source tree root")
	goos := flags.String("goos", "", "Target operating system")
	goarch := flags.String("goarch", "", "Target architecture")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *root == "" {
		return fmt.Errorf("-root is required")
	}
	directory, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	program, err := load.Open(ctx, load.Config{
		Directory: directory, Patterns: flags.Args(), GOOS: *goos, GOARCH: *goarch,
	})
	if err != nil {
		return err
	}
	sites, err := protocol.Analyze(program.SSA, program.Initial, directory)
	if err != nil {
		return err
	}
	return report.WriteSites(output, sites)
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "perstasync:", err)
		os.Exit(1)
	}
}
