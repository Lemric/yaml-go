package yaml

import (
	"fmt"
)

// Flags controls optional parsing and encoding behavior.
type Flags uint32

const (
	ParseTimestamps Flags = 1 << iota
	ParseCustomTags
	ParseRejectAliases
	DumpCompactNestedMappings
	DumpErrorOnUnsupportedType
	DumpNullAsEmpty
	DumpNullAsTilde
	DumpMultiLineLiteralBlock
	DumpForceDoubleQuotesOnValues
	DumpNumericKeysAsStrings
	DumpStructsAsMappings
)

const (
	DefaultMaxNestingDepth      = 100
	DefaultMaxCollectionAliases = 50
)

type Limits struct {
	MaxNestingDepth      int
	MaxCollectionAliases int
}

type Pair struct {
	Key   any
	Value any
}

// Mapping preserves source order and permits the scalar key types supported by YAML.
type Mapping []Pair

func IsMapping(value any) bool {
	_, ok := value.(Mapping)
	return ok
}

type TaggedValue struct {
	Tag   string
	Value any
}

type ParseError struct {
	Problem  string
	Line     int
	Snippet  string
	Filename string
}

func (e *ParseError) Error() string {
	s := e.Problem
	if e.Filename != "" {
		s += fmt.Sprintf(" in %q", e.Filename)
	}
	if e.Line > 0 {
		s += fmt.Sprintf(" at line %d", e.Line)
	}
	if e.Snippet != "" {
		s += fmt.Sprintf(" (near %q)", e.Snippet)
	}
	return s
}

func parseError(problem string, line int, snippet string) error {
	return &ParseError{Problem: problem, Line: line, Snippet: snippet}
}
