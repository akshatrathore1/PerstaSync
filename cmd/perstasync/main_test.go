package main

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	"perstasync/internal/protocol"
)

func TestCLI(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"-root", root, "-goos", runtime.GOOS, "-goarch", runtime.GOARCH, "./testdata/fixtures/direct"}
	var first, second, diagnostics bytes.Buffer
	if err := run(context.Background(), arguments, &first, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), arguments, &second, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("repeated CLI runs differ: %q vs %q", first.String(), second.String())
	}
	var sites []protocol.Site
	if err := json.Unmarshal(first.Bytes(), &sites); err != nil {
		t.Fatal(err)
	}
	levels := make([]protocol.Level, 0, len(sites))
	for _, site := range sites {
		levels = append(levels, site.Level)
		if site.File != "testdata/fixtures/direct/fixture.go" || !site.R3 {
			t.Fatalf("CLI lost source or error-handling evidence: %+v", site)
		}
	}
	expected := []protocol.Level{protocol.L0, protocol.L1, protocol.L2, protocol.L0, protocol.Undetermined, protocol.Undetermined}
	if !reflect.DeepEqual(levels, expected) {
		t.Fatalf("CLI levels got %v, want %v", levels, expected)
	}
	for _, invalid := range [][]string{
		nil,
		{"-root", root},
		{"-root", root, "-goos", runtime.GOOS, "-goarch", runtime.GOARCH, "./testdata/fixtures/does-not-exist"},
	} {
		var output bytes.Buffer
		if err := run(context.Background(), invalid, &output, &diagnostics); err == nil || output.Len() != 0 {
			t.Fatalf("invalid request emitted output=%q err=%v", output.String(), err)
		}
	}
	var help bytes.Buffer
	if err := run(context.Background(), []string{"-h"}, &help, &diagnostics); err != nil || help.Len() != 0 {
		t.Fatalf("help returned err=%v output=%q", err, help.String())
	}
}

func TestCLIOpenFlags(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"-root", root, "-goos", "linux", "-goarch", "amd64"}
	var first, repeated, disabled, diagnostics bytes.Buffer
	for _, output := range []*bytes.Buffer{&first, &repeated} {
		if err := run(context.Background(), append(arguments, "./testdata/fixtures/openflags"), output, &diagnostics); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(first.Bytes(), repeated.Bytes()) {
		t.Fatal("synchronous-write CLI output differs across repeated loads")
	}
	if err := run(context.Background(), append(arguments, "-disable-open-flags", "./testdata/fixtures/openflags"), &disabled, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var enabledSites, disabledSites []protocol.Site
	if err := json.Unmarshal(first.Bytes(), &enabledSites); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(disabled.Bytes(), &disabledSites); err != nil {
		t.Fatal(err)
	}
	if len(enabledSites) == 0 || len(enabledSites) != len(disabledSites) {
		t.Fatalf("ablation changed candidate accounting: %d vs %d", len(enabledSites), len(disabledSites))
	}
	if enabledSites[0].Level != protocol.L1 || disabledSites[0].Level != protocol.L1Unknown || !enabledSites[0].R3 || !disabledSites[0].R3 {
		t.Fatalf("CLI flag coverage: enabled=%+v disabled=%+v", enabledSites[0], disabledSites[0])
	}
}

func TestCLITracking(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		fixture  string
		flag     string
		disabled protocol.Level
	}{
		{"callees", "-disable-interprocedural", protocol.L1Unknown},
		{"structs", "-disable-struct-fields", protocol.Undetermined},
		{"captures", "-disable-alloc-cells", protocol.L1Unknown},
		{"paths", "-disable-paths", protocol.Undetermined},
	} {
		t.Run(test.fixture, func(t *testing.T) {
			for _, disabled := range []bool{false, true} {
				arguments := []string{"-root", root, "-goos", "linux", "-goarch", "amd64"}
				expected := protocol.L2
				if disabled {
					arguments = append(arguments, test.flag)
					expected = test.disabled
				}
				var output, diagnostics bytes.Buffer
				if err := run(context.Background(), append(arguments, "./testdata/fixtures/"+test.fixture), &output, &diagnostics); err != nil {
					t.Fatal(err)
				}
				var sites []protocol.Site
				if err := json.Unmarshal(output.Bytes(), &sites); err != nil {
					t.Fatal(err)
				}
				if len(sites) == 0 || sites[0].Level != expected || !sites[0].R3 {
					t.Fatalf("disabled=%t: expected %s with R3, got %+v", disabled, expected, sites)
				}
			}
		})
	}
}

func TestCLICapabilityDeterminism(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	patterns := []string{
		"./testdata/fixtures/direct", "./testdata/fixtures/openflags", "./testdata/fixtures/callees",
		"./testdata/fixtures/structs", "./testdata/fixtures/captures", "./testdata/fixtures/paths",
	}
	reversed := make([]string, len(patterns))
	for index, pattern := range patterns {
		reversed[len(patterns)-1-index] = pattern
	}
	switches := []string{
		"-disable-open-flags", "-disable-interprocedural", "-disable-struct-fields",
		"-disable-alloc-cells", "-disable-paths",
	}
	configurations := [][]string{nil}
	for _, disabled := range switches {
		configurations = append(configurations, []string{disabled})
	}
	configurations = append(configurations, switches)
	var baseline []protocol.Site
	for _, configuration := range configurations {
		arguments := []string{"-root", root, "-goos", "linux", "-goarch", "amd64"}
		arguments = append(arguments, configuration...)
		var first, second, diagnostics bytes.Buffer
		if err := run(context.Background(), append(append([]string(nil), arguments...), patterns...), &first, &diagnostics); err != nil {
			t.Fatal(err)
		}
		if err := run(context.Background(), append(append([]string(nil), arguments...), reversed...), &second, &diagnostics); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first.Bytes(), second.Bytes()) {
			t.Fatalf("configuration %v changed output with package order", configuration)
		}
		var sites []protocol.Site
		if err := json.Unmarshal(first.Bytes(), &sites); err != nil {
			t.Fatal(err)
		}
		if baseline == nil {
			baseline = sites
		}
		if len(sites) == 0 || len(sites) != len(baseline) {
			t.Fatalf("configuration %v changed candidate accounting: %d vs %d", configuration, len(sites), len(baseline))
		}
		for index, site := range sites {
			original := baseline[index]
			if site.File != original.File || site.Offset != original.Offset || site.R3 != original.R3 {
				t.Errorf("configuration %v changed coordinate/R3 at %d: %+v vs %+v", configuration, index, site, original)
			}
		}
	}
}

func TestPackageBoundaries(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := packages.Load(&packages.Config{Dir: root, Mode: packages.LoadSyntax}, "./cmd/...", "./internal/...")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) == 0 {
		t.Fatal("architecture check loaded no packages")
	}
	commandImports := map[string]bool{
		"context": true, "errors": true, "flag": true, "fmt": true, "io": true, "os": true, "path/filepath": true,
		"perstasync/internal/load": true, "perstasync/internal/protocol": true, "perstasync/internal/report": true,
	}
	for _, pkg := range loaded {
		for _, diagnostic := range pkg.Errors {
			t.Errorf("package load error: %s", diagnostic)
		}
		for _, filename := range pkg.CompiledGoFiles {
			relative, err := filepath.Rel(root, filename)
			if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				t.Errorf("package %s has source outside its module", pkg.PkgPath)
			}
		}
		for importPath := range pkg.Imports {
			if strings.HasPrefix(pkg.PkgPath, "perstasync/internal/") && strings.HasPrefix(importPath, "perstasync/cmd/") {
				t.Errorf("internal package %s imports command %s", pkg.PkgPath, importPath)
			}
			if strings.HasPrefix(pkg.PkgPath, "perstasync/cmd/") && !commandImports[importPath] {
				t.Errorf("command package %s imports non-wiring dependency %s", pkg.PkgPath, importPath)
			}
		}
		if !strings.HasPrefix(pkg.PkgPath, "perstasync/cmd/") {
			continue
		}
		for _, file := range pkg.Syntax {
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if ok && (function.Recv != nil || (function.Name.Name != "main" && function.Name.Name != "run")) {
					t.Errorf("command package declares non-wiring function %s", function.Name.Name)
				}
				if !ok {
					continue
				}
				ast.Inspect(function.Body, func(node ast.Node) bool {
					switch node.(type) {
					case *ast.ForStmt, *ast.RangeStmt, *ast.FuncLit:
						t.Errorf("command function %s contains iteration or a local function instead of delegating behavior", function.Name.Name)
					}
					return true
				})
			}
		}
	}
}
