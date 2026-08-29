package yaml

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"
)

func ParseInline(input string, flags Flags) (any, error) {
	return ParseInlineWithReferences(input, flags, nil)
}

func ParseInlineWithReferences(input string, flags Flags, references map[string]any) (any, error) {
	a := newFlowArena(input)
	p := &a.flow
	*p = flowParser{src: input, flags: flags, refs: references, line: 1, arena: a}
	v, err := p.value(false)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i < len(p.src) && p.src[p.i] != '#' {
		return nil, p.err(fmt.Sprintf("Unexpected characters near %q", p.src[p.i:]))
	}
	return v, nil
}

func ParseScalar(input string) (string, error) {
	p := flowParser{src: input, line: 1}
	p.space()
	if p.i >= len(p.src) {
		return "", nil
	}
	if p.src[p.i] == '\'' || p.src[p.i] == '"' {
		v, err := p.quoted()
		if err != nil {
			return "", err
		}
		p.space()
		if p.i != len(p.src) {
			return "", p.err(fmt.Sprintf("Unexpected characters near %q", p.src[p.i:]))
		}
		return v, nil
	}
	return strings.TrimSpace(input), nil
}

type flowParser struct {
	src        string
	i          int
	line       int
	flags      Flags
	refs       map[string]any
	aliasCount *int
	maxAliases int
	arena      *valueArena
}

func (p *flowParser) err(problem string) error {
	snippet := p.src
	if j := strings.LastIndex(snippet[:min(p.i, len(snippet))], "\n"); j >= 0 {
		snippet = snippet[j+1:]
	}
	if j := strings.IndexByte(snippet, '\n'); j >= 0 {
		snippet = snippet[:j]
	}
	return parseError(problem, max(1, p.line), strings.TrimSpace(snippet))
}

func (p *flowParser) space() {
	for p.i < len(p.src) {
		switch p.src[p.i] {
		case ' ', '\t', '\r':
			p.i++
		case '\n':
			p.i++
			p.line++
		default:
			return
		}
	}
}

func (p *flowParser) value(key bool) (any, error) {
	p.space()
	if p.i >= len(p.src) {
		return "", nil
	}
	switch p.src[p.i] {
	case '[':
		v, err := p.sequence()
		if err != nil {
			return nil, err
		}
		if p.arena != nil {
			return p.arena.sequenceValue(v), nil
		}
		return v, nil
	case '{':
		v, err := p.mapping()
		if err != nil {
			return nil, err
		}
		if p.arena != nil {
			return p.arena.mappingValue(v), nil
		}
		return v, nil
	case '\'', '"':
		v, err := p.quoted()
		if err != nil {
			return nil, err
		}
		if p.arena != nil {
			return p.arena.stringValue(v), nil
		}
		return v, nil
	case '*':
		return p.alias()
	case '&':
		return p.anchor(key)
	case '!':
		return p.tagged(key)
	}
	if strings.ContainsRune("@`|>%", rune(p.src[p.i])) {
		return nil, p.err(fmt.Sprintf("The character %q cannot start a plain scalar; quote the scalar", p.src[p.i]))
	}
	return p.plain(key)
}

func (p *flowParser) sequence() ([]any, error) {
	p.i++
	items := p.arena.sequence(flowCollectionCapacity(p.src, p.i, ']', false))
	for {
		p.spaceAndComments()
		if p.i >= len(p.src) {
			return nil, p.err("Unexpected end of line while parsing a sequence")
		}
		if p.src[p.i] == ']' {
			p.i++
			return items, nil
		}
		if p.src[p.i] == ',' {
			items = append(items, nil)
			p.i++
			continue
		}
		start := p.i
		v, err := p.value(false)
		if err != nil {
			return nil, err
		}
		p.space()
		// YAML permits a compact mapping as one sequence member: [key: value].
		if p.i < len(p.src) && p.src[p.i] == ':' && !isCollection(v) {
			p.i++
			val, err := p.value(false)
			if err != nil {
				return nil, err
			}
			v = Mapping{{Key: v, Value: emptyStringAsNil(val)}}
		} else if p.i == start {
			return nil, p.err("Malformed inline YAML")
		}
		items = append(items, v)
		p.spaceAndComments()
		if p.i >= len(p.src) {
			return nil, p.err("Unexpected end of line while parsing a sequence")
		}
		if p.src[p.i] == ',' {
			p.i++
			p.spaceAndComments()
			if p.i < len(p.src) && p.src[p.i] == ']' {
				p.i++
				return items, nil
			}
			continue
		}
		if p.src[p.i] == ']' {
			p.i++
			return items, nil
		}
		if p.src[p.i] == '}' {
			return nil, p.err("Malformed inline YAML: unexpected closing collection")
		}
		return nil, p.err(fmt.Sprintf("Unexpected token %q", p.src[p.i:]))
	}
}

func (p *flowParser) mapping() (Mapping, error) {
	p.i++
	capacity := flowCollectionCapacity(p.src, p.i, '}', true)
	if strings.Contains(p.src[p.i:], "<<:") {
		capacity += 16
	}
	m := p.arena.mapping(capacity)
	stringIndex := p.arena.lookup(capacity)
	var explicit [64]any
	explicitCount := 0
	for {
		p.spaceAndComments()
		if p.i >= len(p.src) {
			return nil, p.err("Unexpected end of line while parsing a mapping")
		}
		if p.src[p.i] == '}' {
			p.i++
			return m, nil
		}
		if p.src[p.i] == ':' {
			return nil, p.err("Missing mapping key")
		}
		key, err := p.value(true)
		if err != nil {
			return nil, err
		}
		p.space()
		if p.i >= len(p.src) || p.src[p.i] != ':' {
			return nil, p.err("Malformed inline YAML: expected a colon")
		}
		p.i++
		if p.i < len(p.src) && !isSpace(p.src[p.i]) && p.src[p.i] != ',' && p.src[p.i] != '}' && p.src[p.i] != '[' && p.src[p.i] != '{' {
			return nil, p.err("Colons must be followed by a space or an indicator character")
		}
		p.space()
		var val any
		if p.i >= len(p.src) || p.src[p.i] == ',' || p.src[p.i] == '}' || p.src[p.i] == '#' {
			val = nil
		} else {
			val, err = p.value(false)
			if err != nil {
				return nil, err
			}
		}
		if key == "<<" {
			m, err = mergeMappings(m, val)
			if err != nil {
				return nil, p.err(err.Error())
			}
			stringIndex.indexMerged(m)
		} else {
			if err := validImplicitKey(key); err != nil {
				return nil, p.err(err.Error())
			}
			indexed := false
			if text, ok := key.(string); ok && stringIndex.available() {
				if idx, explicit, exists := stringIndex.find(m, text); exists {
					if explicit {
						return nil, p.err(fmt.Sprintf("Duplicate key %q detected", key))
					}
					m[idx].Value = val
					stringIndex.set(m, text, idx, true)
				} else {
					m = append(m, Pair{Key: key, Value: val})
					stringIndex.set(m, text, len(m)-1, true)
				}
				indexed = true
			}
			if !indexed {
				if explicitHas(explicit[:explicitCount], key) {
					return nil, p.err(fmt.Sprintf("Duplicate key %q detected", key))
				}
				if idx := mappingIndex(m, key); idx >= 0 {
					m[idx].Value = val
				} else {
					m = append(m, Pair{Key: key, Value: val})
				}
				if explicitCount < len(explicit) {
					explicit[explicitCount] = key
					explicitCount++
				}
			}
		}
		p.spaceAndComments()
		if p.i >= len(p.src) {
			return nil, p.err("Unexpected end of line while parsing a mapping")
		}
		if p.src[p.i] == ',' {
			p.i++
			continue
		}
		if p.src[p.i] == '}' {
			p.i++
			return m, nil
		}
		if p.src[p.i] == ']' {
			return nil, p.err("Malformed inline YAML: unexpected closing collection")
		}
		return nil, p.err(fmt.Sprintf("Unexpected token %q", p.src[p.i:]))
	}
}

func (p *flowParser) quoted() (string, error) {
	quote := p.src[p.i]
	p.i++
	start := p.i
	segment := start
	var b strings.Builder
	for p.i < len(p.src) {
		c := p.src[p.i]
		p.i++
		if c == quote {
			if quote == '\'' && p.i < len(p.src) && p.src[p.i] == '\'' {
				if b.Cap() == 0 {
					b.Grow(len(p.src) - start)
				}
				b.WriteString(p.src[segment : p.i-1])
				b.WriteByte('\'')
				p.i++
				segment = p.i
				continue
			}
			if b.Cap() == 0 {
				return p.src[start : p.i-1], nil
			}
			b.WriteString(p.src[segment : p.i-1])
			return b.String(), nil
		}
		if c == '\n' {
			if b.Cap() == 0 {
				b.Grow(len(p.src) - start)
			}
			b.WriteString(p.src[segment : p.i-1])
			p.line++
			for p.i < len(p.src) && (p.src[p.i] == ' ' || p.src[p.i] == '\t') {
				p.i++
			}
			if p.i < len(p.src) && p.src[p.i] == '\n' {
				b.WriteByte('\n')
			} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
				b.WriteByte(' ')
			}
			segment = p.i
			continue
		}
		if c != '\\' || quote == '\'' {
			continue
		}
		if b.Cap() == 0 {
			b.Grow(len(p.src) - start)
		}
		b.WriteString(p.src[segment : p.i-1])
		if p.i >= len(p.src) {
			return "", p.err("Unterminated quoted string")
		}
		if p.src[p.i] == '\n' {
			p.i++
			p.line++
			for p.i < len(p.src) && isSpaceNoNL(p.src[p.i]) {
				p.i++
			}
			segment = p.i
			continue
		}
		escaped, err := p.escape()
		if err != nil {
			return "", err
		}
		b.WriteString(escaped)
		segment = p.i
	}
	return "", p.err("Unterminated quoted string")
}

func (p *flowParser) escape() (string, error) {
	c := p.src[p.i]
	p.i++
	switch c {
	case '0':
		return "\x00", nil
	case 'a':
		return "\a", nil
	case 'b':
		return "\b", nil
	case 't', '\t':
		return "\t", nil
	case 'n':
		return "\n", nil
	case 'v':
		return "\v", nil
	case 'f':
		return "\f", nil
	case 'r':
		return "\r", nil
	case 'e':
		return "\x1b", nil
	case ' ':
		return " ", nil
	case '"':
		return "\"", nil
	case '/':
		return "/", nil
	case '\\':
		return "\\", nil
	case 'N':
		return "\u0085", nil
	case '_':
		return "\u00a0", nil
	case 'L':
		return "\u2028", nil
	case 'P':
		return "\u2029", nil
	case 'x', 'u', 'U':
		n := map[byte]int{'x': 2, 'u': 4, 'U': 8}[c]
		if p.i+n > len(p.src) {
			return "", p.err("Invalid Unicode escape sequence")
		}
		x, err := strconv.ParseUint(p.src[p.i:p.i+n], 16, 32)
		if err != nil {
			return "", p.err("Invalid Unicode escape sequence")
		}
		p.i += n
		return string(rune(x)), nil
	default:
		return "", p.err("Found unknown escape character \"\\" + string(c) + "\"")
	}
}

func (p *flowParser) plain(key bool) (any, error) {
	start := p.i
	depth := 0
	for p.i < len(p.src) {
		c := p.src[p.i]
		if depth == 0 && (c == ',' || c == ']' || c == '}') {
			break
		}
		if depth == 0 && c == ':' && (key || p.i+1 == len(p.src) || isSpace(p.src[p.i+1])) {
			break
		}
		if c == '#' && (p.i == start || isSpace(p.src[p.i-1])) {
			break
		}
		if c == '\n' {
			break
		}
		p.i++
	}
	raw := trimYAML(p.src[start:p.i])
	if raw == "" {
		return "", nil
	}
	if raw == "!" {
		return nil, p.err(`The unquoted scalar value "!" is not supported`)
	}
	if !key && strings.Contains(raw, ": ") {
		return nil, p.err("A colon cannot be used in an unquoted mapping value")
	}
	if p.arena != nil {
		return parseArenaScalar(raw, p.flags, p.arena)
	}
	return parseScalarValue(raw, p.flags)
}

func (p *flowParser) alias() (any, error) {
	p.i++
	start := p.i
	for p.i < len(p.src) && isAnchorChar(p.src[p.i]) {
		p.i++
	}
	name := p.src[start:p.i]
	if name == "" {
		return nil, p.err("A reference must contain at least one character")
	}
	if p.flags&ParseRejectAliases != 0 {
		return nil, p.err("Aliases are disabled")
	}
	var v any
	var ok bool
	if p.refs != nil {
		v, ok = p.refs[name]
	} else if p.arena != nil {
		v, ok = p.arena.reference(name)
	}
	if !ok {
		return nil, p.err(fmt.Sprintf("Reference %q does not exist", name))
	}
	if (isCollection(v) || isTaggedCollection(v)) && p.aliasCount != nil {
		*p.aliasCount++
		if *p.aliasCount > p.maxAliases {
			return nil, p.err(fmt.Sprintf("Maximum number of collection aliases (%d) exceeded", p.maxAliases))
		}
	}
	return v, nil
}

func (p *flowParser) anchor(key bool) (any, error) {
	p.i++
	start := p.i
	for p.i < len(p.src) && isAnchorChar(p.src[p.i]) {
		p.i++
	}
	name := p.src[start:p.i]
	if name == "" {
		return nil, p.err("An anchor must contain at least one character")
	}
	p.space()
	if p.i >= len(p.src) || strings.ContainsRune(",]}", rune(p.src[p.i])) {
		if p.refs != nil {
			p.refs[name] = nil
		} else {
			p.arena.setReference(name, nil)
		}
		return nil, nil
	}
	v, err := p.value(key)
	if err == nil {
		if p.refs != nil {
			p.refs[name] = v
		} else {
			p.arena.setReference(name, v)
		}
	}
	return v, err
}

func (p *flowParser) tagged(key bool) (any, error) {
	p.i++
	builtin := false
	if p.i < len(p.src) && p.src[p.i] == '!' {
		builtin = true
		p.i++
	}
	start := p.i
	for p.i < len(p.src) && !isSpace(p.src[p.i]) && !strings.ContainsRune("[]{},", rune(p.src[p.i])) {
		p.i++
	}
	tag := p.src[start:p.i]
	if tag == "" {
		if builtin {
			return nil, p.err(`Built-in tag "!!" is not implemented`)
		}
		if p.i >= len(p.src) || strings.ContainsRune(",]}", rune(p.src[p.i])) {
			return nil, p.err(`The unquoted scalar value "!" is not supported`)
		}
		p.space()
		if p.i >= len(p.src) {
			return nil, p.err(`The unquoted scalar value "!" is not supported`)
		}
		return p.value(key)
	}
	p.space()
	if builtin {
		return p.builtinTag(tag, key)
	}
	if p.flags&ParseCustomTags == 0 {
		return nil, p.err(fmt.Sprintf("Tags support is not enabled; cannot parse !%s", tag))
	}
	var v any = ""
	var err error
	if p.i < len(p.src) && !strings.ContainsRune(",]}", rune(p.src[p.i])) {
		v, err = p.value(key)
	}
	if err != nil {
		return nil, err
	}
	return TaggedValue{Tag: tag, Value: v}, nil
}

func (p *flowParser) builtinTag(tag string, key bool) (any, error) {
	if tag != "str" && tag != "null" && tag != "bool" && tag != "int" && tag != "float" && tag != "binary" {
		if tag == "iterator" {
			return nil, p.err("Found unsupported built-in tag !!iterator")
		}
		return nil, p.err(fmt.Sprintf("built-in tag %q is not implemented", "!!"+tag))
	}
	start := p.i
	var raw string
	quoted := p.i < len(p.src) && (p.src[p.i] == '\'' || p.src[p.i] == '"')
	if quoted {
		v, err := p.quoted()
		if err != nil {
			return nil, err
		}
		raw = v
	} else {
		for p.i < len(p.src) && !strings.ContainsRune(",]}\n", rune(p.src[p.i])) && !(p.src[p.i] == '#' && (p.i == start || isSpace(p.src[p.i-1]))) {
			p.i++
		}
		raw = strings.TrimSpace(p.src[start:p.i])
	}
	if quoted && strings.TrimSpace(p.src[p.i:]) != "" {
		tail := strings.TrimLeft(p.src[p.i:], " \t\r\n")
		if tail != "" && !strings.ContainsRune(",]}#", rune(tail[0])) {
			return nil, p.err("Unexpected characters near \"" + p.src[p.i:] + "\"")
		}
	}
	switch tag {
	case "str":
		return raw, nil
	case "null":
		if raw == "" || raw == "null" || raw == "Null" || raw == "NULL" || raw == "~" {
			return nil, nil
		}
		return nil, p.err(`Value is not a valid "!!null" value`)
	case "bool":
		if raw == "true" || raw == "True" || raw == "TRUE" {
			return true, nil
		}
		if raw == "false" || raw == "False" || raw == "FALSE" {
			return false, nil
		}
		return nil, p.err(`Value is not a valid "!!bool" value`)
	case "int":
		if strings.Contains(raw, "_") {
			return nil, p.err(`Value is not a valid "!!int" value`)
		}
		n, ok, overflow := parseInteger(raw)
		if overflow {
			return nil, p.err("Integer value is out of range")
		}
		if !ok {
			return nil, p.err(`Value is not a valid "!!int" value`)
		}
		return n, nil
	case "float":
		v, ok := parseExplicitFloat(raw)
		if !ok {
			return nil, p.err(`Value is not a valid "!!float" value`)
		}
		return v, nil
	case "binary":
		return decodeBinary(raw, p)
	}
	return nil, nil
}

func (p *flowParser) spaceAndComments() {
	for {
		p.space()
		if p.i >= len(p.src) || p.src[p.i] != '#' {
			return
		}
		for p.i < len(p.src) && p.src[p.i] != '\n' {
			p.i++
		}
	}
}

func parseScalarValue(raw string, flags Flags) (any, error) {
	if raw == "" {
		return "", nil
	}
	switch strings.ToLower(raw) {
	case "null", "~":
		return nil, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	case ".nan":
		return math.NaN(), nil
	case ".inf", "+.inf":
		return math.Inf(1), nil
	case "-.inf":
		return math.Inf(-1), nil
	}
	if raw[0] >= '0' && raw[0] <= '9' {
		if t, ok, err := parseTimestamp(raw); ok {
			if err != nil {
				if flags&ParseTimestamps != 0 {
					return nil, err
				}
				return raw, nil
			}
			if flags&ParseTimestamps != 0 {
				return t, nil
			}
			sec := float64(t.Unix()) + float64(t.Nanosecond())/1e9
			if t.Nanosecond() == 0 {
				return int64(t.Unix()), nil
			}
			return sec, nil
		}
	}
	clean := raw
	numericCandidate := raw != "" && (raw[0] >= '0' && raw[0] <= '9' || raw[0] == '+' || raw[0] == '-')
	if numericCandidate {
		clean = strings.ReplaceAll(raw, "_", "")
	}
	if numericCandidate {
		if n, ok, overflow := parseInteger(clean); ok {
			return n, nil
		} else if overflow {
			return raw, nil
		}
	}
	if numericCandidate && looksFloat(clean) {
		if f, err := strconv.ParseFloat(clean, 64); err == nil || errors.Is(err, strconv.ErrRange) {
			return f, nil
		}
	}
	if numericCandidate {
		return clean, nil
	}
	return raw, nil
}

func parseInteger(raw string) (int, bool, bool) {
	if raw == "" {
		return 0, false, false
	}
	sign, body := 1, raw
	if body[0] == '+' {
		body = body[1:]
	} else if body[0] == '-' {
		sign = -1
		body = body[1:]
	}
	base := 10
	if strings.HasPrefix(body, "0x") {
		base, body = 16, body[2:]
	} else if strings.HasPrefix(body, "0o") {
		base, body = 8, body[2:]
	} else if len(body) > 1 && body[0] == '0' {
		return 0, false, false
	}
	body = strings.ReplaceAll(body, "_", "")
	if body == "" {
		return 0, false, false
	}
	u, err := strconv.ParseUint(body, base, 63)
	if err != nil {
		if _, e := strconv.ParseUint(body, base, 64); e == nil || strings.Contains(err.Error(), "value out of range") {
			return 0, false, true
		}
		return 0, false, false
	}
	if sign < 0 {
		if u > uint64(-minInt()) {
			return 0, false, true
		}
		return -int(u), true, false
	}
	if u > uint64(maxInt()) {
		return 0, false, true
	}
	return int(u), true, false
}

func parseExplicitFloat(raw string) (float64, bool) {
	switch strings.ToLower(raw) {
	case ".nan":
		return math.NaN(), true
	case ".inf", "+.inf":
		return math.Inf(1), true
	case "-.inf":
		return math.Inf(-1), true
	}
	if raw == "" || strings.Contains(raw, "_") {
		return 0, false
	}
	if raw[0] == '.' {
		raw = "0" + raw
	} else if strings.HasPrefix(raw, "-.") {
		raw = "-0" + raw[1:]
	} else if strings.HasPrefix(raw, "+.") {
		raw = "+0" + raw[1:]
	}
	f, err := strconv.ParseFloat(raw, 64)
	return f, err == nil
}

func looksFloat(s string) bool {
	if strings.ContainsAny(s, ".eE") {
		_, err := strconv.ParseFloat(s, 64)
		return err == nil || errors.Is(err, strconv.ErrRange)
	}
	return false
}

func parseTimestamp(s string) (time.Time, bool, error) {
	if len(s) < 10 || s[4] != '-' || s[7] != '-' {
		return time.Time{}, false, nil
	}
	if t, valid, matched := parseTimestampFields(s); matched {
		if valid {
			return t, true, nil
		}
		return time.Time{}, true, fmt.Errorf("invalid date %q", s)
	}
	layouts := []string{"2006-01-02", time.RFC3339Nano, "2006-01-02t15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999 Z07:00", "2006-01-02 15:04:05.999999999Z07:00"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true, nil
		}
	}
	// Date-shaped scalars are errors only when timestamp objects were requested.
	return time.Time{}, true, fmt.Errorf("invalid date %q", s)
}

func parseTimestampFields(s string) (time.Time, bool, bool) {
	year, okYear := decimalField(s, 0, 4)
	month, okMonth := decimalField(s, 5, 2)
	day, okDay := decimalField(s, 8, 2)
	if !okYear || !okMonth || !okDay {
		return time.Time{}, false, false
	}
	if len(s) == 10 {
		t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		return t, t.Year() == year && int(t.Month()) == month && t.Day() == day, true
	}
	if len(s) < 20 || s[10] != 'T' && s[10] != 't' && s[10] != ' ' || s[13] != ':' || s[16] != ':' {
		return time.Time{}, false, false
	}
	hour, okHour := decimalField(s, 11, 2)
	minute, okMinute := decimalField(s, 14, 2)
	second, okSecond := decimalField(s, 17, 2)
	if !okHour || !okMinute || !okSecond || hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, false, true
	}
	position := 19
	nanosecond := 0
	if position < len(s) && s[position] == '.' {
		position++
		start := position
		for position < len(s) && s[position] >= '0' && s[position] <= '9' {
			if position-start < 9 {
				nanosecond = nanosecond*10 + int(s[position]-'0')
			}
			position++
		}
		digits := position - start
		if digits == 0 || digits > 9 {
			return time.Time{}, false, true
		}
		for digits < 9 {
			nanosecond *= 10
			digits++
		}
	}
	if position < len(s) && s[position] == ' ' {
		position++
	}
	offset := 0
	location := time.UTC
	if position < len(s) && s[position] == 'Z' && position+1 == len(s) {
		position++
	} else if position+6 == len(s) && (s[position] == '+' || s[position] == '-') && s[position+3] == ':' {
		offsetHour, okOffsetHour := decimalField(s, position+1, 2)
		offsetMinute, okOffsetMinute := decimalField(s, position+4, 2)
		if !okOffsetHour || !okOffsetMinute || offsetHour > 24 || offsetMinute > 59 || offsetHour == 24 && offsetMinute != 0 {
			return time.Time{}, false, true
		}
		offset = offsetHour*3600 + offsetMinute*60
		if s[position] == '-' {
			offset = -offset
		}
		if offset != 0 {
			location = time.FixedZone("", offset)
		}
		position += 6
	} else {
		return time.Time{}, false, true
	}
	if position != len(s) {
		return time.Time{}, false, true
	}
	t := time.Date(year, time.Month(month), day, hour, minute, second, nanosecond, location)
	valid := t.Year() == year && int(t.Month()) == month && t.Day() == day
	return t, valid, true
}

func decimalField(s string, start, length int) (int, bool) {
	if start < 0 || start+length > len(s) {
		return 0, false
	}
	value := 0
	for i := start; i < start+length; i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		value = value*10 + int(s[i]-'0')
	}
	return value, true
}

func decodeBinary(raw string, p *flowParser) (any, error) {
	if strings.ContainsAny(raw, " \t") {
		var local [1024]byte
		clean := local[:0]
		if len(raw) > len(local) {
			clean = make([]byte, 0, len(raw))
		}
		for i := 0; i < len(raw); i++ {
			if raw[i] != ' ' && raw[i] != '\t' && raw[i] != '\r' && raw[i] != '\n' {
				clean = append(clean, raw[i])
			}
		}
		raw = unsafe.String(unsafe.SliceData(clean), len(clean))
	}
	b, err := base64.StdEncoding.Strict().DecodeString(raw)
	if err != nil {
		return nil, p.err("The value is not valid base64 encoded data")
	}
	if p.arena != nil {
		return p.arena.bytesValue(b), nil
	}
	return b, nil
}

func validImplicitKey(key any) error {
	switch key.(type) {
	case string, int:
		return nil
	case float64:
		return fmt.Errorf("YAML incompatible mapping keys: Quote numeric keys")
	default:
		return fmt.Errorf("YAML incompatible mapping keys: Quote non-string keys")
	}
}

func mappingHas(m Mapping, key any) bool {
	for _, pair := range m {
		if scalarEqual(pair.Key, key) {
			return true
		}
	}
	return false
}

func scalarEqual(a, b any) bool {
	switch a := a.(type) {
	case string:
		v, ok := b.(string)
		return ok && a == v
	case int:
		v, ok := b.(int)
		return ok && a == v
	case int64:
		v, ok := b.(int64)
		return ok && a == v
	case bool:
		v, ok := b.(bool)
		return ok && a == v
	case nil:
		return b == nil
	case float64:
		v, ok := b.(float64)
		return ok && (a == v || math.IsNaN(a) && math.IsNaN(v))
	default:
		return false
	}
}
func explicitHas(keys []any, key any) bool {
	for _, candidate := range keys {
		if scalarEqual(candidate, key) {
			return true
		}
	}
	return false
}

func mergeMappings(dst Mapping, value any) (Mapping, error) {
	mergeOne := func(src Mapping) {
		for _, pair := range src {
			if !mappingHas(dst, pair.Key) {
				dst = append(dst, pair)
			}
		}
	}
	switch v := value.(type) {
	case Mapping:
		mergeOne(v)
	case []any:
		allCollections := len(v) > 0
		for _, item := range v {
			if !isCollection(item) {
				allCollections = false
				break
			}
		}
		if !allCollections {
			for i, item := range v {
				if !mappingHas(dst, i) {
					dst = append(dst, Pair{Key: i, Value: item})
				}
			}
			break
		}
		for _, item := range v {
			switch item := item.(type) {
			case Mapping:
				mergeOne(item)
			case []any:
				for i, x := range item {
					if !mappingHas(dst, i) {
						dst = append(dst, Pair{Key: i, Value: x})
					}
				}
			}
		}
	default:
		return dst, fmt.Errorf("Merge value must be a mapping or a sequence of mappings")
	}
	return dst, nil
}

func emptyStringAsNil(v any) any {
	if s, ok := v.(string); ok && s == "" {
		return nil
	}
	return v
}
func isCollection(v any) bool {
	switch v.(type) {
	case Mapping, []any:
		return true
	}
	return false
}
func isSpace(c byte) bool      { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
func isSpaceNoNL(c byte) bool  { return c == ' ' || c == '\t' || c == '\r' }
func isAnchorChar(c byte) bool { return !isSpace(c) && !strings.ContainsRune(",[]{}#", rune(c)) }
func maxInt() int              { return int(^uint(0) >> 1) }
func minInt() int              { return -maxInt() - 1 }

var _ = utf8.ValidString
