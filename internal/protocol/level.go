package protocol

import "strings"

type Level string

const (
	L0           Level = "L0"
	L1           Level = "L1"
	L2           Level = "L2"
	L1Unknown    Level = "L1-unknown"
	Undetermined Level = "undetermined"
)

func (level Level) Valid() bool {
	switch level {
	case L0, L1, L2, L1Unknown, Undetermined:
		return true
	default:
		return false
	}
}

type Coverage uint8

const (
	CoverageUnknown Coverage = iota
	CoverageAbsent
	CoveragePresent
)

type Facts struct {
	SameDirectory Coverage
	FileCoverage  Coverage
	DirectorySync Coverage
	R3            bool
}

type Result struct {
	Level  Level  `json:"level"`
	R3     bool   `json:"r3"`
	Reason string `json:"reason"`
}

func ClassifyFacts(facts Facts) Result {
	result := Result{R3: facts.R3}
	if facts.SameDirectory > CoveragePresent || facts.FileCoverage > CoveragePresent || facts.DirectorySync > CoveragePresent {
		result.Level = L1Unknown
		result.Reason = "invalid coverage evidence"
		return result
	}
	if facts.SameDirectory != CoveragePresent {
		result.Level = Undetermined
		reasons := []string{"same-directory publication is not established"}
		if facts.SameDirectory == CoverageAbsent {
			reasons[0] = "cross-directory publication is outside the studied protocol"
		}
		if facts.FileCoverage == CoverageUnknown || facts.DirectorySync == CoverageUnknown {
			reasons = append(reasons, "synchronization coverage is unresolved")
		}
		result.Reason = strings.Join(reasons, "; ")
		return result
	}
	switch facts.FileCoverage {
	case CoverageUnknown:
		result.Level = L1Unknown
		result.Reason = "required file synchronization coverage is unresolved"
	case CoverageAbsent:
		result.Level = L0
	case CoveragePresent:
		switch facts.DirectorySync {
		case CoverageUnknown:
			result.Level = L1Unknown
			result.Reason = "post-publication directory synchronization is unresolved"
		case CoverageAbsent:
			result.Level = L1
		case CoveragePresent:
			result.Level = L2
		}
	}
	return result
}
