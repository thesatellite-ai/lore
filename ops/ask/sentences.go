package ask

// sentences.go — the sentence-level cut of a row (devco D-970, bug hunt
// H-229). When ask cuts a row to fit its answer, the excerpt keeps the
// sentences that hold the question's words, each under the heading it
// sits under, joined with an elision mark, and fills what room is left
// with the row's opening.
//
// Why (Sync benchmark set 5, ask 3): the old cut kept one contiguous
// part, the paragraph holding the most of the question's words grown with
// its neighbours. A note whose paragraphs repeat the question's common
// words ("stock", "sync", "warehouse") won with those, and the one
// sentence naming the function asked about ("the only caller of
// applyLevelDelta is ...") sat in a paragraph the room no longer reached:
// S5-26's excerpt dropped the only sentence naming the asked function. A
// word every sentence holds says nothing about which one answers, a word
// one sentence holds picks it, and an identifier (a function, constant or
// error name) the question names picks it most.

import (
	"bytes"
	"cmp"
	"regexp"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/hybrid"
)

// identWeight is how many plain words an identifier-shaped question word
// counts as when sentences are ranked (applyLevelDelta, apply_stock_delta,
// ErrStaleSnapshot, MAX_RUN): the question names the thing it asks about,
// so the sentence naming it goes first.
const identWeight = 4

// isIdentShape reports whether token t reads as a code name rather than a
// word: an underscore, a dot, a digit, or a lower-case letter followed by
// an upper-case one.
func isIdentShape(t string) bool {
	if strings.ContainsAny(t, "_.0123456789") {
		return true
	}
	for i := 1; i < len(t); i++ {
		if t[i-1] >= 'a' && t[i-1] <= 'z' && t[i] >= 'A' && t[i] <= 'Z' {
			return true
		}
	}
	return false
}

// sentence is one unit keepSentences picks or leaves out: a sentence of a
// line (a list item or a code line is one unit), the line and paragraph it
// sits on and the heading lines it sits under, outermost first ("# Note",
// "## Write path"; none before any heading).
type sentence struct {
	text  string
	line  int
	para  int
	heads []string
}

// sentencesOf splits text into its sentences in order: lines (outside a
// code fence a heading line is the heading of what follows, not a unit),
// each line split after ". ", "? " and "! "; a blank line starts a new
// paragraph.
func sentencesOf(text string) []sentence {
	var (
		out   []sentence
		heads []string // the heading path, one line per level
		lvls  []int
		fence string
		para  int
	)
	for li, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if f := fenceOf(trim); f != "" && (fence == "" || strings.HasPrefix(trim, fence)) {
			if fence == "" {
				fence = f
			} else {
				fence = ""
			}
			out = append(out, sentence{text: trim, line: li, para: para, heads: heads})
			continue
		}
		if fence == "" {
			if trim == "" {
				para++
				continue
			}
			if level, _, ok := atxHeading(line); ok {
				for len(lvls) > 0 && lvls[len(lvls)-1] >= level {
					lvls, heads = lvls[:len(lvls)-1], heads[:len(heads)-1]
				}
				// a new slice: the sentences above keep their own path
				heads = append(slices.Clone(heads), trim)
				lvls = append(lvls, level)
				para++
				continue
			}
		}
		if fence != "" {
			out = append(out, sentence{text: strings.TrimRight(line, " \t\r"), line: li, para: para, heads: heads})
			continue
		}
		for _, s := range splitSentences(trim) {
			out = append(out, sentence{text: s, line: li, para: para, heads: heads})
		}
	}
	return out
}

// splitSentences splits one line after each ". ", "? " or "! " (the mark
// stays with its sentence), never after a list item's number ("6. The
// importer ...": devco D-973, S6-26 printed the item without its "6.").
func splitSentences(line string) []string {
	var out []string
	start := 0
	for i := 0; i+1 < len(line); i++ {
		switch line[i] {
		case '.', '?', '!':
			if line[i+1] == ' ' && !(start == 0 && i > 0 && allDigits(line[:i])) {
				if s := strings.TrimSpace(line[start : i+1]); s != "" {
					out = append(out, s)
				}
				start = i + 2
			}
		}
	}
	if s := strings.TrimSpace(line[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// allDigits reports whether s is a number (a list item's "12").
func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// reListItem matches a markdown list item line: "- ", "* ", "+ ", "6. ",
// "6) " or "(6) " first.
var reListItem = regexp.MustCompile(`^(?:[-*+]|\d+[.)]|\(\d+\))\s`)

// leadIn is the index of the sentence that introduces list item i of ss
// (the last sentence of the line above the list, "The six write paths
// are:"), -1 when ss[i] is no list item or none is above it in its
// paragraph. Why (devco D-973, S6-26): an item kept alone ("(6) the bulk
// importer") does not say what it is an item of.
func leadIn(ss []sentence, i int) int {
	if !reListItem.MatchString(ss[i].text) {
		return -1
	}
	for j := i - 1; j >= 0 && ss[j].para == ss[i].para; j-- {
		if ss[j].line != ss[i].line && !reListItem.MatchString(firstOfLine(ss, j).text) {
			return j
		}
	}
	return -1
}

// firstOfLine is the first sentence of the line sentence j sits on.
func firstOfLine(ss []sentence, j int) sentence {
	for j > 0 && ss[j-1].line == ss[j].line {
		j--
	}
	return ss[j]
}

// identWords is the words of words (lower-case) that text writes as a
// whole identifier-shaped token somewhere (isIdentShape: a name with an
// inner capital, an underscore, a digit or a dotted path, equal to the
// word ignoring case). It scans for each word instead of running an
// identifier regexp over the whole row on every cut (devco D-985: that
// regexp over a 13,000-character note cost more than the rest of its
// cut).
func identWords(text string, words []string) map[string]bool {
	out := map[string]bool{}
	var lower []byte
	for _, w := range words {
		w = strings.ToLower(w)
		if w == "" || out[w] {
			continue
		}
		if lower == nil {
			lower = asciiLower(text)
		}
		if identAt(text, lower, w) {
			out[w] = true
		}
	}
	return out
}

// asciiLower is text with its ASCII letters lower-cased, every byte at
// its offset (identifiers are ASCII; other bytes stay as they are).
func asciiLower(text string) []byte {
	b := []byte(text)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return b
}

// identAt reports whether text (lower: asciiLower of it) holds w as a
// whole identifier-shaped token.
func identAt(text string, lower []byte, w string) bool {
	for from := 0; from < len(lower); {
		k := bytes.Index(lower[from:], []byte(w))
		if k < 0 {
			return false
		}
		a := from + k
		b := a + len(w)
		from = a + 1
		for a > 0 && isIdentByte(text[a-1]) {
			a--
		}
		for b < len(text) && isIdentByte(text[b]) {
			b++
		}
		tok := strings.TrimRight(text[a:b], ".")
		if strings.EqualFold(tok, w) && isIdentShape(tok) {
			return true
		}
	}
	return false
}

// isIdentByte reports a byte of an identifier token (letters, digits,
// '_' and the '.' of a dotted path).
func isIdentByte(c byte) bool {
	return c == '_' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// cutWeight is how much question word w counts toward the parts of a
// row an excerpt keeps when hold of the row's n parts (sections or
// sentences) hold it: the scorer's IDF within the row (hybrid.IDF, a word
// every part repeats weighs almost nothing) times its IDF across the
// store (subjectTerms), times identWeight when the row writes it as an
// identifier. One weight for sections and sentences alike (devco D-985,
// hunt H-244).
func cutWeight(n, hold int, w string, st *subjectTerms, ident bool) float64 {
	wt := hybrid.IDF(n, hold) * st.of(w)
	if ident {
		wt *= identWeight
	}
	return wt
}

// sentenceScores rates each sentence by the question's words it holds,
// each word weighing cutWeight; a word every sentence holds weighs
// nothing. top is the heaviest word some sentence holds; hold[k] lists
// the sentences holding words[k] and wt[k] is its weight (0 when it picks
// none).
func sentenceScores(ss []sentence, text string, words []string, st *subjectTerms) (score []float64, top string, hold [][]int, wt []float64) {
	idents := identWords(text, words)
	toks := make([]map[string]bool, len(ss))
	for i, s := range ss {
		toks[i] = tokensOf(s.text)
	}
	score = make([]float64, len(ss))
	hold, wt = make([][]int, len(words)), make([]float64, len(words))
	heaviest := 0.0
	for k, w := range words {
		w = strings.ToLower(w)
		for i := range ss {
			if hasWord(toks[i], w) {
				hold[k] = append(hold[k], i)
			}
		}
		if len(hold[k]) == 0 || (len(hold[k]) == len(ss) && len(ss) > 1) {
			continue
		}
		wt[k] = cutWeight(len(ss), len(hold[k]), w, st, idents[w]) // devco D-973: the store's spread too (subject.go)
		if wt[k] > heaviest {
			heaviest, top = wt[k], w
		}
		for _, i := range hold[k] {
			score[i] += wt[k]
		}
	}
	return score, top, hold, wt
}

// keepSentences is text cut to room bytes by sentence: the best sentence
// (sentenceScores), then while a question word the text holds is not yet
// kept and room is left, the best sentence holding the heaviest such word
// (coverWords), then the other sentences holding the question's words,
// best first, while they fit; printed in text order, each under its
// heading lines, a list item after the sentence leading into it, with an
// elision mark ("…") on its own line where text was left out; then,
// while room is left, the opening sentences. A best sentence longer than
// room is cut around the heaviest word it holds. ok is false when no
// sentence holds a word that tells the sentences apart (the caller cuts
// by paragraph then).
//
// Why the cover step (devco D-985, hunt H-244, owner decision 2026-11-25
// item 2): best first alone spent the room on sentences repeating the
// words the first one already held, and the cut note then said the part
// left out holds a question word the row answers with.
func keepSentences(text string, words []string, room int, st *subjectTerms) (string, bool) {
	ss := sentencesOf(text)
	if len(ss) == 0 || len(words) == 0 {
		return "", false
	}
	score, top, hold, wt := sentenceScores(ss, text, words, st)
	order := make([]int, 0, len(ss))
	for i := range ss {
		if score[i] > 0 {
			order = append(order, i)
		}
	}
	if len(order) == 0 {
		return "", false
	}
	// best first; a tie keeps the order of the text
	slices.SortStableFunc(order, func(a, b int) int {
		switch {
		case score[a] > score[b]:
			return -1
		case score[a] < score[b]:
			return 1
		}
		return 0
	})
	keep := map[int]bool{}
	try := func(i int) bool {
		keep[i] = true
		if len(renderKept(ss, keep)) <= room {
			return true
		}
		delete(keep, i)
		return false
	}
	if !try(order[0]) {
		// the best sentence alone is longer than room: its part around the
		// heaviest word, marked on both sides (never a lower sentence in
		// its place)
		best := ss[order[0]]
		head := ""
		for _, h := range best.heads {
			head += h + "\n"
		}
		marks := 2 * (len(elision) + 1)
		if len(head) > (room-marks)/2 {
			head = "" // a heading path that would crowd out the sentence
		}
		return elision + "\n" + head + window(best.text, []string{top}, max(room-marks-len(head), 1)) + "\n" + elision, true
	}
	if l := leadIn(ss, order[0]); l >= 0 {
		try(l)
	}
	coverWords(ss, keep, score, hold, wt, try)
	for _, i := range order[1:] {
		if keep[i] {
			continue
		}
		if try(i) {
			if l := leadIn(ss, i); l >= 0 {
				try(l)
			}
		}
	}
	// fill with the opening while it fits
	for i := range ss {
		if !keep[i] && !try(i) {
			break
		}
	}
	return renderKept(ss, keep), true
}

// coverWords adds to keep, through try (which keeps a sentence only
// when the cut still fits), the best sentence (by score) holding each
// question word no kept sentence holds yet, heaviest word first, with the
// sentence leading into it when it is a list item; it stops when every
// word some sentence holds is kept or none of the rest fits. hold and wt
// are sentenceScores' per-word sentences and weights (a word of weight 0
// picks no sentence and is skipped).
func coverWords(ss []sentence, keep map[int]bool, score []float64, hold [][]int, wt []float64, try func(int) bool) {
	ks := make([]int, 0, len(wt))
	for k := range wt {
		if wt[k] > 0 {
			ks = append(ks, k)
		}
	}
	// heaviest first; a tie keeps the question's order
	slices.SortStableFunc(ks, func(a, b int) int { return cmp.Compare(wt[b], wt[a]) })
	for _, k := range ks {
		if slices.ContainsFunc(hold[k], func(i int) bool { return keep[i] }) {
			continue // a kept sentence already holds it
		}
		cand := slices.Clone(hold[k])
		slices.SortStableFunc(cand, func(a, b int) int { return cmp.Compare(score[b], score[a]) })
		for _, i := range cand {
			if try(i) {
				if l := leadIn(ss, i); l >= 0 {
					try(l)
				}
				break
			}
		}
	}
}

// renderKept prints the kept sentences of ss in text order: two kept
// sentences next to each other joined as the text joins them (a space on
// one line, a line break, a blank line between paragraphs), a gap marked
// with elision on its own line, the heading lines a sentence sits under
// above it unless the sentence before already printed them, and elision
// first or last when text was left out before the first or after the
// last kept sentence.
func renderKept(ss []sentence, keep map[int]bool) string {
	var b strings.Builder
	prev := -1
	var heads []string // the heading lines printed last
	for i, s := range ss {
		if !keep[i] {
			continue
		}
		switch {
		case prev < 0 && i > 0:
			b.WriteString(elision + "\n")
		case prev >= 0 && prev != i-1:
			b.WriteString("\n" + elision + "\n")
		case prev >= 0 && ss[prev].line == s.line:
			b.WriteString(" ")
		case prev >= 0 && ss[prev].para == s.para:
			b.WriteString("\n")
		case prev >= 0:
			b.WriteString("\n\n")
		}
		same := 0
		for same < len(heads) && same < len(s.heads) && heads[same] == s.heads[same] {
			same++
		}
		for _, h := range s.heads[same:] {
			b.WriteString(h + "\n")
		}
		heads = s.heads
		b.WriteString(s.text)
		prev = i
	}
	if prev >= 0 && prev < len(ss)-1 {
		b.WriteString("\n" + elision)
	}
	return b.String()
}
