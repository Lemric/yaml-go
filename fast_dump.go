package yaml

import (
	"encoding/base64"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"
)

// fastDump handles the allocation-sensitive, ordinary-data path. Features
// requiring escaping, reflection, tags, binary data or literal blocks retain
// the fully general encoder.
func fastDump(value any, inline, indent int, flags Flags) (string, bool) {
	if mapping, ok := value.(Mapping); ok && len(mapping) == 1 {
		if _, binary := mapping[0].Value.([]byte); binary {
			return fastDumpInline(value, flags)
		}
	}
	if flags&^(DumpCompactNestedMappings|DumpNumericKeysAsStrings) != 0 || !fastDumpable(value, inline, 0, false, flags) {
		return "", false
	}
	out, ok := appendFastBlock(make([]byte, 0, fastBlockCapacity(value, inline, indent, 0, flags)), value, inline, indent, 0, flags)
	if !ok {
		return "", false
	}
	if len(out) == 0 {
		return "", true
	}
	return unsafe.String(unsafe.SliceData(out), len(out)), true
}

func fastDumpInline(value any, flags Flags) (string, bool) {
	if flags&^DumpNumericKeysAsStrings != 0 || !fastInlineDumpable(value, flags) {
		return "", false
	}
	if s, ok := value.(string); ok {
		return s, true
	}
	out := appendFastInline(make([]byte, 0, fastInlineCapacity(value, flags)), value, flags)
	if len(out) == 0 {
		return "", true
	}
	return unsafe.String(unsafe.SliceData(out), len(out)), true
}

func fastInlineDumpable(v any, flags Flags) bool {
	switch v := v.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, time.Time, []byte:
		return true
	case string:
		return fastPlain(v)
	case Mapping:
		for _, p := range v {
			if !fastKeyDumpable(p.Key, flags) || !fastInlineDumpable(p.Value, flags) {
				return false
			}
		}
		return true
	case []any:
		for _, x := range v {
			if !fastInlineDumpable(x, flags) {
				return false
			}
		}
		return true
	case TaggedValue:
		return fastInlineDumpable(v.Value, flags)
	default:
		return false
	}
}

func appendFastInline(dst []byte, v any, flags Flags) []byte {
	switch v := v.(type) {
	case Mapping:
		if len(v) == 0 {
			return append(dst, '{', '}')
		}
		dst = append(dst, '{', ' ')
		for i, p := range v {
			if i > 0 {
				dst = append(dst, ',', ' ')
			}
			dst = appendFastKey(dst, p.Key, flags)
			dst = append(dst, ':', ' ')
			dst = appendFastInline(dst, p.Value, flags)
		}
		return append(dst, ' ', '}')
	case []any:
		dst = append(dst, '[')
		for i, x := range v {
			if i > 0 {
				dst = append(dst, ',', ' ')
			}
			dst = appendFastInline(dst, x, flags)
		}
		return append(dst, ']')
	case TaggedValue:
		dst = append(dst, '!')
		dst = append(dst, v.Tag...)
		dst = append(dst, ' ')
		return appendFastInline(dst, v.Value, flags)
	default:
		return appendFastScalar(dst, v)
	}
}

func fastInlineCapacity(v any, flags Flags) int {
	switch v := v.(type) {
	case Mapping:
		n := 4
		for _, p := range v {
			n += fastKeyCapacity(p.Key, flags) + 2 + fastInlineCapacity(p.Value, flags) + 2
		}
		return n
	case []any:
		n := 2
		for _, x := range v {
			n += fastInlineCapacity(x, flags) + 2
		}
		return n
	case TaggedValue:
		return len(v.Tag) + 2 + fastInlineCapacity(v.Value, flags)
	default:
		return fastScalarCapacity(v)
	}
}

func fastBlockCapacity(v any, inline, indent, depth int, flags Flags) int {
	pad := depth * indent
	switch v := v.(type) {
	case TaggedValue:
		if isCollection(v.Value) && depth+1 < inline {
			return len(v.Tag) + 2 + fastBlockCapacity(v.Value, inline, indent, depth, flags)
		}
		return len(v.Tag) + 2 + fastInlineCapacity(v.Value, flags)
	case Mapping:
		if len(v) == 0 {
			return 2
		}
		n := 0
		for _, p := range v {
			n += pad + fastKeyCapacity(p.Key, flags) + 1
			if isCollection(p.Value) && depth+1 < inline && collectionLen(p.Value) > 0 {
				n += 1 + fastBlockCapacity(p.Value, inline, indent, depth+1, flags)
			} else {
				n += 2 + fastScalarCapacity(p.Value)
			}
		}
		return n
	case []any:
		if len(v) == 0 {
			return 2
		}
		n := 0
		for _, item := range v {
			if m, ok := item.(Mapping); ok && depth+1 < inline && len(m) > 0 && flags&DumpCompactNestedMappings != 0 {
				n += pad + 2
				for i, p := range m {
					if i > 0 {
						n += pad + 2
					}
					n += fastKeyCapacity(p.Key, flags) + 3 + fastScalarCapacity(p.Value)
				}
				continue
			}
			if isCollection(item) && depth+1 < inline && collectionLen(item) > 0 {
				n += pad + 2 + fastBlockCapacity(item, inline, indent, depth+1, flags)
			} else {
				n += pad + 3 + fastScalarCapacity(item)
			}
		}
		return n
	default:
		return fastScalarCapacity(v)
	}
}

func fastScalarCapacity(v any) int {
	switch v := v.(type) {
	case nil:
		return 4
	case string:
		return len(v)
	case bool:
		return 5
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return 20
	case float32, float64:
		return 32
	case time.Time:
		return 40
	case []byte:
		return len("!!binary ") + base64.StdEncoding.EncodedLen(len(v))
	case TaggedValue:
		return len(v.Tag) + 2 + fastInlineCapacity(v.Value, 0)
	case Mapping, []any:
		return 2
	default:
		return 0
	}
}

func fastDumpable(v any, inline, depth int, inCompact bool, flags Flags) bool {
	switch v := v.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, time.Time, []byte:
		return true
	case string:
		return fastPlain(v)
	case Mapping:
		if depth >= inline {
			return false
		}
		for _, p := range v {
			if !fastKeyDumpable(p.Key, flags) || !fastDumpable(p.Value, inline, depth+1, false, flags) {
				return false
			}
			if inCompact && isCollection(p.Value) {
				return false
			}
		}
		return true
	case []any:
		if depth >= inline {
			return false
		}
		for _, item := range v {
			compact := false
			if _, ok := item.(Mapping); ok && flags&DumpCompactNestedMappings != 0 {
				compact = true
			}
			if !fastDumpable(item, inline, depth+1, compact, flags) {
				return false
			}
		}
		return true
	case TaggedValue:
		if isCollection(v.Value) {
			return depth+1 < inline && fastDumpable(v.Value, inline, depth, false, flags)
		}
		return fastInlineDumpable(v.Value, flags)
	default:
		return false
	}
}

func fastPlain(s string) bool {
	if s == "" || !utf8.ValidString(s) || strings.TrimSpace(s) != s || strings.ContainsAny(s, " \t\r\n#:,[]{}'\"") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0x85 || r == 0xa0 || r == 0x2028 || r == 0x2029 {
			return false
		}
	}
	switch s[0] {
	case '-', '?', '!', '&', '*', '@', '`', '|', '>', '%':
		return false
	}
	switch strings.ToLower(s) {
	case "null", "~", "true", "false", "yes", "no", "on", "off", ".nan", ".inf", "+.inf", "-.inf":
		return false
	}
	if s[0] >= '0' && s[0] <= '9' || s[0] == '+' || s[0] == '-' {
		return false
	}
	return true
}

func fastKeyDumpable(key any, flags Flags) bool {
	switch key := key.(type) {
	case string:
		return fastPlain(key)
	case int, int64, uint:
		return true
	case int8, int16, int32, uint8, uint16, uint32, uint64:
		return flags&DumpNumericKeysAsStrings == 0
	default:
		return false
	}
}

func fastKeyCapacity(key any, flags Flags) int {
	if text, ok := key.(string); ok {
		return len(text)
	}
	if flags&DumpNumericKeysAsStrings != 0 {
		switch key.(type) {
		case int, int64, uint:
			return 22
		}
	}
	return 20
}

func appendFastKey(dst []byte, key any, flags Flags) []byte {
	quoted := flags&DumpNumericKeysAsStrings != 0
	if quoted {
		switch key.(type) {
		case int, int64, uint:
			dst = append(dst, '\'')
		}
	}
	switch key := key.(type) {
	case string:
		dst = append(dst, key...)
	case int:
		dst = strconv.AppendInt(dst, int64(key), 10)
	case int8:
		dst = strconv.AppendInt(dst, int64(key), 10)
	case int16:
		dst = strconv.AppendInt(dst, int64(key), 10)
	case int32:
		dst = strconv.AppendInt(dst, int64(key), 10)
	case int64:
		dst = strconv.AppendInt(dst, key, 10)
	case uint:
		dst = strconv.AppendUint(dst, uint64(key), 10)
	case uint8:
		dst = strconv.AppendUint(dst, uint64(key), 10)
	case uint16:
		dst = strconv.AppendUint(dst, uint64(key), 10)
	case uint32:
		dst = strconv.AppendUint(dst, uint64(key), 10)
	case uint64:
		dst = strconv.AppendUint(dst, key, 10)
	}
	if quoted {
		switch key.(type) {
		case int, int64, uint:
			dst = append(dst, '\'')
		}
	}
	return dst
}

func appendFastBlock(dst []byte, v any, inline, indent, depth int, flags Flags) ([]byte, bool) {
	pad := depth * indent
	switch v := v.(type) {
	case TaggedValue:
		dst = append(dst, '!')
		dst = append(dst, v.Tag...)
		if isCollection(v.Value) && depth+1 < inline {
			dst = append(dst, '\n')
			return appendFastBlock(dst, v.Value, inline, indent, depth, flags)
		}
		dst = append(dst, ' ')
		return appendFastInline(dst, v.Value, flags), true
	case Mapping:
		if len(v) == 0 {
			return append(dst, '{', '}'), true
		}
		for _, pair := range v {
			dst = appendSpaces(dst, pad)
			dst = appendFastKey(dst, pair.Key, flags)
			dst = append(dst, ':')
			if isCollection(pair.Value) && depth+1 < inline && collectionLen(pair.Value) > 0 {
				dst = append(dst, '\n')
				var ok bool
				dst, ok = appendFastBlock(dst, pair.Value, inline, indent, depth+1, flags)
				if !ok {
					return nil, false
				}
			} else {
				dst = append(dst, ' ')
				dst = appendFastScalar(dst, pair.Value)
				dst = append(dst, '\n')
			}
		}
		return dst, true
	case []any:
		if len(v) == 0 {
			return append(dst, '[', ']'), true
		}
		for _, item := range v {
			if m, ok := item.(Mapping); ok && depth+1 < inline && len(m) > 0 && flags&DumpCompactNestedMappings != 0 {
				dst = appendSpaces(dst, pad)
				dst = append(dst, '-', ' ')
				for i, pair := range m {
					if i > 0 {
						dst = appendSpaces(dst, pad+2)
					}
					dst = appendFastKey(dst, pair.Key, flags)
					dst = append(dst, ':', ' ')
					dst = appendFastScalar(dst, pair.Value)
					dst = append(dst, '\n')
				}
				continue
			}
			if isCollection(item) && depth+1 < inline && collectionLen(item) > 0 {
				dst = appendSpaces(dst, pad)
				dst = append(dst, '-', '\n')
				var ok bool
				dst, ok = appendFastBlock(dst, item, inline, indent, depth+1, flags)
				if !ok {
					return nil, false
				}
				continue
			}
			dst = appendSpaces(dst, pad)
			dst = append(dst, '-', ' ')
			dst = appendFastScalar(dst, item)
			dst = append(dst, '\n')
		}
		return dst, true
	default:
		return appendFastScalar(dst, v), true
	}
}

func appendFastScalar(dst []byte, v any) []byte {
	switch v := v.(type) {
	case nil:
		return append(dst, "null"...)
	case string:
		return append(dst, v...)
	case bool:
		return strconv.AppendBool(dst, v)
	case int:
		return strconv.AppendInt(dst, int64(v), 10)
	case int8:
		return strconv.AppendInt(dst, int64(v), 10)
	case int16:
		return strconv.AppendInt(dst, int64(v), 10)
	case int32:
		return strconv.AppendInt(dst, int64(v), 10)
	case int64:
		return strconv.AppendInt(dst, v, 10)
	case uint:
		return strconv.AppendUint(dst, uint64(v), 10)
	case uint8:
		return strconv.AppendUint(dst, uint64(v), 10)
	case uint16:
		return strconv.AppendUint(dst, uint64(v), 10)
	case uint32:
		return strconv.AppendUint(dst, uint64(v), 10)
	case uint64:
		return strconv.AppendUint(dst, v, 10)
	case float32:
		return appendFastFloat(dst, float64(v), 32)
	case float64:
		return appendFastFloat(dst, v, 64)
	case time.Time:
		return appendFastTime(dst, v)
	case []byte:
		dst = append(dst, "!!binary "...)
		return base64.StdEncoding.AppendEncode(dst, v)
	case TaggedValue:
		dst = append(dst, '!')
		dst = append(dst, v.Tag...)
		dst = append(dst, ' ')
		return appendFastInline(dst, v.Value, 0)
	case Mapping:
		if len(v) == 0 {
			return append(dst, '{', '}')
		}
	case []any:
		if len(v) == 0 {
			return append(dst, '[', ']')
		}
	default:
		return dst
	}
	return dst
}

func appendFastTime(dst []byte, v time.Time) []byte {
	dst = v.AppendFormat(dst, "2006-01-02T15:04:05")
	if ns := v.Nanosecond(); ns != 0 {
		dst = append(dst, '.')
		if ns%1_000_000 == 0 {
			dst = appendFixedDecimal(dst, ns/1_000_000, 3)
		} else {
			dst = appendFixedDecimal(dst, ns/1_000, 6)
		}
	}
	_, offset := v.Zone()
	if offset < 0 {
		dst = append(dst, '-')
		offset = -offset
	} else {
		dst = append(dst, '+')
	}
	dst = appendFixedDecimal(dst, offset/3600, 2)
	dst = append(dst, ':')
	return appendFixedDecimal(dst, offset%3600/60, 2)
}

func appendFixedDecimal(dst []byte, value, width int) []byte {
	start := len(dst)
	for range width {
		dst = append(dst, '0')
	}
	for i := start + width - 1; i >= start; i-- {
		dst[i] += byte(value % 10)
		value /= 10
	}
	return dst
}

func appendFastFloat(dst []byte, v float64, bits int) []byte {
	if math.IsNaN(v) {
		return append(dst, ".NaN"...)
	}
	if math.IsInf(v, 1) {
		return append(dst, ".Inf"...)
	}
	if math.IsInf(v, -1) {
		return append(dst, "-.Inf"...)
	}
	start := len(dst)
	dst = strconv.AppendFloat(dst, v, 'g', -1, bits)
	exponent := -1
	hasPoint := false
	for i := start; i < len(dst); i++ {
		switch dst[i] {
		case '.':
			hasPoint = true
		case 'e':
			dst[i] = 'E'
			exponent = i
		}
	}
	if exponent < 0 && !hasPoint {
		return append(dst, '.', '0')
	}
	if exponent >= 0 && exponent+1 < len(dst) && dst[exponent+1] != '+' && dst[exponent+1] != '-' {
		dst = append(dst, 0)
		copy(dst[exponent+2:], dst[exponent+1:])
		dst[exponent+1] = '+'
	}
	return dst
}
func appendSpaces(dst []byte, n int) []byte {
	for range n {
		dst = append(dst, ' ')
	}
	return dst
}
