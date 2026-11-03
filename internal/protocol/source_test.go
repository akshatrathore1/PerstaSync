package protocol

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"perstasync/internal/load"
)

func TestSourceDirect(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	program, err := load.Open(context.Background(), load.Config{
		Directory: root, Patterns: []string{"./testdata/fixtures/direct"}, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	})
	if err != nil {
		t.Fatal(err)
	}
	sites, err := Analyze(program.SSA, program.Initial, root)
	if err != nil {
		t.Fatal(err)
	}
	levels := make([]Level, 0, len(sites))
	for _, site := range sites {
		levels = append(levels, site.Level)
		if site.File != "testdata/fixtures/direct/fixture.go" || site.Offset < 0 {
			t.Fatalf("invalid source coordinate: %+v", site)
		}
	}
	expected := []Level{L0, L1, L2, L0, Undetermined, Undetermined}
	if !reflect.DeepEqual(levels, expected) {
		t.Fatalf("source levels got %v, want %v; sites=%+v", levels, expected, sites)
	}
	for _, site := range sites {
		if !site.R3 {
			t.Fatalf("discarded file sync/close error before publication was lost: %+v", site)
		}
	}
	repeated, err := Analyze(program.SSA, program.Initial, root)
	if err != nil || !reflect.DeepEqual(sites, repeated) {
		t.Fatalf("nondeterministic classification: %+v err=%v", repeated, err)
	}
}

func TestSourceOpenFile(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	program, err := load.Open(context.Background(), load.Config{
		Directory: root, Patterns: []string{"./testdata/fixtures/openflags"}, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	})
	if err != nil {
		t.Fatal(err)
	}
	sites, err := Analyze(program.SSA, program.Initial, root)
	if err != nil {
		t.Fatal(err)
	}
	expected := []Level{L1Unknown, L1Unknown, L2, L0}
	if len(sites) != len(expected) {
		t.Fatalf("got %d sites, want %d", len(sites), len(expected))
	}
	for index, site := range sites {
		if site.Level != expected[index] || !site.R3 || site.File != "testdata/fixtures/openflags/fixture.go" || site.Offset < 0 {
			t.Errorf("site %d: got %+v, want level %s with unchecked close", index, site, expected[index])
		}
	}
}
