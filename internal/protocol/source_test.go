package protocol

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"perstasync/internal/load"
)

func TestAppendReasonPreservesUniqueEvidence(t *testing.T) {
	for _, test := range []struct {
		first, second, expected string
	}{
		{"", "", ""},
		{"", "path; sync; path", "path; sync"},
		{"path; sync", "sync; interface", "path; sync; interface"},
		{"path; sync; path", "", "path; sync"},
		{"interface; path", "path; interface; external", "interface; path; external"},
	} {
		if actual := appendReason(test.first, test.second); actual != test.expected {
			t.Errorf("reason merge got %q, want %q", actual, test.expected)
		}
	}
}

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

func sourceFixture(t *testing.T, fixture string) (*load.Program, string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	program, err := load.Open(context.Background(), load.Config{
		Directory: root, Patterns: []string{"./testdata/fixtures/" + fixture}, GOOS: "linux", GOARCH: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	return program, root
}

func TestSourceFanoutBound(t *testing.T) {
	program, root := sourceFixture(t, "fanout")
	stats := &analysisStats{}
	sites, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: program.Config.GOOS, MaxFunctionContexts: 64, stats: stats})
	if err == nil || !strings.Contains(err.Error(), "bounded context limit") || len(sites) != 0 {
		t.Fatalf("bounded fanout emitted a partial classification: %+v err=%v", sites, err)
	}
	if stats.invocations > 1000 {
		t.Fatalf("bounded fanout used %d invocations", stats.invocations)
	}
}

type cancellationContext struct {
	context.Context
	stats      *analysisStats
	cancel     context.CancelFunc
	afterCalls int
}

func (control *cancellationContext) Err() error {
	if control.stats.invocations >= control.afterCalls {
		control.cancel()
	}
	return control.Context.Err()
}

func TestSourceCancellation(t *testing.T) {
	program, root := sourceFixture(t, "fanout")
	for _, afterCalls := range []int{0, 32} {
		stats := &analysisStats{}
		parent, cancel := context.WithCancel(context.Background())
		control := &cancellationContext{Context: parent, stats: stats, cancel: cancel, afterCalls: afterCalls}
		sites, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: "linux", Context: control, stats: stats})
		cancel()
		if !errors.Is(err, context.Canceled) || sites != nil || stats.invocations != afterCalls {
			t.Fatalf("cancellation after %d calls: sites=%+v calls=%d err=%v", afterCalls, sites, stats.invocations, err)
		}
	}
}

func TestSourceIgnoresPublicationFreeFanout(t *testing.T) {
	root := t.TempDir()
	var source strings.Builder
	source.WriteString("package fixture\nfunc level00() {}\n")
	for depth := 1; depth <= 24; depth++ {
		fmt.Fprintf(&source, "func level%02d() { level%02d(); level%02d() }\n", depth, depth-1, depth-1)
	}
	for name, data := range map[string]string{
		"go.mod":     "module fixture\n\ngo 1.27.1\n",
		"fixture.go": source.String(),
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	program, err := load.Open(context.Background(), load.Config{Directory: root, Patterns: []string{"./..."}, GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	stats := &analysisStats{}
	sites, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: "linux", stats: stats})
	if err != nil || len(sites) != 0 || stats.invocations != 0 {
		t.Fatalf("publication-free call tree was interpreted: sites=%+v calls=%d err=%v", sites, stats.invocations, err)
	}
}

func TestSourceCallees(t *testing.T) {
	program, root := sourceFixture(t, "callees")
	sites, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: program.Config.GOOS})
	if err != nil {
		t.Fatal(err)
	}
	expected := []Level{L2, L0, L1, L2, L1Unknown, L2, L1, L1, L1Unknown}
	if len(sites) != len(expected) {
		t.Fatalf("got %d sites, want %d: %+v", len(sites), len(expected), sites)
	}
	for index, site := range sites {
		if site.Level != expected[index] || site.R3 != (index != 7) {
			t.Errorf("callee site %d: got %+v, want %s R3=%t", index, site, expected[index], index != 7)
		}
	}
	without, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: program.Config.GOOS, DisableInterprocedural: true})
	if err != nil || len(without) != len(sites) {
		t.Fatalf("callee ablation lost candidates: %+v err=%v", without, err)
	}
	for index, site := range without {
		if site.Level != L1Unknown || site.R3 != sites[index].R3 {
			t.Errorf("callee ablation %d: %+v", index, site)
		}
	}
	repeated, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: program.Config.GOOS})
	if err != nil || !reflect.DeepEqual(sites, repeated) {
		t.Fatalf("callee output changed across runs: %+v err=%v", repeated, err)
	}
}

func TestSourceStructs(t *testing.T) {
	program, root := sourceFixture(t, "structs")
	sites, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: program.Config.GOOS})
	if err != nil {
		t.Fatal(err)
	}
	expected := []Level{L2, L0, L0, L0, L0, L1}
	if len(sites) != len(expected) {
		t.Fatalf("got %d sites, want %d: %+v", len(sites), len(expected), sites)
	}
	for index, site := range sites {
		if site.Level != expected[index] || !site.R3 {
			t.Errorf("struct site %d: got %+v, want %s with R3", index, site, expected[index])
		}
	}
	for _, options := range []Options{
		{GOOS: program.Config.GOOS, DisableStructFields: true},
		{GOOS: program.Config.GOOS, DisableInterprocedural: true},
	} {
		without, err := AnalyzeWithOptions(program.SSA, program.Initial, root, options)
		if err != nil || len(without) != len(sites) {
			t.Fatalf("struct ablation lost candidates: %+v err=%v", without, err)
		}
		for index, site := range without {
			expectedLevel := Undetermined
			if index == 2 || index == 5 {
				expectedLevel = L1Unknown
				if options.DisableInterprocedural {
					expectedLevel = sites[index].Level
				}
			}
			if site.Level != expectedLevel || !site.R3 {
				t.Errorf("struct ablation %d options=%+v: %+v", index, options, site)
			}
		}
	}
}

func TestSourceCaptures(t *testing.T) {
	program, root := sourceFixture(t, "captures")
	sites, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: program.Config.GOOS})
	if err != nil {
		t.Fatal(err)
	}
	expected := []Level{L2, L0, L2, L0, L2, L0, L1, L1Unknown, L0, L0}
	if len(sites) != len(expected) {
		t.Fatalf("got %d sites, want %d: %+v", len(sites), len(expected), sites)
	}
	for index, site := range sites {
		if site.Level != expected[index] || !site.R3 {
			t.Errorf("capture site %d: got %+v, want %s with R3", index, site, expected[index])
		}
	}
	without, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: program.Config.GOOS, DisableAllocCells: true})
	if err != nil || len(without) != len(sites) {
		t.Fatalf("capture ablation lost candidates: %+v err=%v", without, err)
	}
	if without[0].Level != L1Unknown || without[2].Level != L1Unknown {
		t.Fatalf("captured cells did not discriminate their ablation: %+v", without)
	}
	for index, site := range without {
		if site.R3 != sites[index].R3 {
			t.Errorf("capture ablation lost R3 at %d: %+v", index, site)
		}
	}
}

func TestSourcePaths(t *testing.T) {
	program, root := sourceFixture(t, "paths")
	sites, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: program.Config.GOOS})
	if err != nil {
		t.Fatal(err)
	}
	expected := []Level{
		L2, L2, Undetermined, Undetermined, Undetermined, L1,
		L1Unknown, Undetermined, L1Unknown, L1Unknown, L1Unknown, L1Unknown,
	}
	if len(sites) != len(expected) {
		t.Fatalf("got %d sites, want %d: %+v", len(sites), len(expected), sites)
	}
	for index, site := range sites {
		if site.Level != expected[index] || site.R3 != (index != 10) {
			t.Errorf("path site %d: got %+v, want %s R3=%t", index, site, expected[index], index != 10)
		}
	}
	if !strings.Contains(sites[7].Reason, "same-directory") || !strings.Contains(sites[7].Reason, "synchronization") {
		t.Errorf("combined path and dispatch reasons lost: %+v", sites[7])
	}
	if !strings.Contains(sites[6].Reason, "interface") || !strings.Contains(sites[8].Reason, "outside") {
		t.Errorf("dispatch reasons are not explicit: interface=%+v external=%+v", sites[6], sites[8])
	}
	without, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: program.Config.GOOS, DisablePaths: true})
	if err != nil || len(without) != len(sites) {
		t.Fatalf("path ablation lost candidates: %+v err=%v", without, err)
	}
	for index, site := range without {
		expectedLevel := sites[index].Level
		if index < 6 {
			expectedLevel = Undetermined
		}
		if site.Level != expectedLevel || site.R3 != sites[index].R3 {
			t.Errorf("path ablation %d: got %+v, want %s R3=%t", index, site, expectedLevel, sites[index].R3)
		}
	}
}

func TestSourceOpenFile(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	program, err := load.Open(context.Background(), load.Config{
		Directory: root, Patterns: []string{"./testdata/fixtures/openflags"}, GOOS: "linux", GOARCH: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	sites, err := AnalyzeWithOptions(program.SSA, program.Initial, root, Options{GOOS: program.Config.GOOS})
	if err != nil {
		t.Fatal(err)
	}
	expected := []Level{
		L1, L1Unknown, L2, L0, L0, L0, L1, L1Unknown, L1, L0,
		L0, L1Unknown, L1, L0,
	}
	if len(sites) != len(expected) {
		t.Fatalf("got %d sites, want %d", len(sites), len(expected))
	}
	for index, site := range sites {
		if site.Level != expected[index] || !site.R3 || site.File != "testdata/fixtures/openflags/fixture.go" || site.Offset < 0 {
			t.Errorf("site %d: got %+v, want level %s with unchecked close", index, site, expected[index])
		}
	}
	if !strings.Contains(sites[7].Reason, "metadata") {
		t.Errorf("O_DSYNC uncertainty lost its metadata reason: %+v", sites[7])
	}
	for _, options := range []Options{{GOOS: "linux", DisableOpenFlags: true}, {}, {GOOS: "windows"}} {
		without, err := AnalyzeWithOptions(program.SSA, program.Initial, root, options)
		if err != nil || len(without) != len(sites) {
			t.Fatalf("conservative flag analysis: sites=%+v err=%v", without, err)
		}
		for _, index := range []int{0, 6, 8} {
			if without[index].Level != L1Unknown || without[index].R3 != sites[index].R3 {
				t.Errorf("flag boundary %d did not discriminate options %+v: %+v", index, options, without[index])
			}
		}
		for _, index := range []int{1, 2, 3, 4, 5, 7, 9, 10, 11, 12, 13} {
			if without[index].Level != sites[index].Level || without[index].R3 != sites[index].R3 {
				t.Errorf("independent boundary %d changed for options %+v: %+v", index, options, without[index])
			}
		}
	}
}
