package yaml

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseRejectsMalformedDocuments(t *testing.T) {
	tests := []struct {
		name, input string
		messages    []string
	}{
		{"mapping key in multiline", "data:\n    dbal:wrong\n        default_connection: monolith", []string{"Mapping values are not allowed in multi-line blocks", "line 2"}},
		{"non UTF-8", string([]byte{'f', 'o', 'o', ':', ' ', 0xff}), []string{"UTF-8"}},
		{"unindented collection without spaces", "collection:\n-item1\n-item2", nil},
		{"invalid sequence in mapping", "yaml:\n  hash: me\n  - array stuff", nil},
		{"invalid mapping in sequence", "yaml:\n  - array stuff\n  hash: me", nil},
		{"scalar after sequence", "foo:\n    - bar\n\"missing colon\"\n    foo: bar", []string{"missing colon"}},
		{"unterminated quote", "foo:\n    bar: 'first line\n        second line\n'", []string{"Unterminated quoted string"}},
		{"colon in plain value", "foo: bar: baz", []string{"colon cannot be used in an unquoted mapping value"}},
		{"orphan after comment", "key: unquoted\n  # comment\n  next line\nanother_key: works", []string{"Unable to parse", "line 3"}},
		{"root inline map followed by line", "{ foo: bar }\nfoobar", []string{"Unable to parse", "line 2"}},
		{"inline map followed by token", "{ foo: bar } baz", []string{"Unexpected token", "baz"}},
		{"inline sequence followed by token", "['foo'],bar,", []string{"Unexpected token", ",bar,"}},
		{"INI", "[parameters]\n  foo = bar\n  bar = %foo%", []string{"Unable to parse", "line 2"}},
		{"complex mapping", "? \"1\"\n:\n  name: végétalien", []string{"Complex mappings are not supported", "line 1"}},
		{"complex mapping nested in mapping", "diet:\n  ? \"1\"\n  :\n    name: végétalien", []string{"Complex mappings are not supported", "line 2"}},
		{"complex mapping nested in sequence", "- ? \"1\"\n  :\n    name: végétalien", []string{"Complex mappings are not supported", "line 1"}},
		{"extra closing collection", "{\n  \"object\": {\n    \"array\": [\"a\", \"b\", \"c\"]\n  ],\n}\n}", []string{"Malformed", "line"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !utf8.ValidString(tt.input) && tt.name != "non UTF-8" {
				t.Fatal("test setup uses invalid UTF-8 unexpectedly")
			}
			_, err := Parse(tt.input, 0)
			requireError(t, err, tt.messages...)
		})
	}
}

func TestParseRejectsMultipleDocuments(t *testing.T) {
	input := "# Ranking\n---\n- Mark McGwire\n- Sammy Sosa\n# Team ranking\n---\n- Chicago Cubs"
	_, err := Parse(input, 0)
	requireError(t, err, "Multiple documents are not supported")
}

func TestParseRejectsDuplicateKeys(t *testing.T) {
	tests := []struct {
		name, input, key string
	}{
		{"inline", "parent: { child: first, child: duplicate }", "child"},
		{"block", "parent:\n  child: first\n  child: duplicate", "child"},
		{"top-level", "parent: { child: foo }\nparent: { child: bar }", "parent"},
		{"nested mappings", "parent: { child_mapping: { value: bar}, child_mapping: { value: bar} }", "child_mapping"},
		{"nested sequences", "parent: { child_sequence: [key1], child_sequence: [key2] }", "child_sequence"},
		{"duplicate null", "parent:\n  child:\n  child2:\n  child:", "child"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.input, 0)
			requireError(t, err, `Duplicate key "`+tt.key+`" detected`, "line")
		})
	}
}

func TestParseRejectsUnsupportedNonStringMappingKeys(t *testing.T) {
	tests := []struct {
		name, input, message string
	}{
		{"float", "foo:\n    1.2: bar", "Numeric keys are not supported"},
		{"boolean", "true: foo\nfalse: bar", "Non-string keys are not supported"},
	}
	for _, tt := range tests {
		_, err := Parse(tt.input, 0)
		requireError(t, err, tt.message, "Quote")
	}

	got := parseOK(t, "0X4D2: foo\n0x4D2: bar", 0)
	assertValue(t, got, pairs("0X4D2", "foo", 1234, "bar"))

	explicit := "'1.2': bar\n!!str 1.3: baz\n'true': foo\n!!str false: bar\n!!str null: 'null'\n'~': 'null'"
	assertValue(t, parseOK(t, explicit, 0), pairs("1.2", "bar", "1.3", "baz", "true", "foo", "false", "bar", "null", "null", "~", "null"))
}

func TestParseDuplicateErrorLineNumbers(t *testing.T) {
	tests := []struct {
		input string
		line  int
	}{
		{"parent: { child: first, child: duplicate }", 1},
		{"parent:\n  child: first,\n  child: duplicate", 3},
		{"parent:\n  child_mapping:\n    value: bar\n  child_mapping:\n    value: bar", 4},
		{"parent:\n  child_sequence:\n    - key1\n    - key2\n  child_sequence:\n    - key1", 5},
	}
	for _, tt := range tests {
		_, err := Parse(tt.input, 0)
		requireError(t, err, "Duplicate key", "line")
		var parseErr *ParseError
		if !errors.As(err, &parseErr) {
			t.Fatalf("%T is not a ParseError", err)
		}
		if parseErr.Line != tt.line {
			t.Fatalf("line = %d; want %d", parseErr.Line, tt.line)
		}
	}
}

func TestParseErrorLineNumbersSurviveComments(t *testing.T) {
	tests := []struct {
		line  int
		input string
	}{
		{4, "foo:\n    -\n        # bar\n        bar: \"123\","},
		{5, "foo:\n    -\n        # bar\n        # bar\n        bar: \"123\","},
		{8, "foo:\n    -\n        # foobar\n        baz: 123\nbar:\n    -\n        # bar\n        bar: \"123\","},
	}
	for _, tt := range tests {
		_, err := Parse(tt.input, 0)
		requireError(t, err, "Unexpected characters", "line")
		var parseErr *ParseError
		if !errors.As(err, &parseErr) || parseErr.Line != tt.line {
			t.Fatalf("got %#v; want ParseError on line %d", err, tt.line)
		}
	}
}

func TestParseRejectsUnknownAndDisabledTags(t *testing.T) {
	tests := []struct {
		input, message string
	}{
		{"!iterator [foo]", "Tags support is not enabled"},
		{"!iterator foo", "Tags support is not enabled"},
		{"!!iterator foo", "unsupported built-in tag"},
		{"!!foo", `built-in tag "!!foo" is not implemented`},
	}
	for _, tt := range tests {
		_, err := Parse(tt.input, 0)
		requireError(t, err, tt.message)
	}
}

func TestParseRejectsMissingOrCircularReferences(t *testing.T) {
	_, err := Parse("foo: { &foo { a: Steve, <<: *foo} }", 0)
	requireError(t, err, `Reference "foo" does not exist`)

	_, err = Parse("bar: *missing", 0)
	requireError(t, err, `Reference "missing" does not exist`)
}

func TestParseRejectsExcessiveNesting(t *testing.T) {
	var input strings.Builder
	input.WriteString("root:\n")
	for i := 1; i <= DefaultMaxNestingDepth+1; i++ {
		input.WriteString(strings.Repeat("  ", i))
		input.WriteString("level:\n")
	}
	_, err := Parse(input.String(), 0)
	requireError(t, err, "Maximum nesting depth")
}

func TestParseInlineCollectionsSpanningLines(t *testing.T) {
	tests := []struct {
		name, input string
		want        any
	}{
		{"mapping", "{\n  'foo': 'bar',\n  'bar': 'baz'\n}", pairs("foo", "bar", "bar", "baz")},
		{"plain mapping", "{\n  foo: bar,\n  bar: baz\n}", pairs("foo", "bar", "bar", "baz")},
		{"sequence", "[\n  'foo',\n  'bar'\n]", sequence("foo", "bar")},
		{"nested map", "{ foo: { bar: foobar }\n}", pairs("foo", pairs("bar", "foobar"))},
		{"nested sequence", "[ foo, [bar, baz]\n]", sequence("foo", sequence("bar", "baz"))},
		{"sequence in map", "{\n  'foo': ['bar', 'foobar'],\n  'bar': ['baz']\n}", pairs("foo", sequence("bar", "foobar"), "bar", sequence("baz"))},
		{"flow sequence in block map", "foobar: [foo,\n  bar,\n  baz\n]", pairs("foobar", sequence("foo", "bar", "baz"))},
		{"map in sequence", "[\n  'foo',\n  { 'bar': 'baz' }\n]", sequence("foo", pairs("bar", "baz"))},
		{"flow map in block sequence", "- {\n  foo: bar,\n  bar: baz\n}", sequence(pairs("foo", "bar", "bar", "baz"))},
		{"single quoted multiline", "'foo\n\nbar'", "foo\nbar"},
		{"quoted map value", "foo: 'bar\n\nbaz'", pairs("foo", "bar\nbaz")},
		{"comments", "map: { # comment\n  key: value, # another\n  a: b\n}\nparam: some", pairs("map", pairs("key", "value", "a", "b"), "param", "some")},
		{"brackets in strings", `[["]"], ["}"], ["ba[r"], ['[ba]r'], ["bar]"], {foo: "bar{"}, {foo: "b{ar}"}, {foo: 'bar}'}]`, sequence(sequence("]"), sequence("}"), sequence("ba[r"), sequence("[ba]r"), sequence("bar]"), pairs("foo", "bar{"), pairs("foo", "b{ar}"), pairs("foo", "bar}"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertValue(t, parseOK(t, tt.input, 0), tt.want) })
	}
}
