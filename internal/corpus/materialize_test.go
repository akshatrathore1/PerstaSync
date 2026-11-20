package corpus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMaterializationDestination(t *testing.T) {
	root := t.TempDir()
	protected := filepath.Join(root, "source")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	identity := strings.Repeat("a", 64)
	sandbox := filepath.Join(root, "corpus")
	destination, err := materializationDestination(sandbox, []string{protected}, identity)
	if err != nil || destination != filepath.Join(sandbox, identity) {
		t.Fatalf("valid destination: got %q err=%v", destination, err)
	}
	if _, err := os.Stat(sandbox); !os.IsNotExist(err) {
		t.Fatalf("validation created the sandbox: %v", err)
	}
	for _, test := range []struct {
		name      string
		sandbox   string
		protected []string
		identity  string
	}{
		{"missing sandbox", "", []string{protected}, identity},
		{"missing protection", sandbox, nil, identity},
		{"protected root", protected, []string{protected}, identity},
		{"protected descendant", filepath.Join(protected, "corpus"), []string{protected}, identity},
		{"protected ancestor", root, []string{protected}, identity},
		{"second protected tree", sandbox, []string{protected, filepath.Join(sandbox, "module")}, identity},
		{"path traversal", sandbox, []string{protected}, "../source"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := materializationDestination(test.sandbox, test.protected, test.identity); err == nil {
				t.Fatal("accepted an unsafe destination")
			}
		})
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(destination, "existing")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := materializationDestination(sandbox, []string{protected}, identity); err == nil {
		t.Error("accepted a pre-existing destination")
	}
	contents, err := os.ReadFile(sentinel)
	if err != nil || string(contents) != "preserve" {
		t.Fatalf("existing contents changed: %q err=%v", contents, err)
	}
}

func TestMaterializationRejectsGitAncestor(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := materializationDestination(filepath.Join(root, "corpus"), []string{t.TempDir()}, strings.Repeat("a", 64)); err == nil {
		t.Fatal("accepted a sandbox within a Git repository")
	}
}

func TestMaterializationRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	protected := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(protected, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := materializationDestination(filepath.Join(link, "corpus"), []string{protected}, strings.Repeat("a", 64)); err == nil {
		t.Fatal("accepted a symlink path into a protected tree")
	}
}

func TestGitRunnerIsolation(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "empty-repository")
	initialize := exec.Command("git", "-c", "init.defaultBranch=main", "init", "--bare", repository)
	if output, err := initialize.CombinedOutput(); err != nil {
		t.Fatalf("initialize local fixture: %s err=%v", output, err)
	}
	poison := filepath.Join(root, "ambient-config")
	if err := os.WriteFile(poison, []byte("[alias]\n\tinjected = rev-parse\n[core]\n\tbare = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", poison)
	t.Setenv("GIT_CONFIG_SYSTEM", poison)
	t.Setenv("GIT_DIR", filepath.Join(root, "missing-repository"))
	t.Setenv("GIT_WORK_TREE", filepath.Join(root, "missing-worktree"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.bare")
	t.Setenv("GIT_CONFIG_VALUE_0", "false")
	runner, err := newGitRunner(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runner.close(); err != nil {
			t.Error(err)
		}
	})
	output, err := runner.run(context.Background(), repository, "rev-parse", "--is-bare-repository")
	if err != nil || strings.TrimSpace(string(output)) != "true" {
		t.Fatalf("ambient state changed Git execution: %q err=%v", output, err)
	}
	if output, err := runner.run(context.Background(), repository, "config", "--global", "--get", "alias.injected"); err == nil {
		t.Errorf("consumed ambient alias: %q", output)
	}
	argument := "a filename; not a shell command"
	command := runner.command(context.Background(), repository, "status", "--", argument)
	if command.Path != runner.executable || command.Args[len(command.Args)-1] != argument {
		t.Fatalf("argument boundaries changed: path=%q args=%v", command.Path, command.Args)
	}
	for _, required := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_LFS_SKIP_SMUDGE=1"} {
		found := false
		for _, variable := range command.Env {
			found = found || variable == required
		}
		if !found {
			t.Errorf("missing isolation setting %s", required)
		}
	}
	if _, err := runner.run(context.Background(), repository, "nonexistent-corpus-operation"); err == nil {
		t.Error("discarded Git command failure")
	}
}

func localGitSource(t *testing.T, files map[string]string) (*gitRunner, string, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	runner, err := newGitRunner(root)
	if err != nil {
		t.Fatal(err)
	}
	runner.allowLocal = true
	t.Cleanup(func() {
		if err := runner.close(); err != nil {
			t.Error(err)
		}
	})
	if output, err := runner.run(context.Background(), source, "init", "--initial-branch=main"); err != nil {
		t.Fatalf("initialize fixture: %s err=%v", output, err)
	}
	paths := make([]string, 0, len(files))
	for name, content := range files {
		filename := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, name)
	}
	sort.Strings(paths)
	if len(paths) != 0 {
		if _, err := runner.run(context.Background(), source, append([]string{"add", "--"}, paths...)...); err != nil {
			t.Fatal(err)
		}
	}
	return runner, source, commitLocalSource(t, runner, source)
}

func commitLocalSource(t *testing.T, runner *gitRunner, source string) string {
	t.Helper()
	if _, err := runner.run(context.Background(), source,
		"-c", "user.name=Fixture", "-c", "user.email=fixture.invalid", "-c", "commit.gpgsign=false",
		"commit", "--allow-empty", "-m", "Synthetic acquisition source",
	); err != nil {
		t.Fatal(err)
	}
	head, err := runner.run(context.Background(), source, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(head))
}

func TestObservePin(t *testing.T) {
	runner, source, commit := localGitSource(t, map[string]string{"go.mod": "module fixture\n", "file.go": "package fixture\n"})
	before := time.Now().UTC()
	pin, err := observePin(context.Background(), runner, filepath.Dir(source), source)
	if err != nil || pin.URL != source || pin.DefaultBranch != "main" || pin.Commit != commit || !strings.HasPrefix(pin.GitVersion, "git version ") {
		t.Fatalf("observed pin: %+v err=%v", pin, err)
	}
	observed, err := time.Parse(time.RFC3339Nano, pin.ObservedUTC)
	if err != nil || observed.Before(before) || observed.After(time.Now().UTC()) {
		t.Fatalf("invalid observation date %q: %v", pin.ObservedUTC, err)
	}
}

func TestRemoteHead(t *testing.T) {
	commit := strings.Repeat("a", 40)
	valid := "ref: refs/heads/main\tHEAD\n" + commit + "\tHEAD\n"
	branch, observed, err := parseRemoteHead([]byte(valid))
	if err != nil || branch != "main" || observed != commit {
		t.Fatalf("valid remote head: branch=%q commit=%q err=%v", branch, observed, err)
	}
	for _, invalid := range []string{
		"", commit + "\tHEAD\n", "ref: refs/heads/main\tHEAD\n",
		"ref: refs/tags/main\tHEAD\n" + commit + "\tHEAD\n",
		"ref: refs/heads/main\tHEAD\nabc123\tHEAD\n",
		valid + commit + "\tHEAD\n",
		valid + "ref: refs/heads/other\tHEAD\n",
	} {
		if _, _, err := parseRemoteHead([]byte(invalid)); err == nil {
			t.Errorf("accepted ambiguous/incomplete HEAD output %q", invalid)
		}
	}
}

func TestPinnedCheckout(t *testing.T) {
	runner, source, commit := localGitSource(t, map[string]string{
		"go.mod": "module fixture\n", "file.go": "package fixture\n",
		"nested/file.go": "package nested\n", "space directory/file.go": "package spaced\n",
		"vendor/ignored.go": "package ignored\n", "nested/vendor/ignored.go": "package ignored\n",
	})
	if err := os.WriteFile(filepath.Join(source, "untracked.go"), []byte("package fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pin, err := observePin(context.Background(), runner, filepath.Dir(source), source)
	if err != nil {
		t.Fatal(err)
	}
	sandbox := t.TempDir()
	checkout, err := materializePin(context.Background(), runner, sandbox, []string{source, t.TempDir()}, pin)
	if err != nil || checkout.Pin.Commit != commit || checkout.GoFileCount != 3 || !pathWithin(checkout.Directory, sandbox) {
		t.Fatalf("pinned checkout: %+v err=%v", checkout, err)
	}
	contents, err := os.ReadFile(filepath.Join(checkout.Directory, "file.go"))
	if err != nil || string(contents) != "package fixture\n" {
		t.Fatalf("checkout bytes: %q err=%v", contents, err)
	}
	if _, err := materializePin(context.Background(), runner, sandbox, []string{source}, pin); err == nil {
		t.Error("reused an existing checkout destination")
	}
	if contents, err := os.ReadFile(filepath.Join(checkout.Directory, "file.go")); err != nil || string(contents) != "package fixture\n" {
		t.Fatalf("existing checkout changed: %q err=%v", contents, err)
	}
	head, err := runner.run(context.Background(), source, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || strings.TrimSpace(string(head)) != commit {
		t.Fatalf("source commit changed: %q err=%v", head, err)
	}
}

func TestPinnedCheckoutRejectsInvalidSources(t *testing.T) {
	for _, test := range []struct {
		name       string
		files      map[string]string
		mismatch   bool
		ineligible bool
	}{
		{"missing root module", map[string]string{"file.go": "package fixture\n"}, false, true},
		{"nested-only module", map[string]string{"nested/go.mod": "module fixture\n", "nested/file.go": "package fixture\n"}, false, true},
		{"no Go files", map[string]string{"go.mod": "module fixture\n"}, false, true},
		{"vendor-only Go", map[string]string{"go.mod": "module fixture\n", "vendor/file.go": "package fixture\n"}, false, true},
		{"mismatched pin", map[string]string{"go.mod": "module fixture\n", "file.go": "package fixture\n"}, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner, source, commit := localGitSource(t, test.files)
			pin, err := observePin(context.Background(), runner, filepath.Dir(source), source)
			if err != nil {
				t.Fatal(err)
			}
			if test.mismatch {
				pin.Commit = strings.Repeat("f", 40)
			}
			sandbox := t.TempDir()
			if _, err := materializePin(context.Background(), runner, sandbox, []string{source}, pin); err == nil || errors.Is(err, ErrIneligible) != test.ineligible {
				t.Fatalf("invalid source classification: err=%v, want ineligible=%t", err, test.ineligible)
			}
			entries, err := os.ReadDir(sandbox)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed checkout left files: %v err=%v", entries, err)
			}
			head, err := runner.run(context.Background(), source, "rev-parse", "--verify", "HEAD^{commit}")
			if err != nil || strings.TrimSpace(string(head)) != commit {
				t.Fatalf("source changed after failure: %q err=%v", head, err)
			}
		})
	}
}

func TestPinnedCheckoutRejectsMovedHead(t *testing.T) {
	runner, source, original := localGitSource(t, map[string]string{"go.mod": "module fixture\n", "file.go": "package fixture\n"})
	pin, err := observePin(context.Background(), runner, filepath.Dir(source), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file.go"), []byte("package fixture\nvar State = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.run(context.Background(), source, "add", "--", "file.go"); err != nil {
		t.Fatal(err)
	}
	moved := commitLocalSource(t, runner, source)
	if moved == original {
		t.Fatal("moved-head fixture did not change the commit")
	}
	sandbox := t.TempDir()
	if _, err := materializePin(context.Background(), runner, sandbox, []string{source}, pin); err == nil || errors.Is(err, ErrIneligible) {
		t.Fatalf("moved head was repinned or excluded: %v", err)
	}
	entries, err := os.ReadDir(sandbox)
	if err != nil || len(entries) != 0 {
		t.Fatalf("moved-head failure left a checkout: %v err=%v", entries, err)
	}
}

func TestPinnedCheckoutSkipsAmbientEffects(t *testing.T) {
	runner, source, initial := localGitSource(t, map[string]string{
		"go.mod": "module fixture\n", "file.go": "package fixture\n",
		".gitattributes": "file.go filter=ambient\n",
	})
	modules := "[submodule \"dependency\"]\n\tpath = dependency\n\turl = " + filepath.ToSlash(source) + "\n"
	if err := os.WriteFile(filepath.Join(source, ".gitmodules"), []byte(modules), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.run(context.Background(), source, "add", "--", ".gitmodules"); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.run(context.Background(), source, "update-index", "--add", "--cacheinfo", "160000,"+initial+",dependency"); err != nil {
		t.Fatal(err)
	}
	commitLocalSource(t, runner, source)
	if _, err := runner.run(context.Background(), source, "tag", "not-to-fetch"); err != nil {
		t.Fatal(err)
	}
	hooks := t.TempDir()
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	poison := filepath.Join(t.TempDir(), "global-config")
	configuration := "[core]\n\thooksPath = " + strconv.Quote(filepath.ToSlash(hooks)) + "\n[filter \"ambient\"]\n\tsmudge = unavailable-corpus-filter\n\trequired = true\n"
	if err := os.WriteFile(poison, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", poison)
	pin, err := observePin(context.Background(), runner, filepath.Dir(source), source)
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := materializePin(context.Background(), runner, t.TempDir(), []string{source}, pin)
	if err != nil || checkout.GoFileCount != 1 {
		t.Fatalf("ambient effects changed checkout: %+v err=%v", checkout, err)
	}
	tags, err := runner.run(context.Background(), checkout.Directory, "tag", "--list")
	if err != nil || len(bytes.TrimSpace(tags)) != 0 {
		t.Fatalf("implicit tags were fetched: %q err=%v", tags, err)
	}
	if _, err := os.Lstat(filepath.Join(checkout.Directory, "dependency", ".git")); !os.IsNotExist(err) {
		t.Fatalf("submodule was populated: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(checkout.Directory, "file.go"))
	if err != nil || string(contents) != "package fixture\n" {
		t.Fatalf("filter changed source bytes: %q err=%v", contents, err)
	}
}

func TestPinnedCheckoutRejectsExternalSymlink(t *testing.T) {
	runner, source, _ := localGitSource(t, map[string]string{"go.mod": "module fixture\n", "file.go": "package fixture\n"})
	externalRoot := t.TempDir()
	external := filepath.Join(externalRoot, "external.go")
	if err := os.WriteFile(external, []byte("package external\n"), 0600); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(source, "file.go")
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filename); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := runner.run(context.Background(), source, "add", "--", "file.go"); err != nil {
		t.Fatal(err)
	}
	commitLocalSource(t, runner, source)
	pin, err := observePin(context.Background(), runner, filepath.Dir(source), source)
	if err != nil {
		t.Fatal(err)
	}
	sandbox := t.TempDir()
	if _, err := materializePin(context.Background(), runner, sandbox, []string{source, externalRoot}, pin); err == nil || errors.Is(err, ErrIneligible) {
		t.Fatalf("unsafe source symlink was accepted or silently excluded: %v", err)
	}
	entries, err := os.ReadDir(sandbox)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unsafe checkout remained: %v err=%v", entries, err)
	}
	contents, err := os.ReadFile(external)
	if err != nil || string(contents) != "package external\n" {
		t.Fatalf("protected source changed: %q err=%v", contents, err)
	}
}

func TestReadPin(t *testing.T) {
	pin := Pin{
		URL: "https://example.org/repository.git", DefaultBranch: "main", Commit: strings.Repeat("a", 40),
		ObservedUTC: "2026-11-20T00:00:00Z", GitVersion: "git version test",
	}
	encoded, err := json.Marshal(pin)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadPin(bytes.NewReader(encoded))
	if err != nil || decoded != pin {
		t.Fatalf("pin round trip: %+v err=%v", decoded, err)
	}
	for _, field := range []string{"url", "default_branch", "commit", "observed_utc", "git_version"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, field)
		missing, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPin(bytes.NewReader(missing)); err == nil {
			t.Errorf("accepted missing pin field %s", field)
		}
	}
	for _, input := range []string{"{}", "null", string(encoded) + string(encoded), string(encoded) + "trailing"} {
		if _, err := ReadPin(strings.NewReader(input)); err == nil {
			t.Errorf("accepted incomplete/multiple pin documents %q", input)
		}
	}
	for _, mutate := range []func(*Pin){
		func(input *Pin) { input.URL = "../local-source" },
		func(input *Pin) { input.URL = "https://example.org/repository.git?mutable=1" },
		func(input *Pin) { input.Commit = "main" },
		func(input *Pin) { input.Commit = strings.Repeat("A", 40) },
		func(input *Pin) { input.DefaultBranch = " " },
		func(input *Pin) { input.ObservedUTC = "yesterday" },
		func(input *Pin) { input.ObservedUTC = "2026-11-20T00:00:00+01:00" },
		func(input *Pin) { input.GitVersion = "unknown" },
	} {
		invalid := pin
		mutate(&invalid)
		data, err := json.Marshal(invalid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPin(bytes.NewReader(data)); err == nil {
			t.Errorf("accepted invalid pin %+v", invalid)
		}
	}
}

func TestAcquisitionRejectsUnsafeRequests(t *testing.T) {
	root := t.TempDir()
	config := AcquisitionConfig{Sandbox: root, Protected: []string{root}}
	if _, err := ObservePin(context.Background(), config, "https://example.org/repository.git"); err == nil {
		t.Error("observer accepted a protected sandbox")
	}
	if _, err := MaterializePin(context.Background(), config, Pin{}); err == nil {
		t.Error("materializer accepted a protected sandbox")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unsafe request changed protected root: %v err=%v", entries, err)
	}
	config.Protected = []string{t.TempDir()}
	if _, err := ObservePin(context.Background(), config, t.TempDir()); err == nil {
		t.Error("production observer accepted local transport")
	}
	entries, err = os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected transport left control files: %v err=%v", entries, err)
	}
}
