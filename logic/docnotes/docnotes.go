// Package docnotes splits a markdown document (a TECHNICAL.md, a README)
// into the self-contained pieces `lore docs import` drafts as notes
// (devco D-940, H-189; per section since D-962, H-221).
//
// Why it exists: a repo's own docs already say how things work and why a
// choice was made, but an agent only sees what lore holds. Drafting one
// note per section, each pointing back at its doc line, gives the agent
// those facts on day one while keeping them honest: every draft is
// unverified until a person or agent checks it against the code.
//
// Why per section, not per paragraph (H-221): a paragraph or a list item
// read alone says nothing about which module or step it belongs to ("Parse
// it in batches of 500."), a list cut from the sentence that introduces it
// loses its meaning, and a decision written as "Decision." / "Considered."
// / "Why." paragraphs fell apart into four memories titled "Decision",
// "Considered", "Considered", "Why". A real doc gave 986 drafts of which 75
// were kept.
//
// What one piece is:
//
//	fact        one section: its heading path as the first line, then its
//	            paragraphs and lists (nested items kept, a list stays with
//	            its lead-in); a section over MaxPieceRunes is cut at block
//	            boundaries into parts ("… (part 2)")
//	decision    a section whose paragraphs are labelled Decision. / Chosen:
//	            (with Considered. / Why. / Rationale: …) is ONE decision,
//	            titled with its heading; a paragraph elsewhere that states
//	            a choice and its reason ("we chose X over Y because …") is
//	            a decision of its own
//	skipped     boilerplate sections (contents, license, contributing, code
//	            of conduct, badges, acknowledgements) and everything under
//	            them, link-only lines and lists (a table of contents, "See
//	            [X](x).", badges), fenced and indented code, tables, HTML,
//	            front matter, rules, link definitions, and pieces under
//	            MinWords words
//
// No model is called: the split is plain text rules, so the same document
// always gives the same pieces.
//
// Layer L1: pure, no I/O, stdlib only.
package docnotes

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// MinWords is the fewest words a piece needs to become a note: "ok" or
// "see below" says nothing on its own.
const MinWords = 5

// MaxTitleRunes bounds a title cut from text (its first sentence, cut at
// a word).
const MaxTitleRunes = 80

// MaxPieceRunes bounds one fact piece: a longer section is cut into parts
// at block boundaries, so one note stays readable in a search hit.
const MaxPieceRunes = 2000

// SectionSep joins a heading path ("widget module › Overview").
const SectionSep = " › "

// Kind is what a piece drafts into.
type Kind string

const (
	// Fact is how something works: a memory row.
	Fact Kind = "fact"
	// Decision is a stated choice with its reason: a decision row.
	Decision Kind = "decision"
)

// Block is one paragraph or one whole list (nested items included) of a
// section: the unit the repeat check of `docs import` drops on its own.
type Block struct {
	// Line is the 1-based line the block starts on.
	Line int
	// Text is the block's markdown: a paragraph on one line, a list one
	// item per line with its marker and nesting kept.
	Text string
}

// Piece is one note-sized part of a document.
type Piece struct {
	Kind Kind
	// Line is the 1-based line of its first block.
	Line int
	// Section is the heading path above it ("Billing › How it works").
	Section string
	// Text is the note body: the heading path, a blank line, then the
	// blocks separated by blank lines (just the blocks when no heading).
	Text string
	// Title is what the note is called: the heading path of a fact (plus
	// " (part N)"), the heading of a labelled decision, else the first
	// sentence.
	Title string
	// Blocks are the paragraphs and lists the piece holds, in order.
	Blocks []Block
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
	// choiceRe and reasonRe decide a prose Decision: both must appear.
	choiceRe = regexp.MustCompile(`(?i)\b(we|i|the team)\s+(chose|choose|decided|picked|pick|went with|go with|prefer|preferred|selected|opted|settled on)\b|\b(decision|decided)\s*:|\b(instead of|rather than)\b`)
	reasonRe = regexp.MustCompile(`(?i)\b(because|since|so that|due to|which means|as it|as they)\b`)
	// choiceLabelRe starts the paragraph that names the choice of a
	// labelled decision section ("Decision. …", "**Chosen:** …"); the
	// other labels (Considered, Why, Rationale …) ride along with it.
	choiceLabelRe = regexp.MustCompile(`(?i)^(?:\*\*|__)?(decision|decided|chosen|choice)(?:\*\*|__)?\s*[.:]`)
	// numberingRe is a decision heading's numbering ("D1.", "ADR-3:",
	// "2.1"), dropped from its title.
	numberingRe = regexp.MustCompile(`^(?:[A-Za-z]{1,4}[-\s]?\d+(?:\.\d+)*|\d+(?:\.\d+)*)[.:)]?\s+`)
	// boilerplateRe is a heading whose section is about the doc or the
	// project's paperwork, not about how the code works.
	boilerplateRe = regexp.MustCompile(`(?i)^(?:table of contents|contents|toc|index|licen[cs]e|licensing|contributing|contributions?|contributors|how to contribute|code of conduct|badges|sponsors?|acknowledge?ments?|credits|authors?)$`)
	// linkOnlyRe is a line that is only a link, an image or a badge
	// (optionally "see …" before it): a table-of-contents entry or a
	// pointer elsewhere says nothing itself.
	linkOnlyRe = regexp.MustCompile(`(?i)^(?:(?:see|read|more|details|for more(?: details| information)?)\s*:?,?\s+(?:also\s+)?)?(?:!?\[(?:[^\]]|\]\()*?\]\([^)]*\)\)?\s*[,;]?\s*)+[.]?$`)
	// sentenceEnd ends a title at the first sentence.
	sentenceEnd = regexp.MustCompile(`[.!?](\s|$)`)
)

// Dropped counts what Split left out of a document, by reason, so an
// import can say per doc why it drafted less than the doc holds (H-221:
// the owner wants the counts, not a silent cut).
type Dropped struct {
	// Boilerplate is the sections under a contents, license, contributing
	// or similar heading.
	Boilerplate int
	// LinkOnly is the blocks that were only links (a table of contents,
	// "See [X](x).", badges).
	LinkOnly int
	// TooShort is the pieces under MinWords words.
	TooShort int
}

// section is one heading and the blocks under it (before the next
// heading of any level).
type section struct {
	path   []string
	blocks []Block
}

// Split returns the pieces of the markdown text src, in document order.
func Split(src string) []Piece {
	out, _ := SplitCounted(src)
	return out
}

// SplitCounted is Split, plus what it left out and why.
func SplitCounted(src string) ([]Piece, Dropped) {
	var (
		out []Piece
		d   Dropped
	)
	for _, s := range sections(src) {
		out = append(out, sectionPieces(s, &d)...)
	}
	return out, d
}

// sections parses src into its sections; a section with no heading above
// it (the text before the first heading) has an empty path.
func sections(src string) []section {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var (
		out      []section
		cur      = section{}
		paths    []string
		block    []string
		bLine    int
		inList   bool
		gap      bool // a blank line inside a list: a loose list may go on
		fence    string
		inHTML   bool
		comment  bool
		listBase = -1
	)
	flush := func() {
		if len(block) > 0 {
			text := strings.Join(block, " ")
			if inList {
				text = strings.Join(block, "\n")
			}
			cur.blocks = append(cur.blocks, Block{Line: bLine, Text: text})
		}
		block, inList, gap, listBase = nil, false, false, -1
	}
	startSection := func() {
		flush()
		if len(cur.blocks) > 0 {
			out = append(out, cur)
		}
		cur = section{path: append([]string(nil), paths...)}
	}
	for i := frontMatterEnd(lines); i < len(lines); i++ {
		raw := strings.ReplaceAll(lines[i], "\t", "    ")
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
		if inList && gap && t != "" && !bulletRe.MatchString(raw) && !startsIndented(raw) {
			flush() // a blank line, then unindented text: the list has ended
		}
		switch {
		case t == "":
			if inList {
				gap = true
			} else {
				flush()
			}
		case strings.HasPrefix(t, "<"):
			flush()
			comment = strings.HasPrefix(t, "<!--")
			inHTML = !comment || !strings.Contains(t, "-->")
		case headingRe.MatchString(t):
			m := headingRe.FindStringSubmatch(t)
			level := len(m[1])
			if level <= len(paths) {
				paths = paths[:level-1]
			}
			for len(paths) < level-1 {
				paths = append(paths, "")
			}
			paths = append(paths, strings.TrimSpace(m[2]))
			startSection()
		case strings.HasPrefix(t, "|"), ruleRe.MatchString(t), linkDefRe.MatchString(raw):
			flush()
		case bulletRe.MatchString(raw):
			m := bulletRe.FindStringSubmatch(raw)
			if !inList {
				flush()
				inList, bLine, listBase = true, n, len(m[1])
			}
			gap = false
			indent := max(len(m[1])-listBase, 0)
			marker := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), m[2]))
			block = append(block, strings.Repeat(" ", indent)+marker+" "+strings.TrimSpace(m[2]))
		case len(block) == 0 && startsIndented4(raw):
			// an indented code block (not a list continuation)
		case inList && (startsIndented(raw) || (!gap && lazyContinuation(block))):
			gap = false
			block[len(block)-1] += " " + strings.TrimSpace(strings.TrimPrefix(t, ">"))
		default:
			if inList {
				flush()
			}
			if len(block) == 0 {
				bLine = n
			}
			block = append(block, strings.TrimSpace(strings.TrimPrefix(t, ">")))
		}
	}
	startSection()
	return out
}

// sectionPieces turns one section into its notes (none for boilerplate).
func sectionPieces(s section, d *Dropped) []Piece {
	for _, h := range s.path {
		if boilerplateRe.MatchString(strings.Trim(h, " *_:`")) {
			d.Boilerplate++
			return nil
		}
	}
	var blocks []Block
	for _, b := range s.blocks {
		if linkOnly(b.Text) {
			d.LinkOnly++
		} else {
			blocks = append(blocks, b)
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	path := joinPath(s.path)
	if labelledDecision(blocks) {
		p, ok := build(Decision, path, decisionTitle(s.path, blocks), blocks)
		if !ok {
			d.TooShort++
			return nil
		}
		return []Piece{p}
	}
	var out, facts []Piece
	var run []Block
	emit := func() {
		if len(run) == 0 {
			return
		}
		if p, ok := build(Fact, path, path, run); ok {
			facts = append(facts, p)
		} else {
			d.TooShort++
		}
		run = nil
	}
	size := 0
	for i, b := range blocks {
		if !strings.Contains(b.Text, "\n") && !isListBlock(b.Text) && choiceRe.MatchString(b.Text) && reasonRe.MatchString(b.Text) {
			if p, ok := build(Decision, path, Title(b.Text), []Block{b}); ok {
				out = append(out, p)
			} else {
				d.TooShort++
			}
			continue
		}
		// a lead-in ("… three phases:") stays with the list after it
		joined := i > 0 && strings.HasSuffix(blocks[i-1].Text, ":") && isListBlock(b.Text)
		r := len([]rune(b.Text))
		if len(run) > 0 && !joined && size+r > MaxPieceRunes {
			emit()
			size = 0
		}
		run = append(run, b)
		size += r
	}
	emit()
	if len(facts) > 1 && path != "" {
		for i := range facts {
			facts[i].Title = path + " (part " + strconv.Itoa(i+1) + ")"
		}
	}
	// a prose decision is cut out of its section: back in document order
	out = append(out, facts...)
	sortByLine(out)
	return out
}

// build makes one piece of blocks under path; false when it says too
// little to be a note.
func build(kind Kind, path, title string, blocks []Block) (Piece, bool) {
	parts := make([]string, 0, len(blocks)+1)
	var content []string
	for _, b := range blocks {
		content = append(content, b.Text)
	}
	if words(strings.Join(content, " ")) < MinWords {
		return Piece{}, false
	}
	if path != "" {
		parts = append(parts, path)
	}
	parts = append(parts, content...)
	text := strings.Join(parts, "\n\n")
	if title == "" {
		title = Title(content[0])
	}
	return Piece{Kind: kind, Line: blocks[0].Line, Section: path, Text: text, Title: title, Blocks: blocks, CodeRefs: codeRefs(text)}, true
}

// labelledDecision reports whether a paragraph of the section names its
// choice with a label ("Decision. …", "Chosen: …").
func labelledDecision(blocks []Block) bool {
	for _, b := range blocks {
		if choiceLabelRe.MatchString(b.Text) {
			return true
		}
	}
	return false
}

// decisionTitle is a labelled decision's title: its heading without the
// numbering, else the labelled choice's first sentence.
func decisionTitle(path []string, blocks []Block) string {
	if len(path) > 0 {
		if h := strings.TrimSpace(numberingRe.ReplaceAllString(path[len(path)-1], "")); words(h) > 0 {
			return h
		}
	}
	for _, b := range blocks {
		if loc := choiceLabelRe.FindStringIndex(b.Text); loc != nil {
			return Title(strings.TrimSpace(b.Text[loc[1]:]))
		}
	}
	return ""
}

// joinPath is the heading path with empty levels left out.
func joinPath(path []string) string {
	var p []string
	for _, h := range path {
		if h != "" {
			p = append(p, h)
		}
	}
	return strings.Join(p, SectionSep)
}

// isListBlock reports whether a block is a list (its first line has a
// bullet or number marker).
func isListBlock(text string) bool {
	first, _, _ := strings.Cut(text, "\n")
	return bulletRe.MatchString(first)
}

// linkOnly reports whether a block is only links: a table-of-contents
// list, a "See [X](x)." pointer, a row of badges.
func linkOnly(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if m := bulletRe.FindStringSubmatch(l); m != nil {
			l = strings.TrimSpace(m[2])
		}
		if !linkOnlyRe.MatchString(l) {
			return false
		}
	}
	return true
}

// sortByLine orders pieces by their first line (stable, a handful).
func sortByLine(ps []Piece) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && ps[j].Line < ps[j-1].Line; j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}

// startsIndented reports whether a line continues a list item (indented).
func startsIndented(raw string) bool {
	return strings.HasPrefix(raw, "  ")
}

// startsIndented4 is an indented code block's indent (tabs are four
// spaces by then).
func startsIndented4(raw string) bool {
	return strings.HasPrefix(raw, "    ")
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

// Title is the first sentence of text, cut at a word to MaxTitleRunes,
// with markdown emphasis and code marks dropped.
func Title(text string) string {
	t := strings.NewReplacer("**", "", "__", "", "`", "").Replace(text)
	if loc := sentenceEnd.FindStringIndex(t); loc != nil {
		t = t[:loc[0]]
	}
	t = strings.Join(strings.Fields(t), " ")
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

// WithBlocks is p rebuilt from a subset of its blocks (the ones a repeat
// check kept): the same kind, heading path and title, the text and code
// references recomputed; false when what is left says too little.
func (p Piece) WithBlocks(blocks []Block) (Piece, bool) {
	if len(blocks) == 0 {
		return Piece{}, false
	}
	title := p.Title
	if p.Section == "" && p.Kind == Fact {
		title = "" // titled by its first sentence, which may be gone
	}
	return build(p.Kind, p.Section, title, blocks)
}
