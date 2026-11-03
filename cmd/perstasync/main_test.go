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
