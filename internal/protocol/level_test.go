package protocol

import "testing"

func TestLevel(t *testing.T) {
	cases := []struct {
		name  string
		facts Facts
		level Level
	}{
		{"no sync", Facts{CoveragePresent, CoverageAbsent, CoverageAbsent, false}, L0},
		{"file only", Facts{CoveragePresent, CoveragePresent, CoverageAbsent, false}, L1},
		{"file and directory", Facts{CoveragePresent, CoveragePresent, CoveragePresent, false}, L2},
		{"uncovered metadata", Facts{CoveragePresent, CoverageAbsent, CoveragePresent, false}, L0},
		{"unknown file coverage", Facts{CoveragePresent, CoverageUnknown, CoveragePresent, false}, L1Unknown},
		{"unknown directory sync", Facts{CoveragePresent, CoveragePresent, CoverageUnknown, false}, L1Unknown},
		{"unknown path", Facts{CoverageUnknown, CoveragePresent, CoveragePresent, false}, Undetermined},
		{"cross directory", Facts{CoverageAbsent, CoveragePresent, CoveragePresent, false}, Undetermined},
		{"unchecked errors remain orthogonal", Facts{CoveragePresent, CoveragePresent, CoveragePresent, true}, L2},
		{"zero evidence is unresolved", Facts{}, Undetermined},
		{"invalid evidence", Facts{CoveragePresent, Coverage(99), CoveragePresent, false}, L1Unknown},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := ClassifyFacts(testCase.facts)
			if result.Level != testCase.level || result.R3 != testCase.facts.R3 {
				t.Fatalf("got %+v, want level=%s r3=%v", result, testCase.level, testCase.facts.R3)
			}
			if !result.Level.Valid() {
				t.Fatalf("invalid level: %q", result.Level)
			}
			if (result.Level == L1Unknown || result.Level == Undetermined) && result.Reason == "" {
				t.Fatal("unresolved classification has no reason")
			}
		})
	}
	if Level("").Valid() {
		t.Fatal("empty level is valid")
	}
}
