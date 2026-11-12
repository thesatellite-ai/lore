// Package mdwrap finds hard-wrapped Markdown: a paragraph, bullet or table
// cell continued on the next physical line. Every Markdown file lore
// writes (a store's README.md, ~/.lore/README.md, the blocks it puts into
// CLAUDE.md / AGENTS.md, .lore/LORE.md, generated docs tables) keeps one
// physical line per paragraph, bullet or cell, so a person's editor wraps
// it to their window and a diff of one sentence is one line. Tests of each
// writer call Check on the exact text it writes; nothing else imports it.
//
//	Check(text) ─▶ for each line, outside front matter and code fences,
//	               with blockquote markers (">") stripped:
//	                 a non-blank line right after a non-blank prose line
//	                 (same quote depth) that does not start a block of its
//	                 own (bullet, numbered item, heading, table row, fence,
//	                 HTML comment, rule) ─▶ one Problem
package mdwrap

import (
	"regexp"
	"strconv"
	"strings"
)

// Problem is one continued line: its 1-based line number and text.
type Problem struct {
	Line int
	Text string
}

// String is "line N: <text>".
func (p Problem) String() string { return "line " + strconv.Itoa(p.Line) + ": " + p.Text }

// blockStart is a line that starts a block of its own after any indent:
// a bullet, a numbered item, a heading, a table row, a fence, an HTML
// comment or a horizontal rule.
var blockStart = regexp.MustCompile("^\\s*(?:[-*+]\\s|[0-9]+[.)]\\s|#{1,6}(?:\\s|$)|\\||```|~~~|<!--|-->|---+\\s*$|\\*\\*\\*+\\s*$)")

// notProse is a line after which a next line may follow directly: a
// heading, a table row, an HTML comment line, a rule.
var notProse = regexp.MustCompile(`^\s*(?:#{1,6}(?:\s|$)|\||<!--|-->|---+\s*$|\*\*\*+\s*$)|-->\s*$`)

// fence opens or closes a code block: a run of 3+ backticks or tildes
// (CommonMark: a block closes only on a run of the same character at
// least as long, with nothing after it, so ```` can hold ``` lines).
var fence = regexp.MustCompile("^\\s*(`{3,}|~{3,})(.*)$")

// stripQuote removes blockquote markers and returns the depth.
func stripQuote(l string) (string, int) {
	depth := 0
	for {
		t := strings.TrimLeft(l, " ")
		if !strings.HasPrefix(t, ">") {
			return l, depth
		}
		depth++
		l = strings.TrimPrefix(strings.TrimPrefix(t, ">"), " ")
	}
}

// Check returns every line of text that continues the paragraph, bullet
// or cell of the line before it.
func Check(text string) []Problem {
	lines := strings.Split(text, "\n")
	start := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" { // YAML front matter
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				start = i + 1
				break
			}
		}
	}
	var out []Problem
	open := "" // the fence of the code block we are in ("" = none)
	prevProse, prevDepth := false, 0
	for i := start; i < len(lines); i++ {
		body, depth := stripQuote(lines[i])
		if f := fence.FindStringSubmatch(body); f != nil {
			switch {
			case open == "":
				open = f[1]
			case f[1][0] == open[0] && len(f[1]) >= len(open) && strings.TrimSpace(f[2]) == "":
				open = ""
			}
			prevProse = false
			continue
		}
		if open != "" {
			continue
		}
		if strings.TrimSpace(body) == "" {
			prevProse = false
			continue
		}
		if prevProse && depth == prevDepth && !blockStart.MatchString(body) {
			out = append(out, Problem{Line: i + 1, Text: lines[i]})
		}
		prevProse, prevDepth = !notProse.MatchString(body), depth
	}
	return out
}

// Lines renders problems one per line, for a test failure.
func Lines(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.String() + "\n")
	}
	return b.String()
}
