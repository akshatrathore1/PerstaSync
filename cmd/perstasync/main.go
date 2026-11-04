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
	disableOpenFlags := flags.Bool("disable-open-flags", false, "Disable synchronous-write flag coverage")
	disableInterprocedural := flags.Bool("disable-interprocedural", false, "Disable callee argument and result tracking")
	disableStructFields := flags.Bool("disable-struct-fields", false, "Disable struct-field handle and path tracking")
	disableAllocCells := flags.Bool("disable-alloc-cells", false, "Disable captured scalar-cell tracking")
	disablePaths := flags.Bool("disable-paths", false, "Disable derived parent and file-name path tracking")
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
	sites, err := protocol.AnalyzeWithOptions(program.SSA, program.Initial, directory, protocol.Options{
		GOOS: program.Config.GOOS, DisableOpenFlags: *disableOpenFlags,
		DisableInterprocedural: *disableInterprocedural,
		DisableStructFields:    *disableStructFields,
		DisableAllocCells:      *disableAllocCells,
		DisablePaths:           *disablePaths,
	})
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
