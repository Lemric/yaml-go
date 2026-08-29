package yaml

import (
	"fmt"
	"math"
	"testing"
)

type compatibilityCase struct {
	name     string
	yaml     string
	want     any
	dumpSkip bool
}

func fixtureNaN() float64              { return math.NaN() }
func fixturePositiveInfinity() float64 { return math.Inf(1) }
func fixtureNegativeInfinity() float64 { return math.Inf(-1) }

func TestCompatibilityCorpus(t *testing.T) {
	if len(compatibilityCases) != 130 {
		t.Fatalf("generated compatibility count = %d; want 130", len(compatibilityCases))
	}
	for _, test := range compatibilityCases {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.yaml, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !sameFixtureValue(got, test.want) {
				t.Fatalf("value mismatch\n got: %#v\nwant: %#v\nyaml:\n%s", got, test.want, test.yaml)
			}
		})
	}
}

func TestCompatibilityCorpusDumpRoundTrips(t *testing.T) {
	for _, test := range compatibilityCases {
		if test.dumpSkip {
			continue
		}
		t.Run(test.name, func(t *testing.T) {
			original, err := Parse(test.yaml, 0)
			if err != nil {
				t.Fatal(err)
			}
			dumped, err := Dump(original, 10, 4, 0)
			if err != nil {
				t.Fatal(err)
			}
			roundTripped, err := Parse(dumped, 0)
			if err != nil {
				t.Fatalf("cannot parse dumped fixture: %v\n%s", err, dumped)
			}
			if !sameFixtureValue(roundTripped, original) {
				t.Fatalf("round-trip mismatch\noriginal: %#v\n dumped: %s\n parsed: %#v", original, dumped, roundTripped)
			}
		})
	}
}

func sameFixtureValue(got, want any) bool {
	if gotFloat, ok := got.(float64); ok {
		wantFloat, ok := want.(float64)
		return ok && (gotFloat == wantFloat || math.IsNaN(gotFloat) && math.IsNaN(wantFloat))
	}
	switch got := got.(type) {
	case Mapping:
		want, ok := want.(Mapping)
		if !ok || len(got) != len(want) {
			return false
		}
		for i := range got {
			if !sameFixtureValue(got[i].Key, want[i].Key) || !sameFixtureValue(got[i].Value, want[i].Value) {
				return false
			}
		}
		return true
	case []any:
		want, ok := want.([]any)
		if !ok || len(got) != len(want) {
			return false
		}
		for i := range got {
			if !sameFixtureValue(got[i], want[i]) {
				return false
			}
		}
		return true
	case []byte:
		switch want := want.(type) {
		case []byte:
			return string(got) == string(want)
		case string:
			return string(got) == want
		default:
			return false
		}
	default:
		return fmt.Sprintf("%T:%v", got, got) == fmt.Sprintf("%T:%v", want, want)
	}
}
