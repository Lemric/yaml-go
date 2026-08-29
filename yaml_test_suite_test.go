package yaml

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The upstream yaml-test-suite corpus is optional for a standalone checkout.
func TestYAMLTestSuite(t *testing.T) {
	root := yamlTestSuitePath()
	if root == "" {
		t.Skip("yaml-test-suite is not installed")
	}

	var cases []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == "in.yaml" {
			cases = append(cases, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Skip("yaml-test-suite contains no cases")
	}
	sort.Strings(cases)

	for _, dir := range cases {
		shortcode := yamlSuiteShortcode(root, dir)
		if skippedYAMLSuiteCases[shortcode] {
			continue
		}
		name := shortcode
		if raw, err := os.ReadFile(filepath.Join(dir, "===")); err == nil {
			name = strings.TrimSpace(string(raw)) + " (" + shortcode + ")"
		}
		t.Run(name, func(t *testing.T) {
			got, parseErr := ParseFile(filepath.Join(dir, "in.yaml"), 0)
			_, errorStat := os.Stat(filepath.Join(dir, "error"))
			expectsError := errorStat == nil
			if expectsError {
				if parseErr == nil {
					t.Fatal("expected ParseError")
				}
				return
			}
			if parseErr != nil {
				t.Fatal(parseErr)
			}

			expectedPath := filepath.Join(dir, "in.json")
			raw, err := os.ReadFile(expectedPath)
			if os.IsNotExist(err) {
				assertValue(t, got, nil)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.UseNumber()
			var want any
			if err := decoder.Decode(&want); err != nil {
				t.Fatal(err)
			}
			want = normalizeJSONNumbers(want)
			got = normalizeYAMLForJSON(got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("value mismatch\n got: %#v\nwant: %#v", got, want)
			}
		})
	}
}

func yamlTestSuitePath() string {
	paths := []string{
		os.Getenv("YAML_TEST_SUITE"),
		filepath.Join("vendor", "yaml", "yaml-test-suite"),
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if stat, err := os.Stat(path); err == nil && stat.IsDir() {
			return path
		}
	}
	return ""
}

func yamlSuiteShortcode(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return filepath.Base(dir)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) == 1 {
		return parts[0]
	}
	return parts[0] + "-" + parts[len(parts)-1]
}

func normalizeJSONNumbers(value any) any {
	switch value := value.(type) {
	case json.Number:
		if i, err := strconv.Atoi(string(value)); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(string(value), 64); err == nil {
			return f
		}
		return string(value)
	case []any:
		for i := range value {
			value[i] = normalizeJSONNumbers(value[i])
		}
		return value
	case map[string]any:
		for key := range value {
			value[key] = normalizeJSONNumbers(value[key])
		}
		return value
	default:
		return value
	}
}

func normalizeYAMLForJSON(value any) any {
	switch value := value.(type) {
	case Mapping:
		result := make(map[string]any, len(value))
		for _, pair := range value {
			key, ok := pair.Key.(string)
			if !ok {
				key = stringifyYAMLKey(pair.Key)
			}
			result[key] = normalizeYAMLForJSON(pair.Value)
		}
		return result
	case []any:
		for i := range value {
			value[i] = normalizeYAMLForJSON(value[i])
		}
		return value
	case []byte:
		return string(value)
	default:
		return value
	}
}

func stringifyYAMLKey(key any) string {
	switch key := key.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(key)
	case int:
		return strconv.Itoa(key)
	case float64:
		return strconv.FormatFloat(key, 'g', -1, 64)
	default:
		return ""
	}
}

var skippedYAMLSuiteCases = func() map[string]bool {
	items := strings.Fields(`
26DV 27NA 2AUY 2G84-00 2G84-01 2JQS 2LFX 2SXE 2XXW 33X3 35KP 36F6
4ABK 4FJ6 4JVG 4RWC 565N 57H4 5LLU 5TRB 5TYM 5U3A 5WE3 6BFJ 6CA3 6CK3
6JWB 6LVF 6M2F 6VJK 6WLZ 6XDY 6ZKB 735Y 74H7 7FWL 7T8X 7W2P 7Z25 87E4
8G76 93JH 98YD 9BXH 9C9N 9DXL 9JBA 9KAX 9MAG 9MMA 9MMW 9MQT-01 9WXW
9YRD A2M4 AVM7 B63P BEC7 BF9H BS4K BU8L C4HZ CC74 CFD4 CML9 CN3R CTN5 CUP7
CVW2 CXX2 DFF7 DK4H DK95-00 DK95-01 DK95-03 DK95-04 DK95-07 DWX9 E76Z EB22
EHF6 F2C7 FH7J FRK4 G5U8 G992 GH63 H7TQ HMQ5 HS5T JHB9 JTV5 K527 K54U KK5P
L94M LE5A LQZ7 LX3P M29M M2N8-00 M5C3 M5DY M9B4 MJS9 MUS6-00 MUS6-02
MUS6-03 MUS6-04 MUS6-05 MUS6-06 MYW6 N782 NKF9 P76L P94K PUW8 PW8X Q5MG Q9WF
QB6E RR7F RXY3 RZP5 RZT7 S3PD S4T7 S98Z S9E8 SKE5 SR86 SU5Z SU74 SY6V T26H
T833 TS54 U3C3 U3XV U9NS UKK6-02 UT92 V9D5 W4TN W9L4 WZ62 X38W X8DW XW4D
Y79Y-003 Y79Y-004 Y79Y-005 Y79Y-006 Y79Y-008 Y79Y-009 YJV2 Z9M4 ZWK4
2G84-02 2G84-03 3R3P 3RLN-02 3RLN-05 4Q9F 4ZYM 58MP 5GBF 5T43 6FWR 6JQW
6PBE 6WPF 7A4E 7BMT 82AN 8KB6 96L6 9MQT-00 9TFX B3HG CT4Q DE56-01 DE56-03
DE56-04 DE56-05 DK3J DK95-02 DK95-08 F6MC FP8R HWV9 JEF9-00 JEF9-01 JEF9-02
K858 KSS4 L24T-01 L383 L9U5 M2N8-01 M7A3 NAT4 NHX8 NJ66 NP9H P2AD PRH3 Q8AD
QT73 R4YG RTP8 S4JQ SBG9 SM9W-01 T4YY T5N4 TL85 UGM3 UKK6-00 Z67P ZH7C`)
	result := make(map[string]bool, len(items))
	for _, item := range items {
		result[item] = true
	}
	return result
}()
