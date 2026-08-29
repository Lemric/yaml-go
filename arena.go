package yaml

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// A parse arena keeps all slices and scalar interface boxes for ordinary
// documents in one independently owned heap object. Oversized inputs fall back
// to regular growing slices without changing the public representation.
type valueArena struct {
	pairs      [64]Pair
	values     [64]any
	boxes      [96]scalarBox
	pairBuf    []Pair
	valueBuf   []any
	boxBuf     []scalarBox
	lineBuf    []sourceLine
	refBuf     []arenaReference
	activeBuf  []string
	slotBuf    []mappingSlot
	pairAt     int
	valueAt    int
	boxAt      int
	slotAt     int
	byteAt     int
	lines      [64]sourceLine
	byteValues [16][]byte
	block      blockParser
	flow       flowParser
	refs       [32]arenaReference
	refAt      int
	active     [32]string
}

type valueArena1K struct {
	arena  valueArena
	pairs  [1536]Pair
	values [768]any
	boxes  [3200]scalarBox
	lines  [1026]sourceLine
	refs   [1024]arenaReference
	active [128]string
	slots  [2048]mappingSlot
}

type valueArena5K struct {
	arena  valueArena
	pairs  [7168]Pair
	values [3072]any
	boxes  [16_000]scalarBox
	lines  [5002]sourceLine
	refs   [5000]arenaReference
	active [128]string
	slots  [16_384]mappingSlot
}

type valueArena10K struct {
	arena  valueArena
	pairs  [14_336]Pair
	values [6144]any
	boxes  [32_000]scalarBox
	lines  [10_002]sourceLine
	refs   [10_000]arenaReference
	active [128]string
	slots  [32_768]mappingSlot
}

type arenaReference struct {
	name  string
	value any
}

type mappingSlot struct {
	hash     uint64
	index    int32
	explicit bool
}

type mappingLookup struct{ slots []mappingSlot }

func (l mappingLookup) available() bool { return len(l.slots) != 0 }

func (l mappingLookup) find(mapping Mapping, key string) (int, bool, bool) {
	if len(l.slots) == 0 {
		return 0, false, false
	}
	hash := stringHash(key)
	mask := len(l.slots) - 1
	for probe := 0; probe < len(l.slots); probe++ {
		slot := &l.slots[(int(hash)+probe)&mask]
		if slot.hash == 0 {
			return 0, false, false
		}
		if slot.hash == hash {
			index := int(slot.index)
			if existing, ok := mapping[index].Key.(string); ok && existing == key {
				return index, slot.explicit, true
			}
		}
	}
	return 0, false, false
}

func (l mappingLookup) set(mapping Mapping, key string, index int, explicit bool) {
	if len(l.slots) == 0 {
		return
	}
	hash := stringHash(key)
	mask := len(l.slots) - 1
	for probe := 0; probe < len(l.slots); probe++ {
		slot := &l.slots[(int(hash)+probe)&mask]
		if slot.hash == 0 {
			slot.hash = hash
			slot.index = int32(index)
			slot.explicit = explicit
			return
		}
		if slot.hash == hash {
			existing, ok := mapping[slot.index].Key.(string)
			if ok && existing == key {
				slot.index = int32(index)
				slot.explicit = explicit
				return
			}
		}
	}
}

func (l mappingLookup) indexMerged(mapping Mapping) {
	if len(l.slots) == 0 {
		return
	}
	for i, pair := range mapping {
		if key, ok := pair.Key.(string); ok {
			if _, _, found := l.find(mapping, key); !found {
				l.set(mapping, key, i, false)
			}
		}
	}
}

func stringHash(value string) uint64 {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	hash := offset
	for i := 0; i < len(value); i++ {
		hash ^= uint64(value[i])
		hash *= prime
	}
	return hash | 1
}

type scalarBox struct {
	s   string
	i   int
	i64 int64
	f   float64
	b   bool
	t   time.Time
	m   Mapping
	a   []any
}

type emptyInterface struct{ typ, data unsafe.Pointer }

var (
	stringInterfaceType   = interfaceType("")
	intInterfaceType      = interfaceType(0)
	int64InterfaceType    = interfaceType(int64(0))
	floatInterfaceType    = interfaceType(float64(0))
	boolInterfaceType     = interfaceType(false)
	timeInterfaceType     = interfaceType(time.Time{})
	mappingInterfaceType  = interfaceType(Mapping(nil))
	sequenceInterfaceType = interfaceType([]any(nil))
	bytesInterfaceType    = interfaceType([]byte(nil))
)

func newDocumentArena(input string) *valueArena {
	return newValueArena(strings.Count(input, "\n") + 1)
}

func newFlowArena(input string) *valueArena {
	return newValueArena(len(input)/8 + 1)
}

func newValueArena(size int) *valueArena {
	switch {
	case size <= len(valueArena{}.lines):
		a := new(valueArena)
		return a.use(a.pairs[:], a.values[:], a.boxes[:], a.lines[:], a.refs[:], a.active[:], nil)
	case size <= 1025:
		a := new(valueArena1K)
		return a.arena.use(a.pairs[:], a.values[:], a.boxes[:], a.lines[:], a.refs[:], a.active[:], a.slots[:])
	case size <= 5001:
		a := new(valueArena5K)
		return a.arena.use(a.pairs[:], a.values[:], a.boxes[:], a.lines[:], a.refs[:], a.active[:], a.slots[:])
	case size <= 10_001:
		a := new(valueArena10K)
		return a.arena.use(a.pairs[:], a.values[:], a.boxes[:], a.lines[:], a.refs[:], a.active[:], a.slots[:])
	default:
		return newLargeValueArena(size)
	}
}

func newLargeValueArena(size int) *valueArena {
	a := new(valueArena)
	pairs := make([]Pair, scaledArenaCapacity(size, len(valueArena10K{}.pairs)))
	values := make([]any, scaledArenaCapacity(size, len(valueArena10K{}.values)))
	boxes := make([]scalarBox, scaledArenaCapacity(size, len(valueArena10K{}.boxes)))
	lines := make([]sourceLine, size+1)
	refs := make([]arenaReference, size)
	active := make([]string, len(valueArena10K{}.active))
	slots := make([]mappingSlot, largeArenaSlotCapacity(size))
	return a.use(pairs, values, boxes, lines, refs, active, slots)
}

func scaledArenaCapacity(size, capacityAt10K int) int {
	const baseline = 10_000
	whole := size / baseline
	remainder := size % baseline
	return whole*capacityAt10K + (remainder*capacityAt10K+baseline-1)/baseline
}

func largeArenaSlotCapacity(size int) int {
	capacity := 128
	for capacity < size*2 {
		capacity <<= 1
	}
	return capacity
}

func (a *valueArena) use(pairs []Pair, values []any, boxes []scalarBox, lines []sourceLine, refs []arenaReference, active []string, slots []mappingSlot) *valueArena {
	a.pairBuf = pairs
	a.valueBuf = values
	a.boxBuf = boxes
	a.lineBuf = lines
	a.refBuf = refs
	a.activeBuf = active
	a.slotBuf = slots
	return a
}

func (a *valueArena) lookup(capacity int) mappingLookup {
	if capacity <= 64 {
		return mappingLookup{}
	}
	size := 128
	for size < capacity*2 {
		size <<= 1
	}
	if a == nil || a.slotAt+size > len(a.slotBuf) {
		return mappingLookup{}
	}
	start := a.slotAt
	a.slotAt += size
	return mappingLookup{slots: a.slotBuf[start : start+size]}
}

func interfaceType(v any) unsafe.Pointer { return (*emptyInterface)(unsafe.Pointer(&v)).typ }
func boxed(typ, data unsafe.Pointer) any {
	return *(*any)(unsafe.Pointer(&emptyInterface{typ: typ, data: data}))
}

func (a *valueArena) mapping(capacity int) Mapping {
	if capacity < 0 {
		capacity = 0
	}
	if a != nil && a.pairAt+capacity <= len(a.pairBuf) {
		start := a.pairAt
		a.pairAt += capacity
		return a.pairBuf[start : start : start+capacity]
	}
	return make(Mapping, 0, capacity)
}
func (a *valueArena) sequence(capacity int) []any {
	if capacity < 0 {
		capacity = 0
	}
	if a != nil && a.valueAt+capacity <= len(a.valueBuf) {
		start := a.valueAt
		a.valueAt += capacity
		return a.valueBuf[start : start : start+capacity]
	}
	return make([]any, 0, capacity)
}
func (a *valueArena) nextBox() *scalarBox {
	if a == nil || a.boxAt >= len(a.boxBuf) {
		return nil
	}
	b := &a.boxBuf[a.boxAt]
	a.boxAt++
	return b
}
func (a *valueArena) stringValue(v string) any {
	if b := a.nextBox(); b != nil {
		b.s = v
		return boxed(stringInterfaceType, unsafe.Pointer(&b.s))
	}
	return v
}
func (a *valueArena) intValue(v int) any {
	if b := a.nextBox(); b != nil {
		b.i = v
		return boxed(intInterfaceType, unsafe.Pointer(&b.i))
	}
	return v
}
func (a *valueArena) int64Value(v int64) any {
	if b := a.nextBox(); b != nil {
		b.i64 = v
		return boxed(int64InterfaceType, unsafe.Pointer(&b.i64))
	}
	return v
}
func (a *valueArena) floatValue(v float64) any {
	if b := a.nextBox(); b != nil {
		b.f = v
		return boxed(floatInterfaceType, unsafe.Pointer(&b.f))
	}
	return v
}
func (a *valueArena) boolValue(v bool) any {
	if b := a.nextBox(); b != nil {
		b.b = v
		return boxed(boolInterfaceType, unsafe.Pointer(&b.b))
	}
	return v
}
func (a *valueArena) timeValue(v time.Time) any {
	if b := a.nextBox(); b != nil {
		b.t = v
		return boxed(timeInterfaceType, unsafe.Pointer(&b.t))
	}
	return v
}
func (a *valueArena) mappingValue(v Mapping) any {
	if b := a.nextBox(); b != nil {
		b.m = v
		return boxed(mappingInterfaceType, unsafe.Pointer(&b.m))
	}
	return v
}
func (a *valueArena) sequenceValue(v []any) any {
	if b := a.nextBox(); b != nil {
		b.a = v
		return boxed(sequenceInterfaceType, unsafe.Pointer(&b.a))
	}
	return v
}
func (a *valueArena) bytesValue(v []byte) any {
	if a != nil && a.byteAt < len(a.byteValues) {
		value := &a.byteValues[a.byteAt]
		a.byteAt++
		*value = v
		return boxed(bytesInterfaceType, unsafe.Pointer(value))
	}
	return v
}
func (a *valueArena) setReference(name string, value any) {
	for i := a.refAt - 1; i >= 0; i-- {
		if a.refBuf[i].name == name {
			a.refBuf[i].value = value
			return
		}
	}
	if a.refAt < len(a.refBuf) {
		a.refBuf[a.refAt] = arenaReference{name, value}
		a.refAt++
	}
}
func (a *valueArena) reference(name string) (any, bool) {
	for i := a.refAt - 1; i >= 0; i-- {
		if a.refBuf[i].name == name {
			return a.refBuf[i].value, true
		}
	}
	return nil, false
}

func parseArenaScalar(raw string, flags Flags, a *valueArena) (any, error) {
	if raw == "" {
		return a.stringValue(""), nil
	}
	switch strings.ToLower(raw) {
	case "null", "~":
		return nil, nil
	case "true":
		return a.boolValue(true), nil
	case "false":
		return a.boolValue(false), nil
	case ".nan":
		return a.floatValue(math.NaN()), nil
	case ".inf", "+.inf":
		return a.floatValue(math.Inf(1)), nil
	case "-.inf":
		return a.floatValue(math.Inf(-1)), nil
	}
	if raw[0] >= '0' && raw[0] <= '9' {
		if t, ok, err := parseTimestamp(raw); ok {
			if err != nil {
				if flags&ParseTimestamps != 0 {
					return nil, err
				}
				return a.stringValue(raw), nil
			}
			if flags&ParseTimestamps != 0 {
				return a.timeValue(t), nil
			}
			if t.Nanosecond() == 0 {
				return a.int64Value(t.Unix()), nil
			}
			return a.floatValue(float64(t.Unix()) + float64(t.Nanosecond())/1e9), nil
		}
	}
	numeric := raw[0] >= '0' && raw[0] <= '9' || raw[0] == '+' || raw[0] == '-'
	clean := raw
	if numeric && strings.ContainsRune(raw, '_') {
		clean = strings.ReplaceAll(raw, "_", "")
	}
	if numeric {
		if n, ok, overflow := parseInteger(clean); ok {
			return a.intValue(n), nil
		} else if overflow {
			return a.stringValue(raw), nil
		}
		if looksFloat(clean) {
			f, err := strconv.ParseFloat(clean, 64)
			if err == nil || strings.Contains(err.Error(), "value out of range") {
				return a.floatValue(f), nil
			}
		}
		return a.stringValue(clean), nil
	}
	return a.stringValue(raw), nil
}

func flowCollectionCapacity(src string, start int, close byte, mapping bool) int {
	depth := 0
	quote := byte(0)
	count := 0
	has := false
	for i := start; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
				continue
			}
			if c == quote {
				if quote == '\'' && i+1 < len(src) && src[i+1] == '\'' {
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
			has = true
		case ']', '}':
			if depth == 0 && c == close {
				if has {
					count++
				}
				return count
			}
		case ',':
			if depth == 0 {
				count++
				has = false
			}
		default:
			if depth == 0 && !isSpace(c) && c != '#' {
				has = true
			}
		}
	}
	if has {
		count++
	}
	if mapping && count == 0 {
		return 1
	}
	return count
}
