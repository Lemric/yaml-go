package yaml

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
	"unsafe"
)

type Parser struct {
	limits Limits
}

func NewParser(limits Limits) *Parser {
	if limits.MaxNestingDepth < 0 {
		limits.MaxNestingDepth = 0
	}
	if limits.MaxCollectionAliases < 0 {
		limits.MaxCollectionAliases = 0
	}
	return &Parser{limits: limits}
}

func Parse(input string, flags Flags) (any, error) {
	return ParseWithLimits(input, flags, Limits{DefaultMaxNestingDepth, DefaultMaxCollectionAliases})
}

func ParseWithLimits(input string, flags Flags, limits Limits) (any, error) {
	return NewParser(limits).Parse(input, flags)
}

func ParseFile(path string, flags Flags) (any, error) {
	return ParseFileWithLimits(path, flags, Limits{DefaultMaxNestingDepth, DefaultMaxCollectionAliases})
}

func ParseFileWithLimits(path string, flags Flags, limits Limits) (any, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file %q does not exist", path)
		}
		return nil, fmt.Errorf("file %q cannot be read: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("file %q cannot be read", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("file %q cannot be read: %w", path, err)
	}
	v, err := ParseWithLimits(string(b), flags, limits)
	if pe, ok := err.(*ParseError); ok {
		pe.Filename = path
	}
	return v, err
}

type sourceLine struct {
	raw    string
	text   string
	indent int
	num    int
}

type blockParser struct {
	lines      []sourceLine
	flags      Flags
	limits     Limits
	refs       map[string]any
	active     []string
	aliasCount int
	arena      *valueArena
}

func (p *Parser) Parse(input string, flags Flags) (any, error) {
	if !utf8.ValidString(input) {
		return nil, parseError("The YAML value does not appear to be valid UTF-8", 1, "")
	}
	input = strings.TrimPrefix(input, "\ufeff")
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	a := newDocumentArena(input)
	lines := a.lineBuf[:0]
	documents := 0
	lineNum := 0
	for start := 0; start <= len(input); {
		end := strings.IndexByte(input[start:], '\n')
		if end < 0 {
			end = len(input)
		} else {
			end += start
		}
		raw := input[start:end]
		lastLine := end == len(input)
		start = end + 1
		lineNum++
		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		if indent < len(raw) && raw[indent] == '\t' {
			return nil, parseError("A YAML file cannot contain tabs as indentation", lineNum, strings.TrimSpace(raw))
		}
		trim := strings.TrimSpace(raw)
		if strings.HasPrefix(trim, "%YAML") {
			if lastLine {
				break
			}
			continue
		}
		if indent == 0 && strings.HasPrefix(trim, "---") {
			documents++
			if documents > 1 {
				return nil, parseError("Multiple documents are not supported", lineNum, trim)
			}
			rest := strings.TrimSpace(strings.TrimPrefix(trim, "---"))
			if rest == "" || strings.HasPrefix(rest, "%") || strings.HasPrefix(rest, "!") || len(rest) > 4096 {
				if lastLine {
					break
				}
				continue
			}
			raw = strings.Repeat(" ", indent) + rest
			trim = rest
		}
		if trim == "..." {
			if lastLine {
				break
			}
			continue
		}
		lines = append(lines, sourceLine{raw: raw, text: raw[indent:], indent: indent, num: lineNum})
		if lastLine {
			break
		}
	}
	bp := &a.block
	*bp = blockParser{lines: lines, flags: flags, limits: p.limits, arena: a, active: a.activeBuf[:0]}
	i := bp.skip(0)
	if i >= len(lines) {
		return nil, nil
	}
	v, next, err := bp.node(i, lines[i].indent, 0)
	if err != nil {
		return nil, err
	}
	next = bp.skip(next)
	if next < len(lines) {
		line := lines[next]
		return nil, parseError("Unable to parse the YAML string", line.num, strings.TrimSpace(line.raw))
	}
	return v, nil
}

func (p *blockParser) node(i, indent, depth int) (any, int, error) {
	if depth > p.limits.MaxNestingDepth {
		line := p.lines[min(i, len(p.lines)-1)]
		return nil, i, parseError(fmt.Sprintf("Maximum nesting depth of %d exceeded", p.limits.MaxNestingDepth), line.num, strings.TrimSpace(line.raw))
	}
	i = p.skip(i)
	if i >= len(p.lines) {
		return nil, i, nil
	}
	line := p.lines[i]
	trim := strings.TrimSpace(line.text)
	if isSeqLine(line, indent) {
		v, n, err := p.sequenceNode(i, indent, depth)
		if err != nil {
			return nil, n, err
		}
		return p.arena.sequenceValue(v), n, nil
	}
	if findMappingColon(line.text) >= 0 {
		v, n, err := p.mappingNode(i, indent, depth)
		if err != nil {
			return nil, n, err
		}
		return p.arena.mappingValue(v), n, nil
	}
	if strings.HasPrefix(trim, "?") {
		return nil, i, parseError("Complex mappings are not supported", line.num, trim)
	}
	return p.scalarNode(i, indent, depth)
}

func (p *blockParser) sequenceNode(i, indent, depth int) ([]any, int, error) {
	capacity := 0
	for j := i; j < len(p.lines); j++ {
		if isSeqLine(p.lines[j], indent) {
			capacity++
			continue
		}
		if strings.TrimSpace(p.lines[j].text) != "" && p.lines[j].indent <= indent {
			break
		}
	}
	items := p.arena.sequence(capacity)
	for {
		i = p.skip(i)
		if i >= len(p.lines) || !isSeqLine(p.lines[i], indent) {
			break
		}
		line := p.lines[i]
		rest := trimYAML(strings.TrimPrefix(line.text, "-"))
		contentIndent := indent + 1
		for contentIndent-indent < len(line.text) && line.text[contentIndent-indent] == ' ' {
			contentIndent++
		}
		if strings.HasPrefix(rest, "?") {
			return nil, i, parseError("Complex mappings are not supported", line.num, strings.TrimSpace(line.raw))
		}
		if rest == "" || strings.HasPrefix(rest, "#") {
			next := p.skip(i + 1)
			if next >= len(p.lines) || p.lines[next].indent < indent || isSeqLine(p.lines[next], indent) || p.lines[next].indent == indent {
				items = append(items, nil)
				i = i + 1
				continue
			}
			childIndent := p.lines[next].indent
			v, n, err := p.node(next, childIndent, depth+1)
			if err != nil {
				return nil, i, err
			}
			items = append(items, v)
			i = n
			continue
		}
		if isSeqText(rest) {
			virtual := sourceLine{raw: line.raw, text: rest, indent: contentIndent, num: line.num}
			p.lines[i] = virtual
			v, n, err := p.sequenceNode(i, contentIndent, depth+1)
			p.lines[i] = line
			if err != nil {
				return nil, i, err
			}
			items = append(items, p.arena.sequenceValue(v))
			i = n
			continue
		}
		// A compact mapping starts on the same line as the dash.
		if colon := findMappingColon(rest); colon >= 0 {
			virtual := sourceLine{raw: line.raw, text: rest, indent: contentIndent, num: line.num}
			p.lines[i] = virtual
			v, n, err := p.mappingNode(i, contentIndent, depth+1)
			p.lines[i] = line
			if err != nil {
				return nil, i, err
			}
			items = append(items, p.arena.mappingValue(v))
			i = n
			continue
		}
		v, n, err := p.valueText(rest, i, indent, depth+1)
		if err != nil {
			return nil, i, err
		}
		items = append(items, v)
		i = n
	}
	return items, i, nil
}

func (p *blockParser) mappingNode(i, indent, depth int) (Mapping, int, error) {
	capacity := 0
	hasMerge := false
	for j := i; j < len(p.lines); j++ {
		if p.lines[j].indent == indent && findMappingColon(p.lines[j].text) >= 0 {
			capacity++
			if strings.HasPrefix(strings.TrimSpace(p.lines[j].text), "<<:") {
				hasMerge = true
			}
			continue
		}
		if strings.TrimSpace(p.lines[j].text) != "" && p.lines[j].indent < indent {
			break
		}
	}
	if hasMerge {
		capacity += 16
	}
	m := p.arena.mapping(capacity)
	stringIndex := p.arena.lookup(capacity)
	var explicitKeys [64]any
	explicitCount := 0
	for {
		i = p.skip(i)
		if i >= len(p.lines) {
			break
		}
		line := p.lines[i]
		if line.indent != indent || isSeqLine(line, indent) {
			break
		}
		trim := strings.TrimSpace(line.text)
		if strings.HasPrefix(trim, "?") {
			return nil, i, parseError("Complex mappings are not supported", line.num, trim)
		}
		colon := findMappingColon(line.text)
		if colon < 0 {
			break
		}
		keyText := strings.TrimSpace(line.text[:colon])
		if keyText == "" {
			return nil, i, parseError("Missing mapping key", line.num, trim)
		}
		key, err := p.parseKey(keyText, line.num)
		if err != nil {
			return nil, i, err
		}
		if key != "<<" && explicitHas(explicitKeys[:explicitCount], key) {
			return nil, i, parseError(fmt.Sprintf("Duplicate key %q detected", key), line.num, trim)
		}
		rest := stripComment(trimYAML(line.text[colon+1:]))
		var value any
		next := i + 1
		if rest == "" {
			j := p.skip(next)
			if j < len(p.lines) && (p.lines[j].indent > indent || isSeqLine(p.lines[j], indent)) {
				childIndent := p.lines[j].indent
				if isSeqLine(p.lines[j], indent) {
					childIndent = indent
				}
				value, next, err = p.node(j, childIndent, depth+1)
			} else {
				value = nil
			}
		} else {
			value, next, err = p.valueText(rest, i, indent, depth+1)
		}
		if err != nil {
			return nil, i, err
		}
		if key == "<<" {
			m, err = mergeMappings(m, value)
			if err != nil {
				return nil, i, parseError(err.Error(), line.num, trim)
			}
			stringIndex.indexMerged(m)
		} else {
			if text, ok := key.(string); ok && stringIndex.available() {
				if idx, explicit, exists := stringIndex.find(m, text); exists {
					if explicit {
						return nil, i, parseError(fmt.Sprintf("Duplicate key %q detected", key), line.num, trim)
					}
					m[idx].Value = value
					stringIndex.set(m, text, idx, true)
				} else {
					m = append(m, Pair{Key: key, Value: value})
					stringIndex.set(m, text, len(m)-1, true)
				}
				i = next
				continue
			}
			if idx := mappingIndex(m, key); idx >= 0 {
				m[idx].Value = value
			} else {
				m = append(m, Pair{Key: key, Value: value})
			}
			if explicitCount < len(explicitKeys) {
				explicitKeys[explicitCount] = key
				explicitCount++
			}
		}
		i = next
	}
	return m, i, nil
}

func (p *blockParser) scalarNode(i, indent, depth int) (any, int, error) {
	line := p.lines[i]
	j := p.skip(i + 1)
	plainStart := len(strings.TrimSpace(line.text)) > 0 && !strings.ContainsRune("[{\"'", rune(strings.TrimSpace(line.text)[0]))
	if plainStart && j < len(p.lines) && p.lines[j].indent > indent && findMappingColon(p.lines[j].text) >= 0 {
		return nil, i, parseError("Mapping values are not allowed in multi-line blocks", line.num, strings.TrimSpace(line.raw))
	}
	return p.valueText(strings.TrimSpace(line.text), i, indent-1, depth)
}

func (p *blockParser) valueText(text string, lineIndex, parentIndent, depth int) (any, int, error) {
	line := p.lines[lineIndex]
	text = stripComment(trimYAML(text))
	if text == "" {
		return nil, lineIndex + 1, nil
	}
	// Prefixes can wrap either an inline value or a following block node.
	if text[0] == '&' {
		name, rest := splitPrefix(text[1:])
		if name == "" {
			return nil, lineIndex, parseError("An anchor must contain at least one character", line.num, strings.TrimSpace(line.raw))
		}
		if containsString(p.active, name) {
			return nil, lineIndex, p.circular(name, line.num)
		}
		p.active = append(p.active, name)
		v, next, err := p.prefixedValue(rest, lineIndex, parentIndent, depth)
		p.active = p.active[:len(p.active)-1]
		if err == nil {
			p.arena.setReference(name, v)
		}
		return v, next, err
	}
	if text[0] == '*' {
		name, _ := splitPrefix(text[1:])
		if name == "" {
			return nil, lineIndex, parseError("A reference must contain at least one character", line.num, strings.TrimSpace(line.raw))
		}
		if p.flags&ParseRejectAliases != 0 {
			return nil, lineIndex, parseError("Aliases are disabled", line.num, strings.TrimSpace(line.raw))
		}
		if containsString(p.active, name) {
			return nil, lineIndex, p.circular(name, line.num)
		}
		v, ok := p.arena.reference(name)
		if !ok {
			return nil, lineIndex, parseError(fmt.Sprintf("Reference %q does not exist", name), line.num, strings.TrimSpace(line.raw))
		}
		if isCollection(v) || isTaggedCollection(v) {
			p.aliasCount++
			if p.aliasCount > p.limits.MaxCollectionAliases {
				return nil, lineIndex, parseError(fmt.Sprintf("Maximum number of collection aliases (%d) exceeded", p.limits.MaxCollectionAliases), line.num, strings.TrimSpace(line.raw))
			}
		}
		return v, lineIndex + 1, nil
	}
	if text[0] == '!' {
		if strings.HasPrefix(text, "!!") {
			tag, rest := splitPrefix(text[2:])
			if (tag == "binary") && isBlockIndicator(strings.TrimSpace(rest)) {
				raw, next, err := p.blockScalar(lineIndex, parentIndent, strings.TrimSpace(rest))
				if err != nil {
					return nil, next, err
				}
				fp := flowParser{src: raw, flags: p.flags, refs: p.refs, line: line.num, aliasCount: &p.aliasCount, maxAliases: p.limits.MaxCollectionAliases, arena: p.arena}
				v, err := decodeBinary(raw, &fp)
				return v, next, err
			}
			return p.inlineAcrossLines(text, lineIndex)
		}
		tag, rest := splitPrefix(text[1:])
		if tag == "" {
			return p.prefixedValue(rest, lineIndex, parentIndent, depth)
		}
		if p.flags&ParseCustomTags == 0 {
			return nil, lineIndex, parseError("Tags support is not enabled", line.num, strings.TrimSpace(line.raw))
		}
		v, next, err := p.prefixedValue(rest, lineIndex, parentIndent, depth)
		if err != nil {
			return nil, next, err
		}
		if rest == "" && v == nil {
			v = ""
		}
		return TaggedValue{Tag: tag, Value: v}, next, nil
	}
	if isBlockIndicator(text) {
		v, next, err := p.blockScalar(lineIndex, parentIndent, text)
		if err != nil {
			return nil, next, err
		}
		return p.arena.stringValue(v), next, nil
	}
	if text[0] == '[' || text[0] == '{' || text[0] == '\'' || text[0] == '"' {
		return p.inlineAcrossLines(text, lineIndex)
	}
	// Anchors and aliases inside flow collections need the shared table.
	if strings.ContainsAny(text, "&*") && (strings.Contains(text, "[") || strings.Contains(text, "{")) {
		return p.inlineAcrossLines(text, lineIndex)
	}
	if strings.Contains(text, ": ") {
		return nil, lineIndex, parseError("A colon cannot be used in an unquoted mapping value", line.num, strings.TrimSpace(line.raw))
	}
	// Plain values may continue on more-indented lines.
	var localParts [64]string
	parts := localParts[:1]
	parts[0] = text
	next := lineIndex + 1
	hadBlank := false
	for next < len(p.lines) {
		ln := p.lines[next]
		trim := strings.TrimSpace(ln.text)
		if trim == "" {
			hadBlank = true
			next++
			continue
		}
		if strings.HasPrefix(trim, "#") {
			break
		}
		if ln.indent <= parentIndent || findMappingColon(ln.text) >= 0 || isSeqLine(ln, ln.indent) {
			break
		}
		if hadBlank {
			parts = append(parts, "\n"+trim)
		} else {
			parts = append(parts, trim)
		}
		hadBlank = false
		next++
	}
	joined := strings.ReplaceAll(strings.Join(parts, " "), " \n", "\n")
	v, err := parseArenaScalar(joined, p.flags, p.arena)
	if err != nil {
		return nil, lineIndex, parseError(err.Error(), line.num, strings.TrimSpace(line.raw))
	}
	if n, ok := v.(int64); ok && p.flags&ParseTimestamps == 0 {
		v = int(n)
	}
	return v, next, nil
}

func (p *blockParser) prefixedValue(rest string, lineIndex, parentIndent, depth int) (any, int, error) {
	rest = strings.TrimSpace(rest)
	if rest != "" {
		return p.valueText(rest, lineIndex, parentIndent, depth)
	}
	j := p.skip(lineIndex + 1)
	if j >= len(p.lines) {
		return nil, lineIndex + 1, nil
	}
	if p.lines[j].indent < parentIndent {
		return nil, lineIndex + 1, nil
	}
	return p.node(j, p.lines[j].indent, depth+1)
}

func (p *blockParser) inlineAcrossLines(first string, i int) (any, int, error) {
	if balanced, quoted := flowComplete(first); balanced && !quoted {
		fp := flowParser{src: first, flags: p.flags, refs: p.refs, line: p.lines[i].num, aliasCount: &p.aliasCount, maxAliases: p.limits.MaxCollectionAliases, arena: p.arena}
		v, err := fp.value(false)
		if err != nil {
			return nil, i, err
		}
		fp.spaceAndComments()
		fp.space()
		if fp.i < len(fp.src) {
			problem := fmt.Sprintf("Unexpected token %q", fp.src[fp.i:])
			if strings.HasPrefix(fp.src[fp.i:], ",") && len(fp.src) > 0 && (fp.src[0] == '\'' || fp.src[0] == '"') {
				problem = fmt.Sprintf("Unexpected characters near %q", fp.src[fp.i:])
			}
			if strings.HasPrefix(fp.src[fp.i:], "]") {
				problem = "Malformed inline YAML: " + problem
			}
			return nil, i, parseError(problem, p.lines[i].num, strings.TrimSpace(p.lines[i].raw))
		}
		return v, i + 1, nil
	}
	var b strings.Builder
	b.WriteString(first)
	balanced, quoted := flowComplete(first)
	next := i + 1
	for (!balanced || quoted) && next < len(p.lines) {
		if quoted && p.lines[i].indent > 0 && p.lines[next].indent < p.lines[i].indent {
			return nil, i, parseError("Unterminated quoted string", p.lines[i].num, strings.TrimSpace(p.lines[i].raw))
		}
		b.WriteByte('\n')
		b.WriteString(p.lines[next].text)
		balanced, quoted = flowComplete(b.String())
		next++
	}
	fp := flowParser{src: b.String(), flags: p.flags, refs: p.refs, line: p.lines[i].num, aliasCount: &p.aliasCount, maxAliases: p.limits.MaxCollectionAliases, arena: p.arena}
	v, err := fp.value(false)
	if err != nil {
		return nil, i, err
	}
	fp.spaceAndComments()
	fp.space()
	if fp.i < len(fp.src) {
		problem := fmt.Sprintf("Unexpected token %q", fp.src[fp.i:])
		if strings.HasPrefix(fp.src[fp.i:], ",") && len(fp.src) > 0 && (fp.src[0] == '\'' || fp.src[0] == '"') {
			problem = fmt.Sprintf("Unexpected characters near %q", fp.src[fp.i:])
		}
		if strings.HasPrefix(fp.src[fp.i:], "]") {
			problem = "Malformed inline YAML: " + problem
		}
		return nil, i, parseError(problem, p.lines[i].num, strings.TrimSpace(p.lines[i].raw))
	}
	return v, next, nil
}

func (p *blockParser) blockScalar(i, parentIndent int, indicator string) (string, int, error) {
	folded := indicator[0] == '>'
	chomp := byte(0)
	explicit := 0
	for j := 1; j < len(indicator); j++ {
		c := indicator[j]
		if c == '+' || c == '-' {
			chomp = c
		}
		if c >= '1' && c <= '9' {
			explicit = int(c - '0')
		}
		if c == '#' || isSpace(c) {
			break
		}
	}
	start := i + 1
	end := start
	contentIndent := 0
	if explicit > 0 {
		contentIndent = parentIndent + explicit
	}
	for j := start; j < len(p.lines); j++ {
		ln := p.lines[j]
		trim := strings.TrimSpace(ln.raw)
		if trim != "" {
			if contentIndent == 0 {
				if ln.indent <= parentIndent {
					break
				}
				contentIndent = ln.indent
			}
			if ln.indent < contentIndent {
				break
			}
		}
		end = j + 1
	}
	if contentIndent == 0 {
		contentIndent = parentIndent + max(1, explicit)
	}
	var localRows [64]string
	rows := localRows[:0]
	if end-start > len(localRows) {
		rows = make([]string, 0, end-start)
	}
	for j := start; j < end; j++ {
		raw := p.lines[j].raw
		if j == start && strings.TrimSpace(raw) == "" {
			rows = append(rows, "")
			continue
		}
		cut := min(contentIndent, len(raw))
		rows = append(rows, raw[cut:])
	}
	trailing := len(rows) > 0
	clip := trailing && (end < len(p.lines) || len(p.lines) > 0 && p.lines[len(p.lines)-1].raw == "")
	raw := buildBlockScalar(rows, folded, end < len(p.lines), chomp, clip)
	return raw, end, nil
}

func buildBlockScalar(rows []string, folded, appendLine bool, chomp byte, clip bool) string {
	capacity := 1
	for _, row := range rows {
		capacity += len(row) + 1
	}
	out := make([]byte, 0, capacity)
	if folded {
		for i := 0; i < len(rows); {
			if rows[i] == "" {
				j := i
				for j < len(rows) && rows[j] == "" {
					j++
				}
				for range j - i {
					out = append(out, '\n')
				}
				i = j
				continue
			}
			out = append(out, rows[i]...)
			if i+1 < len(rows) && rows[i+1] != "" {
				if hasLeadingSpace(rows[i]) || hasLeadingSpace(rows[i+1]) {
					out = append(out, '\n')
				} else {
					out = append(out, ' ')
				}
			}
			i++
		}
	} else {
		for i, row := range rows {
			if i > 0 {
				out = append(out, '\n')
			}
			out = append(out, row...)
		}
	}
	if appendLine {
		out = append(out, '\n')
	}
	if chomp != '+' {
		for len(out) > 0 && out[len(out)-1] == '\n' {
			out = out[:len(out)-1]
		}
		if chomp == 0 && clip {
			out = append(out, '\n')
		}
	}
	if len(out) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(out), len(out))
}

func (p *blockParser) parseKey(text string, line int) (any, error) {
	var v any
	var err error
	if len(text) > 0 && (text[0] == '\'' || text[0] == '"' || text[0] == '!' || text[0] == '[' || text[0] == '{') {
		v, err = ParseInlineWithReferences(text, p.flags, p.refs)
	} else {
		v, err = parseArenaScalar(text, p.flags, p.arena)
	}
	if err != nil {
		if pe, ok := err.(*ParseError); ok {
			pe.Line = line
		}
		return nil, err
	}
	if err := validImplicitKey(v); err != nil {
		msg := err.Error()
		if _, ok := v.(float64); ok {
			msg = "Numeric keys are not supported. Quote the key to use it as a string"
		}
		if _, ok := v.(bool); ok {
			msg = "Non-string keys are not supported. Quote the key to use it as a string"
		}
		return nil, parseError(msg, line, text)
	}
	return v, nil
}

func (p *blockParser) skip(i int) int {
	for i < len(p.lines) {
		s := strings.TrimSpace(p.lines[i].text)
		if s != "" && !strings.HasPrefix(s, "#") {
			break
		}
		i++
	}
	return i
}

func (p *blockParser) circular(name string, line int) error {
	chain := append(append([]string(nil), p.active...), name)
	return parseError("Circular reference detected: "+strings.Join(chain, " -> "), line, "")
}

func findMappingColon(s string) int {
	quote := byte(0)
	square, curly := 0, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
				continue
			}
			if c == quote {
				if quote == '\'' && i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '[':
			square++
		case ']':
			square--
		case '{':
			curly++
		case '}':
			curly--
		case ':':
			if square == 0 && curly == 0 && (i+1 == len(s) || isSpace(s[i+1]) || strings.ContainsRune("[{", rune(s[i+1]))) {
				return i
			}
		case '#':
			if square == 0 && curly == 0 && (i == 0 || isSpace(s[i-1])) {
				return -1
			}
		}
	}
	return -1
}

func stripComment(s string) string {
	quote := byte(0)
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
				continue
			}
			if c == quote {
				if quote == '\'' && i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case '#':
			if depth == 0 && (i == 0 || isSpace(s[i-1])) {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return trimYAML(s)
}

func flowComplete(s string) (bool, bool) {
	quote := byte(0)
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
				continue
			}
			if c == quote {
				if quote == '\'' && i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		}
	}
	return depth == 0, quote != 0
}

func splitPrefix(s string) (string, string) {
	i := 0
	for i < len(s) && !isSpace(s[i]) && !strings.ContainsRune("[]{},", rune(s[i])) {
		i++
	}
	return s[:i], strings.TrimSpace(s[i:])
}
func isBlockIndicator(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && (s[0] == '|' || s[0] == '>')
}
func isSeqLine(l sourceLine, indent int) bool {
	if l.indent != indent || len(l.text) == 0 || l.text[0] != '-' {
		return false
	}
	return len(l.text) == 1 || isSpace(l.text[1])
}
func isSeqText(s string) bool { return len(s) > 0 && s[0] == '-' && (len(s) == 1 || isSpace(s[1])) }
func mappingIndex(m Mapping, key any) int {
	for i := range m {
		if scalarEqual(m[i].Key, key) {
			return i
		}
	}
	return -1
}
func hasLeadingSpace(s string) bool { return len(s) > 0 && (s[0] == ' ' || s[0] == '\t') }
func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
func isTaggedCollection(v any) bool {
	if t, ok := v.(TaggedValue); ok {
		return isCollection(t.Value)
	}
	return false
}
func trimYAML(s string) string { return strings.Trim(s, " \t\r\n") }
