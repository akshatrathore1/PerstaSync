package criticality

import (
	"fmt"
	"strings"
)

type Class string

const (
	C0 Class = "C0"
	C1 Class = "C1"
	C2 Class = "C2"
	C3 Class = "C3"
	C4 Class = "C4"
)

func (class Class) Weight() (int, error) {
	switch class {
	case C0:
		return 0, nil
	case C1:
		return 4, nil
	case C2:
		return 3, nil
	case C3:
		return 2, nil
	case C4:
		return 1, nil
	default:
		return 0, fmt.Errorf("invalid criticality class %q", class)
	}
}

type Evidence struct {
	Class         Class
	Justification string
}

type Result struct {
	Class         Class  `json:"class"`
	Ambiguous     bool   `json:"ambiguous"`
	AlsoPossible  Class  `json:"also_possible"`
	Justification string `json:"justification"`
}

func Resolve(candidates []Evidence) (Result, error) {
	if len(candidates) == 0 {
		return Result{}, fmt.Errorf("criticality requires consequence evidence")
	}
	primary := candidates[0]
	alternative := candidates[0]
	seen := make(map[Class]bool)
	for _, candidate := range candidates {
		weight, err := candidate.Class.Weight()
		if err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(candidate.Justification) == "" {
			return Result{}, fmt.Errorf("%s requires a consequence or lifecycle justification", candidate.Class)
		}
		if candidate.Class == C0 && len(candidates) != 1 {
			return Result{}, fmt.Errorf("C0 is a lifecycle exclusion, not an ambiguity rank")
		}
		if seen[candidate.Class] {
			return Result{}, fmt.Errorf("duplicate criticality candidate %s", candidate.Class)
		}
		seen[candidate.Class] = true
		primaryWeight, _ := primary.Class.Weight()
		alternativeWeight, _ := alternative.Class.Weight()
		if weight < primaryWeight {
			primary = candidate
		}
		if weight > alternativeWeight {
			alternative = candidate
		}
	}
	result := Result{Class: primary.Class, Justification: primary.Justification}
	if primary.Class != alternative.Class {
		result.Ambiguous = true
		result.AlsoPossible = alternative.Class
		result.Justification += "; alternative " + string(alternative.Class) + ": " + alternative.Justification
	}
	return result, nil
}
