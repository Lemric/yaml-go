package yaml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTreatsFilenameAsContent(t *testing.T) {
	path := filepath.Join("testdata", "fixtures", "index.yaml")
	assertValue(t, parseOK(t, path, 0), path)
}

func TestParseFile(t *testing.T) {
	path := filepath.Join("testdata", "fixtures", "index.yaml")
	got, err := ParseFile(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := sequence("escaped-characters", "comments", "compact", "core", "comment-scalars", "merge-keys", "quoted-keys", "anchors", "basic", "block-mappings", "documents", "invalid", "flow-collections", "folded-scalars", "nulls-and-empty", "specification-examples", "scalar-types", "unindented-collections")
	assertValue(t, got, want)
}

func TestParseFileErrors(t *testing.T) {
	_, err := ParseFile(filepath.Join("testdata", "fixtures", "nonexistent.yaml"), 0)
	requireError(t, err, "does not exist")

	dir := t.TempDir()
	_, err = ParseFile(dir, 0)
	requireError(t, err, "cannot be read")
}

func writeLintFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLintValidFiles(t *testing.T) {
	first := writeLintFile(t, "foo: bar")
	second := filepath.Join(filepath.Dir(first), "second.yaml")
	if err := os.WriteFile(second, []byte("bar: baz"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Lint([]string{first, second}, LintOptions{Verbose: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d; output: %s", result.ExitCode, result.Output)
	}
	if !strings.Contains(result.Output, "OK in ") {
		t.Fatalf("verbose success output = %q", result.Output)
	}
}

func TestLintInvalidFile(t *testing.T) {
	path := writeLintFile(t, "\nfoo:\nbar")
	result, err := Lint([]string{path}, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("exit code = %d; want 1", result.ExitCode)
	}
	if !strings.Contains(result.Output, `Unable to parse at line 3 (near "bar")`) {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestLintGitHubFormat(t *testing.T) {
	path := writeLintFile(t, "foo:\nbar")
	result, err := Lint([]string{path}, LintOptions{Format: LintFormatGitHub})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("exit code = %d", result.ExitCode)
	}
	want := "::error file=" + path + ",line=2,col=0::Unable to parse at line 2"
	if !strings.Contains(result.Output, want) {
		t.Fatalf("output %q does not contain %q", result.Output, want)
	}
}

func TestLintGitLabFormat(t *testing.T) {
	path := writeLintFile(t, "foo:\nbar")
	result, err := Lint([]string{path}, LintOptions{Format: LintFormatGitLab})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("exit code = %d", result.ExitCode)
	}
	var report []struct {
		CheckName   string `json:"check_name"`
		Severity    string `json:"severity"`
		Description string `json:"description"`
		Fingerprint string `json:"fingerprint"`
		Location    struct {
			Path  string `json:"path"`
			Lines struct {
				Begin int `json:"begin"`
			} `json:"lines"`
		} `json:"location"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Output)), &report); err != nil {
		t.Fatalf("GitLab output is not JSON: %v\n%s", err, result.Output)
	}
	if len(report) != 1 || report[0].CheckName != "yaml-lint" || report[0].Severity != "major" || report[0].Location.Path != path || report[0].Location.Lines.Begin != 2 || report[0].Fingerprint == "" {
		t.Fatalf("unexpected report: %#v", report)
	}
	if !strings.Contains(report[0].Description, "Unable to parse at line 2") {
		t.Fatalf("description = %q", report[0].Description)
	}
}

func TestLintGitLabValidOutputIsEmptyArray(t *testing.T) {
	path := writeLintFile(t, "foo: bar")
	result, err := Lint([]string{path}, LintOptions{Format: LintFormatGitLab})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Output) != "[]" {
		t.Fatalf("result = %#v", result)
	}
}

func TestLintAutoDetectsGitHubActions(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "1")
	path := writeLintFile(t, "foo:\nbar")
	result, err := Lint([]string{path}, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "::error file=") {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestLintCustomTags(t *testing.T) {
	path := writeLintFile(t, "foo: !my_tag {foo: bar}")
	withoutTags, err := Lint([]string{path}, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if withoutTags.ExitCode != 1 {
		t.Fatalf("custom tags unexpectedly accepted without option")
	}
	withTags, err := Lint([]string{path}, LintOptions{ParseCustomTags: true})
	if err != nil {
		t.Fatal(err)
	}
	if withTags.ExitCode != 0 {
		t.Fatalf("custom tags rejected with option: %s", withTags.Output)
	}
}

func TestLintExclude(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.yaml")
	second := filepath.Join(dir, "second.yaml")
	if err := os.WriteFile(first, []byte("invalid:\nnext"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("bar: baz"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Lint([]string{first, second}, LintOptions{Exclude: []string{first}, Verbose: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Output, "All 1 YAML files contain valid syntax") {
		t.Fatalf("result = %#v", result)
	}
}

func TestLintMissingFileReturnsOperationalError(t *testing.T) {
	_, err := Lint([]string{filepath.Join(t.TempDir(), "gone.yaml")}, LintOptions{})
	requireError(t, err, "does not exist")
}

func TestLintFormats(t *testing.T) {
	want := []LintFormat{LintFormatText, LintFormatJSON, LintFormatGitHub, LintFormatGitLab}
	if got := SupportedLintFormats(); !equalLintFormats(got, want) {
		t.Fatalf("formats = %#v; want %#v", got, want)
	}
}

func equalLintFormats(a, b []LintFormat) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
