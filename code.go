package main

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const codeDetectLimit = 16 * 1024 // bytes inspected, even for a very large clip

// Strong syntax cues take precedence over ambiguous expressions. A bare
// word, number or assignment remains ordinary text.
var codeRules = []struct {
	language string
	pattern  *regexp.Regexp
}{
	{"PHP", regexp.MustCompile(`<\?php\b`)},
	{"Rust", regexp.MustCompile(`(?m)^\s*(pub\s+)?(async\s+)?fn\s+\w+\s*\(|^\s*(use\s+\w+::|impl\s+\w+|let\s+mut\s+\w+\s*=)`)},
	{"Swift", regexp.MustCompile(`(?m)^\s*(import\s+(SwiftUI|UIKit|Foundation)\b|func\s+\w+\s*\([^\n]*\)\s*->)`)},
	{"Go", regexp.MustCompile(`(?m)^\s*(package\s+\w+\s*$|func\s+(\([^\n)]*\)\s*)?\w+\s*\(|type\s+\w+\s+(struct|interface)\s*\{|\w+(\s*,\s*\w+)*\s*:=\s*\S)`)},
	{"Python", regexp.MustCompile(`(?m)^\s*((async\s+)?def\s+\w+\s*\([^\n]*\)\s*(->[^\n:]+)?:|class\s+\w+(\([^\n]*\))?\s*:|from\s+[\w.]+\s+import\s+|import\s+[\w.]+\s*$|for\s+\w+\s+in\s+[^\n]+:\s*$)`)},
	{"C#", regexp.MustCompile(`(?m)^\s*(using\s+(System|Microsoft)[\w.]*\s*;|namespace\s+[\w.]+\s*[;{])|\bConsole\.(WriteLine|Write)\s*\(`)},
	{"Java", regexp.MustCompile(`(?m)^\s*(import\s+java[\w.*]+\s*;|public\s+(static\s+)?(class|void|interface)\s+\w+)|\bSystem\.out\.\w+\s*\(`)},
	{"Kotlin", regexp.MustCompile(`(?m)^\s*((suspend\s+)?fun\s+\w+\s*\(|(val|var)\s+\w+\s*:\s*\w+\s*=)`)},
	{"TypeScript", regexp.MustCompile(`(?m)^\s*(export\s+)?(interface\s+\w+\s*(extends[^\n{]+)?\{|type\s+\w+\s*=|(const|let)\s+\w+\s*:\s*[\w<\[\]| >]+\s*=)`)},
	{"JavaScript", regexp.MustCompile(`(?m)^\s*(export\s+(default\s+)?)?((async\s+)?function\s*\w*\s*\(|(const|let|var)\s+[\w$]+\s*=\s*\S|import\s+[^\n]+\s+from\s+["'])|=>\s*\{|\b(console\.\w+|document\.\w+)\s*\(`)},
	{"C++", regexp.MustCompile(`(?m)^\s*(#include\s*[<"]|template\s*<|using\s+namespace\s+)|\bstd::\w+`)},
	{"C", regexp.MustCompile(`(?m)^\s*((int|void|char|float|double)\s+\**\w+\s*\([^\n;]*\)\s*\{|typedef\s+struct\b)`)},
	{"SQL", regexp.MustCompile(`(?is)^\s*(select\s+.+\s+from\s+\S|insert\s+into\s+\S|update\s+\S+\s+set\s+|delete\s+from\s+\S|create\s+(table|index|view)\s+\S|alter\s+table\s+\S|with\s+\w+\s+as\s*\()`)},
	{"Shell", regexp.MustCompile(`(?m)^#![^\n]*(/|\b)(ba|z|fi|k)?sh\b|^\s*(if\s+\[.+\]\s*;\s*then|for\s+\w+\s+in\s+[^\n]+;\s*do|export\s+\w+=\S)`)},
	{"XML", regexp.MustCompile(`(?i)^\s*<\?xml\b|^\s*<svg\b`)},
	{"HTML", regexp.MustCompile(`(?is)^\s*<!doctype\s+html\b|<([a-z][\w:-]*)\b[^>]*>[^<]*</[a-z][\w:-]*\s*>|^\s*<(html|head|body|div|span|script|style|input|button)\b[^>]*>`)},
	{"CSS", regexp.MustCompile(`(?s)^\s*[.#@]?[a-zA-Z][\w\s.#,:>+\[\]="'-]*\{\s*[a-zA-Z-]+\s*:\s*[^{};]+[;}]`)},
	{"JSON", regexp.MustCompile(`^\s*\{\s*"[^"\n]+"\s*:`)},
	{"", regexp.MustCompile(`(?m)^\s*(if|for|while|switch)\s*\([^\n]+\)\s*\{|^\s*[\w$.]+\s*\([^\n]*\)\s*;\s*$`)},
}

// detectCode is bounded and local. Fenced snippets may name any language
// supported by MyGo; unfenced snippets need a recognizable syntax cue.
func detectCode(text string) (language, body string, ok bool) {
	if len(text) > codeDetectLimit {
		end := codeDetectLimit
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end]
	}
	body = strings.Trim(text, "\r\n")
	s := strings.TrimSpace(body)
	if strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~") {
		if header, rest, found := strings.Cut(s, "\n"); found {
			marker := s[:3]
			hint := strings.TrimSpace(header[3:])
			if lang := editor.LanguageNamed(hint); lang != nil {
				language = lang.Name
			}
			body = strings.TrimRight(rest, "\r\n")
			if strings.TrimSpace(body) == marker {
				return "", "", false
			}
			if end := strings.LastIndex(body, "\n"); end >= 0 && strings.TrimSpace(body[end+1:]) == marker {
				body = strings.TrimRight(body[:end], "\r\n")
			}
			if language == "" {
				language, _ = inferCodeLanguage(strings.TrimSpace(body))
			}
			return language, body, strings.TrimSpace(body) != ""
		}
	}
	language, ok = inferCodeLanguage(s)
	return language, body, ok
}

func inferCodeLanguage(s string) (string, bool) {
	if len(s) >= 2 && (s[0] == '{' || s[0] == '[') && json.Valid([]byte(s)) {
		return "JSON", true
	}
	for _, rule := range codeRules {
		if rule.pattern.MatchString(s) {
			return rule.language, true
		}
	}
	return "", false
}

type codePreview struct {
	language string
	spans    []ui.Span
	kinds    []editor.TokenKind
	styled   bool
	dark     bool
}

var (
	lightCodeTheme = editor.DefaultTheme(lightTheme)
	darkCodeTheme  = editor.DefaultTheme(darkTheme)
)

// Only cards actually built create these small previews. They belong to the
// current clip list and are released with it when the panel is hidden.
func (c *Clip) codePreview() *codePreview {
	if c.code != nil {
		return c.code
	}
	language, body, _ := detectCode(c.Text)
	c.code = highlightCode(language, body, 8)
	return c.code
}

// highlightCode tokenizes at most maxLines lines of body as language.
func highlightCode(language, body string, maxLines int) *codePreview {
	preview := &codePreview{language: language}
	lang := editor.LanguageNamed(language)
	if lang == nil || lang.Tokenizer == nil {
		// A code fence without a known language still gets basic highlighting,
		// without claiming it is JavaScript in the card's language label.
		lang = editor.LanguageNamed("JavaScript")
	}
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\t", "    ")
	var state uint64
	var tokens []editor.Token
	for i, line := range strings.SplitN(body, "\n", maxLines+1) {
		if i == maxLines {
			break
		}
		if i > 0 {
			preview.spans = append(preview.spans, ui.Span{Text: "\n"})
			preview.kinds = append(preview.kinds, editor.TokenText)
		}
		tokens, state = lang.Tokenizer.Tokenize(line, state, tokens[:0])
		start := 0
		for _, token := range tokens {
			preview.spans = append(preview.spans, ui.Span{Text: line[start:token.End]})
			preview.kinds = append(preview.kinds, token.Kind)
			start = token.End
		}
		if start < len(line) {
			preview.spans = append(preview.spans, ui.Span{Text: line[start:]})
			preview.kinds = append(preview.kinds, editor.TokenText)
		}
	}
	return preview
}

func (p *codePreview) styledSpans(t *ui.Theme) []ui.Span {
	if p.styled && p.dark == t.Dark {
		return p.spans
	}
	if p.styled {
		p.spans = slices.Clone(p.spans) // Earlier frame snapshots keep their colors.
	}
	colors := lightCodeTheme
	if t.Dark {
		colors = darkCodeTheme
	}
	for i, kind := range p.kinds {
		style := colors.Tokens[kind]
		color := style.Color
		if color == (ui.Color{}) {
			color = colors.Text
		}
		p.spans[i].Color, p.spans[i].Italic = color, style.Italic
		if style.Bold {
			p.spans[i].Weight = 600
		}
	}
	p.dark, p.styled = t.Dark, true
	return p.spans
}
