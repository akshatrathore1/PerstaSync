package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

type Interval struct {
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

type Rate struct {
	numerator   uint64
	denominator uint64
	interval    Interval
	exclusions  uint64
	initialized bool
}

type rateRecord struct {
	Numerator   *uint64         `json:"numerator"`
	Denominator *uint64         `json:"denominator"`
	Interval    *intervalRecord `json:"interval"`
	Exclusions  *uint64         `json:"exclusions"`
}

type intervalRecord struct {
	Lower *float64 `json:"lower"`
	Upper *float64 `json:"upper"`
}

func NewRate(numerator, denominator uint64, interval Interval, exclusions uint64) (Rate, error) {
	if denominator == 0 {
		return Rate{}, fmt.Errorf("a rate requires a nonzero denominator")
	}
	if numerator > denominator {
		return Rate{}, fmt.Errorf("rate numerator exceeds its denominator")
	}
	if math.IsNaN(interval.Lower) || math.IsNaN(interval.Upper) || math.IsInf(interval.Lower, 0) || math.IsInf(interval.Upper, 0) {
		return Rate{}, fmt.Errorf("rate interval must be finite")
	}
	if interval.Lower < 0 || interval.Upper > 1 || interval.Lower > interval.Upper {
		return Rate{}, fmt.Errorf("rate interval must be ordered within [0, 1]")
	}
	return Rate{numerator: numerator, denominator: denominator, interval: interval, exclusions: exclusions, initialized: true}, nil
}

func (rate Rate) MarshalJSON() ([]byte, error) {
	if !rate.initialized {
		return nil, fmt.Errorf("rate evidence is missing; construct the rate before emitting it")
	}
	return json.Marshal(rateRecord{
		Numerator:   &rate.numerator,
		Denominator: &rate.denominator,
		Interval:    &intervalRecord{Lower: &rate.interval.Lower, Upper: &rate.interval.Upper},
		Exclusions:  &rate.exclusions,
	})
}

func (rate *Rate) UnmarshalJSON(data []byte) error {
	var record rateRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return err
	}
	if record.Numerator == nil || record.Denominator == nil || record.Interval == nil || record.Exclusions == nil {
		return fmt.Errorf("rate requires numerator, denominator, interval, and exclusions")
	}
	if record.Interval.Lower == nil || record.Interval.Upper == nil {
		return fmt.Errorf("rate interval requires lower and upper bounds")
	}
	validated, err := NewRate(*record.Numerator, *record.Denominator, Interval{Lower: *record.Interval.Lower, Upper: *record.Interval.Upper}, *record.Exclusions)
	if err != nil {
		return err
	}
	*rate = validated
	return nil
}

func WriteRate(writer io.Writer, rate Rate) error {
	return json.NewEncoder(writer).Encode(rate)
}
