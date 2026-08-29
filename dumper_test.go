package yaml

import (
	"encoding/base64"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDumperIndentationAndInlineLevels(t *testing.T) {
	data := pairs(
		"", "bar",
		"foo", "#bar",
		"foo'bar", Mapping{},
		"bar", sequence(1, "foo", pairs("a", "A")),
		"foobar", pairs("foo", "bar", "bar", sequence(1, "foo"), "foobar", pairs("foo", "bar", "bar", sequence(1, "foo"))),
	)

	t.Run("custom indentation", func(t *testing.T) {
		d, err := NewDumper(7)
		if err != nil {
			t.Fatal(err)
		}
		got, err := d.Dump(data, 4, 0)
		if err != nil {
			t.Fatal(err)
		}
		want := "'': bar\nfoo: '#bar'\n\"foo'bar\": {}\nbar:\n       - 1\n       - foo\n       -\n              a: A\nfoobar:\n       foo: bar\n       bar:\n              - 1\n              - foo\n       foobar:\n              foo: bar\n              bar:\n                     - 1\n                     - foo\n"
		if got != want {
			t.Fatalf("custom indentation mismatch\n got:\n%s\nwant:\n%s", got, want)
		}
		assertValue(t, parseOK(t, got, 0), data)
	})

	tests := []struct {
		level int
		want  string
	}{
		{-10, "{ '': bar, foo: '#bar', \"foo'bar\": {}, bar: [1, foo, { a: A }], foobar: { foo: bar, bar: [1, foo], foobar: { foo: bar, bar: [1, foo] } } }"},
		{0, "{ '': bar, foo: '#bar', \"foo'bar\": {}, bar: [1, foo, { a: A }], foobar: { foo: bar, bar: [1, foo], foobar: { foo: bar, bar: [1, foo] } } }"},
		{1, "'': bar\nfoo: '#bar'\n\"foo'bar\": {}\nbar: [1, foo, { a: A }]\nfoobar: { foo: bar, bar: [1, foo], foobar: { foo: bar, bar: [1, foo] } }\n"},
		{2, "'': bar\nfoo: '#bar'\n\"foo'bar\": {}\nbar:\n    - 1\n    - foo\n    - { a: A }\nfoobar:\n    foo: bar\n    bar: [1, foo]\n    foobar: { foo: bar, bar: [1, foo] }\n"},
		{3, "'': bar\nfoo: '#bar'\n\"foo'bar\": {}\nbar:\n    - 1\n    - foo\n    -\n        a: A\nfoobar:\n    foo: bar\n    bar:\n        - 1\n        - foo\n    foobar:\n        foo: bar\n        bar: [1, foo]\n"},
		{4, "'': bar\nfoo: '#bar'\n\"foo'bar\": {}\nbar:\n    - 1\n    - foo\n    -\n        a: A\nfoobar:\n    foo: bar\n    bar:\n        - 1\n        - foo\n    foobar:\n        foo: bar\n        bar:\n            - 1\n            - foo\n"},
	}
	for _, tt := range tests {
		t.Run("inline level "+strings.TrimSpace(strings.ReplaceAll(strings.Repeat("x", tt.level+10), "x", ".")), func(t *testing.T) {
			if got := dumpOK(t, data, tt.level, 4, 0); got != tt.want {
				t.Fatalf("level %d mismatch\n got:\n%s\nwant:\n%s", tt.level, got, tt.want)
			}
		})
	}
}

func TestNewDumperRejectsNonPositiveIndentation(t *testing.T) {
	for _, indent := range []int{0, -4} {
		_, err := NewDumper(indent)
		requireError(t, err, "indentation must be greater than zero")
	}
}

type dumpStructOuter struct {
	Outer1 string          `yaml:"outer1"`
	Outer2 dumpStructInner `yaml:"outer2"`
}

type dumpStructInner struct {
	Inner1 string         `yaml:"inner1"`
	Inner2 string         `yaml:"inner2"`
	Inner3 dumpStructDeep `yaml:"inner3"`
}

type dumpStructDeep struct {
	Deep1 string `yaml:"deep1"`
	Deep2 string `yaml:"deep2"`
}

func TestDumpGoStructAsMapping(t *testing.T) {
	data := dumpStructOuter{
		Outer1: "a",
		Outer2: dumpStructInner{Inner1: "b", Inner2: "c", Inner3: dumpStructDeep{Deep1: "d", Deep2: "e"}},
	}
	want := "outer1: a\nouter2:\n    inner1: b\n    inner2: c\n    inner3: { deep1: d, deep2: e }\n"
	if got := dumpOK(t, data, 2, 4, DumpStructsAsMappings); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDumpSimpleMappingsInSequences(t *testing.T) {
	tests := []struct {
		name  string
		data  any
		flags Flags
		want  string
	}{
		{"expanded one key", pairs("servers", sequence(pairs("url", "http://example.com"))), 0, "servers:\n    -\n        url: 'http://example.com'\n"},
		{"compact one key", pairs("servers", sequence(pairs("url", "http://example.com"))), DumpCompactNestedMappings, "servers:\n    - url: 'http://example.com'\n"},
		{"expanded two keys", pairs("servers", sequence(pairs("url", "http://example.com", "port", 80))), 0, "servers:\n    -\n        url: 'http://example.com'\n        port: 80\n"},
		{"compact two keys", pairs("servers", sequence(pairs("url", "http://example.com", "port", 80))), DumpCompactNestedMappings, "servers:\n    - url: 'http://example.com'\n      port: 80\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dumpOK(t, tt.data, 3, 4, tt.flags)
			if got != tt.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tt.want)
			}
			assertValue(t, parseOK(t, got, 0), tt.data)
		})
	}
}

func TestDumpUnsupportedGoValues(t *testing.T) {
	value := pairs("unsupported", make(chan int), "bar", 1)
	if got := dumpOK(t, value, 0, 4, 0); got != "{ unsupported: null, bar: 1 }" {
		t.Fatalf("default unsupported value dump = %q", got)
	}
	_, err := Dump(value, 0, 4, DumpErrorOnUnsupportedType)
	requireError(t, err, "unsupported")
}

func TestDumpNullFormats(t *testing.T) {
	_, err := Dump(pairs("foo", "bar"), 0, 4, DumpNullAsEmpty|DumpNullAsTilde)
	requireError(t, err, "cannot be used together")

	tests := []struct {
		name  string
		data  any
		flags Flags
		want  string
	}{
		{"empty expanded mapping", pairs("qux", pairs("foo", "bar", "baz", nil)), DumpNullAsEmpty, "qux:\n    foo: bar\n    baz: \n"},
		{"empty inline mapping", pairs("foo", nil, "qux", pairs("foo", "bar", "baz", nil)), DumpNullAsEmpty, "foo: \nqux: { foo: bar, baz:  }\n"},
		{"empty nested maps", pairs("foo", nil, "qux", pairs("foo", "bar", "baz", nil)), DumpNullAsEmpty, "foo: \nqux:\n    foo: bar\n    baz: \n"},
		{"empty expanded sequence", pairs("qux", sequence("foo", nil, "bar")), DumpNullAsEmpty, "qux:\n    - foo\n    - \n    - bar\n"},
		{"empty inline sequence", pairs("foo", nil, "qux", sequence("foo", nil, "bar")), DumpNullAsEmpty, "foo: \nqux: [foo, , bar]\n"},
		{"tilde", pairs("foo", nil), DumpNullAsTilde, "{ foo: ~ }"},
	}
	levels := map[string]int{"empty expanded mapping": 2, "empty inline mapping": 1, "empty nested maps": 10, "empty expanded sequence": 2, "empty inline sequence": 1, "tilde": 0}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dumpOK(t, tt.data, levels[tt.name], 4, tt.flags); got != tt.want {
				t.Fatalf("got %q; want %q", got, tt.want)
			}
		})
	}
	if got := dumpOK(t, nil, 2, 4, DumpNullAsEmpty); got != "null" {
		t.Fatalf("root null with DumpNullAsEmpty = %q", got)
	}
}

func TestDumpScalarEscapes(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"empty", "", "''"}, {"nul", "\x00", `"\0"`}, {"bell", "\x07", `"\a"`},
		{"backspace", "\x08", `"\b"`}, {"tab", "\t", `"\t"`}, {"line feed", "\n", `"\n"`},
		{"vertical tab", "\v", `"\v"`}, {"form feed", "\f", `"\f"`}, {"carriage return", "\r", `"\r"`},
		{"escape", "\x1b", `"\e"`}, {"space", " ", `' '`}, {"quote", `"`, `'"'`},
		{"slash", "/", "/"}, {"backslash", `\`, `\`}, {"del", "\x7f", "\"\x7f\""},
		{"next line", "\u0085", `"\N"`}, {"non-breaking space", "\u00a0", `"\_"`},
		{"line separator", "\u2028", `"\L"`}, {"paragraph separator", "\u2029", `"\P"`}, {"colon", ":", `':'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dumpOK(t, tt.input, 2, 4, 0); got != tt.want {
				t.Fatalf("got %q; want %q", got, tt.want)
			}
			assertValue(t, parseOK(t, tt.want, 0), tt.input)
		})
	}
}

func TestDumpBinaryData(t *testing.T) {
	data, err := os.ReadFile("testdata/fixtures/arrow.gif")
	if err != nil {
		t.Fatal(err)
	}
	want := "{ data: !!binary " + base64.StdEncoding.EncodeToString(data) + " }"
	if got := dumpOK(t, pairs("data", data), 2, 4, 0); got != want {
		t.Fatalf("binary dump mismatch")
	}
	invalidUTF8 := string([]byte{'f', 0xc3, 0x3f, 'r'})
	if got := dumpOK(t, invalidUTF8, 2, 4, 0); got != "!!binary ZsM/cg==" {
		t.Fatalf("invalid UTF-8 dump = %q", got)
	}
}

func TestDumpInlineValues(t *testing.T) {
	tests := []struct {
		name string
		data any
		want string
	}{
		{"null", nil, "null"}, {"false", false, "false"}, {"true", true, "true"}, {"integer", 12, "12"},
		{"numeric string", "1_2", `'1_2'`}, {"leading underscore", "_12", "_12"}, {"trailing underscore", "12_", `'12_'`},
		{"float", 1230.0, "1230.0"}, {"large float", 1.23e45, "1.23E+45"},
		{"positive infinity", math.Inf(1), ".Inf"}, {"negative infinity", math.Inf(-1), "-.Inf"},
		{"hash", "foo#bar", `'foo#bar'`}, {"hash after space", "foo # bar", `'foo # bar'`},
		{"single quote preferred double", "isn't it a nice single quote", `"isn't it a nice single quote"`},
		{"double quote preferred single", `this is "double quoted"`, `'this is "double quoted"'`},
		{"dash", "-", `'-'`}, {"dash prefix", "-dash", `'-dash'`},
		{"pre YAML boolean", "yes", `'yes'`},
		{"sequence", sequence("foo", "bar", false, nil, 12), "[foo, bar, false, null, 12]"},
		{"empty mapping", Mapping{}, "{}"},
		{"mapping", pairs("foo", "bar", "bar", "foo: bar"), `{ foo: bar, bar: 'foo: bar' }`},
		{"nested", pairs("foo", pairs("bar", "foo")), `{ foo: { bar: foo } }`},
		{"ideographic space", "\u3000", `'　'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inlineDumpOK(t, tt.data, 0)
			if got != tt.want {
				t.Fatalf("got %q; want %q", got, tt.want)
			}
			if math.IsNaN(asFloat(tt.data)) {
				return
			}
			assertValue(t, inlineParseOK(t, got, 0), tt.data)
		})
	}
}

func asFloat(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func TestDumpTaggedValues(t *testing.T) {
	tests := []struct {
		name  string
		data  any
		level int
		flags Flags
		want  string
	}{
		{"top scalar", TaggedValue{Tag: "user", Value: "jane"}, 2, 0, "!user jane"},
		{"top inline map", TaggedValue{Tag: "user", Value: pairs("name", "jane")}, 1, 0, "!user { name: jane }"},
		{"top expanded map", TaggedValue{Tag: "user", Value: pairs("name", "jane")}, 2, 0, "!user\nname: jane\n"},
		{"top multiline", TaggedValue{Tag: "text", Value: "a\nb\n"}, 2, DumpMultiLineLiteralBlock, "!text |\n    a\n    b\n"},
		{"sequence inline", sequence(TaggedValue{Tag: "user", Value: pairs("username", "jane")}, TaggedValue{Tag: "names", Value: sequence("john", "claire")}, TaggedValue{Tag: "number", Value: 5}), 1, 0, "- !user { username: jane }\n- !names [john, claire]\n- !number 5\n"},
		{"mapping inline", pairs("user1", TaggedValue{Tag: "user", Value: pairs("username", "jane")}, "names1", TaggedValue{Tag: "names", Value: sequence("john", "claire")}), 1, 0, "user1: !user { username: jane }\nnames1: !names [john, claire]\n"},
		{"tagged null", pairs("foo", TaggedValue{Tag: "bar", Value: nil}), 2, 0, "foo: !bar null\n"},
		{"tagged multiline list", sequence(TaggedValue{Tag: "bar", Value: "a\nb"}), 2, DumpMultiLineLiteralBlock, "- !bar |-\n    a\n    b"},
		{"tagged multiline keep", pairs("foo", TaggedValue{Tag: "bar", Value: "a\nb\n\n\n"}), 2, DumpMultiLineLiteralBlock, "foo: !bar |+\n    a\n    b\n\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dumpOK(t, tt.data, tt.level, 4, tt.flags)
			if got != tt.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tt.want)
			}
			assertValue(t, parseOK(t, got, ParseCustomTags), tt.data)
		})
	}
}

func TestDumpMultilineLiteralBlocks(t *testing.T) {
	tests := []struct {
		name, value, want string
	}{
		{"strip", "one\ntwo", "value: |-\n    one\n    two"},
		{"clip", "one\ntwo\n", "value: |\n    one\n    two\n"},
		{"keep two", "one\ntwo\n\n", "value: |+\n    one\n    two\n\n"},
		{"keep three", "one\ntwo\n\n\n", "value: |+\n    one\n    two\n\n\n"},
		{"leading spaces", "    first\nsecond", "value: |4-\n        first\n    second"},
		{"empty first line then spaces", "\n    second\nthird", "value: |4-\n\n        second\n    third"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := pairs("value", tt.value)
			got := dumpOK(t, data, 2, 4, DumpMultiLineLiteralBlock)
			if got != tt.want {
				t.Fatalf("got %q; want %q", got, tt.want)
			}
			assertValue(t, parseOK(t, got, 0), data)
		})
	}

	data := sequence("a\nb", "c\nd")
	want := "- |-\n    a\n    b\n- |-\n    c\n    d"
	if got := dumpOK(t, data, 2, 4, DumpMultiLineLiteralBlock); got != want {
		t.Fatalf("multiple literal blocks = %q", got)
	}

	if got := dumpOK(t, "a\nb\n", 2, 4, DumpMultiLineLiteralBlock); got != `"a\nb\n"` {
		t.Fatalf("top-level multiline string = %q", got)
	}
}

func TestDumpForceDoubleQuotesOnValues(t *testing.T) {
	tests := []struct {
		name string
		data Mapping
		want string
	}{
		{"empty", pairs("foo", ""), `{ foo: '' }`},
		{"double quote", pairs("foo", `"`), `{ foo: "\"" }`},
		{"single quote", pairs("foo", `'`), `{ foo: "'" }`},
		{"line break", pairs("foo", "line\nbreak"), "{ foo: \"line\\nbreak\" }"},
		{"tab", pairs("foo", "tab\tcharacter"), "{ foo: \"tab\\tcharacter\" }"},
		{"backslash", pairs("foo", `back\slash`), `{ foo: "back\\slash" }`},
		{"nested", pairs("foo", pairs("bar", "bat", "baz", 23)), `{ foo: { bar: "bat", baz: 23 } }`},
		{"mixed", pairs("foo", "bat", "bar", 23, "baz", true, "qux", "# hash"), `{ foo: "bat", bar: 23, baz: true, qux: "# hash" }`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dumpOK(t, tt.data, 0, 4, DumpForceDoubleQuotesOnValues); got != tt.want {
				t.Fatalf("got %q; want %q", got, tt.want)
			}
		})
	}
}

func TestDumpNumericKeysAsStrings(t *testing.T) {
	tests := []struct {
		name  string
		data  any
		level int
		flags Flags
		want  string
	}{
		{"inline with flag", pairs(200, "foo"), 0, DumpNumericKeysAsStrings, `{ '200': foo }`},
		{"inline without flag", pairs(200, "foo"), 0, 0, `{ 200: foo }`},
		{"expanded with flag", pairs(200, "foo"), 4, DumpNumericKeysAsStrings, "'200': foo\n"},
		{"expanded without flag", pairs(200, "foo"), 4, 0, "200: foo\n"},
		{"sequence unaffected", sequence(200, "foo"), 4, DumpNumericKeysAsStrings, "- 200\n- foo\n"},
		{"tagged", pairs(200, TaggedValue{Tag: "number", Value: 5}), 4, DumpNumericKeysAsStrings, "'200': !number 5\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dumpOK(t, tt.data, tt.level, 4, tt.flags); got != tt.want {
				t.Fatalf("got %q; want %q", got, tt.want)
			}
		})
	}
}

func TestDumpTime(t *testing.T) {
	tests := []struct {
		name string
		date time.Time
		want string
	}{
		{"seconds", time.Date(2023, 1, 24, 1, 2, 3, 0, time.UTC), "date: 2023-01-24T01:02:03+00:00\n"},
		{"milliseconds padded", time.Date(2023, 1, 24, 1, 2, 3, 400_000_000, time.UTC), "date: 2023-01-24T01:02:03.400+00:00\n"},
		{"milliseconds", time.Date(2023, 1, 24, 1, 2, 3, 456_000_000, time.UTC), "date: 2023-01-24T01:02:03.456+00:00\n"},
		{"microseconds", time.Date(2023, 1, 24, 1, 2, 3, 456_789_000, time.UTC), "date: 2023-01-24T01:02:03.456789+00:00\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dumpOK(t, pairs("date", tt.date), 1, 4, 0); got != tt.want {
				t.Fatalf("got %q; want %q", got, tt.want)
			}
		})
	}
}

func TestDumpCompactNestedMappings(t *testing.T) {
	data := pairs("planets", sequence(
		pairs("name", "Mercury", "distance", 57910000, "properties", sequence(pairs("name", "size", "value", 4879), pairs("name", "moons", "value", 0))),
		pairs("name", "Jupiter", "distance", 778500000, "properties", sequence(pairs("name", "size", "value", 139820), pairs("name", "moons", "value", 79))),
	))
	want := "planets:\n  - name: Mercury\n    distance: 57910000\n    properties:\n      - name: size\n        value: 4879\n      - name: moons\n        value: 0\n  - name: Jupiter\n    distance: 778500000\n    properties:\n      - name: size\n        value: 139820\n      - name: moons\n        value: 79\n"
	if got := dumpOK(t, data, 10, 2, DumpCompactNestedMappings); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
