package load

import (
	"archive/zip"
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"perstasync/internal/corpus"
)

func TestOpen(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Directory: root, Patterns: []string{"./testdata/fixtures/loader"}, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	program, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if program.SSA == nil || program.CallGraph == nil || len(program.Initial) != 1 {
		t.Fatalf("incomplete loaded program: %+v", program)
	}
	summary, err := Summarize(program)
	if err != nil || summary.PackageCount == 0 || len(summary.InitialPackages) != 1 || summary.InitialPackages[0] != "perstasync/testdata/fixtures/loader" {
		t.Fatalf("load summary: %+v err=%v", summary, err)
	}
	if _, err := Summarize(nil); err == nil {
		t.Fatal("summarized a missing program")
	}
	function := program.Initial[0].Func("Publish")
	if function == nil || len(function.Blocks) == 0 {
		t.Fatal("fixture function has no SSA body")
	}
	config.Patterns = []string{"./testdata/fixtures/does-not-exist"}
	if _, err := Open(context.Background(), config); err == nil {
		t.Fatal("loading an absent package returned success")
	}
	if _, err := Open(context.Background(), Config{}); err == nil {
		t.Fatal("implicit package/build context accepted")
	}
}

func offlineModuleFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "application")
	proxy := filepath.Join(root, "proxy")
	versionRoot := filepath.Join(proxy, "example.org", "offline-dependency", "@v")
	for _, folder := range []string{directory, versionRoot} {
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
	}
	module := "module example.org/offline-dependency\n\ngo 1.27.1\n"
	files := map[string]string{
		filepath.Join(directory, "go.mod"):         "module example.org/application\n\ngo 1.27.1\n\nrequire example.org/offline-dependency v1.0.0\n",
		filepath.Join(directory, "application.go"): "package application\n\nimport \"example.org/offline-dependency\"\n\nfunc Value() int { return dependency.Value() }\n",
		filepath.Join(versionRoot, "v1.0.0.mod"):   module,
		filepath.Join(versionRoot, "v1.0.0.info"):  "{\"Version\":\"v1.0.0\",\"Time\":\"2000-01-01T00:00:00Z\"}\n",
		filepath.Join(versionRoot, "list"):         "v1.0.0\n",
	}
	for filename, content := range files {
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive, err := os.Create(filepath.Join(versionRoot, "v1.0.0.zip"))
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	for _, file := range []struct{ name, content string }{
		{"go.mod", module}, {"dependency.go", "package dependency\n\nfunc Value() int { return 42 }\n"},
	} {
		entry, err := writer.Create("example.org/offline-dependency@v1.0.0/" + file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(file.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	proxyPath := filepath.ToSlash(proxy)
	if filepath.VolumeName(proxy) != "" {
		proxyPath = "/" + proxyPath
	}
	return directory, proxy, (&url.URL{Scheme: "file", Path: proxyPath}).String()
}

func TestOpenUsesExplicitCaches(t *testing.T) {
	directory, proxy, proxyURL := offlineModuleFixture(t)
	moduleCache, buildCache := t.TempDir(), t.TempDir()
	command := exec.Command("go", "mod", "download", "all")
	command.Dir = directory
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "GO") {
			command.Env = append(command.Env, variable)
		}
	}
	command.Env = append(command.Env,
		"GOENV=off", "GOWORK=off", "GOSUMDB=off", "GOPROXY="+proxyURL, "GOTOOLCHAIN=local",
		"GOMODCACHE="+moduleCache, "GOCACHE="+buildCache,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prefetch synthetic dependency: %s err=%v", output, err)
	}
	cleanupModuleCache(t, directory, moduleCache, buildCache)
	if err := os.Rename(proxy, proxy+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOMODCACHE", t.TempDir())
	t.Setenv("GOCACHE", t.TempDir())
	config := Config{
		Directory: directory, Patterns: []string{"./..."}, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		ModuleCache: moduleCache, BuildCache: buildCache,
	}
	moduleBefore, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	sumsBefore, err := os.ReadFile(filepath.Join(directory, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	program, err := Open(context.Background(), config)
	if err != nil || program == nil || len(program.Initial) != 1 || program.Initial[0].Func("Value") == nil {
		t.Fatalf("offline load did not use explicit caches: program=%v err=%v", program, err)
	}
	for _, incomplete := range []Config{
		{Directory: directory, Patterns: config.Patterns, GOOS: config.GOOS, GOARCH: config.GOARCH, ModuleCache: moduleCache},
		{Directory: directory, Patterns: config.Patterns, GOOS: config.GOOS, GOARCH: config.GOARCH, BuildCache: buildCache},
	} {
		if _, err := Open(context.Background(), incomplete); err == nil {
			t.Error("accepted an incomplete cache context")
		}
	}
	missing := config
	missing.ModuleCache = t.TempDir()
	if _, err := Open(context.Background(), missing); err == nil {
		t.Error("missing offline dependency was silently resolved")
	}
	moduleAfter, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if err != nil || !bytes.Equal(moduleBefore, moduleAfter) {
		t.Fatalf("loading changed module input: %v", err)
	}
	sumsAfter, err := os.ReadFile(filepath.Join(directory, "go.sum"))
	if err != nil || !bytes.Equal(sumsBefore, sumsAfter) {
		t.Fatalf("loading changed checksum input: %v", err)
	}
}

func TestPrefetchPreservesSources(t *testing.T) {
	directory, proxy, proxyURL := offlineModuleFixture(t)
	sandbox := filepath.Dir(directory)
	moduleBefore, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	ambientModule, ambientBuild := t.TempDir(), t.TempDir()
	t.Setenv("GOMODCACHE", ambientModule)
	t.Setenv("GOCACHE", ambientBuild)
	prepared, err := corpus.Prefetch(context.Background(), corpus.PrefetchConfig{
		Directory: directory, Sandbox: sandbox, Protected: []string{t.TempDir()},
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Proxy: proxyURL, SumDB: "off",
	})
	if err != nil || prepared.ID == "" || prepared.ModuleFile == "" || prepared.GoVersion == "" {
		t.Fatalf("prepared dependency context: %+v err=%v", prepared, err)
	}
	cleanupModuleCache(t, directory, prepared.ModuleCache, prepared.BuildCache)
	if err := os.Rename(proxy, proxy+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	config := Config{
		Directory: directory, Patterns: []string{"./..."}, GOOS: prepared.GOOS, GOARCH: prepared.GOARCH,
		ModuleCache: prepared.ModuleCache, BuildCache: prepared.BuildCache, ModuleFile: prepared.ModuleFile,
	}
	if program, err := Open(context.Background(), config); err != nil || program == nil {
		t.Fatalf("prepared context failed offline: %v", err)
	}
	moduleAfter, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if err != nil || !bytes.Equal(moduleBefore, moduleAfter) {
		t.Fatalf("prefetch changed pinned module input: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("prefetch or loading wrote a source checksum file: %v", err)
	}
	sums, err := os.ReadFile(strings.TrimSuffix(prepared.ModuleFile, ".mod") + ".sum")
	if err != nil || !bytes.Contains(sums, []byte("example.org/offline-dependency v1.0.0")) {
		t.Fatalf("alternate checksum input missing: %q err=%v", sums, err)
	}
	for _, cache := range []string{ambientModule, ambientBuild} {
		entries, err := os.ReadDir(cache)
		if err != nil || len(entries) != 0 {
			t.Fatalf("ambient cache changed during prefetch: %v err=%v", entries, err)
		}
	}
}

func TestPrefetchLocalReplacement(t *testing.T) {
	directory, _, proxyURL := offlineModuleFixture(t)
	local := filepath.Join(directory, "local")
	if err := os.Mkdir(local, 0700); err != nil {
		t.Fatal(err)
	}
	module := "module example.org/application\n\ngo 1.27.1\n\nrequire (\nexample.org/offline-dependency v1.0.0\nexample.org/local v0.0.0\n)\nreplace example.org/local => ./local\n"
	for filename, content := range map[string]string{
		filepath.Join(directory, "go.mod"):         module,
		filepath.Join(directory, "application.go"): "package application\nimport (\"example.org/offline-dependency\"; \"example.org/local\")\nfunc Value() int { return dependency.Value() + local.Value() }\n",
		filepath.Join(local, "go.mod"):             "module example.org/local\n\ngo 1.27.1\n",
		filepath.Join(local, "local.go"):           "package local\nfunc Value() int { return 1 }\n",
	} {
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := corpus.Prefetch(context.Background(), corpus.PrefetchConfig{
		Directory: directory, Sandbox: filepath.Dir(directory), Protected: []string{t.TempDir()},
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Proxy: proxyURL, SumDB: "off",
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupModuleCache(t, directory, prepared.ModuleCache, prepared.BuildCache)
	if program, err := Open(context.Background(), Config{
		Directory: directory, Patterns: []string{"./..."}, GOOS: prepared.GOOS, GOARCH: prepared.GOARCH,
		ModuleCache: prepared.ModuleCache, BuildCache: prepared.BuildCache, ModuleFile: prepared.ModuleFile,
	}); err != nil || program == nil || program.Initial[0].Func("Value") == nil {
		t.Fatalf("relative replacement lost the original root: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if err != nil || string(after) != module {
		t.Fatalf("local replacement changed source module: %v", err)
	}
}

func TestPrefetchRejectsExternalReplacement(t *testing.T) {
	directory, _, proxyURL := offlineModuleFixture(t)
	external := filepath.Join(filepath.Dir(directory), "external")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "go.mod"), []byte("module example.org/offline-dependency\n\ngo 1.27.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	module := "module example.org/application\n\ngo 1.27.1\nrequire example.org/offline-dependency v1.0.0\nreplace example.org/offline-dependency => ../external\n"
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := corpus.Prefetch(context.Background(), corpus.PrefetchConfig{
		Directory: directory, Sandbox: filepath.Dir(directory), Protected: []string{t.TempDir()},
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Proxy: proxyURL, SumDB: "off",
	})
	if err == nil || !strings.Contains(err.Error(), "within the materialized checkout") {
		t.Fatalf("unmaterialized replacement was accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(directory), ".dependencies")); !os.IsNotExist(err) {
		t.Fatalf("rejected replacement left a dependency context: %v", err)
	}
}

func TestPrefetchFailureCleansContext(t *testing.T) {
	directory, _, proxyURL := offlineModuleFixture(t)
	root := filepath.Dir(directory)
	missing := filepath.Join(root, "proxy", "example.org", "missing", "@v")
	if err := os.MkdirAll(missing, 0700); err != nil {
		t.Fatal(err)
	}
	for filename, content := range map[string]string{
		filepath.Join(missing, "v1.0.0.mod"):  "module example.org/missing\n\ngo 1.27.1\n",
		filepath.Join(missing, "v1.0.0.info"): "{\"Version\":\"v1.0.0\",\"Time\":\"2000-01-01T00:00:00Z\"}\n",
	} {
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	module, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	module = append(module, []byte("\nrequire example.org/missing v1.0.0\n")...)
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), module, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = corpus.Prefetch(context.Background(), corpus.PrefetchConfig{
		Directory: directory, Sandbox: root, Protected: []string{t.TempDir()},
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Proxy: proxyURL, SumDB: "off",
	})
	if err == nil {
		t.Fatal("accepted a dependency without its module ZIP")
	}
	entries, readErr := os.ReadDir(filepath.Join(root, ".dependencies"))
	if !os.IsNotExist(readErr) && (readErr != nil || len(entries) != 0) {
		t.Fatalf("failed prefetch left a dependency context: %v err=%v", entries, readErr)
	}
	after, readErr := os.ReadFile(filepath.Join(directory, "go.mod"))
	if readErr != nil || !bytes.Equal(module, after) {
		t.Fatalf("failed prefetch changed module input: %v", readErr)
	}
	if _, err := os.Stat(filepath.Join(directory, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("failed prefetch wrote source go.sum: %v", err)
	}
}

func cleanupModuleCache(t *testing.T, directory, moduleCache, buildCache string) {
	t.Helper()
	t.Cleanup(func() {
		command := exec.Command("go", "clean", "-modcache")
		command.Dir = directory
		for _, variable := range os.Environ() {
			name, _, _ := strings.Cut(variable, "=")
			if !strings.HasPrefix(strings.ToUpper(name), "GO") {
				command.Env = append(command.Env, variable)
			}
		}
		command.Env = append(command.Env,
			"GOENV=off", "GOWORK=off", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off",
			"GOMODCACHE="+moduleCache, "GOCACHE="+buildCache,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("clean fixture module cache: %s err=%v", output, err)
		}
	})
}
