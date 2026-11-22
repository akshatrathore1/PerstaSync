package main

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"

	"perstasync/internal/corpus"
	"perstasync/internal/load"
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

func TestCLIAcquisitionWiring(t *testing.T) {
	root := t.TempDir()
	sandbox := filepath.Join(root, "sandbox")
	protected := []string{filepath.Join(root, "source-one"), filepath.Join(root, "source-two")}
	pin := corpus.Pin{
		URL: "https://example.org/repository.git", DefaultBranch: "main", Commit: strings.Repeat("a", 40),
		ObservedUTC: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC).Format(time.RFC3339), GitVersion: "git version test",
	}
	originalObserve, originalMaterialize := observeCorpusPin, materializeCorpusPin
	t.Cleanup(func() { observeCorpusPin, materializeCorpusPin = originalObserve, originalMaterialize })
	observeCorpusPin = func(_ context.Context, config corpus.AcquisitionConfig, source string) (corpus.Pin, error) {
		if config.Sandbox != sandbox || !reflect.DeepEqual(config.Protected, protected) || source != pin.URL {
			t.Fatalf("pin wiring: config=%+v source=%q", config, source)
		}
		return pin, nil
	}
	var output, diagnostics bytes.Buffer
	protectedList := strings.Join(protected, string(os.PathListSeparator))
	if err := run(context.Background(), []string{"pin", "-sandbox", sandbox, "-protected", protectedList, pin.URL}, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var observed corpus.Pin
	if err := json.Unmarshal(output.Bytes(), &observed); err != nil || observed != pin {
		t.Fatalf("pin output: %+v err=%v", observed, err)
	}
	pinFile := filepath.Join(root, "pin.json")
	data, err := json.Marshal(pin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pinFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	materializeCorpusPin = func(_ context.Context, config corpus.AcquisitionConfig, input corpus.Pin) (corpus.Checkout, error) {
		if config.Sandbox != sandbox || !reflect.DeepEqual(config.Protected, protected) || input != pin {
			t.Fatalf("materialize wiring: config=%+v pin=%+v", config, input)
		}
		return corpus.Checkout{Pin: input, GoFileCount: 7, Directory: filepath.Join(root, "sensitive-checkout")}, nil
	}
	output.Reset()
	if err := run(context.Background(), []string{"materialize", "-sandbox", sandbox, "-protected", protectedList, "-pin", pinFile}, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), root) {
		t.Fatalf("materialization output disclosed a local path: %s", output.String())
	}
	var checkout corpus.Checkout
	if err := json.Unmarshal(output.Bytes(), &checkout); err != nil || checkout.Pin != pin || checkout.GoFileCount != 7 || checkout.Directory != "" {
		t.Fatalf("materialize output: %+v err=%v", checkout, err)
	}
	for _, invalid := range [][]string{
		{"pin", "-sandbox", sandbox, pin.URL},
		{"materialize", "-sandbox", sandbox, "-protected", protectedList},
	} {
		output.Reset()
		if err := run(context.Background(), invalid, &output, &diagnostics); err == nil || output.Len() != 0 {
			t.Fatalf("invalid acquisition emitted output=%q err=%v", output.String(), err)
		}
	}
}

func TestCLIOfflineCaches(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.27.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := "package fixture\n\nimport \"os\"\n\nfunc Publish() error { return os.Rename(\"state.tmp\", \"state\") }\n"
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	moduleCache, buildCache := t.TempDir(), t.TempDir()
	ambientModule, ambientBuild := t.TempDir(), t.TempDir()
	t.Setenv("GOMODCACHE", ambientModule)
	t.Setenv("GOCACHE", ambientBuild)
	base := []string{"-root", root, "-goos", runtime.GOOS, "-goarch", runtime.GOARCH}
	var output, diagnostics bytes.Buffer
	for _, option := range []string{"-module-cache", "-build-cache"} {
		arguments := append(append([]string(nil), base...), option, moduleCache, "./...")
		if err := run(context.Background(), arguments, &output, &diagnostics); err == nil || !strings.Contains(err.Error(), "both absolute module and build cache") || output.Len() != 0 {
			t.Fatalf("partial cache context: output=%q err=%v", output.String(), err)
		}
	}
	arguments := append(base, "-module-cache", moduleCache, "-build-cache", buildCache, "./...")
	if err := run(context.Background(), arguments, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var sites []protocol.Site
	if err := json.Unmarshal(output.Bytes(), &sites); err != nil || len(sites) != 1 || sites[0].File != "fixture.go" || sites[0].Level != protocol.L1Unknown {
		t.Fatalf("offline CLI output: %+v err=%v", sites, err)
	}
	entries, err := os.ReadDir(buildCache)
	if err != nil || len(entries) == 0 {
		t.Fatalf("explicit build cache was not used: %v", err)
	}
	for _, cache := range []string{ambientModule, ambientBuild} {
		entries, err := os.ReadDir(cache)
		if err != nil || len(entries) != 0 {
			t.Fatalf("ambient cache was changed: %v err=%v", entries, err)
		}
	}
}

func TestCLIPrefetchWiring(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	protected := []string{t.TempDir(), t.TempDir()}
	prepared := corpus.Dependencies{
		ID: strings.Repeat("a", 64), PreparedUTC: "2026-11-21T00:00:00Z", GoVersion: "go version test",
		GOOS: "linux", GOARCH: "amd64", Proxy: "https://proxy.example", SumDB: "sum.golang.org",
		SourceModHash: strings.Repeat("b", 64), SourceSumHash: strings.Repeat("c", 64), SourceSumPresent: true,
		ModuleHash: strings.Repeat("d", 64), SumHash: strings.Repeat("e", 64),
		ModuleFile: filepath.Join(root, "sensitive.mod"), ModuleCache: filepath.Join(root, "modules"), BuildCache: filepath.Join(root, "build"),
	}
	original := prefetchCorpusDependencies
	t.Cleanup(func() { prefetchCorpusDependencies = original })
	expectResume := false
	prefetchCorpusDependencies = func(_ context.Context, config corpus.PrefetchConfig) (corpus.Dependencies, error) {
		if config.Directory != source || config.Sandbox != root || !reflect.DeepEqual(config.Protected, protected) || config.GOOS != "linux" || config.GOARCH != "amd64" || config.Proxy != "https://proxy.example" || config.SumDB != "sum.golang.org" || config.Resume != expectResume {
			t.Fatalf("prefetch wiring: %+v", config)
		}
		return prepared, nil
	}
	protectedList := strings.Join(protected, string(os.PathListSeparator))
	arguments := []string{"prefetch", "-root", source, "-sandbox", root, "-protected", protectedList, "-goos", "linux", "-goarch", "amd64", "-proxy", "https://proxy.example", "-sumdb", "sum.golang.org"}
	var output, diagnostics bytes.Buffer
	if err := run(context.Background(), arguments, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var decoded corpus.Dependencies
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded.ID != prepared.ID || decoded.ModuleFile != "" || decoded.ModuleCache != "" || decoded.BuildCache != "" {
		t.Fatalf("prefetch output: %+v err=%v", decoded, err)
	}
	output.Reset()
	expectResume = true
	if err := run(context.Background(), append(arguments, "-resume"), &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := run(context.Background(), []string{"prefetch", "-root", source}, &output, &diagnostics); err == nil || output.Len() != 0 {
		t.Fatalf("invalid prefetch emitted output=%q err=%v", output.String(), err)
	}
}

func TestCLIDependencyContext(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	for filename, content := range map[string]string{
		filepath.Join(source, "go.mod"):     "module fixture\n\ngo 1.27.1\n",
		filepath.Join(source, "fixture.go"): "package fixture\nimport \"os\"\nfunc Publish() error { return os.Rename(\"state.tmp\", \"state\") }\n",
	} {
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	proxy := filepath.Join(root, "proxy")
	if err := os.Mkdir(proxy, 0700); err != nil {
		t.Fatal(err)
	}
	proxyPath := filepath.ToSlash(proxy)
	if filepath.VolumeName(proxy) != "" {
		proxyPath = "/" + proxyPath
	}
	prepared, err := corpus.Prefetch(context.Background(), corpus.PrefetchConfig{
		Directory: source, Sandbox: root, Protected: []string{t.TempDir()}, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Proxy: (&url.URL{Scheme: "file", Path: proxyPath}).String(), SumDB: "off",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	contextFile := filepath.Join(root, "dependencies.json")
	if err := os.WriteFile(contextFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	protected := strings.Join([]string{t.TempDir(), t.TempDir()}, string(os.PathListSeparator))
	arguments := []string{"-root", source, "-goos", runtime.GOOS, "-goarch", runtime.GOARCH, "-dependency-context", contextFile, "-sandbox", root, "-protected", protected, "-load-only", "./..."}
	var output, diagnostics bytes.Buffer
	if err := run(context.Background(), arguments, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var summary load.Summary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil || summary.PackageCount == 0 || !reflect.DeepEqual(summary.InitialPackages, []string{"fixture"}) {
		t.Fatalf("context load summary: %+v err=%v", summary, err)
	}
	output.Reset()
	arguments[3] = "plan9"
	if err := run(context.Background(), arguments, &output, &diagnostics); err == nil || output.Len() != 0 {
		t.Fatalf("mismatched context emitted output=%q err=%v", output.String(), err)
	}
}

func TestCLIPhaseDiagnosticsPreserveJSON(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"-root", root, "-goos", "linux", "-goarch", "amd64", "./testdata/fixtures/direct"}
	var baseline, traced, diagnostics bytes.Buffer
	if err := run(context.Background(), arguments, &baseline, &diagnostics); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "classifier.pprof")
	tracedArguments := append([]string{"-progress", "-timeout", "30s", "-cpu-profile", profile}, arguments...)
	if err := run(context.Background(), tracedArguments, &traced, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(baseline.Bytes(), traced.Bytes()) || !strings.Contains(diagnostics.String(), "package loading started") || !strings.Contains(diagnostics.String(), "classifier completed") {
		t.Fatalf("phase diagnostics changed results or omitted phases: %s", diagnostics.String())
	}
	if info, err := os.Stat(profile); err != nil || info.Size() == 0 {
		t.Fatalf("classifier profile unavailable: %v", err)
	}
	profileBytes, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	traced.Reset()
	if err := run(context.Background(), tracedArguments, &traced, &diagnostics); err == nil || traced.Len() != 0 {
		t.Fatalf("existing profile emitted a classification: output=%q err=%v", traced.String(), err)
	}
	preserved, err := os.ReadFile(profile)
	if err != nil || !bytes.Equal(profileBytes, preserved) {
		t.Fatalf("existing profile was changed: %v", err)
	}
}

func TestCLICancellationEmitsNoJSON(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	arguments := []string{"-root", root, "-goos", "linux", "-goarch", "amd64", "./testdata/fixtures/direct"}
	var output, diagnostics bytes.Buffer
	if err := run(parent, arguments, &output, &diagnostics); err == nil || output.Len() != 0 {
		t.Fatalf("cancelled request emitted output=%q err=%v", output.String(), err)
	}
	arguments = append([]string{"-timeout", "-1s"}, arguments...)
	if err := run(context.Background(), arguments, &output, &diagnostics); err == nil || output.Len() != 0 {
		t.Fatalf("negative deadline emitted output=%q err=%v", output.String(), err)
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
		"bytes": true, "context": true, "encoding/json": true, "errors": true, "flag": true, "fmt": true, "io": true, "log": true, "os": true, "path/filepath": true,
		"perstasync/internal/corpus": true,
		"perstasync/internal/load":   true, "perstasync/internal/protocol": true, "perstasync/internal/report": true,
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
				if ok && (function.Recv != nil || (function.Name.Name != "main" && function.Name.Name != "run" && function.Name.Name != "runClassify" && function.Name.Name != "runPin" && function.Name.Name != "runMaterialize" && function.Name.Name != "runPrefetch")) {
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
