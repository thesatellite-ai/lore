package wire

// searchrank.go — how `lore search` reads free text and ranks its hits
// (D-435 natural-language queries, D-436 relevance-first order).
//
//	"how do we deploy orbit -friday"
//	  ─▶ topic words [deploy orbit] (querywords.Terms: stop words out)
//	  ─▶ FTS5: ("deploy" OR "orbit") NOT friday   — a row needs one word, not all
//	  ─▶ per word: which rows hold it (same FTS5 stemming) ─▶ matched count per row
//	  ─▶ score = (matched + 0.6·title share + 0.3/(1+bm25 rank)) / (words+1)
//
// One more matched word always outranks any title or rank bonus (the
// bonus stays under 1), and the score means the same in every kind, so
// hits sort by it across kinds. Before, each kind's hits got 1/(60+rank)
// (reciprocal rank fusion): every score was 0.016-0.017 and the list
// interleaved one hit per kind whatever matched.
//
// A query written in FTS5 syntax ("phrase", prefix*, AND / OR / NOT,
// column filters) is run as written: every hit matched the whole query.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/metaquery"
	"github.com/thesatellite-ai/lore/logic/querywords"
	"github.com/thesatellite-ai/lore/model/errs"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

// Score weights (see the file comment); their sum stays under 1.
const (
	scoreTitle = 0.6
	scoreRank  = 0.3
)

// searchWords is the free text of one search as the ranking reads it.
type searchWords struct {
	// terms are the topic words a hit is scored on (lower-case, stop
	// words out); empty when the text has none (conditions only).
	terms []string
	// fts is the FTS5 text the tables are searched with.
	fts string
	// natural is set for plain text with more than one topic word: a row
	// needs only some of them, and termFTS finds each word's rows.
	natural bool
	// not is the FTS5 tail leaving out the negated words (" NOT …"), ""
	// without any; each word's own search carries it too.
	not string
}

// ftsSyntax are the characters and words that make a query FTS5 syntax
// rather than plain words.
const ftsSyntaxChars = `"*(){}^:`

var ftsSyntaxWords = map[string]bool{"AND": true, "OR": true, "NOT": true, "NEAR": true}

// wordsOf plans the free text of a search: plain words are read as
// topic words (any of them matches, D-435); FTS5 syntax is kept as
// written (metaquery.FTS, every word required).
func wordsOf(text string, parsed []metaquery.Term) searchWords {
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
		return searchWords{terms: querywords.Terms(plainSearchText(strings.Join(pos, " "))), fts: full}
	}
	terms := querywords.Terms(strings.Join(pos, " "))
	w := searchWords{terms: terms, natural: len(terms) > 1}
	if len(neg) > 0 {
		w.not = " NOT " + strings.Join(quoteEach(neg), " NOT ")
	}
	if len(terms) == 1 && wordForms(terms[0]) == "" && len(pos) == 1 {
		// one word with no other forms to find (an emoji, a CJK word, a
		// short word): the query as written, as before D-435
		w.fts = full
		return w
	}
	if len(terms) == 0 {
		w.fts = full
		return w
	}
	forms := make([]string, len(terms))
	for i, t := range terms {
		forms[i] = termMatch(t)
	}
	w.fts = "(" + strings.Join(forms, " OR ") + ")" + w.not
	return w
}

// termFTS is the FTS5 text finding the rows of one topic word.
func (w searchWords) termFTS(term string) string {
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
// but deploy and deploys as "deploi", so `search deploy` missed the row
// about a deployment. Whole forms, not a prefix: "stor*" for stored
// would find storm.
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
	return "(" + strings.Join(quoteEach(forms), " OR ") + ")"
}

// termMatch is the FTS5 text of one topic word: any of its forms
// (wordForms), else the word itself (FTS5 stems it as the index does).
func termMatch(term string) string {
	if f := wordForms(term); f != "" {
		return f
	}
	return quoteEach([]string{term})[0]
}

// quoteEach is each word as an FTS5 string (its punctuation no syntax;
// FTS5 still stems and segments inside the quotes).
func quoteEach(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	return out
}

// scoreHit is a hit's relevance in [0, 1): matched of the words' n, the
// share of the words its title holds, and its bm25 rank in its kind.
// With no words (conditions only) every hit matched all there was.
func scoreHit(matched, n int, titleShare float64, rank int) float64 {
	if n == 0 {
		matched = 0
	}
	bonus := scoreTitle*titleShare + scoreRank/float64(1+rank)
	return (float64(matched) + bonus) / float64(n+1)
}

// titleShare is the share of terms title holds (0 without terms or title).
func titleShare(title string, terms []string) float64 {
	if title == "" || len(terms) == 0 {
		return 0
	}
	return float64(querywords.Coverage(title, terms)) / float64(len(terms))
}

// scoreInOrder scores hits that matched every term (condition searches:
// meta key:value, resource field:value), keeping their order as rank.
func scoreInOrder(hits []opssearch.Hit, terms []string) {
	for i := range hits {
		hits[i].Score = scoreHit(len(terms), len(terms), titleShare(hits[i].Title, terms), i)
	}
}

// resourceKindNames maps every kind id this lens sees to its name, for
// the resource hits' kind (D-436). No kinds view yet is an empty map.
func (a *App) resourceKindNames(ctx context.Context) (map[string]string, error) {
	res, err := a.SQL(ctx, opssql.Request{Query: `SELECT id, name FROM kinds_all`, Timeout: opssql.DefaultTimeout})
	if err != nil {
		if errs.Has(err, errs.InvalidInput) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := make(map[string]string, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) > 1 {
			out[r[0].String()] = r[1].String()
		}
	}
	return out, nil
}
