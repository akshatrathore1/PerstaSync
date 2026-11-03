package report

import (
	"bytes"
	"encoding/json"
	"testing"

	"perstasync/internal/protocol"
)

func TestWriteSites(t *testing.T) {
	sites := []protocol.Site{
		{File: "fixture.go", Offset: 20, Result: protocol.Result{Level: protocol.L1}},
		{File: "fixture.go", Offset: 10, Result: protocol.Result{Level: protocol.L0, R3: true}},
	}
	var first, second bytes.Buffer
	if err := WriteSites(&first, sites); err != nil {
		t.Fatal(err)
	}
	if sites[0].Offset != 20 {
		t.Fatal("reporter reordered the caller's records")
	}
	if err := WriteSites(&second, []protocol.Site{sites[1], sites[0]}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("record ordering changed output: %q vs %q", first.String(), second.String())
	}
	var decoded []protocol.Site
	if err := json.Unmarshal(first.Bytes(), &decoded); err != nil || len(decoded) != 2 || decoded[0].Offset != 10 || !decoded[0].R3 {
		t.Fatalf("invalid reported records: %+v err=%v", decoded, err)
	}
	var empty bytes.Buffer
	if err := WriteSites(&empty, nil); err != nil || empty.String() != "[]\n" {
		t.Fatalf("empty records: %q err=%v", empty.String(), err)
	}
}

func TestWriteSitesRejectsInvalidRecords(t *testing.T) {
	invalid := []protocol.Site{
		{File: "", Result: protocol.Result{Level: protocol.L0}},
		{File: "/fixture.go", Result: protocol.Result{Level: protocol.L0}},
		{File: "C:/fixture.go", Result: protocol.Result{Level: protocol.L0}},
		{File: "..", Result: protocol.Result{Level: protocol.L0}},
		{File: "../fixture.go", Result: protocol.Result{Level: protocol.L0}},
		{File: "folder/../fixture.go", Result: protocol.Result{Level: protocol.L0}},
		{File: "fixture.go", Offset: -1, Result: protocol.Result{Level: protocol.L0}},
		{File: "fixture.go", Result: protocol.Result{Level: protocol.Level("invalid")}},
		{File: "fixture.go", Result: protocol.Result{Level: protocol.L1Unknown}},
		{File: "fixture.go", Result: protocol.Result{Level: protocol.Undetermined}},
	}
	for _, site := range invalid {
		var output bytes.Buffer
		if err := WriteSites(&output, []protocol.Site{site}); err == nil || output.Len() != 0 {
			t.Fatalf("invalid record emitted output: %+v output=%q err=%v", site, output.String(), err)
		}
	}
	valid := protocol.Site{File: "fixture.go", Result: protocol.Result{Level: protocol.L0}}
	var output bytes.Buffer
	if err := WriteSites(&output, []protocol.Site{valid, valid}); err == nil || output.Len() != 0 {
		t.Fatalf("duplicate records emitted output=%q err=%v", output.String(), err)
	}
}
