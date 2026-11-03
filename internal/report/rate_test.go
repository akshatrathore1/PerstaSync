package report

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

func TestRateRequiresEvidence(t *testing.T) {
	invalid := []string{
		`{}`,
		`null`,
		`{"denominator":4,"interval":{"lower":0,"upper":1},"exclusions":0}`,
		`{"numerator":1,"interval":{"lower":0,"upper":1},"exclusions":0}`,
		`{"numerator":1,"denominator":4,"exclusions":0}`,
		`{"numerator":1,"denominator":4,"interval":{"lower":0,"upper":1}}`,
		`{"numerator":null,"denominator":4,"interval":{"lower":0,"upper":1},"exclusions":0}`,
		`{"numerator":1,"denominator":4,"interval":{},"exclusions":0}`,
		`{"numerator":1,"denominator":4,"interval":{"lower":0},"exclusions":0}`,
		`{"numerator":1,"denominator":4,"interval":{"upper":1},"exclusions":0}`,
		`{"numerator":0,"denominator":0,"interval":{"lower":0,"upper":1},"exclusions":0}`,
		`{"numerator":5,"denominator":4,"interval":{"lower":0,"upper":1},"exclusions":0}`,
		`{"numerator":1,"denominator":4,"interval":{"lower":0.8,"upper":0.2},"exclusions":0}`,
		`{"numerator":1,"denominator":4,"interval":{"lower":-0.1,"upper":1},"exclusions":0}`,
		`{"numerator":1,"denominator":4,"interval":{"lower":0,"upper":1.1},"exclusions":0}`,
		`{"numerator":1,"denominator":4,"interval":{"lower":0,"upper":1},"exclusions":-1}`,
		`{"numerator":1,"denominator":4,"interval":{"lower":0,"upper":1},"exclusions":0,"estimate":0.25}`,
	}
	for _, input := range invalid {
		t.Run(input, func(t *testing.T) {
			var rate Rate
			if err := json.Unmarshal([]byte(input), &rate); err == nil {
				t.Fatal("accepted an incomplete or invalid rate")
			}
		})
	}
	var output bytes.Buffer
	if err := WriteRate(&output, Rate{}); err == nil || output.Len() != 0 {
		t.Fatalf("uninitialized rate emitted output: %q err=%v", output.String(), err)
	}
}

func TestRateExplicitZeros(t *testing.T) {
	rate, err := NewRate(0, 4, Interval{Lower: 0, Upper: 0.5}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WriteRate(&output, rate); err != nil {
		t.Fatal(err)
	}
	expected := "{\"numerator\":0,\"denominator\":4,\"interval\":{\"lower\":0,\"upper\":0.5},\"exclusions\":0}\n"
	if output.String() != expected {
		t.Fatalf("got %q, want %q", output.String(), expected)
	}
	var decoded Rate
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	var repeated bytes.Buffer
	if err := WriteRate(&repeated, decoded); err != nil || !bytes.Equal(output.Bytes(), repeated.Bytes()) {
		t.Fatalf("rate round trip changed output: %q err=%v", repeated.String(), err)
	}
}

func TestRateRejectsNonfiniteInterval(t *testing.T) {
	for _, interval := range []Interval{
		{Lower: math.NaN(), Upper: 1},
		{Lower: 0, Upper: math.NaN()},
		{Lower: math.Inf(-1), Upper: 1},
		{Lower: 0, Upper: math.Inf(1)},
	} {
		if _, err := NewRate(1, 4, interval, 0); err == nil {
			t.Fatalf("accepted a nonfinite interval: %+v", interval)
		}
	}
}
