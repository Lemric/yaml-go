package yaml

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These tests intentionally define the public contract before its implementation.
// Mapping is expected to retain insertion order and to support scalar keys, unlike
// a Go map. This is required for stable dumps and YAML-compatible mapping keys.

func pairs(values ...any) Mapping {
	if len(values)%2 != 0 {
		panic("pairs requires key/value arguments")
	}
	m := make(Mapping, 0, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		m = append(m, Pair{Key: values[i], Value: values[i+1]})
	}
	return m
}

func sequence(values ...any) []any { return values }

func assertValue(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("value mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func assertFloat(t *testing.T, got any, want float64) {
	t.Helper()
	n, ok := got.(float64)
	if !ok || (math.IsNaN(want) && !math.IsNaN(n)) || (!math.IsNaN(want) && n != want) {
		t.Fatalf("got %#v; want float64(%v)", got, want)
	}
}

func requireError(t *testing.T, err error, contains ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, part := range contains {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("error %q does not contain %q", err, part)
		}
	}
}

func parseOK(t *testing.T, input string, flags Flags) any {
	t.Helper()
	got, err := Parse(input, flags)
	if err != nil {
		t.Fatalf("Parse(%q): %v", input, err)
	}
	return got
}

func inlineParseOK(t *testing.T, input string, flags Flags) any {
	t.Helper()
	got, err := ParseInline(input, flags)
	if err != nil {
		t.Fatalf("ParseInline(%q): %v", input, err)
	}
	return got
}

func dumpOK(t *testing.T, value any, inline, indent int, flags Flags) string {
	t.Helper()
	got, err := Dump(value, inline, indent, flags)
	if err != nil {
		t.Fatalf("Dump(%#v): %v", value, err)
	}
	return got
}

func inlineDumpOK(t *testing.T, value any, flags Flags) string {
	t.Helper()
	got, err := DumpInline(value, flags)
	if err != nil {
		t.Fatalf("DumpInline(%#v): %v", value, err)
	}
	return got
}

func TestParseAndDumpRoundTrip(t *testing.T) {
	want := pairs("lorem", "ipsum", "dolor", "sit")
	encoded := dumpOK(t, want, 2, 4, 0)
	assertValue(t, parseOK(t, encoded, 0), want)
}

func TestDumpRejectsNonPositiveIndentation(t *testing.T) {
	for _, indent := range []int{0, -4} {
		_, err := Dump(pairs("lorem", "ipsum"), 2, indent, 0)
		requireError(t, err, "indentation must be greater than zero")
	}
}

func TestParseLimits(t *testing.T) {
	t.Run("nesting depth", func(t *testing.T) {
		input := "root:\n  child:\n    grandchild:\n      greatgrandchild: value\n"
		_, err := ParseWithLimits(input, 0, Limits{MaxNestingDepth: 2, MaxCollectionAliases: DefaultMaxCollectionAliases})
		requireError(t, err, "Maximum nesting depth of 2 exceeded")
	})

	t.Run("collection aliases in a file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "aliases.yaml")
		if err := os.WriteFile(path, []byte("defaults: &defaults [foo, bar]\ncopy: *defaults\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := ParseFileWithLimits(path, 0, Limits{MaxNestingDepth: DefaultMaxNestingDepth, MaxCollectionAliases: 0})
		requireError(t, err, "Maximum number of collection aliases (0) exceeded")
	})
}

func TestParserIsReusableAndStateless(t *testing.T) {
	p := NewParser(Limits{MaxNestingDepth: DefaultMaxNestingDepth, MaxCollectionAliases: DefaultMaxCollectionAliases})
	for i := 0; i < 2; i++ {
		got, err := p.Parse("# translations/messages.en.yaml\n\n", 0)
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("empty commented document = %#v; want nil", got)
		}
	}
	_, err := p.Parse("abc:\n\tabc", 0)
	requireError(t, err, "tabs as indentation", "line 2")
}

func TestParserClearsAnchorsBetweenRuns(t *testing.T) {
	p := NewParser(Limits{MaxNestingDepth: DefaultMaxNestingDepth, MaxCollectionAliases: DefaultMaxCollectionAliases})
	if _, err := p.Parse("foo: &foo\n  baz: foobar\nbar:\n  <<: *foo\n", 0); err != nil {
		t.Fatal(err)
	}
	_, err := p.Parse("bar:\n  <<: *foo\n", 0)
	requireError(t, err, `Reference "foo" does not exist`, "line 2")
}

func TestParserResetsDocumentLengthBetweenRuns(t *testing.T) {
	p := NewParser(Limits{MaxNestingDepth: DefaultMaxNestingDepth, MaxCollectionAliases: DefaultMaxCollectionAliases})
	if _, err := p.Parse("foo: bar", 0); err != nil {
		t.Fatal(err)
	}
	got, err := p.Parse("a:\n    b: |\n        row\n        row2\nc: d", 0)
	if err != nil {
		t.Fatal(err)
	}
	assertValue(t, got, pairs("a", pairs("b", "row\nrow2\n"), "c", "d"))
}

func TestTimeValuesUseStandardLibraryTime(t *testing.T) {
	want := time.Date(2001, 12, 15, 2, 59, 43, 100_000_000, time.UTC)
	got := inlineParseOK(t, "2001-12-15T02:59:43.1Z", ParseTimestamps)
	assertValue(t, got, want)
}

func TestParseErrorFormatting(t *testing.T) {
	err := &ParseError{Problem: "Error message", Line: 42, Snippet: "foo: bar", Filename: "/var/www/app/config.yml"}
	want := `Error message in "/var/www/app/config.yml" at line 42 (near "foo: bar")`
	if err.Error() != want {
		t.Fatalf("Error() = %q; want %q", err.Error(), want)
	}

	unicodeErr := &ParseError{Problem: "Error message", Line: 42, Snippet: "foo: bar", Filename: "äöü.yml"}
	if !strings.Contains(unicodeErr.Error(), `in "äöü.yml"`) {
		t.Fatalf("unicode filename was corrupted: %q", unicodeErr)
	}

	var target *ParseError
	if !errors.As(err, &target) {
		t.Fatalf("ParseError must participate in errors.As")
	}
}
