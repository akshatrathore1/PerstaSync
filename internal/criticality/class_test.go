package criticality

import (
	"strings"
	"testing"
)

func TestCriticality(t *testing.T) {
	weights := map[Class]int{C0: 0, C1: 4, C2: 3, C3: 2, C4: 1}
	for class, expected := range weights {
		weight, err := class.Weight()
		if err != nil || weight != expected {
			t.Fatalf("%s: weight=%d err=%v, want %d", class, weight, err, expected)
		}
		if class.EligibleForDurabilityRates() != (class != C0) {
			t.Fatalf("%s eligible-rate membership is inconsistent with the C0 exclusion", class)
		}
		if !class.IncludedInFlagDiagnostic() {
			t.Fatalf("%s was excluded from the all-class flagged-site diagnostic", class)
		}
	}
	invalidClass := Class("invalid")
	if invalidClass.EligibleForDurabilityRates() || invalidClass.IncludedInFlagDiagnostic() {
		t.Fatal("invalid class was admitted to a denominator")
	}
	boundaries := []Evidence{
		{C0, "constructed lifecycle: scratch is consumed within one invocation and never read after restart"},
		{C1, "constructed reader: recovery reads the committed position; loss rolls back acknowledged records"},
		{C2, "constructed reader: authenticated startup requires the sole stored private key; loss prevents startup"},
		{C3, "constructed reader: restart loads routing configuration; loss requires manual repair without C1/C2 consequences"},
		{C4, "constructed reader: startup automatically rebuilds a lookup cache from independent durable data"},
	}
	for _, boundary := range boundaries {
		t.Run(string(boundary.Class), func(t *testing.T) {
			result, err := Resolve([]Evidence{boundary})
			if err != nil || result.Class != boundary.Class || result.Ambiguous || result.AlsoPossible != "" || result.Justification != boundary.Justification {
				t.Fatalf("documented boundary: got %+v err=%v, want %+v", result, err, boundary)
			}
			for _, missing := range []string{"", " \t\n"} {
				if _, err := Resolve([]Evidence{{boundary.Class, missing}}); err == nil {
					t.Fatalf("%s accepted missing consequence/lifecycle evidence", boundary.Class)
				}
			}
		})
	}
	for _, first := range boundaries[1:] {
		for _, second := range boundaries[1:] {
			if first.Class == second.Class {
				continue
			}
			result, err := Resolve([]Evidence{first, second})
			primary, alternative := first, second
			if weights[primary.Class] > weights[alternative.Class] {
				primary, alternative = alternative, primary
			}
			if err != nil || result.Class != primary.Class || result.AlsoPossible != alternative.Class || !result.Ambiguous || !strings.Contains(result.Justification, first.Justification) || !strings.Contains(result.Justification, second.Justification) {
				t.Fatalf("pair %s/%s: got %+v err=%v, want primary %s and alternative %s", first.Class, second.Class, result, err, primary.Class, alternative.Class)
			}
		}
		for _, candidates := range [][]Evidence{{boundaries[0], first}, {first, boundaries[0]}} {
			if _, err := Resolve(candidates); err == nil {
				t.Fatalf("C0 was accepted as an eligible ambiguity rank: %+v", candidates)
			}
		}
	}
	for _, candidates := range [][]Evidence{
		{{C2, "startup reader requires credential"}, {C4, "automatic safe regeneration"}},
		{{C4, "automatic safe regeneration"}, {C2, "startup reader requires credential"}},
	} {
		result, err := Resolve(candidates)
		if err != nil || result.Class != C4 || result.AlsoPossible != C2 || !result.Ambiguous {
			t.Fatalf("non-adjacent ambiguity: got %+v err=%v", result, err)
		}
	}
	cases := [][]Evidence{
		nil,
		{{C1, ""}},
		{{C2, " "}},
		{{Class("invalid"), "reader evidence"}},
		{{C0, "same invocation"}, {C4, "regeneration"}},
		{{C4, "regeneration"}, {C4, "regeneration"}},
	}
	for _, candidates := range cases {
		if _, err := Resolve(candidates); err == nil {
			t.Fatalf("accepted invalid evidence: %+v", candidates)
		}
	}
	result, err := Resolve([]Evidence{{C0, "scratch consumed within the invocation"}})
	if err != nil || result.Class != C0 || result.Ambiguous {
		t.Fatalf("C0 lifecycle evidence: got %+v err=%v", result, err)
	}
}
