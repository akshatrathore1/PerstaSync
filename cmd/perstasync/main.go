package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"perstasync/internal/corpus"
	"perstasync/internal/load"
	"perstasync/internal/protocol"
	"perstasync/internal/report"
)

var observeCorpusPin = corpus.ObservePin
var materializeCorpusPin = corpus.MaterializePin
var prefetchCorpusDependencies = corpus.Prefetch
var resolveCorpusDependencies = corpus.ResolveDependencies

func run(ctx context.Context, arguments []string, output, diagnostics io.Writer) error {
	if len(arguments) != 0 {
		switch arguments[0] {
		case "pin":
			return runPin(ctx, arguments[1:], output, diagnostics)
		case "materialize":
			return runMaterialize(ctx, arguments[1:], output, diagnostics)
		case "prefetch":
			return runPrefetch(ctx, arguments[1:], output, diagnostics)
		}
	}
	return runClassify(ctx, arguments, output, diagnostics)
}

func runClassify(ctx context.Context, arguments []string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("perstasync", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	root := flags.String("root", "", "Source tree root")
	goos := flags.String("goos", "", "Target operating system")
	goarch := flags.String("goarch", "", "Target architecture")
	moduleCache := flags.String("module-cache", "", "Absolute offline module cache")
	buildCache := flags.String("build-cache", "", "Absolute offline build cache")
	dependencyContext := flags.String("dependency-context", "", "Prepared dependency context JSON")
	sandbox := flags.String("sandbox", "", "Corpus sandbox root")
	protected := flags.String("protected", "", "Protected source roots as an OS path list")
	loadOnly := flags.Bool("load-only", false, "Load packages without protocol classification")
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
	moduleFile := ""
	if *dependencyContext != "" {
		if *moduleCache != "" || *buildCache != "" || *sandbox == "" || *protected == "" {
			return fmt.Errorf("-dependency-context requires -sandbox and -protected and cannot be combined with direct cache flags")
		}
		data, err := os.ReadFile(*dependencyContext)
		if err != nil {
			return err
		}
		dependencies, err := corpus.ReadDependencies(bytes.NewReader(data))
		if err != nil {
			return err
		}
		dependencies, err = resolveCorpusDependencies(ctx, corpus.AcquisitionConfig{Sandbox: *sandbox, Protected: filepath.SplitList(*protected)}, directory, dependencies)
		if err != nil {
			return err
		}
		if dependencies.GOOS != *goos || dependencies.GOARCH != *goarch {
			return fmt.Errorf("dependency context build platform differs from the classification request")
		}
		*moduleCache, *buildCache, moduleFile = dependencies.ModuleCache, dependencies.BuildCache, dependencies.ModuleFile
	}
	program, err := load.Open(ctx, load.Config{
		Directory: directory, Patterns: flags.Args(), GOOS: *goos, GOARCH: *goarch,
		ModuleCache: *moduleCache, BuildCache: *buildCache, ModuleFile: moduleFile,
	})
	if err != nil {
		return err
	}
	if *loadOnly {
		summary, err := load.Summarize(program)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(summary)
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

func runPin(ctx context.Context, arguments []string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("perstasync pin", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	sandbox := flags.String("sandbox", "", "Corpus sandbox root")
	protected := flags.String("protected", "", "Protected source roots as an OS path list")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 1 || *sandbox == "" || *protected == "" {
		return fmt.Errorf("pin requires -sandbox, -protected and exactly one repository URL")
	}
	pin, err := observeCorpusPin(ctx, corpus.AcquisitionConfig{Sandbox: *sandbox, Protected: filepath.SplitList(*protected)}, flags.Arg(0))
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(pin)
}

func runMaterialize(ctx context.Context, arguments []string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("perstasync materialize", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	sandbox := flags.String("sandbox", "", "Corpus sandbox root")
	protected := flags.String("protected", "", "Protected source roots as an OS path list")
	pinFile := flags.String("pin", "", "Pin JSON file")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *pinFile == "" || *sandbox == "" || *protected == "" {
		return fmt.Errorf("materialize requires -sandbox, -protected, -pin and no positional arguments")
	}
	data, err := os.ReadFile(*pinFile)
	if err != nil {
		return err
	}
	pin, err := corpus.ReadPin(bytes.NewReader(data))
	if err != nil {
		return err
	}
	checkout, err := materializeCorpusPin(ctx, corpus.AcquisitionConfig{Sandbox: *sandbox, Protected: filepath.SplitList(*protected)}, pin)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(checkout)
}

func runPrefetch(ctx context.Context, arguments []string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("perstasync prefetch", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	root := flags.String("root", "", "Materialized source root")
	sandbox := flags.String("sandbox", "", "Corpus sandbox root")
	protected := flags.String("protected", "", "Protected source roots as an OS path list")
	goos := flags.String("goos", "", "Target operating system")
	goarch := flags.String("goarch", "", "Target architecture")
	proxy := flags.String("proxy", "", "Single module proxy URL")
	sumdb := flags.String("sumdb", "", "Checksum database policy")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *root == "" || *sandbox == "" || *protected == "" || *goos == "" || *goarch == "" || *proxy == "" || *sumdb == "" {
		return fmt.Errorf("prefetch requires root, sandbox, protected roots, build platform, proxy, checksum policy and no positional arguments")
	}
	directory, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	dependencies, err := prefetchCorpusDependencies(ctx, corpus.PrefetchConfig{
		Directory: directory, Sandbox: *sandbox, Protected: filepath.SplitList(*protected),
		GOOS: *goos, GOARCH: *goarch, Proxy: *proxy, SumDB: *sumdb,
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(dependencies)
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "perstasync:", err)
		os.Exit(1)
	}
}
