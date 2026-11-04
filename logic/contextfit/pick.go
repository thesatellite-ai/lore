package contextfit

// On-demand picking for `lore context` (CONTEXT_BUDGET 6.8): glob matching
// of a path against a rule's globs, the words of a path or prompt, and
// fitting picked entries under a byte cap without cutting one.
//
//	Pick ─► each entry full while it fits ─► else its line ─► else stop:
//	        Out ends with "more: <command>" (never a cut mid-entry)

import (
	"path"
	"strings"
	"unicode"
)

// SplitGlobs splits a rule's globs field (comma, semicolon, space or line
// separated) into patterns.
func SplitGlobs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
}

// MatchGlob reports whether the slash path p matches pattern, where "**"
// matches any number of path segments and every other segment uses
// path.Match rules. A pattern without a slash matches the base name at any
// depth (the gitignore convention).
func MatchGlob(pattern, p string) bool {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "./")
	p = strings.TrimPrefix(p, "./")
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "/") {
		ok, err := path.Match(pattern, path.Base(p))
		return err == nil && ok
	}
	return matchSegs(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(segs); i++ {
				if matchSegs(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// minWord is the shortest word used for matching (shorter words match
// everything: "go", "src").
const minWord = 4

// Words returns the distinct lower-case words of s (letters and digits) of
// at least four characters, in first-seen order; "_", "-", "/" and "."
// split words, so a path yields its directory and file stem words.
func Words(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(w)) < minWord || seen[w] || stopWords[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// stopWords are frequent words that carry no topic.
var stopWords = map[string]bool{"this": true, "that": true, "with": true, "from": true, "have": true,
	"what": true, "when": true, "where": true, "which": true, "there": true, "their": true, "about": true,
	"would": true, "could": true, "should": true, "into": true, "then": true, "than": true, "them": true}

// Score is how many of words appear in text (lower-cased).
func Score(text string, words []string) int {
	t := strings.ToLower(text)
	n := 0
	for _, w := range words {
		if strings.Contains(t, w) {
			n++
		}
	}
	return n
}

// Piece is one picked entry: its full and line text.
type Piece struct {
	ID         string
	Full, Line string
}

// Picked is the fitted output.
type Picked struct {
	Text string
	// Shown counts pieces printed (full or line); Lines counts the lines.
	Shown, Lines int
	// More is true when pieces were left out for the cap.
	More bool
}

// Pick writes pieces in order under maxBytes (0 = no cap), reserving
// reserve bytes for the closing "more:" line: a piece goes in full when it
// fits, else as its line, else picking stops (6.8: never split an entry).
func Pick(pieces []Piece, maxBytes, reserve int) Picked {
	var b strings.Builder
	var out Picked
	for _, p := range pieces {
		switch {
		case maxBytes <= 0 || b.Len()+len(p.Full)+reserve <= maxBytes:
			b.WriteString(p.Full)
		case b.Len()+len(p.Line)+reserve <= maxBytes:
			b.WriteString(p.Line)
			out.Lines++
		default:
			out.More = true
			out.Text = b.String()
			return out
		}
		out.Shown++
	}
	out.Text = b.String()
	return out
}
