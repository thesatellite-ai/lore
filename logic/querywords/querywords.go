// Package querywords is the one word-level matcher of a person's free
// text (layer L1, pure): `lore search` and `lore context --for-prompt`
// read a question the same way (D-435).
//
//	"How do we deploy Orbit?" ─▶ Terms ─▶ [deploy orbit]   (stop words out, lower-case, end punctuation trimmed)
//	Has("Deploying orbit to staging", "deploy") ─▶ true   (same Base "deplo": deploying / deployment / deploys)
//	Coverage(text, terms) ─▶ how many terms the text holds
//
// Before D-435 `lore search` required every word of the query, stop words
// included, so "how do we deploy orbit" matched no row while `search
// deploy` found twenty. A question is now read as its topic words; the
// caller ranks rows by how many of them they hold.
package querywords

import (
	"strings"
	"unicode"
)

// stopWords carry no topic: question words, pronouns, auxiliaries,
// articles and the like. Kept small and English; a query made only of
// them keeps its words (Terms).
var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an the and or but if of to in on at by for with from into onto about as
		is are was were be been being am do does did done doing have has had having
		i me my mine we us our ours you your yours he him his she her it its they them their theirs
		this that these those there here what which who whom whose where when why how
		can could should would will shall may might must not no yes so than then too very just also
		any some all each every please tell show find need want know get got go goes`) {
		stopWords[w] = true
	}
}

// IsStop reports whether the lower-case word w carries no topic.
func IsStop(w string) bool { return stopWords[w] }

// Terms is the topic words of q in first-seen order: split at white
// space, punctuation trimmed from both ends ("orbit?" is orbit; inner
// punctuation stays, so deploy.prod.yml is one word), lower-cased,
// repeats and stop words dropped. When every word is a stop word the
// words are kept ("the who" still searches for something). A word ending
// in '*' is a prefix (invoi*) and keeps its '*' (Coverage, D-465).
func Terms(q string) []string {
	var all, topic []string
	seen := map[string]bool{}
	for _, f := range strings.Fields(q) {
		w := strings.ToLower(strings.TrimFunc(f, func(r rune) bool { return unicode.IsPunct(r) }))
		if w != "" && strings.HasSuffix(f, PrefixMark) {
			w += PrefixMark
		}
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		all = append(all, w)
		if !stopWords[w] {
			topic = append(topic, w)
		}
	}
	if len(topic) == 0 {
		return all
	}
	return topic
}

// minStem is the shortest stem Stem leaves: shorter ones match too much.
const minStem = 3

// suffixes Stem strips, longest first; the first that leaves at least
// minStem characters wins.
var suffixes = []string{"ments", "ment", "ings", "ing", "edly", "ies", "ers", "ed", "er", "ly", "es", "s"}

// Stem is a light English stem of the lower-case word w: one common
// suffix off (deploying, deployment, deployed and deploys are deploy;
// running is run; locally is local; servers is serv). It is a match
// prefix, not a dictionary word: Has looks for it inside the text, so
// "serv" finds server and service alike.
func Stem(w string) string {
	for _, s := range suffixes {
		base, ok := strings.CutSuffix(w, s)
		if !ok || len([]rune(base)) < minStem {
			continue
		}
		if s == "s" && strings.HasSuffix(base, "s") {
			return w // class, address: not a plural
		}
		if s == "ies" {
			return base + "y"
		}
		// running -> runn -> run: a doubled last consonant goes
		if r := []rune(base); len(r) > minStem && r[len(r)-1] == r[len(r)-2] && !strings.ContainsRune("aeiouls", r[len(r)-1]) {
			return string(r[:len(r)-1])
		}
		return base
	}
	return w
}

// minBase is the shortest Base: a final e or y stays on a shorter stem.
const minBase = 4

// Base is the part every form of the lower-case word w starts with: its
// Stem without a final e or y when at least minBase characters remain
// (stage and staging share "stag"; deploy and deployment "deplo").
func Base(w string) string {
	s := Stem(w)
	if b := strings.TrimRight(s, "ey"); len(b) == len(s)-1 && len([]rune(b)) >= minBase {
		return b
	}
	return s
}

// Has reports whether text holds a form of term: a word of text with the
// same Base (case-insensitive; deployment holds deploy, storm does not
// hold stored). A term that is not one plain word (deploy.prod.yml,
// well-known, a CJK word) is looked for as written inside the text.
func Has(text, term string) bool { return Coverage(text, []string{term}) == 1 }

// PrefixMark ends a prefix term (invoi*): it matches every word starting
// with the rest, as FTS5's prefix query does.
const PrefixMark = "*"

// Coverage is how many of terms text holds (Has). A prefix term (invoi*)
// is held by a word of text starting with it, so a prefix query gives a
// title its share like a word query does (D-465).
func Coverage(text string, terms []string) int {
	lower := strings.ToLower(text)
	var bases map[string]bool // the Base of every word of text, made on first use
	n := 0
	for _, t := range terms {
		t = strings.ToLower(t)
		if p, ok := strings.CutSuffix(t, PrefixMark); ok && p != "" {
			if hasPrefixWord(lower, p) {
				n++
			}
			continue
		}
		if !plainWord(t) {
			if strings.Contains(lower, t) {
				n++
			}
			continue
		}
		if bases == nil {
			bases = map[string]bool{}
			for _, w := range strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
				bases[Base(w)] = true
			}
		}
		if bases[Base(t)] {
			n++
		}
	}
	return n
}

// plainWord reports whether w is one word of ASCII letters and digits,
// the words Stem knows the forms of.
func plainWord(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// hasPrefixWord reports whether a word of the lower-case text starts with
// prefix (inner punctuation splits words, as the FTS5 index does).
func hasPrefixWord(lower, prefix string) bool {
	for _, w := range strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if strings.HasPrefix(w, prefix) {
			return true
		}
	}
	return false
}
