package yaml

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestInlineLocaleIndependentFloatDump(t *testing.T) {
	t.Setenv("LC_NUMERIC", "fr_FR.UTF-8")
	if got := inlineDumpOK(t, 1.2, 0); got != "1.2" {
		t.Fatalf("locale changed numeric dump to %q", got)
	}
}

func TestInlineExponentialLookingStringRoundTrip(t *testing.T) {
	want := "686e444"
	assertValue(t, inlineParseOK(t, inlineDumpOK(t, want, 0), 0), want)
}

func TestInlineRejectsInvalidTaggedSequenceTail(t *testing.T) {
	_, err := ParseInline("!foo { bar: baz } qux", ParseCustomTags)
	requireError(t, err, "Unexpected")
}

func TestInlineReferencesCoverEveryCollectionPosition(t *testing.T) {
	references := map[string]any{
		"var": "var-value",
		"foo": pairs("a", "Steve", "b", "Clark", "c", "Brian"),
	}
	tests := []struct {
		name, input string
		want        any
	}{
		{"scalar", "*var", "var-value"},
		{"list", "[*var]", sequence("var-value")},
		{"nested list", "[[*var]]", sequence(sequence("var-value"))},
		{"map in list", "[{key: *var}]", sequence(pairs("key", "var-value"))},
		{"embedded map", "[key: *var]", sequence(pairs("key", "var-value"))},
		{"map", "{key: *var}", pairs("key", "var-value")},
		{"list in map", "{key: [*var]}", pairs("key", sequence("var-value"))},
		{"map in map", "{foo: {bar: *var}}", pairs("foo", pairs("bar", "var-value"))},
		{"map alias in sequence", "[*foo]", sequence(pairs("a", "Steve", "b", "Clark", "c", "Brian"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseInlineWithReferences(test.input, 0, references)
			if err != nil {
				t.Fatal(err)
			}
			assertValue(t, got, test.want)
		})
	}
}

func TestInlineAsteriskFollowedByCommentStillRequiresAliasName(t *testing.T) {
	_, err := ParseInline("{ foo: * #foo }", 0)
	requireError(t, err, "reference must contain at least one character", "line 1")
}

func TestInlineQuotedReferenceLikeStringsRemainStrings(t *testing.T) {
	mapping := `{foo: '&foo', bar: "&bar", baz: !!str '&baz'}`
	assertValue(t, inlineParseOK(t, mapping, 0), pairs("foo", "&foo", "bar", "&bar", "baz", "&baz"))
	sequenceYAML := `['&foo', "&bar", !!str '&baz']`
	assertValue(t, inlineParseOK(t, sequenceYAML, 0), sequence("&foo", "&bar", "&baz"))
}

func TestFlowAndBlockAnchorsProduceEquivalentValues(t *testing.T) {
	assertValue(t, inlineParseOK(t, "[&string4]", 0), sequence(nil))
	assertValue(t, inlineParseOK(t, "{foo: &string4}", 0), pairs("foo", nil))
	assertValue(t, inlineParseOK(t, "[!!str &string3]", 0), sequence("&string3"))
	assertValue(t, inlineParseOK(t, "{foo: !!str &string3}", 0), pairs("foo", "&string3"))

	input := "block:\n  - '&string1'\n  - \"&string2\"\n  - !!str &string3\n  - &string4\nflow: ['&string1', \"&string2\", !!str &string3, &string4]"
	want := sequence("&string1", "&string2", "&string3", nil)
	assertValue(t, parseOK(t, input, 0), pairs("block", want, "flow", want))
}

func TestNestedTimestampListAsTime(t *testing.T) {
	want := time.Date(2001, 12, 15, 2, 59, 43, 100_000_000, time.UTC)
	assertValue(t, inlineParseOK(t, "{nested: [2001-12-15T02:59:43.1Z]}", ParseTimestamps), pairs("nested", sequence(want)))
}

func TestDumpTimeRetainsOffset(t *testing.T) {
	berlin := time.FixedZone("Europe/Berlin", 2*60*60)
	value := time.Date(2001, 7, 15, 21, 59, 43, 0, berlin)
	if got := inlineDumpOK(t, value, 0); got != "2001-07-15T21:59:43+02:00" {
		t.Fatalf("got %q", got)
	}
}

func TestDumpNumericKeyFlagOnComplexMapping(t *testing.T) {
	value := pairs(
		42, pairs("foo", 43, 44, "bar"),
		45, "baz",
		46, 46,
	)
	want := "{ '42': { foo: 43, '44': bar }, '45': baz, '46': 46 }"
	if got := inlineDumpOK(t, value, DumpNumericKeysAsStrings); got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}

func TestDumpMultilineCarriageReturns(t *testing.T) {
	tests := []struct {
		name string
		data any
		want string
	}{
		{"CRLF", sequence("a\r\nb\nc"), "- \"a\\r\\nb\\nc\"\n"},
		{"standalone CR", pairs("parent", pairs("foo", "bar\n\rbaz: qux")), "parent:\n    foo: \"bar\\n\\rbaz: qux\"\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := dumpOK(t, test.data, 4, 4, DumpMultiLineLiteralBlock)
			if got != test.want {
				t.Fatalf("got %q; want %q", got, test.want)
			}
			assertValue(t, parseOK(t, got, 0), test.data)
		})
	}
}

func TestDumpMultilineFirstLineContainingOnlySpaces(t *testing.T) {
	data := pairs("data", pairs("multi_line", "    \nthe second line\nThe third line."))
	wantYAML := "data:\n    multi_line: |-\n            \n        the second line\n        The third line."
	got := dumpOK(t, data, 2, 4, DumpMultiLineLiteralBlock)
	if got != wantYAML {
		t.Fatalf("got %q; want %q", got, wantYAML)
	}
	// YAML strips the spaces-only first content line.
	wantValue := pairs("data", pairs("multi_line", "\nthe second line\nThe third line."))
	assertValue(t, parseOK(t, got, 0), wantValue)
}

func TestTopLevelTaggedMultilineChomping(t *testing.T) {
	tests := []struct {
		name, value, want string
	}{
		{"clip", "one\ntwo\n", "!my-tag |\n    one\n    two\n"},
		{"keep two", "one\ntwo\n\n", "!my-tag |+\n    one\n    two\n\n"},
		{"keep three", "one\ntwo\n\n\n", "!my-tag |+\n    one\n    two\n\n\n"},
		{"strip", "one\ntwo", "!my-tag |-\n    one\n    two"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := dumpOK(t, TaggedValue{Tag: "my-tag", Value: test.value}, 2, 4, DumpMultiLineLiteralBlock)
			if got != test.want {
				t.Fatalf("got %q; want %q", got, test.want)
			}
		})
	}
}

func TestForceDoubleQuotesCoversAllScalarKinds(t *testing.T) {
	tests := []struct {
		name string
		data Mapping
		want string
	}{
		{"colon", pairs("foo", "colon: value"), `{ foo: "colon: value" }`},
		{"dash", pairs("foo", "- dash"), `{ foo: "- dash" }`},
		{"question", pairs("foo", "? question"), `{ foo: "? question" }`},
		{"hash", pairs("foo", "# hash"), `{ foo: "# hash" }`},
		{"integer unaffected", pairs("foo", 23), `{ foo: 23 }`},
		{"boolean unaffected", pairs("foo", true), `{ foo: true }`},
		{"null unaffected", pairs("foo", nil), `{ foo: null }`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dumpOK(t, test.data, 0, 4, DumpForceDoubleQuotesOnValues); got != test.want {
				t.Fatalf("got %q; want %q", got, test.want)
			}
		})
	}
}

func TestDumpIdeographicSpacesInMappings(t *testing.T) {
	data := pairs("alone", "\u3000", "within_string", "a\u3000b", "regular_space", "a b")
	want := "alone: '　'\nwithin_string: 'a　b'\nregular_space: 'a b'\n"
	if got := dumpOK(t, data, 2, 4, 0); got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}

func TestDumpTimestampFractionPrecision(t *testing.T) {
	tests := []struct {
		nanoseconds int
		want        string
	}{
		{450_000_000, "date: 2023-01-24T01:02:03.450+00:00\n"},
		{456_700_000, "date: 2023-01-24T01:02:03.456700+00:00\n"},
		{456_780_000, "date: 2023-01-24T01:02:03.456780+00:00\n"},
	}
	for _, test := range tests {
		value := time.Date(2023, 1, 24, 1, 2, 3, test.nanoseconds, time.UTC)
		if got := dumpOK(t, pairs("date", value), 1, 4, 0); got != test.want {
			t.Fatalf("got %q; want %q", got, test.want)
		}
	}
}

func TestParserTrailingSpacesAfterMappingKey(t *testing.T) {
	input := "items:  \n  foo: bar"
	assertValue(t, parseOK(t, input, 0), pairs("items", pairs("foo", "bar")))
}

func TestParserLastResortDoesNotHideErrors(t *testing.T) {
	for _, input := range []string{"a\n    b:", "a\n\nb\n    c:", " &  *  !  |  >  '  \"  %  @  ` #, { asd a;sdasd }-@^qw3"} {
		_, err := Parse(input, 0)
		requireError(t, err)
	}
}

func TestParserInlineCommentsAfterValues(t *testing.T) {
	tests := []struct {
		input string
		want  Mapping
	}{
		{"{\n  foo: 3, # comment\n  bar: 3\n}", pairs("foo", 3, "bar", 3)},
		{"{\n  foo: 3 #comment\n}", pairs("foo", 3)},
		{"{\n  foo: 3\t# comment\n}", pairs("foo", 3)},
		{"{\n  foo: example.com/#about\n}", pairs("foo", "example.com/#about")},
	}
	for _, test := range tests {
		assertValue(t, parseOK(t, test.input, 0), test.want)
	}
}

func TestParserEscapedQuotesAcrossLines(t *testing.T) {
	single := "entries:\n - message: 'No emails - Address: ''test@example.com''\n       Keyword: ''Order'''\n   outcome: failed"
	wantSingle := pairs("entries", sequence(pairs("message", "No emails - Address: 'test@example.com' Keyword: 'Order'", "outcome", "failed")))
	assertValue(t, parseOK(t, single, 0), wantSingle)

	double := "entries:\n - message: \"No emails - Address: \\\"test@example.com\\\"\n       Keyword: \\\"Order\\\"\"\n   outcome: failed"
	wantDouble := pairs("entries", sequence(pairs("message", `No emails - Address: "test@example.com" Keyword: "Order"`, "outcome", "failed")))
	assertValue(t, parseOK(t, double, 0), wantDouble)
}

func TestParserSingleQuotedBackslashIsLiteral(t *testing.T) {
	assertValue(t, parseOK(t, "foo: 'bar\\'", 0), pairs("foo", "bar\\"))
}

func TestParserRejectsInvalidEscapedQuoteInFlowSequence(t *testing.T) {
	_, err := Parse(`["\"]`, 0)
	requireError(t, err)
}

func TestParseFileRejectsUnreadableFileWhenPermissionsApply(t *testing.T) {
	path := writeLintFile(t, "foo: bar")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if file, openErr := os.Open(path); openErr == nil {
		_ = file.Close()
		t.Skip("this platform or user can still read permission-zero files")
	}
	_, err := ParseFile(path, 0)
	requireError(t, err, "cannot be read")
}

func TestParseLargeCollectionAliasedOnceRetainsAllItems(t *testing.T) {
	items := make([]string, 500)
	for i := range items {
		items[i] = "item"
	}
	input := "defaults: &defaults [" + strings.Join(items, ", ") + "]\na: *defaults"
	got := parseOK(t, input, 0)
	mapping := got.(Mapping)
	aliased := mapping[1].Value.([]any)
	if len(aliased) != 500 {
		t.Fatalf("alias item count = %d; want 500", len(aliased))
	}
}
