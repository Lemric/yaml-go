package yaml

import (
	"encoding/base64"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Dumper struct{ indent int }

func NewDumper(indent int) (*Dumper, error) {
	if indent <= 0 {
		return nil, fmt.Errorf("indentation must be greater than zero")
	}
	return &Dumper{indent: indent}, nil
}

func Dump(value any, inline, indent int, flags Flags) (string, error) {
	if indent <= 0 {
		return "", fmt.Errorf("indentation must be greater than zero")
	}
	d := Dumper{indent: indent}
	return d.Dump(value, inline, flags)
}

func (d *Dumper) Dump(value any, inline int, flags Flags) (string, error) {
	if flags&DumpNullAsEmpty != 0 && flags&DumpNullAsTilde != 0 {
		return "", fmt.Errorf("DumpNullAsEmpty and DumpNullAsTilde cannot be used together")
	}
	if out, ok := fastDump(value, inline, d.indent, flags); ok {
		return out, nil
	}
	v := value
	var err error
	if flags&DumpStructsAsMappings != 0 {
		v, err = normalizeDumpValue(value, flags)
		if err != nil {
			return "", err
		}
	}
	if m, ok := v.(Mapping); ok && len(m) == 1 {
		if _, ok := m[0].Value.([]byte); ok {
			return encodeInline(v, flags, false)
		}
	}
	return d.encode(v, inline, 0, flags, true)
}

func DumpInline(value any, flags Flags) (string, error) {
	if flags&DumpNullAsEmpty != 0 && flags&DumpNullAsTilde != 0 {
		return "", fmt.Errorf("DumpNullAsEmpty and DumpNullAsTilde cannot be used together")
	}
	if out, ok := fastDumpInline(value, flags); ok {
		return out, nil
	}
	return encodeInline(value, flags, false)
}

func (d *Dumper) encode(value any, inline, depth int, flags Flags, root bool) (string, error) {
	if tagged, ok := value.(TaggedValue); ok {
		return d.encodeTagged(tagged, inline, depth, flags, root)
	}
	if depth >= inline || !isCollection(value) {
		return encodeInline(value, flags, false)
	}
	pad := strings.Repeat(" ", depth*d.indent)
	switch v := value.(type) {
	case Mapping:
		if len(v) == 0 {
			return "{}", nil
		}
		var b strings.Builder
		for _, pair := range v {
			key, err := encodeKey(pair.Key, flags)
			if err != nil {
				return "", err
			}
			val := pair.Value
			if tv, ok := val.(TaggedValue); ok {
				prefix := "!" + tv.Tag
				if shouldLiteral(tv.Value, flags) {
					lit := d.literal(tv.Value.(string), depth+1)
					b.WriteString(pad)
					b.WriteString(key)
					b.WriteString(": ")
					b.WriteString(prefix)
					b.WriteByte(' ')
					b.WriteString(lit)
					continue
				}
				if isCollection(tv.Value) && depth+1 < inline {
					b.WriteString(pad)
					b.WriteString(key)
					b.WriteString(": ")
					b.WriteString(prefix)
					b.WriteByte('\n')
					s, err := d.encode(tv.Value, inline, depth+1, flags, false)
					if err != nil {
						return "", err
					}
					b.WriteString(s)
					continue
				}
				s, err := encodeInline(tv.Value, flags, true)
				if err != nil {
					return "", err
				}
				b.WriteString(pad)
				b.WriteString(key)
				b.WriteString(": ")
				b.WriteString(prefix)
				b.WriteByte(' ')
				b.WriteString(s)
				b.WriteByte('\n')
				continue
			}
			if shouldLiteral(val, flags) {
				b.WriteString(pad)
				b.WriteString(key)
				b.WriteString(": ")
				b.WriteString(d.literal(val.(string), depth+1))
				continue
			}
			if isCollection(val) && depth+1 < inline && collectionLen(val) > 0 {
				b.WriteString(pad)
				b.WriteString(key)
				b.WriteString(":\n")
				s, err := d.encode(val, inline, depth+1, flags, false)
				if err != nil {
					return "", err
				}
				b.WriteString(s)
			} else {
				s, err := encodeInline(val, flags, true)
				if err != nil {
					return "", err
				}
				b.WriteString(pad)
				b.WriteString(key)
				b.WriteString(": ")
				b.WriteString(s)
				b.WriteByte('\n')
			}
		}
		return b.String(), nil
	case []any:
		if len(v) == 0 {
			return "[]", nil
		}
		var b strings.Builder
		for itemIndex, item := range v {
			if tv, ok := item.(TaggedValue); ok {
				b.WriteString(pad)
				b.WriteString("- !")
				b.WriteString(tv.Tag)
				if shouldLiteral(tv.Value, flags) {
					b.WriteByte(' ')
					b.WriteString(d.literal(tv.Value.(string), depth+1))
					if itemIndex < len(v)-1 && !strings.HasSuffix(b.String(), "\n") {
						b.WriteByte('\n')
					}
					continue
				}
				if isCollection(tv.Value) && depth+1 < inline {
					b.WriteByte('\n')
					s, err := d.encode(tv.Value, inline, depth+1, flags, false)
					if err != nil {
						return "", err
					}
					b.WriteString(s)
					continue
				}
				s, err := encodeInline(tv.Value, flags, true)
				if err != nil {
					return "", err
				}
				b.WriteByte(' ')
				b.WriteString(s)
				b.WriteByte('\n')
				continue
			}
			if shouldLiteral(item, flags) {
				b.WriteString(pad)
				b.WriteString("- ")
				b.WriteString(d.literal(item.(string), depth+1))
				if itemIndex < len(v)-1 && !strings.HasSuffix(b.String(), "\n") {
					b.WriteByte('\n')
				}
				continue
			}
			if isCollection(item) && depth+1 < inline && collectionLen(item) > 0 {
				if m, ok := item.(Mapping); ok && flags&DumpCompactNestedMappings != 0 {
					s, err := d.encode(m, inline, depth+1, flags, false)
					if err != nil {
						return "", err
					}
					prefix := strings.Repeat(" ", (depth+1)*d.indent)
					s = strings.TrimPrefix(s, prefix)
					continuation := strings.Repeat(" ", depth*d.indent+2)
					s = strings.ReplaceAll(s, "\n"+prefix, "\n"+continuation)
					b.WriteString(pad)
					b.WriteString("- ")
					b.WriteString(s)
					continue
				}
				b.WriteString(pad)
				b.WriteString("-\n")
				s, err := d.encode(item, inline, depth+1, flags, false)
				if err != nil {
					return "", err
				}
				b.WriteString(s)
			} else {
				s, err := encodeInline(item, flags, true)
				if err != nil {
					return "", err
				}
				b.WriteString(pad)
				b.WriteString("- ")
				b.WriteString(s)
				b.WriteByte('\n')
			}
		}
		return b.String(), nil
	}
	return encodeInline(value, flags, false)
}

func (d *Dumper) encodeTagged(v TaggedValue, inline, depth int, flags Flags, root bool) (string, error) {
	prefix := "!" + v.Tag
	if shouldLiteral(v.Value, flags) {
		return prefix + " " + d.literal(v.Value.(string), depth+1), nil
	}
	if isCollection(v.Value) && depth+1 < inline {
		s, err := d.encode(v.Value, inline, depth, flags, root)
		if err != nil {
			return "", err
		}
		return prefix + "\n" + s, nil
	}
	s, err := encodeInline(v.Value, flags, true)
	if err != nil {
		return "", err
	}
	return prefix + " " + s, nil
}

func (d *Dumper) literal(s string, depth int) string {
	trailing := len(s) - len(strings.TrimRight(s, "\n"))
	indicator := "|-"
	if trailing == 1 {
		indicator = "|"
	} else if trailing > 1 {
		indicator = "|+"
	}
	body := strings.TrimSuffix(s, strings.Repeat("\n", trailing))
	lines := strings.Split(body, "\n")
	explicit := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" && strings.HasPrefix(lines[0], " ") {
		explicit = d.indent
		indicator = "|" + strconv.Itoa(d.indent) + strings.TrimPrefix(indicator, "|")
	} else if len(lines) > 1 && lines[0] == "" && strings.HasPrefix(lines[1], " ") {
		explicit = d.indent
		indicator = "|" + strconv.Itoa(d.indent) + strings.TrimPrefix(indicator, "|")
	}
	pad := strings.Repeat(" ", depth*d.indent)
	var b strings.Builder
	b.WriteString(indicator)
	b.WriteByte('\n')
	for i, line := range lines {
		if line != "" {
			b.WriteString(pad)
		}
		b.WriteString(line)
		if i < len(lines)-1 || trailing > 0 {
			b.WriteByte('\n')
		}
	}
	if trailing > 1 {
		b.WriteString(strings.Repeat("\n", trailing-1))
	}
	_ = explicit
	return b.String()
}

func encodeInline(value any, flags Flags, asValue bool) (string, error) {
	switch v := value.(type) {
	case nil:
		if asValue && flags&DumpNullAsEmpty != 0 {
			return "", nil
		}
		if flags&DumpNullAsTilde != 0 {
			return "~", nil
		}
		return "null", nil
	case bool:
		return strconv.FormatBool(v), nil
	case int:
		return strconv.Itoa(v), nil
	case int8:
		return strconv.FormatInt(int64(v), 10), nil
	case int16:
		return strconv.FormatInt(int64(v), 10), nil
	case int32:
		return strconv.FormatInt(int64(v), 10), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float32:
		return formatFloat(float64(v), 32), nil
	case float64:
		return formatFloat(v, 64), nil
	case string:
		return encodeString(v, flags, asValue), nil
	case []byte:
		return "!!binary " + base64.StdEncoding.EncodeToString(v), nil
	case time.Time:
		return formatTime(v), nil
	case TaggedValue:
		s, err := encodeInline(v.Value, flags, true)
		return "!" + v.Tag + " " + s, err
	case []any:
		parts := make([]string, len(v))
		for i := range v {
			s, err := encodeInline(v[i], flags, true)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case Mapping:
		if len(v) == 0 {
			return "{}", nil
		}
		parts := make([]string, len(v))
		for i, pair := range v {
			k, err := encodeKey(pair.Key, flags)
			if err != nil {
				return "", err
			}
			s, err := encodeInline(pair.Value, flags, true)
			if err != nil {
				return "", err
			}
			parts[i] = k + ": " + s
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	default:
		if flags&DumpErrorOnUnsupportedType != 0 {
			return "", fmt.Errorf("unsupported value of type %T", value)
		}
		return "null", nil
	}
}

func encodeKey(key any, flags Flags) (string, error) {
	if flags&DumpNumericKeysAsStrings != 0 {
		switch v := key.(type) {
		case int:
			return "'" + strconv.Itoa(v) + "'", nil
		case int64:
			return "'" + strconv.FormatInt(v, 10) + "'", nil
		case uint:
			return "'" + strconv.FormatUint(uint64(v), 10) + "'", nil
		}
	}
	return encodeInline(key, flags, false)
}

func encodeString(s string, flags Flags, asValue bool) string {
	if !utf8.ValidString(s) {
		return "!!binary " + base64.StdEncoding.EncodeToString([]byte(s))
	}
	if needsDoubleQuote(s) || asValue && flags&DumpForceDoubleQuotesOnValues != 0 && s != "" {
		return doubleQuote(s)
	}
	if needsQuote(s) {
		if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
			return "\"" + strings.ReplaceAll(s, "\\", "\\\\") + "\""
		}
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return s
}

func needsDoubleQuote(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\t' || r == 0x7f || r == '\u0085' || r == '\u00a0' || r == '\u2028' || r == '\u2029' {
			return true
		}
	}
	return strings.ContainsAny(s, "\n\r\t")
}

func doubleQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case 0:
			b.WriteString(`\0`)
		case 7:
			b.WriteString(`\a`)
		case 8:
			b.WriteString(`\b`)
		case 9:
			b.WriteString(`\t`)
		case 10:
			b.WriteString(`\n`)
		case 11:
			b.WriteString(`\v`)
		case 12:
			b.WriteString(`\f`)
		case 13:
			b.WriteString(`\r`)
		case 27:
			b.WriteString(`\e`)
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case 0x85:
			b.WriteString(`\N`)
		case 0xa0:
			b.WriteString(`\_`)
		case 0x2028:
			b.WriteString(`\L`)
		case 0x2029:
			b.WriteString(`\P`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func needsQuote(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return true
	}
	if strings.ContainsRune(s, '\u3000') || strings.ContainsAny(s, "#:,[]{}") {
		return true
	}
	if strings.ContainsAny(s, "'\"") {
		return true
	}
	if strings.Contains(s, " ") {
		return true
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "?") || strings.HasPrefix(s, "!") || strings.HasPrefix(s, "&") || strings.HasPrefix(s, "*") || strings.HasPrefix(s, "@") {
		return true
	}
	switch strings.ToLower(s) {
	case "yes", "no", "on", "off":
		return true
	}
	if v, _ := parseScalarValue(s, 0); fmt.Sprintf("%T:%v", v, v) != fmt.Sprintf("%T:%v", s, s) {
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	return false
}

func formatFloat(f float64, bits int) string {
	if math.IsNaN(f) {
		return ".NaN"
	}
	if math.IsInf(f, 1) {
		return ".Inf"
	}
	if math.IsInf(f, -1) {
		return "-.Inf"
	}
	s := strconv.FormatFloat(f, 'g', -1, bits)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	if i := strings.IndexByte(s, 'e'); i >= 0 {
		s = s[:i] + "E" + normalizeExponent(s[i+1:])
	}
	return s
}
func normalizeExponent(s string) string {
	if s == "" {
		return s
	}
	if s[0] != '+' && s[0] != '-' {
		return "+" + s
	}
	return s
}
func formatTime(t time.Time) string {
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign = "-"
		off = -off
	}
	frac := ""
	if t.Nanosecond() != 0 {
		if t.Nanosecond()%1_000_000 == 0 {
			frac = fmt.Sprintf(".%03d", t.Nanosecond()/1_000_000)
		} else {
			frac = fmt.Sprintf(".%06d", t.Nanosecond()/1_000)
		}
	}
	return t.Format("2006-01-02T15:04:05") + frac + fmt.Sprintf("%s%02d:%02d", sign, off/3600, (off%3600)/60)
}
func shouldLiteral(v any, flags Flags) bool {
	s, ok := v.(string)
	return ok && flags&DumpMultiLineLiteralBlock != 0 && strings.Contains(s, "\n") && !strings.Contains(s, "\r")
}
func collectionLen(v any) int {
	switch x := v.(type) {
	case Mapping:
		return len(x)
	case []any:
		return len(x)
	}
	return 0
}

func normalizeDumpValue(value any, flags Flags) (any, error) {
	if value == nil {
		return nil, nil
	}
	if t, ok := value.(TaggedValue); ok {
		v, err := normalizeDumpValue(t.Value, flags)
		return TaggedValue{Tag: t.Tag, Value: v}, err
	}
	if _, ok := value.(time.Time); ok {
		return value, nil
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, nil
		}
		return normalizeDumpValue(rv.Elem().Interface(), flags)
	}
	if rv.Kind() == reflect.Struct {
		if flags&DumpStructsAsMappings == 0 {
			if flags&DumpErrorOnUnsupportedType != 0 {
				return nil, fmt.Errorf("unsupported struct %T", value)
			}
			return nil, nil
		}
		rt := rv.Type()
		m := make(Mapping, 0, rv.NumField())
		for i := 0; i < rv.NumField(); i++ {
			f := rt.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name := f.Tag.Get("yaml")
			if name == "-" {
				continue
			}
			if j := strings.IndexByte(name, ','); j >= 0 {
				name = name[:j]
			}
			if name == "" {
				name = f.Name
			}
			v, err := normalizeDumpValue(rv.Field(i).Interface(), flags)
			if err != nil {
				return nil, err
			}
			m = append(m, Pair{Key: name, Value: v})
		}
		return m, nil
	}
	if m, ok := value.(Mapping); ok {
		r := make(Mapping, len(m))
		for i, p := range m {
			v, err := normalizeDumpValue(p.Value, flags)
			if err != nil {
				return nil, err
			}
			r[i] = Pair{Key: p.Key, Value: v}
		}
		return r, nil
	}
	if a, ok := value.([]any); ok {
		r := make([]any, len(a))
		for i := range a {
			v, err := normalizeDumpValue(a[i], flags)
			if err != nil {
				return nil, err
			}
			r[i] = v
		}
		return r, nil
	}
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.UnsafePointer:
		if flags&DumpErrorOnUnsupportedType != 0 {
			return nil, fmt.Errorf("unsupported value of type %T", value)
		}
		return nil, nil
	}
	return value, nil
}
