package criticality

import "testing"

func TestCriticality(t *testing.T) {
	weights := map[Class]int{C0: 0, C1: 4, C2: 3, C3: 2, C4: 1}
	for class, expected := range weights {
		weight, err := class.Weight()
		if err != nil || weight != expected {
			t.Fatalf("%s: weight=%d err=%v, want %d", class, weight, err, expected)
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
