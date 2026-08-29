package yaml

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseTopLevelAndEmptyValues(t *testing.T) {
	tests := []struct {
		name, input string
		want        any
		flags       Flags
	}{
		{"number", "5", 5, 0},
		{"null", "null", nil, 0},
		{"empty expanded mapping value", "foo:\n    bar:\n    baz: qux", pairs("foo", pairs("bar", nil, "baz", "qux")), 0},
		{"empty expanded sequence value", "foo:\n    - bar\n    -\n    - baz", pairs("foo", sequence("bar", nil, "baz")), 0},
		{"tagged number", "!number 5", TaggedValue{Tag: "number", Value: 5}, ParseCustomTags},
		{"tagged null", "!tag null", TaggedValue{Tag: "tag", Value: nil}, ParseCustomTags},
		{"tagged string", "!user barbara", TaggedValue{Tag: "user", Value: "barbara"}, ParseCustomTags},
		{"tagged inline map", "!user { name: barbara }", TaggedValue{Tag: "user", Value: pairs("name", "barbara")}, ParseCustomTags},
		{"tagged block map", "!user\nname: barbara", TaggedValue{Tag: "user", Value: pairs("name", "barbara")}, ParseCustomTags},
		{"tagged block list", "!users\n- barbara", TaggedValue{Tag: "users", Value: sequence("barbara")}, ParseCustomTags},
		{"non-breaking space is content", "-\u00a0foo", "-\u00a0foo", 0},
		{"empty mapping value", "hash:", pairs("hash", nil), 0},
		{"only comments", "# comment 1\n# comment 2", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertValue(t, parseOK(t, tt.input, tt.flags), tt.want) })
	}
}

func TestParseTaggedBlockScalars(t *testing.T) {
	input := "- !text |\n  first line\n  second line\n- !text >-\n  folded\n  text\n- !!binary |\n  SGVsbG8=\n- plain"
	want := sequence(
		TaggedValue{Tag: "text", Value: "first line\nsecond line\n"},
		TaggedValue{Tag: "text", Value: "folded text"},
		[]byte("Hello"),
		"plain",
	)
	assertValue(t, parseOK(t, input, ParseCustomTags), want)

	nested := "foo:\n  - !text |\n    a\n    b"
	assertValue(t, parseOK(t, nested, ParseCustomTags), pairs("foo", sequence(TaggedValue{Tag: "text", Value: "a\nb"})))
}

func TestParseRejectsTabsAsIndentation(t *testing.T) {
	for _, input := range []string{"foo:\n\tbar", "foo:\n \tbar", "foo:\n\t bar", "foo:\n \t bar"} {
		_, err := Parse(input, 0)
		requireError(t, err, "cannot contain tabs as indentation", "line 2")
	}
}

func TestParseAcceptsTabsAsTokenSeparators(t *testing.T) {
	for _, input := range []string{"foo: bar", "foo:\tbar", "foo: \tbar", "foo:\t bar"} {
		assertValue(t, parseOK(t, input, 0), pairs("foo", "bar"))
	}
}

func TestParseDocumentMarkersAndDirective(t *testing.T) {
	assertValue(t, parseOK(t, "--- %YAML:1.0\nfoo\n...", 0), "foo")
	assertValue(t, parseOK(t, "%YAML 1.2\n---\nfoo: 1\nbar: 2", 0), pairs("foo", 1, "bar", 2))
	assertValue(t, parseOK(t, "---\nfoo: bar\n...\n\n", 0), pairs("foo", "bar"))
}

func TestParseBlockChomping(t *testing.T) {
	tests := []struct {
		name, indicator, between, after string
		foo, bar                        string
	}{
		{"literal strip one newline", "|-", "", "\n", "one\ntwo", "one\ntwo"},
		{"literal strip many newlines", "|-", "\n", "\n\n", "one\ntwo", "one\ntwo"},
		{"literal strip no final newline", "|-", "", "", "one\ntwo", "one\ntwo"},
		{"literal clip one newline", "|", "", "\n", "one\ntwo\n", "one\ntwo\n"},
		{"literal clip many newlines", "|", "\n", "\n\n", "one\ntwo\n", "one\ntwo\n"},
		{"literal clip no final newline", "|", "", "", "one\ntwo\n", "one\ntwo"},
		{"literal keep one newline", "|+", "", "\n", "one\ntwo\n", "one\ntwo\n"},
		{"literal keep many newlines", "|+", "\n", "\n\n", "one\ntwo\n\n", "one\ntwo\n\n"},
		{"literal keep no final newline", "|+", "", "", "one\ntwo\n", "one\ntwo"},
		{"folded strip one newline", ">-", "", "\n", "one two", "one two"},
		{"folded strip many newlines", ">-", "\n", "\n\n", "one two", "one two"},
		{"folded strip no final newline", ">-", "", "", "one two", "one two"},
		{"folded clip one newline", ">", "", "\n", "one two\n", "one two\n"},
		{"folded clip many newlines", ">", "\n", "\n\n", "one two\n", "one two\n"},
		{"folded clip no final newline", ">", "", "", "one two\n", "one two"},
		{"folded keep one newline", ">+", "", "\n", "one two\n", "one two\n"},
		{"folded keep many newlines", ">+", "\n", "\n\n", "one two\n\n", "one two\n\n"},
		{"folded keep no final newline", ">+", "", "", "one two\n", "one two"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := "foo: " + tt.indicator + "\n    one\n    two\n" + tt.between + "bar: " + tt.indicator + "\n    one\n    two" + tt.after
			assertValue(t, parseOK(t, input, 0), pairs("foo", tt.foo, "bar", tt.bar))
		})
	}

	leading := "foo: |-\n\n\n    bar\n"
	assertValue(t, parseOK(t, leading, 0), pairs("foo", "\n\nbar"))

	unindented := "foo:\n- bar: |\n    one\n\n    two"
	assertValue(t, parseOK(t, unindented, 0), pairs("foo", sequence(pairs("bar", "one\n\ntwo"))))
}

func TestParseSequencesAndMappingsAcrossIndentation(t *testing.T) {
	tests := []struct {
		name, input string
		want        any
	}{
		{"sequence started by lone dash", "a:\n-\n  b:\n  -\n    bar: baz\n- foo\nd: e", pairs("a", sequence(pairs("b", sequence(pairs("bar", "baz"))), "foo"), "d", "e")},
		{"comment between mapping members", "a:\n    b:\n        - c\n# comment\n    d: e", pairs("a", pairs("b", sequence("c"), "d", "e"))},
		{"non-string after comments", "a:\n    b:\n        {}\n# comment\n    d:\n        1.1", pairs("a", pairs("b", Mapping{}, "d", 1.1))},
		{"mapping in sequence on new line", "foo:\n  -\n    bar: foobar", pairs("foo", sequence(pairs("bar", "foobar")))},
		{"comment before nested mapping", "foo:\n    - bar:\n        # comment\n        baz: [1, 2, 3]", pairs("foo", sequence(pairs("bar", pairs("baz", sequence(1, 2, 3)))))},
		{"blank before nested map", "foo:\n\n    bar: baz", pairs("foo", pairs("bar", "baz"))},
		{"blank sequence item", "foo:\n-\n\n", pairs("foo", sequence(nil))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertValue(t, parseOK(t, tt.input, 0), tt.want) })
	}
}

func TestParseMultilinePlainScalars(t *testing.T) {
	tests := []struct {
		name, input string
		want        any
	}{
		{"last resort", "test:\n  You can have things that don't look like strings here\n  true\n  yes you can", pairs("test", "You can have things that don't look like strings here true yes you can")},
		{"different indentation", "a:\n    b\n       c", pairs("a", "b c")},
		{"top-level", "foo\nbar\n\nbaz", "foo bar\nbaz"},
		{"mapping value", "foo: bar\n  baz\n   foobar\n  foo\nbar: baz", pairs("foo", "bar baz foobar foo", "bar", "baz")},
		{"blank lines", "foo:\n  line 1\n\n  line 2", pairs("foo", "line 1\nline 2")},
		{"comment ends scalar", "key: unquoted\n  # comment\nanother_key: works", pairs("key", "unquoted", "another_key", "works")},
		{"comment after continuation", "key: unquoted\n  next line\n  # comment\nanother_key: works", pairs("key", "unquoted next line", "another_key", "works")},
		{"block mapping multiline", "foo:\n- bar:\n    one\n\n    two\n    three", pairs("foo", sequence(pairs("bar", "one\ntwo three")))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertValue(t, parseOK(t, tt.input, 0), tt.want) })
	}
}

func TestParseMultilineQuotedScalars(t *testing.T) {
	tests := []struct {
		name, input string
		want        any
	}{
		{"double quoted", "foo: \"bar\n  baz\n   foobar\nfoo\"\nbar: baz", pairs("foo", "bar baz foobar foo", "bar", "baz")},
		{"line continuation", "foobar:\n    \"foo\\\n    bar\"", pairs("foobar", "foobar")},
		{"hash is content", "foo:\n    foobar: 'foo\n      #bar'\n    bar: baz", pairs("foo", pairs("foobar", "foo #bar", "bar", "baz"))},
		{"blank line", "foobar: 'foo\n\n    bar'", pairs("foobar", "foo\nbar")},
		{"escaped quote", "foobar: \"foo\n    \\\"bar\\\"\n    baz\"", pairs("foobar", `foo "bar" baz`)},
		{"double quoted backslash", "foobar: \"foo\n    bar\\\\\"", pairs("foobar", "foo bar\\")},
		{"backslashes before newline", "foobar: \"foo\\\\\n    bar\"", pairs("foobar", "foo\\ bar")},
		{"single quoted backslash", "foobar: 'foo\\\n    bar'", pairs("foobar", "foo\\ bar")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertValue(t, parseOK(t, tt.input, 0), tt.want) })
	}
}

func TestParseCommentsAndBlockScalarContent(t *testing.T) {
	literal := "content: |\n    # comment 1\n    header\n\n        # comment 2\n        <body>\n            <h1>title</h1>\n        </body>\n\n    footer # comment3"
	wantLiteral := "# comment 1\nheader\n\n    # comment 2\n    <body>\n        <h1>title</h1>\n    </body>\n\nfooter # comment3"
	assertValue(t, parseOK(t, literal, 0), pairs("content", wantLiteral))

	folded := "test: >\n    <h2>A heading</h2>\n\n    <ul>\n    <li>a list</li>\n    <li>may be a good example</li>\n    </ul>"
	assertValue(t, parseOK(t, folded, 0), pairs("test", "<h2>A heading</h2>\n<ul> <li>a list</li> <li>may be a good example</li> </ul>"))

	indented := "test: >\n    <h2>A heading</h2>\n\n    <ul>\n      <li>a list</li>\n      <li>may be a good example</li>\n    </ul>"
	assertValue(t, parseOK(t, indented, 0), pairs("test", "<h2>A heading</h2>\n<ul>\n  <li>a list</li>\n  <li>may be a good example</li>\n</ul>"))
}

func TestParseAnchorsAliasesAndMergeKeys(t *testing.T) {
	input := "var: &var var-value\nscalar: *var\nlist: [*var]\nnested: [[*var]]\nmap: {key: *var}\nfoo: {bar: &baz baz}\nbar: {foo: *baz}\n"
	want := pairs("var", "var-value", "scalar", "var-value", "list", sequence("var-value"), "nested", sequence(sequence("var-value")), "map", pairs("key", "var-value"), "foo", pairs("bar", "baz"), "bar", pairs("foo", "baz"))
	assertValue(t, parseOK(t, input, 0), want)

	merge := "foo: &FOO\n    bar: 1\nbar: &BAR\n    baz: 2\n    <<: *FOO\nbaz:\n    baz_foo: 3\n    <<:\n        baz_bar: 4\nfoobar:\n    bar: ~\n    <<: [*FOO, *BAR]"
	wantMerge := pairs(
		"foo", pairs("bar", 1),
		"bar", pairs("baz", 2, "bar", 1),
		"baz", pairs("baz_foo", 3, "baz_bar", 4),
		"foobar", pairs("bar", nil, "baz", 2),
	)
	assertValue(t, parseOK(t, merge, 0), wantMerge)

	anchoredMerge := "mergekeyrefdef:\n    a: foo\n    <<: &quux\n        b: bar\n        c: baz\nmergekeyderef:\n    d: quux\n    <<: *quux"
	assertValue(t, parseOK(t, anchoredMerge, 0), pairs("mergekeyrefdef", pairs("a", "foo", "b", "bar", "c", "baz"), "mergekeyderef", pairs("d", "quux", "b", "bar", "c", "baz")))
}

func TestParseAliasesFollowedByComments(t *testing.T) {
	input := "var: &var var-value\nscalar: *var # comment\nlist:\n  - *var # comment\nmap: { key: *var, # comment\n  other: plain }"
	want := pairs("var", "var-value", "scalar", "var-value", "list", sequence("var-value"), "map", pairs("key", "var-value", "other", "plain"))
	assertValue(t, parseOK(t, input, 0), want)

	merge := "base: &base\n    a: foo\nderived:\n    <<: *base # comment\n    b: bar"
	assertValue(t, parseOK(t, merge, 0), pairs("base", pairs("a", "foo"), "derived", pairs("a", "foo", "b", "bar")))
}

func TestParseAliasLimits(t *testing.T) {
	t.Run("collection expansion count", func(t *testing.T) {
		p := NewParser(Limits{MaxNestingDepth: DefaultMaxNestingDepth, MaxCollectionAliases: 5})
		_, err := p.Parse("a0: &a0 [foo]\na1: &a1 [*a0, *a0, *a0]\npayload: [*a1, *a1, *a1]", 0)
		requireError(t, err, "Maximum number of collection aliases")
	})

	t.Run("tagged collections count", func(t *testing.T) {
		p := NewParser(Limits{MaxNestingDepth: DefaultMaxNestingDepth, MaxCollectionAliases: 2})
		_, err := p.Parse("a: &a !my_tag [foo, bar]\nb: *a\nc: *a\nd: *a", ParseCustomTags)
		requireError(t, err, "Maximum number of collection aliases")
	})

	t.Run("scalar aliases do not count", func(t *testing.T) {
		p := NewParser(Limits{MaxNestingDepth: DefaultMaxNestingDepth, MaxCollectionAliases: 1})
		got, err := p.Parse("anchor: &val scalar_value\na: *val\nb: *val\nc: *val\nd: *val", 0)
		if err != nil {
			t.Fatal(err)
		}
		assertValue(t, got, pairs("anchor", "scalar_value", "a", "scalar_value", "b", "scalar_value", "c", "scalar_value", "d", "scalar_value"))
	})

	t.Run("large collection once", func(t *testing.T) {
		items := make([]string, 500)
		wantItems := make([]any, 500)
		for i := range items {
			items[i] = fmt.Sprintf("item%d", i+1)
			wantItems[i] = items[i]
		}
		got := parseOK(t, "defaults: &defaults ["+strings.Join(items, ", ")+"]\na: *defaults", 0)
		assertValue(t, got, pairs("defaults", wantItems, "a", wantItems))
	})

	for _, input := range []string{"defaults: &defaults [foo, bar]\na: *defaults", "defaults: &defaults [foo, bar]\na: [*defaults]"} {
		_, err := Parse(input, ParseRejectAliases)
		requireError(t, err, "Aliases are disabled")
	}
}

func TestParseDetectsCircularReferences(t *testing.T) {
	inputs := []string{
		"foo:\n  - &foo\n    - &bar\n      bar: foobar\n      baz: *foo",
		"foo: &foo\n  bar: &bar\n    foobar: baz\n    baz: *foo",
		"foo: &foo\n  bar: &bar\n    foobar: baz\n    <<: *foo",
	}
	for _, input := range inputs {
		_, err := Parse(input, ParseCustomTags)
		requireError(t, err, "Circular reference", "foo", "bar")
	}
}

func TestParseBinaryBlockScalars(t *testing.T) {
	for _, input := range []string{
		`data: !!binary "SGVsbG8gd29ybGQ="`,
		"data: !!binary 'SGVsbG8gd29ybGQ='",
		"data: !!binary |\n    SGVsbG8gd29ybGQ=",
		"data: !!binary |\n    SGVs bG8gd 29ybGQ=",
	} {
		assertValue(t, parseOK(t, input, 0), pairs("data", []byte("Hello world")))
	}
	for _, input := range []string{
		`data: !!binary "SGVsbG8d29ybGQ="`,
		"data: !!binary |\n    SGVsbG8#d29ybGQ=",
		"data: !!binary |\n    SGVsbG8gd29yb===",
	} {
		_, err := Parse(input, 0)
		requireError(t, err, "base64 encoded data")
	}
}

func TestParseDates(t *testing.T) {
	assertValue(t, parseOK(t, "date: 2002-12-14T01:23:45.670000Z", 0), pairs("date", 1039829025.67))
	assertValue(t, parseOK(t, "date: 2002-12-14", ParseTimestamps), pairs("date", time.Date(2002, 12, 14, 0, 0, 0, 0, time.UTC)))
}

func TestParseCustomTags(t *testing.T) {
	tests := []struct {
		name, input string
		want        any
	}{
		{"scalars", "foo: !inline bar\nquz: !long >\n  this is a long\n  text", pairs("foo", TaggedValue{Tag: "inline", Value: "bar"}, "quz", TaggedValue{Tag: "long", Value: "this is a long text"})},
		{"sequences", "- !foo\n    - yaml\n- !quz [bar]", sequence(TaggedValue{Tag: "foo", Value: sequence("yaml")}, TaggedValue{Tag: "quz", Value: sequence("bar")})},
		{"nested mapping", "!foo\nfoo: !quz [bar]\nquz: !foo\n   quz: bar", TaggedValue{Tag: "foo", Value: pairs("foo", TaggedValue{Tag: "quz", Value: sequence("bar")}, "quz", TaggedValue{Tag: "foo", Value: pairs("quz", "bar")})}},
		{"inline", "- !foo [foo, bar]\n- !quz {foo: bar, quz: !bar {one: bar}}", sequence(TaggedValue{Tag: "foo", Value: sequence("foo", "bar")}, TaggedValue{Tag: "quz", Value: pairs("foo", "bar", "quz", TaggedValue{Tag: "bar", Value: pairs("one", "bar")})})},
		{"non-specific", "! 12", 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertValue(t, parseOK(t, tt.input, ParseCustomTags), tt.want) })
	}
}

func TestParseBlockScalarArray(t *testing.T) {
	input := "anyOf:\n  - $ref: >-\n      #/string/bar\nanyOfMultiline:\n  - $ref: >-\n      #/string/bar\n      second line\nnested:\n  anyOf:\n    - $ref: >-\n        #/string/bar"
	want := pairs(
		"anyOf", sequence(pairs("$ref", "#/string/bar")),
		"anyOfMultiline", sequence(pairs("$ref", "#/string/bar second line")),
		"nested", pairs("anyOf", sequence(pairs("$ref", "#/string/bar"))),
	)
	assertValue(t, parseOK(t, input, 0), want)
}

func TestParseBlockScalarModifiers(t *testing.T) {
	plus := "parameters:\n    abc: |+5 # plus five spaces indent\n         one\n         two\n         three\n         four\n         five"
	want := pairs("parameters", pairs("abc", "one\ntwo\nthree\nfour\nfive"))
	assertValue(t, parseOK(t, plus, 0), want)

	minus := "parameters:\n    abc: |-3 # minus\n       one\n       two\n       three\n       four\n       five"
	assertValue(t, parseOK(t, minus, 0), want)
}

func TestParseWhitespaceAtEndOfLine(t *testing.T) {
	assertValue(t, parseOK(t, "\nfoo:\n    arguments: [ '@bar' ]  \n", 0), pairs("foo", pairs("arguments", sequence("@bar"))))
	assertValue(t, parseOK(t, "\nfoo:\n    bar: {} \n", 0), pairs("foo", pairs("bar", Mapping{})))
	assertValue(t, parseOK(t, "foo: 'bar' \nfoobar: baz", 0), pairs("foo", "bar", "foobar", "baz"))
}

func TestParseIdeographicSpaces(t *testing.T) {
	input := "unquoted: \u3000\nquoted: '\u3000'\nwithin_string: 'a\u3000b'\nregular_space: 'a b'"
	want := pairs("unquoted", "\u3000", "quoted", "\u3000", "within_string", "a\u3000b", "regular_space", "a b")
	assertValue(t, parseOK(t, input, 0), want)
}

func TestParseLargeInputs(t *testing.T) {
	t.Run("plain value", func(t *testing.T) {
		value := strings.Repeat("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx ", 20_000)
		data := pairs("x", value)
		assertValue(t, parseOK(t, dumpOK(t, data, 2, 4, 0), 0), data)
	})
	t.Run("directive", func(t *testing.T) {
		assertValue(t, parseOK(t, "%YAML:"+strings.Repeat("1", 100_000)+"\nfoo: bar\n", 0), pairs("foo", "bar"))
	})
	t.Run("leading comment", func(t *testing.T) {
		assertValue(t, parseOK(t, "#"+strings.Repeat("comment", 20_000)+"\nfoo: bar\n", 0), pairs("foo", "bar"))
	})
	t.Run("document marker", func(t *testing.T) {
		assertValue(t, parseOK(t, "--- "+strings.Repeat("header", 20_000)+"\nfoo: bar\n...   ", 0), pairs("foo", "bar"))
	})
}
