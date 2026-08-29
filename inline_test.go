package yaml

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestInlineParseScalars(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		want  any
		flags Flags
	}{
		{"empty", "", "", 0},
		{"null", "null", nil, 0},
		{"tilde null", "~", nil, 0},
		{"false", "false", false, 0},
		{"true", "true", true, 0},
		{"positive integer", "12", 12, 0},
		{"negative integer", "-12", -12, 0},
		{"explicit positive integer", "+12", 12, 0},
		{"integer separators", "1_2", 12, 0},
		{"leading separator is string", "_12", "_12", 0},
		{"trailing separator", "12_", 12, 0},
		{"double quoted", `"quoted string"`, "quoted string", 0},
		{"single quoted", `'quoted string'`, "quoted string", 0},
		{"float", "1234.0", 1234.0, 0},
		{"exponent", "12.30e+02", 1230.0, 0},
		{"float separators", "123.45_67", 123.4567, 0},
		{"hexadecimal", "0x4D2", 1234, 0},
		{"hex separators", "0x_4_D_2_", 1234, 0},
		{"uppercase hex prefix is string", "0X4D2", "0X4D2", 0},
		{"octal", "0o2333", 1243, 0},
		{"octal separators", "0o_2_3_3_3", 1243, 0},
		{"numeric-looking quoted string", `'686e444'`, "686e444", 0},
		{"exponential numeric", "686e444", math.Inf(1), 0},
		{"integer overflow remains string", "123456789123456789123456789123456789", "123456789123456789123456789123456789", 0},
		{"escaped CRLF", `"foo\r\nbar"`, "foo\r\nbar", 0},
		{"hash without space", `'foo#bar'`, "foo#bar", 0},
		{"hash with space quoted", `'foo # bar'`, "foo # bar", 0},
		{"color", `'#cfcfcf'`, "#cfcfcf", 0},
		{"double colon", "::form_base.html.twig", "::form_base.html.twig", 0},
		{"YAML 1.1 yes stays string", "yes", "yes", 0},
		{"YAML 1.1 no stays string", "no", "no", 0},
		{"YAML 1.1 on stays string", "on", "on", 0},
		{"YAML 1.1 off stays string", "off", "off", 0},
		{"date defaults to unix seconds", "2007-10-30", int64(1193702400), 0},
		{"timestamp defaults to unix seconds", "2007-10-30T02:59:43Z", int64(1193713183), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertValue(t, inlineParseOK(t, tt.yaml, tt.flags), tt.want)
		})
	}
}

func TestParseScalarUnescapesCorrectSingleQuotes(t *testing.T) {
	got, err := ParseScalar(`'don''t do somthin'' like that'`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "don't do somthin' like that" {
		t.Fatalf("got %q", got)
	}
}

func TestIsMapping(t *testing.T) {
	tests := []struct {
		value any
		want  bool
	}{
		{Mapping{}, true},
		{pairs("foo", 1), true},
		{sequence(), false},
		{sequence(1, 2, 3), false},
	}
	for _, tt := range tests {
		if got := IsMapping(tt.value); got != tt.want {
			t.Fatalf("IsMapping(%#v) = %t; want %t", tt.value, got, tt.want)
		}
	}
}

func TestInlineParseSpecialFloats(t *testing.T) {
	for _, input := range []string{".nan", ".NaN", ".NAN", "!!float .NaN"} {
		assertFloat(t, inlineParseOK(t, input, 0), math.NaN())
	}
	assertFloat(t, inlineParseOK(t, ".Inf", 0), math.Inf(1))
	assertFloat(t, inlineParseOK(t, "-.Inf", 0), math.Inf(-1))
	if got := inlineDumpOK(t, math.NaN(), 0); got != ".NaN" {
		t.Fatalf("NaN dump = %q", got)
	}
}

func TestInlineParseCollections(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want any
	}{
		{"sequence", "[foo, http://urls.are/no/mappings, false, null, 12]", sequence("foo", "http://urls.are/no/mappings", false, nil, 12)},
		{"spaced sequence", "[  foo  ,   bar , false  ,  null     ,  12  ]", sequence("foo", "bar", false, nil, 12)},
		{"quoted commas", "['foo,bar', 'foo bar']", sequence("foo,bar", "foo bar")},
		{"mapping", `{foo: bar,bar: foo,"false": false, "null": null,integer: 12}`, pairs("foo", "bar", "bar", "foo", "false", false, "null", nil, "integer", 12)},
		{"quoted mapping", `{'foo': 'bar', "bar": 'foo: bar'}`, pairs("foo", "bar", "bar", "foo: bar")},
		{"quoted colon key", `{"foo:bar": "baz"}`, pairs("foo:bar", "baz")},
		{"nested sequences", "[foo, [bar, foo]]", sequence("foo", sequence("bar", "foo"))},
		{"mapping in sequence", "[foo, {bar: foo}]", sequence("foo", pairs("bar", "foo"))},
		{"nested mapping", "{ foo: {bar: foo} }", pairs("foo", pairs("bar", "foo"))},
		{"sequence in mapping", "{ foo: [bar, foo] }", pairs("foo", sequence("bar", "foo"))},
		{"deep collections", "[foo, [bar, [foo, [bar, foo]], foo]]", sequence("foo", sequence("bar", sequence("foo", sequence("bar", "foo")), "foo"))},
		{"embedded map in sequence", "[ foo: { bar: [ 'foobar', 12 ] } ]", sequence(pairs("foo", pairs("bar", sequence("foobar", 12))))},
		{"binary", "{ uid: !!binary Ju0Yh+uqSXOagJZFTlUt8g== }", pairs("uid", []byte{0x26, 0xed, 0x18, 0x87, 0xeb, 0xaa, 0x49, 0x73, 0x9a, 0x80, 0x96, 0x45, 0x4e, 0x55, 0x2d, 0xf2})},
		{"empty elements", "[foo, , bar]", sequence("foo", nil, "bar")},
		{"leading empty element", "[, foo, bar]", sequence(nil, "foo", "bar")},
		{"trailing comma is not an element", "[foo, bar, ]", sequence("foo", "bar")},
		{"missing mapping value at end", "{foo:}", pairs("foo", nil)},
		{"missing mapping value before comma", "{foo:, bar: baz}", pairs("foo", nil, "bar", "baz")},
		{"empty string key", `{ "": foo }`, pairs("", "foo")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertValue(t, inlineParseOK(t, tt.yaml, 0), tt.want) })
	}
}

func TestInlineCoreSchemaTags(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want any
	}{
		{"single quoted string", "!!str 'foo'", "foo"},
		{"number as string", "!!str 1", "1"},
		{"empty null", "!!null", nil},
		{"quoted null", `!!null "null"`, nil},
		{"true", "!!bool TRUE", true},
		{"false", `!!bool "false"`, false},
		{"integer", "!!int -42", -42},
		{"octal", "!!int 0o17", 15},
		{"hex", "!!int 0x1F", 31},
		{"float", "!!float 0.01", 0.01},
		{"float no fractional part", "!!float 1", 1.0},
		{"float no integer part", "!!float .5", 0.5},
		{"float exponent", "!!float 1.2e3", 1200.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertValue(t, inlineParseOK(t, tt.yaml, 0), tt.want) })
	}
}

func TestInlineRejectsInvalidCoreSchemaValues(t *testing.T) {
	tests := []struct{ yaml, message string }{
		{"!!bool 1", `not a valid "!!bool" value`},
		{"!!bool yes", `not a valid "!!bool" value`},
		{"!!bool tRue", `not a valid "!!bool" value`},
		{"!!null foo", `not a valid "!!null" value`},
		{"!!int foo", `not a valid "!!int" value`},
		{"!!int 1.22", `not a valid "!!int" value`},
		{"!!int 1_000", `not a valid "!!int" value`},
		{"!!int 99999999999999999999", "out of range"},
		{"!!float foo", `not a valid "!!float" value`},
		{`!!float ""`, `not a valid "!!float" value`},
		{`!!int "1" 2`, `Unexpected characters near " 2"`},
	}
	for _, tt := range tests {
		_, err := ParseInline(tt.yaml, 0)
		requireError(t, err, tt.message)
	}
}

func TestInlineCustomTags(t *testing.T) {
	tests := []struct {
		yaml string
		want any
	}{
		{"!number 5", TaggedValue{Tag: "number", Value: 5}},
		{`!number "5"`, TaggedValue{Tag: "number", Value: "5"}},
		{"[!foo]", sequence(TaggedValue{Tag: "foo", Value: ""})},
		{`[!foo ""]`, sequence(TaggedValue{Tag: "foo", Value: ""})},
		{"{foo: !bar}", pairs("foo", TaggedValue{Tag: "bar", Value: ""})},
		{`{foo: !bar ""}`, pairs("foo", TaggedValue{Tag: "bar", Value: ""})},
	}
	for _, tt := range tests {
		assertValue(t, inlineParseOK(t, tt.yaml, ParseCustomTags), tt.want)
	}

	_, err := ParseInline("!iterator [foo]", 0)
	requireError(t, err, "Tags support is not enabled", "!iterator")
	_, err = ParseInline("!!iterator foo", 0)
	requireError(t, err, "unsupported built-in tag")
}

func TestInlineAnchorsAndReferences(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want any
	}{
		{"quoted mapping value", `{ foo: &a "FOO", bar: *a }`, pairs("foo", "FOO", "bar", "FOO")},
		{"quoted braces", `{ foo: &a "${FOO}", bar: *a }`, pairs("foo", "${FOO}", "bar", "${FOO}")},
		{"quoted comma", `{ foo: &a "a,b", bar: *a }`, pairs("foo", "a,b", "bar", "a,b")},
		{"sequence value", `{ foo: &a [a, b], bar: *a }`, pairs("foo", sequence("a", "b"), "bar", sequence("a", "b"))},
		{"mapping value", `{ foo: &a { k: v }, bar: *a }`, pairs("foo", pairs("k", "v"), "bar", pairs("k", "v"))},
		{"anchored sequence values", `[&a "FOO", *a]`, sequence("FOO", "FOO")},
		{"merge key", `{ <<: &a { k: v }, bar: 2 }`, pairs("k", "v", "bar", 2)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertValue(t, inlineParseOK(t, tt.yaml, 0), tt.want) })
	}
}

func TestInlineParseReferencesArgument(t *testing.T) {
	references := map[string]any{"var": "var-value"}
	tests := []struct {
		yaml string
		want any
	}{
		{"*var", "var-value"},
		{"[ *var ]", sequence("var-value")},
		{"[[ *var ]]", sequence(sequence("var-value"))},
		{"[ { key: *var } ]", sequence(pairs("key", "var-value"))},
		{"{ key: [*var] }", pairs("key", sequence("var-value"))},
	}
	for _, tt := range tests {
		got, err := ParseInlineWithReferences(tt.yaml, 0, references)
		if err != nil {
			t.Fatal(err)
		}
		assertValue(t, got, tt.want)
	}
}

func TestInlineTimestamps(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want time.Time
	}{
		{"canonical", "2001-12-15T02:59:43.1Z", time.Date(2001, 12, 15, 2, 59, 43, 100_000_000, time.UTC)},
		{"offset", "2001-12-15t21:59:43.10-05:00", time.Date(2001, 12, 15, 21, 59, 43, 100_000_000, time.FixedZone("", -5*60*60))},
		{"date", "2001-12-15", time.Date(2001, 12, 15, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertValue(t, inlineParseOK(t, tt.yaml, ParseTimestamps), tt.want) })
	}
	_, err := ParseInline("2024-50-50", ParseTimestamps)
	requireError(t, err, "invalid date")
}

func TestInlineBinary(t *testing.T) {
	for _, input := range []string{`!!binary "SGVsbG8gd29ybGQ="`, "!!binary 'SGVsbG8gd29ybGQ='", `!!binary  "SGVs bG8gd 29ybGQ="`} {
		assertValue(t, inlineParseOK(t, input, 0), []byte("Hello world"))
	}
	for _, input := range []string{`!!binary "SGVsbG8d29ybGQ="`, `!!binary "SGVsbG8#d29ybGQ="`, `!!binary "SGVsbG8gd29yb==="`, `!!binary "SGVsbG8gd29ybG=Q"`} {
		_, err := ParseInline(input, 0)
		requireError(t, err, "base64 encoded data")
	}
}

func TestInlineOctalNotation(t *testing.T) {
	tests := []struct {
		yaml string
		want any
	}{
		{"0o34", 28}, {"+0o34", 28}, {"-0o34", -28},
		{"034", "034"}, {"0_2_3_3_3", "02333"}, {"-034", "-034"}, {"0123456789", "0123456789"},
	}
	for _, tt := range tests {
		assertValue(t, inlineParseOK(t, tt.yaml, 0), tt.want)
	}
}

func TestInlineSyntaxErrors(t *testing.T) {
	tests := []struct {
		name, yaml, message string
	}{
		{"unknown escape", `"Foo\Var"`, `unknown escape character "\V"`},
		{"unterminated escape", `"Foo\"`, ""},
		{"bad single quotes", `'don't do somthin' like that'`, ""},
		{"bad double quotes", `"don"t do somthin" like that"`, ""},
		{"invalid mapping key", `{ "foo " bar": "bar" }`, ""},
		{"colon without separator", `{foo:""}`, "Colons must be followed by a space"},
		{"trailing mapping content", `[foo] bar`, ""},
		{"trailing sequence content", `{ foo: bar } bar`, ""},
		{"duplicate embedded key", `[ foo: { bar: 1, bar: 2 } ]`, `Duplicate key "bar"`},
		{"missing mapping key", `{: foo}`, "Missing mapping key"},
		{"unfinished map", `{abc: 'def'`, "Unexpected end of line"},
		{"malformed mapping", `{this, is not, supported}`, "Malformed inline YAML"},
		{"empty alias", `{ foo: * }`, "reference must contain at least one character"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseInline(tt.yaml, 0)
			requireError(t, err, tt.message)
		})
	}
}

func TestInlineRejectsUnsupportedPlainScalarIndicators(t *testing.T) {
	for _, indicator := range []string{"@", "`", "|", ">", "%"} {
		_, err := ParseInline("{ foo: "+indicator+"foo }", 0)
		requireError(t, err, "cannot start a plain scalar", "quote the scalar")
	}
	for _, input := range []string{"!", "! ", "[!]", `[!, "foo"]`, "{foo: !}", "!]]]"} {
		_, err := ParseInline(input, 0)
		requireError(t, err, `unquoted scalar value "!" is not supported`)
	}
}

func TestInlineQuotedExclamationMarks(t *testing.T) {
	tests := []struct {
		yaml string
		want any
	}{
		{`"!"`, "!"}, {`"! "`, "! "}, {`["!"]`, sequence("!")},
		{`["!", "foo"]`, sequence("!", "foo")}, {`{foo: "!"}`, pairs("foo", "!")},
	}
	for _, tt := range tests {
		assertValue(t, inlineParseOK(t, tt.yaml, 0), tt.want)
	}
}

func TestInlineVeryLongQuotedStringRoundTrip(t *testing.T) {
	want := strings.Repeat("x\r\n\\\"x\"x", 1000)
	value := pairs("longStringWithQuotes", want)
	assertValue(t, inlineParseOK(t, inlineDumpOK(t, value, 0), 0), value)
}

func TestInlineMappingKeysRejectImplicitNonStrings(t *testing.T) {
	for _, input := range []string{`{true: "foo"}`, `{false: "foo"}`, `{null: "foo"}`, `{0.25: "foo"}`} {
		_, err := ParseInline(input, 0)
		requireError(t, err, "incompatible mapping keys", "Quote")
	}
}

func TestInlineComments(t *testing.T) {
	assertValue(t, inlineParseOK(t, `"foo"#comment`, 0), "foo")
	assertValue(t, inlineParseOK(t, "foo#nocomment", 0), "foo#nocomment")
}

func TestInlineIdeographicSpace(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{{"\u3000", "\u3000"}, {"'\u3000'", "\u3000"}, {"'a\u3000b'", "a\u3000b"}} {
		assertValue(t, inlineParseOK(t, tc.yaml, 0), tc.want)
	}
}
