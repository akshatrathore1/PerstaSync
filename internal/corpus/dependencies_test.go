package corpus

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func prefetchFixture(t *testing.T) PrefetchConfig {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	proxy := filepath.Join(root, "proxy")
	for _, directory := range []string{source, proxy} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module fixture\n\ngo 1.27.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	proxyPath := filepath.ToSlash(proxy)
	if filepath.VolumeName(proxy) != "" {
		proxyPath = "/" + proxyPath
	}
	return PrefetchConfig{
		Directory: source, Sandbox: root, Protected: []string{t.TempDir()},
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Proxy: (&url.URL{Scheme: "file", Path: proxyPath}).String(), SumDB: "off",
	}
}

func TestModuleProxyBoundaries(t *testing.T) {
	config := prefetchFixture(t)
	for _, valid := range []string{config.Proxy, "https://proxy.golang.org"} {
		if err := validateModuleProxy(valid, config.Sandbox); err != nil {
			t.Errorf("valid proxy %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"direct", "http://example.org/proxy", "https://example.org,other", "https://example.org|other",
		"https://example.org/proxy?extra=1", "https://example.org/proxy#fragment", "file://host/proxy",
	} {
		if err := validateModuleProxy(invalid, config.Sandbox); err == nil {
			t.Errorf("accepted unsupported/fallback proxy %q", invalid)
		}
	}
}

func TestPrefetchMetadataAndCollisions(t *testing.T) {
	config := prefetchFixture(t)
	prepared, err := Prefetch(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var public Dependencies
	if err := json.Unmarshal(data, &public); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(public.Proxy, filepath.ToSlash(config.Sandbox)) || public.ModuleFile != "" || public.ModuleCache != "" || public.BuildCache != "" {
		t.Fatalf("dependency metadata contains local paths: %+v", public)
	}
	before, err := os.ReadFile(prepared.ModuleFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Prefetch(context.Background(), config); err == nil {
		t.Error("overwrote an existing dependency context")
	}
	after, err := os.ReadFile(prepared.ModuleFile)
	if err != nil || string(before) != string(after) {
		t.Fatalf("existing context changed: %v", err)
	}
}

func TestPrefetchRejectsRedirectedContext(t *testing.T) {
	config := prefetchFixture(t)
	protected := config.Protected[0]
	if err := os.Symlink(protected, filepath.Join(config.Sandbox, ".dependencies")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := Prefetch(context.Background(), config); err == nil {
		t.Error("accepted redirected dependency storage")
	}
	entries, err := os.ReadDir(protected)
	if err != nil || len(entries) != 0 {
		t.Fatalf("prefetch wrote into a protected tree: %v err=%v", entries, err)
	}
}

func TestPrefetchEnvironment(t *testing.T) {
	config := prefetchFixture(t)
	t.Setenv("HTTPS_PROXY", "https://unconfigured.example")
	t.Setenv("HTTP_PROXY", "https://unconfigured.example")
	t.Setenv("GOAUTH", "unconfigured-helper")
	variables := make(map[string]string)
	for _, variable := range isolatedGoEnvironment(config, "modules", "build", "control") {
		name, value, _ := strings.Cut(variable, "=")
		variables[strings.ToUpper(name)] = value
	}
	if variables["HTTPS_PROXY"] != "" || variables["HTTP_PROXY"] != "" || variables["GOAUTH"] != "off" || variables["GOTOOLCHAIN"] != "local" || variables["GOVCS"] != "*:off" {
		t.Fatal("prefetch inherited transport or command settings")
	}
}

func TestPrefetchChecksumPresence(t *testing.T) {
	config := prefetchFixture(t)
	absent, err := Prefetch(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Directory, "go.sum"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	empty, err := Prefetch(context.Background(), config)
	if err != nil || absent.ID == empty.ID {
		t.Fatalf("absent and empty checksum inputs share a context: err=%v", err)
	}
}

func TestReadAndResolveDependencies(t *testing.T) {
	config := prefetchFixture(t)
	prepared, err := Prefetch(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadDependencies(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveDependencies(context.Background(), AcquisitionConfig{Sandbox: config.Sandbox, Protected: config.Protected}, config.Directory, decoded)
	if err != nil || resolved.ModuleFile != prepared.ModuleFile || resolved.ModuleCache != prepared.ModuleCache || resolved.BuildCache != prepared.BuildCache {
		t.Fatalf("resolved dependency context: %+v err=%v", resolved, err)
	}
	data, err := os.ReadFile(prepared.ModuleFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prepared.ModuleFile, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDependencies(context.Background(), AcquisitionConfig{Sandbox: config.Sandbox, Protected: config.Protected}, config.Directory, decoded); err == nil {
		t.Fatal("accepted modified prepared module input")
	}
	if err := os.WriteFile(prepared.ModuleFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	sumFile := strings.TrimSuffix(prepared.ModuleFile, ".mod") + ".sum"
	if err := os.WriteFile(sumFile, []byte("modified checksum input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDependencies(context.Background(), AcquisitionConfig{Sandbox: config.Sandbox, Protected: config.Protected}, config.Directory, decoded); err == nil {
		t.Fatal("accepted changed prepared checksum input")
	}
	if err := os.Remove(sumFile); err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(filepath.Join(config.Directory, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Directory, "go.mod"), append(module, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDependencies(context.Background(), AcquisitionConfig{Sandbox: config.Sandbox, Protected: config.Protected}, config.Directory, decoded); err == nil {
		t.Fatal("accepted changed source module input")
	}
}

func TestReadDependenciesRejectsInvalidMetadata(t *testing.T) {
	config := prefetchFixture(t)
	prepared, err := Prefetch(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "source_sum_present")
	missingPresence, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDependencies(strings.NewReader(string(missingPresence))); err == nil {
		t.Error("accepted omitted checksum-file presence")
	}
	for _, input := range []string{"{}", "null", string(encoded) + string(encoded), string(encoded) + "trailing"} {
		if _, err := ReadDependencies(strings.NewReader(input)); err == nil {
			t.Errorf("accepted invalid dependency context %q", input)
		}
	}
	for _, mutate := range []func(*Dependencies){
		func(value *Dependencies) { value.ID = strings.Repeat("f", 64) },
		func(value *Dependencies) { value.PreparedUTC = "yesterday" },
		func(value *Dependencies) { value.GoVersion = "unknown" },
		func(value *Dependencies) { value.GOOS = "" },
		func(value *Dependencies) { value.Proxy = "file:/absolute/path" },
		func(value *Dependencies) { value.Proxy = "https://example.org,other" },
		func(value *Dependencies) { value.SumDB = "unrecorded.example" },
		func(value *Dependencies) { value.ModuleHash = "main" },
	} {
		invalid := prepared
		mutate(&invalid)
		data, err := json.Marshal(invalid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadDependencies(strings.NewReader(string(data))); err == nil {
			t.Errorf("accepted invalid dependency metadata %+v", invalid)
		}
	}
}
