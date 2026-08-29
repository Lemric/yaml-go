package yaml

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type LintFormat string

const (
	LintFormatText   LintFormat = "text"
	LintFormatJSON   LintFormat = "json"
	LintFormatGitHub LintFormat = "github"
	LintFormatGitLab LintFormat = "gitlab"
)

type LintOptions struct {
	Verbose         bool
	Format          LintFormat
	ParseCustomTags bool
	Exclude         []string
}

type LintResult struct {
	ExitCode int
	Output   string
}

func SupportedLintFormats() []LintFormat {
	return []LintFormat{LintFormatText, LintFormatJSON, LintFormatGitHub, LintFormatGitLab}
}

type lintIssue struct {
	Path, Message string
	Line          int
}

func Lint(files []string, options LintOptions) (LintResult, error) {
	format := options.Format
	if format == "" {
		if os.Getenv("GITHUB_ACTIONS") != "" {
			format = LintFormatGitHub
		} else {
			format = LintFormatText
		}
	}
	excluded := make(map[string]bool, len(options.Exclude))
	for _, p := range options.Exclude {
		excluded[p] = true
	}
	issues := make([]lintIssue, 0)
	valid := make([]string, 0)
	flags := Flags(0)
	if options.ParseCustomTags {
		flags |= ParseCustomTags
	}
	for _, path := range files {
		if excluded[path] {
			continue
		}
		_, err := ParseFile(path, flags)
		if err == nil {
			valid = append(valid, path)
			continue
		}
		var pe *ParseError
		if e, ok := err.(*ParseError); ok {
			pe = e
		} else {
			return LintResult{}, err
		}
		message := "Unable to parse"
		message += fmt.Sprintf(" at line %d", pe.Line)
		if pe.Snippet != "" {
			message += fmt.Sprintf(" (near %q)", pe.Snippet)
		}
		issues = append(issues, lintIssue{Path: path, Message: message, Line: pe.Line})
	}
	result := LintResult{}
	if len(issues) > 0 {
		result.ExitCode = 1
	}
	switch format {
	case LintFormatGitHub:
		var b strings.Builder
		for _, x := range issues {
			_, _ = fmt.Fprintf(&b, "::error file=%s,line=%d,col=0::%s\n", x.Path, x.Line, x.Message)
		}
		result.Output = b.String()
	case LintFormatGitLab:
		type lines struct {
			Begin int `json:"begin"`
		}
		type location struct {
			Path  string `json:"path"`
			Lines lines  `json:"lines"`
		}
		type item struct {
			CheckName   string   `json:"check_name"`
			Severity    string   `json:"severity"`
			Description string   `json:"description"`
			Fingerprint string   `json:"fingerprint"`
			Location    location `json:"location"`
		}
		out := make([]item, 0, len(issues))
		for _, x := range issues {
			sum := sha256.Sum256([]byte(x.Path + ":" + fmt.Sprint(x.Line) + ":" + x.Message))
			out = append(out, item{"yaml-lint", "major", x.Message, hex.EncodeToString(sum[:]), location{x.Path, lines{x.Line}}})
		}
		raw, _ := json.Marshal(out)
		result.Output = string(raw) + "\n"
	case LintFormatJSON:
		raw, _ := json.Marshal(issues)
		result.Output = string(raw) + "\n"
	default:
		var b strings.Builder
		for _, x := range issues {
			_, _ = fmt.Fprintf(&b, "ERROR in %s: %s\n", x.Path, x.Message)
		}
		if options.Verbose {
			for _, p := range valid {
				_, _ = fmt.Fprintf(&b, "OK in %s\n", p)
			}
			if len(issues) == 0 {
				_, _ = fmt.Fprintf(&b, "All %d YAML files contain valid syntax\n", len(valid))
			}
		}
		result.Output = b.String()
	}
	return result, nil
}
