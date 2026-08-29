package yaml

import (
	"strconv"
	"strings"
	"testing"
)

const maxLargeNestedDocumentAllocations = 16.0

var (
	benchmarkValue any
	benchmarkText  string
)

const benchmarkDocument = `service:
  name: gateway
  enabled: true
  endpoints:
    - path: /health
      timeout: 250
    - path: /v1/items
      timeout: 1500
  metadata: { region: eu-central, replicas: 3 }
`

const benchmarkAliases = `defaults: &defaults
  timeout: 1500
  retries: 4
  headers: [accept, content-type, authorization]
read:
  <<: *defaults
  path: /v1/read
write:
  <<: *defaults
  path: /v1/write
`

func BenchmarkParseInline(b *testing.B) {
	const input = `{name: gateway, enabled: true, ports: [8080, 8443], ratio: 0.75}`
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	for range b.N {
		value, err := ParseInline(input, 0)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkValue = value
	}
}

func BenchmarkParseDocument(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchmarkDocument)))
	for range b.N {
		value, err := Parse(benchmarkDocument, 0)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkValue = value
	}
}

func BenchmarkParseAliasesAndMerge(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchmarkAliases)))
	for range b.N {
		value, err := Parse(benchmarkAliases, 0)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkValue = value
	}
}

func BenchmarkDumpDocument(b *testing.B) {
	value, err := Parse(benchmarkDocument, 0)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for range b.N {
		text, err := Dump(value, 4, 2, DumpCompactNestedMappings)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkText = text
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchmarkDocument)))
	for range b.N {
		value, err := Parse(benchmarkDocument, 0)
		if err != nil {
			b.Fatal(err)
		}
		text, err := Dump(value, 4, 2, DumpCompactNestedMappings)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkValue = value
		benchmarkText = text
	}
}

func BenchmarkParseLargeDocuments(b *testing.B) {
	benchmarkLargeDocuments(b, func(b *testing.B, document string) {
		b.SetBytes(int64(len(document)))
		for range b.N {
			value, err := Parse(document, 0)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkValue = value
		}
	})
}

func TestParseLargeNestedDocumentAllocationBudget(t *testing.T) {
	document := buildNestedBenchmarkDocument(1_000)
	var parseErr error
	allocations := testing.AllocsPerRun(5, func() {
		value, err := Parse(document, ParseRejectAliases)
		benchmarkValue = value
		parseErr = err
	})
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	t.Logf("allocations per large nested document = %.0f", allocations)
	if allocations > maxLargeNestedDocumentAllocations {
		t.Fatalf("allocations = %.0f; budget is at most %.0f", allocations, maxLargeNestedDocumentAllocations)
	}
}

func BenchmarkParseLargeNestedDocument(b *testing.B) {
	document := buildNestedBenchmarkDocument(1_000)
	b.SetBytes(int64(len(document)))
	b.ReportAllocs()
	value, err := Parse(document, ParseRejectAliases)
	if err != nil {
		b.Fatal(err)
	}
	benchmarkValue = value
	b.ResetTimer()
	for range b.N {
		value, err := Parse(document, ParseRejectAliases)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkValue = value
	}
}

func BenchmarkDumpLargeDocuments(b *testing.B) {
	benchmarkLargeDocuments(b, func(b *testing.B, document string) {
		value, err := Parse(document, 0)
		if err != nil {
			b.Fatal(err)
		}
		b.SetBytes(int64(len(document)))
		b.ResetTimer()
		for range b.N {
			text, err := Dump(value, 4, 2, DumpCompactNestedMappings)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkText = text
		}
	})
}

func BenchmarkRoundTripLargeDocuments(b *testing.B) {
	benchmarkLargeDocuments(b, func(b *testing.B, document string) {
		b.SetBytes(int64(len(document)))
		for range b.N {
			value, err := Parse(document, 0)
			if err != nil {
				b.Fatal(err)
			}
			text, err := Dump(value, 4, 2, DumpCompactNestedMappings)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkValue = value
			benchmarkText = text
		}
	})
}

func benchmarkLargeDocuments(b *testing.B, run func(*testing.B, string)) {
	b.Helper()
	for _, lines := range [...]int{1_000, 5_000, 10_000} {
		document := buildBenchmarkDocument(lines)
		b.Run(strconv.Itoa(lines)+"_lines", func(b *testing.B) {
			b.ReportAllocs()
			run(b, document)
		})
	}
}

func buildBenchmarkDocument(lines int) string {
	var out strings.Builder
	out.Grow(lines * 28)
	for line := range lines {
		out.WriteString("field_")
		out.WriteString(strconv.Itoa(line))
		out.WriteString(": ")
		switch line % 8 {
		case 0:
			out.WriteString("value_")
			out.WriteString(strconv.Itoa(line))
		case 1:
			out.WriteString(strconv.Itoa(line * 17))
		case 2:
			out.WriteString("true")
		case 3:
			out.WriteString("[alpha, beta, ")
			out.WriteString(strconv.Itoa(line))
			out.WriteByte(']')
		case 4:
			out.WriteString("{region: eu, replicas: ")
			out.WriteString(strconv.Itoa(line%9 + 1))
			out.WriteByte('}')
		case 5:
			out.WriteString("null")
		case 6:
			out.WriteString("0.75")
		case 7:
			out.WriteString("'quoted_value_")
			out.WriteString(strconv.Itoa(line))
			out.WriteByte('\'')
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func buildNestedBenchmarkDocument(entries int) string {
	var out strings.Builder
	out.Grow(entries * 360)
	out.WriteString("entries:\n")
	for index := 0; index < entries; index++ {
		out.WriteString("  entry_")
		out.WriteString(strconv.Itoa(index))
		out.WriteString(":\n")
		out.WriteString("    type: state_machine\n")
		out.WriteString("    store:\n")
		out.WriteString("      type: subject\n")
		out.WriteString("    supports: [subject]\n")
		out.WriteString("    initial: ready\n")
		out.WriteString("    states: [ready, done]\n")
		out.WriteString("    transitions:\n")
		out.WriteString("      advance:\n")
		out.WriteString("        from: ready\n")
		out.WriteString("        to: done\n")
		out.WriteString("        guard: \"subject.enabled\"\n")
		out.WriteString("      reset:\n")
		out.WriteString("        from: done\n")
		out.WriteString("        to: ready\n")
	}
	return out.String()
}
