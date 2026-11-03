package load

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
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
