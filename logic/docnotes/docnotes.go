// Package docnotes splits a markdown document (a TECHNICAL.md, a README)
// into the fact-like pieces `lore docs import` drafts as notes (devco
// D-940, H-189).
//
// Why it exists: a repo's own docs already say how things work and why a
// choice was made, but an agent only sees what lore holds. Drafting one
// note per paragraph or bullet, each pointing back at its doc line,
// gives the agent those facts on day one while keeping them honest: every
// draft is unverified until a person or agent checks it against the code.
//
// What one piece is:
//
//	paragraph           consecutive text lines, joined with spaces
//	bullet / numbered   one item (its indented continuation lines joined)
//	skipped             headings (they become the Section), fenced and
//	                    indented code, tables, HTML, front matter, rules,
//	                    link definitions, and pieces under MinWords words
//
// A piece is a Decision when it states a choice and its reason ("we chose
// X over Y because …"): a choice word and a reason word both appear.
// Everything else is a Fact. No model is called: the split is plain text
// rules, so the same document always gives the same pieces.
//
// Layer L1: pure, no I/O, stdlib only.
package docnotes

import (
	"regexp"
	"strings"
	"unicode"
)

// MinWords is the fewest words a piece needs to become a note: "ok" or
// "see below" says nothing on its own.
const MinWords = 5

// MaxTitleRunes bounds a decision's title (its first sentence, cut at a
// word).
const MaxTitleRunes = 80

// Kind is what a piece drafts into.
type Kind string

const (
	// Fact is how something works: a memory row.
	Fact Kind = "fact"
	// Decision is a stated choice with its reason: a decision row.
	Decision Kind = "decision"
)

// Piece is one fact-like paragraph or bullet of a document.
type Piece struct {
	Kind Kind
	// Line is the 1-based line the piece starts on.
	Line int
	// Section is the heading path above it ("Billing service > How it works").
	Section string
	// Text is the piece's text on one line, markdown kept.
	Text string
	// Title is a decision's headline (its first sentence); "" for a fact.
	Title string
	// CodeRefs are the path:line references the text names
	// ("internal/queue/worker.go:3"), in order, each once.
	CodeRefs []string
}

var (
	headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	bulletRe  = regexp.MustCompile(`^(\s*)(?:[-*+]|\d{1,9}[.)])\s+(.*)$`)
	fenceRe   = regexp.MustCompile("^\\s*(```|~~~)")
	linkDefRe = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s*\S`)
	ruleRe    = regexp.MustCompile(`^\s{0,3}([-*_])(\s*[-*_]){2,}\s*$`)
	// codeRefRe is a repo path with an extension and a line: a.go:12,
	// internal/x/y.ts:3. The path part must hold a dot-extension so a
	// clock time (10:30) or a host:port never counts.
	codeRefRe = regexp.MustCompile(`(?:^|[\s(\[` + "`" + `'"])((?:[A-Za-z0-9_.\-]+/)*[A-Za-z0-9_\-][A-Za-z0-9_.\-]*\.[A-Za-z][A-Za-z0-9]{0,7}):([1-9][0-9]{0,6})\b`)
	// choiceRe and reasonRe decide a Decision: both must appear.
	choiceRe = regexp.MustCompile(`(?i)\b(we|i|the team)\s+(chose|choose|decided|picked|pick|went with|go with|prefer|preferred|selected|opted|settled on)\b|\b(decision|decided)\s*:|\b(instead of|rather than)\b`)
	reasonRe = regexp.MustCompile(`(?i)\b(because|since|so that|due to|which means|as it|as they)\b`)
	// sentenceEnd ends a title at the first sentence.
	sentenceEnd = regexp.MustCompile(`[.!?](\s|$)`)
)

// Split returns the pieces of the markdown text src, in document order.
func Split(src string) []Piece {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var (
		out      []Piece
		sections []string
		cur      []string
		curLine  int
		curList  bool
		fence    string
		inHTML   bool
		comment  bool
	)
	flush := func() {
		if len(cur) > 0 {
			if p, ok := piece(curLine, strings.Join(sections, " > "), strings.Join(cur, " ")); ok {
				out = append(out, p)
			}
		}
		cur, curList = nil, false
	}
	start := frontMatterEnd(lines)
	for i := start; i < len(lines); i++ {
		raw := lines[i]
		t := strings.TrimSpace(raw)
		n := i + 1
		if fence != "" { // inside a fenced block: skip to its close
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if m := fenceRe.FindStringSubmatch(raw); m != nil {
			flush()
			fence = m[1]
			continue
		}
		if inHTML { // an HTML comment runs to -->, an HTML block to a blank line
			if (comment && strings.Contains(t, "-->")) || (!comment && t == "") {
				inHTML = false
			}
			continue
		}
		switch {
		case t == "":
			flush()
		case strings.HasPrefix(t, "<"):
			flush()
			comment = strings.HasPrefix(t, "<!--")
			inHTML = !comment || !strings.Contains(t, "-->")
		case headingRe.MatchString(t):
			flush()
			m := headingRe.FindStringSubmatch(t)
			level := len(m[1])
			if level <= len(sections) {
				sections = sections[:level-1]
			}
			for len(sections) < level-1 {
				sections = append(sections, "")
			}
			sections = append(sections, strings.TrimSpace(m[2]))
		case strings.HasPrefix(t, "|"), ruleRe.MatchString(t), linkDefRe.MatchString(raw):
			flush()
		case bulletRe.MatchString(raw):
			flush()
			m := bulletRe.FindStringSubmatch(raw)
			cur, curLine, curList = []string{strings.TrimSpace(m[2])}, n, true
		case len(cur) == 0 && (strings.HasPrefix(raw, "    ") || strings.HasPrefix(raw, "\t")):
			// an indented code block (not a list continuation)
		default:
			if len(cur) == 0 {
				curLine = n
			} else if curList && !startsIndented(raw) && !lazyContinuation(cur) {
				flush()
				curLine = n
			}
			cur = append(cur, strings.TrimSpace(strings.TrimPrefix(t, ">")))
		}
	}
	flush()
	return out
}

// startsIndented reports whether a line continues a list item (indented).
func startsIndented(raw string) bool {
	return strings.HasPrefix(raw, "  ") || strings.HasPrefix(raw, "\t")
}

// lazyContinuation: markdown lets a list item's text run on unindented
// while the item's last line does not end a sentence.
func lazyContinuation(cur []string) bool {
	last := strings.TrimSpace(cur[len(cur)-1])
	return last != "" && !strings.ContainsAny(last[len(last)-1:], ".!?:")
}

// frontMatterEnd is the first line after a leading YAML front matter
// block (--- … ---), 0 when there is none.
func frontMatterEnd(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return i + 1
		}
	}
	return 0
}

// piece builds one Piece; false when the text is too short to say anything.
func piece(line int, section, text string) (Piece, bool) {
	text = strings.Join(strings.Fields(text), " ")
	if words(text) < MinWords {
		return Piece{}, false
	}
	p := Piece{Kind: Fact, Line: line, Section: strings.Trim(section, " >"), Text: text, CodeRefs: codeRefs(text)}
	if choiceRe.MatchString(text) && reasonRe.MatchString(text) {
		p.Kind = Decision
		p.Title = Title(text)
	}
	return p, true
}

// Title is the first sentence of text, cut at a word to MaxTitleRunes,
// with markdown emphasis and code marks dropped.
func Title(text string) string {
	t := strings.NewReplacer("**", "", "__", "", "`", "").Replace(text)
	if loc := sentenceEnd.FindStringIndex(t); loc != nil {
		t = t[:loc[0]]
	}
	t = strings.TrimSpace(t)
	r := []rune(t)
	if len(r) <= MaxTitleRunes {
		return t
	}
	cut := string(r[:MaxTitleRunes])
	if i := strings.LastIndexFunc(cut, unicode.IsSpace); i > MaxTitleRunes/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:-") + "…"
}

// codeRefs are the path:line references in text, each once.
func codeRefs(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range codeRefRe.FindAllStringSubmatch(text, -1) {
		ref := m[1] + ":" + m[2]
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}

// words counts the words of s that hold a letter or digit.
func words(s string) int {
	n := 0
	for _, f := range strings.Fields(s) {
		if strings.IndexFunc(f, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			n++
		}
	}
	return n
}

// Norm is text folded for an exact-repeat check: lower case, letters and
// digits only, single spaces.
func Norm(text string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}
