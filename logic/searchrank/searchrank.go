// Package searchrank is the one reading and ranking of a search's free
// text (layer L1, pure): `lore search` (wire) and every `<kind> search`
// (ops/memory.SearchFields) plan their full-text match and score their
// hits here, so a question finds the same rows in the same order through
// either (D-435, D-436, D-461).
//
//	"how do we deploy orbit -friday"
//	  ─▶ topic words [deploy orbit] (querywords.Terms: stop words out)
//	  ─▶ FTS5: ("deploy" OR "orbit") NOT friday   — a row needs one word, not all
//	  ─▶ per word: which rows hold it (same FTS5 stemming) ─▶ matched count per row
//	  ─▶ score = (matched + 0.6·title share + 0.3/(1+bm25 rank)) / (words+1)
//
// One more matched word always outranks any title or rank bonus (the
// bonus stays under 1), and the score means the same in every kind.
//
// A query written in FTS5 syntax ("phrase", prefix*, AND / OR / NOT,
// column filters) is run as written: every hit matched the whole query.
// Its words still give the title share, a prefix word (invoi*) as a
// prefix (querywords.Coverage), so a title hit outranks a body hit for
// words, phrases and prefixes alike (D-465).
package searchrank

import (
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/logic/metaquery"
	"github.com/thesatellite-ai/lore/logic/querywords"
)

// Score weights (see the package comment); their sum stays under 1.
const (
	scoreTitle = 0.6
	scoreRank  = 0.3
)

// Words is the free text of one search as the ranking reads it.
type Words struct {
	// Terms are the topic words a hit is scored on (lower-case, stop
	// words out; a prefix word keeps its trailing '*'); empty when the
	// text has none (conditions only).
	Terms []string
	// FTS is the FTS5 text the tables are searched with.
	FTS string
	// Natural is set for plain text with more than one topic word: a row
	// needs only some of them, and TermFTS finds each word's rows.
	Natural bool
	// not is the FTS5 tail leaving out the negated words (" NOT …"), ""
	// without any; each word's own search carries it too.
	not string
}

// ftsSyntaxChars and ftsSyntaxWords make a query FTS5 syntax rather than
// plain words.
const ftsSyntaxChars = `"*(){}^:`

var ftsSyntaxWords = map[string]bool{"AND": true, "OR": true, "NOT": true, "NEAR": true}

// Of plans text with no meta conditions (a `<kind> search` query).
func Of(text string) Words {
	return Plan(text, metaquery.Parse(text, func(string) bool { return false }))
}

// Plan plans the free text of a search from its parsed terms: plain words
// are read as topic words (any of them matches, D-435); FTS5 syntax is
// kept as written (metaquery.FTS, every word required).
func Plan(text string, parsed []metaquery.Term) Words {
	var pos, neg []string
	for _, t := range parsed {
		if t.Kind != metaquery.Text {
			continue
		}
		if n, ok := metaquery.Negated(t.Word); ok {
			neg = append(neg, n)
			continue
		}
		pos = append(pos, t.Word)
	}
	syntax := strings.ContainsAny(text, ftsSyntaxChars)
	for _, w := range strings.Fields(text) {
		syntax = syntax || ftsSyntaxWords[w]
	}
	full := metaquery.FTS(parsed)
	if syntax {
		return Words{Terms: querywords.Terms(prefixText(strings.Join(pos, " "))), FTS: full}
	}
	terms := querywords.Terms(strings.Join(pos, " "))
	w := Words{Terms: terms, Natural: len(terms) > 1}
	if len(neg) > 0 {
		w.not = " NOT " + strings.Join(QuoteEach(neg), " NOT ")
	}
	if len(terms) == 0 || len(terms) == 1 && wordForms(terms[0]) == "" && len(pos) == 1 {
		// no topic word, or one word with no other forms to find (an
		// emoji, a CJK word, a short word): the query as written
		w.FTS = full
		return w
	}
	forms := make([]string, len(terms))
	for i, t := range terms {
		forms[i] = termMatch(t)
	}
	w.FTS = "(" + strings.Join(forms, " OR ") + ")" + w.not
	return w
}

// TermFTS is the FTS5 text finding the rows of one topic word.
func (w Words) TermFTS(term string) string {
	q := termMatch(term)
	if w.not != "" {
		q = "(" + q + ")" + w.not
	}
	return q
}

// minFormWord is the shortest word whose other forms are searched too: a
// shorter one ("go", "api") is searched as written.
const minFormWord = 4

// formSuffixes are added to a word's stem to spell its other forms.
var formSuffixes = []string{"", "e", "s", "es", "ed", "ing", "ment", "er", "ly"}

// wordForms is the FTS5 text finding every form of an ASCII word (its
// querywords.Stem plus each formSuffixes ending, each a quoted word, any
// of them), "" for a word with no forms to add. The index's porter
// stemmer alone does not join them: it indexes deployment as "deploy"
// but deploy and deploys as "deploi". Whole forms, not a prefix: "stor*"
// for stored would find storm.
func wordForms(term string) string {
	if len(term) < minFormWord {
		return ""
	}
	for _, r := range term {
		if r > 0x7f || !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	stem := querywords.Stem(term)
	seen := map[string]bool{term: true}
	forms := []string{term}
	for _, sfx := range formSuffixes {
		f := stem + sfx
		if !seen[f] {
			seen[f] = true
			forms = append(forms, f)
		}
	}
	return "(" + strings.Join(QuoteEach(forms), " OR ") + ")"
}

// termMatch is the FTS5 text of one topic word: any of its forms
// (wordForms), else the word itself (FTS5 stems it as the index does).
func termMatch(term string) string {
	if f := wordForms(term); f != "" {
		return f
	}
	return QuoteEach([]string{term})[0]
}

// QuoteEach is each word as an FTS5 string (its punctuation no syntax;
// FTS5 still stems and segments inside the quotes).
func QuoteEach(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	return out
}

// Quoted is q with every white-space word an FTS5 string: the retry of a
// query FTS5 refused as syntax (every word must match).
func Quoted(q string) string { return strings.Join(QuoteEach(strings.Fields(q)), " ") }

// PlainText turns an FTS5 query into plain words: operators, quotes and
// wildcards dropped.
func PlainText(q string) string {
	return plain(q, strings.NewReplacer(`"`, " ", "*", " ", "(", " ", ")", " "))
}

// prefixText is PlainText keeping a prefix word's trailing '*' (invoi*),
// which querywords.Coverage reads as a prefix.
func prefixText(q string) string {
	return plain(q, strings.NewReplacer(`"`, " ", "(", " ", ")", " "))
}

// plain is q through r, FTS5 operators dropped.
func plain(q string, r *strings.Replacer) string {
	var words []string
	for _, w := range strings.Fields(r.Replace(q)) {
		if ftsSyntaxWords[w] || strings.Trim(w, "*") == "" {
			continue
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

// Score is a hit's relevance in [0, 1): matched of the words' n, the
// share of the words its title holds, and its bm25 rank in its kind.
// With no words (conditions only) every hit matched all there was.
func Score(matched, n int, titleShare float64, rank int) float64 {
	if n == 0 {
		matched = 0
	}
	bonus := scoreTitle*titleShare + scoreRank/float64(1+rank)
	return (float64(matched) + bonus) / float64(n+1)
}

// TitleShare is the share of terms title holds (0 without terms or title).
func TitleShare(title string, terms []string) float64 {
	if title == "" || len(terms) == 0 {
		return 0
	}
	return float64(querywords.Coverage(title, terms)) / float64(len(terms))
}

// Item is one hit of a table before ordering: Label names it (its title,
// else the start of its body), Matched is how many topic words it holds
// (read only for a Natural query; any other hit held every word).
type Item struct {
	Label   string
	Matched int
}

// Ranked is one ordered hit: Index into the items, its Score, and
// Partial when a Natural query's row holds only some of the words.
type Ranked struct {
	Index   int
	Score   float64
	Partial bool
}

// Order orders one table's items (given in bm25 order) by words held,
// then title share, keeping bm25 order within a tie, cut to limit, each
// scored (Score).
func (w Words) Order(items []Item, limit int) []Ranked {
	n := len(w.Terms)
	matched := make([]int, len(items))
	share := make([]float64, len(items))
	idx := make([]int, len(items))
	for i, it := range items {
		matched[i] = n
		if w.Natural {
			matched[i] = it.Matched
		}
		share[i] = TitleShare(it.Label, w.Terms)
		idx[i] = i
	}
	sort.SliceStable(idx, func(x, y int) bool {
		a, b := idx[x], idx[y]
		if matched[a] != matched[b] {
			return matched[a] > matched[b]
		}
		return share[a] > share[b]
	})
	out := make([]Ranked, 0, min(len(idx), limit))
	for rank, i := range idx {
		if rank == limit {
			break
		}
		out = append(out, Ranked{Index: i, Score: Score(matched[i], n, share[i], rank), Partial: w.Natural && matched[i] < n})
	}
	return out
}

// labelRunes is how much of a title-less row's body serves as its label.
const labelRunes = 80

// Label is what names a hit for ranking: its title, else the start of the
// first line of body, which is what names a memory (a kind with no
// title) in every listing (devco D-463).
func Label(title, body string) string {
	if title != "" {
		return title
	}
	line, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	if r := []rune(line); len(r) > labelRunes {
		line = string(r[:labelRunes])
	}
	return line
}
