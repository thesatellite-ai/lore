// Package hybrid ranks texts for a question by meaning and by words at
// once (layer L1, pure): stemmed BM25 over an in-memory inverted index
// that reads text the way lore's full-text index does (porter over
// unicode61, diacritics removed, the dotless i as i), static vectors from
// logic/embed, and the two fused as wsum50:
//
//	fused = 0.5 × bm25 / best bm25 + 0.5 × cosine / best cosine
//
// over the first Pool places of each source (a text in neither pool
// scores 0; a negative cosine counts as 0).
//
// Why this fusion (devco D-894, hunt H-149, the memory-engine
// exploration): on LongMemEval_S and LoCoMo it is the only one that beat
// both single sources on both sets; reciprocal-rank fusion lost 6-8 points
// on LoCoMo and learned weights overfit one set. Each source divided by
// its best score keeps the fusion monotone and free of thresholds.
package hybrid

import (
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	porter "github.com/blevesearch/go-porterstemmer"
	"golang.org/x/text/unicode/norm"

	"github.com/thesatellite-ai/lore/logic/embed"
	"github.com/thesatellite-ai/lore/logic/textnorm"
)

// Pool is how many places of each source take part in the fusion.
const Pool = 50

// BM25 parameters (Lucene's defaults, which FTS5's bm25() also uses).
const (
	k1 = 1.2
	b  = 0.75
)

// Doc is one text of an index with its vector (embed.Model.Embed of the
// same text; the caller keeps vectors between builds, so a text is
// embedded once, not per question).
type Doc struct {
	Text string
	Vec  []float32
	// Terms are Text's index terms (Terms of Text) when the caller kept
	// them, nil to read them from Text: a store's index rebuilt after one
	// row changed reads only that row's text (devco D-902).
	Terms []string
}

// Index is the BM25 postings and vectors of a set of texts.
type Index struct {
	post map[string][]posting
	lens []float32
	avg  float64
	vecs [][]float32
}

// posting is one text holding a term, with the term's count in it.
type posting struct {
	doc int32
	tf  float32
}

// Build indexes docs in their order (a result's place is the doc's index).
func Build(docs []Doc) *Index {
	ix := &Index{post: map[string][]posting{}, lens: make([]float32, len(docs)), vecs: make([][]float32, len(docs))}
	total := 0
	tf := map[string]int{}
	var scratch []string
	for i, d := range docs {
		clear(tf)
		toks := d.Terms
		if toks == nil {
			// scratch, never a kept Terms slice: reusing one would write
			// over another doc's kept terms
			scratch = Terms(scratch[:0], d.Text)
			toks = scratch
		}
		for _, w := range toks {
			tf[w]++
		}
		for w, c := range tf {
			ix.post[w] = append(ix.post[w], posting{int32(i), float32(c)})
		}
		ix.lens[i] = float32(len(toks))
		total += len(toks)
		ix.vecs[i] = d.Vec
	}
	if len(docs) > 0 {
		ix.avg = float64(total) / float64(len(docs))
	}
	return ix
}

// Bytes estimates the heap the index takes besides its vectors (which
// the caller keeps): postings, terms and lengths. The read server counts
// it in what it keeps on purpose (devco D-902).
func (ix *Index) Bytes() int64 {
	n := int64(len(ix.lens))*(4+24) + 48
	for t, ps := range ix.post {
		n += int64(len(t)) + 16 + 24 + 48 + int64(cap(ps))*8
	}
	return n
}

// Len is the number of indexed texts.
func (ix *Index) Len() int { return len(ix.lens) }

// BM25 scores every text for the query words (each word analysed as the
// index reads text; duplicates count once).
func (ix *Index) BM25(words []string) []float64 {
	out := make([]float64, len(ix.lens))
	n := float64(len(ix.lens))
	if n == 0 || ix.avg == 0 {
		return out
	}
	seen := map[string]bool{}
	var toks []string
	for _, w := range words {
		toks = Terms(toks[:0], w)
		for _, t := range toks {
			if seen[t] {
				continue
			}
			seen[t] = true
			ps := ix.post[t]
			if len(ps) == 0 {
				continue
			}
			idf := IDF(len(ix.lens), len(ps))
			for _, p := range ps {
				f := float64(p.tf)
				out[p.doc] += idf * f * (k1 + 1) / (f + k1*(1-b+b*float64(ix.lens[p.doc])/ix.avg))
			}
		}
	}
	return out
}

// IDF is the weight BM25 gives a term df of n texts hold (Lucene's
// ln(1 + (n-df+0.5)/(df+0.5)), never negative): a term few texts hold
// weighs much, one every text holds almost nothing.
//
// Why exported (devco D-985, hunt H-244): lore ask picks the sections and
// sentences of a long row it prints by how rare the question's words
// are; it reads that rarity with this same function, from the same index
// its rows were scored with (Index.TermIDF) and within the row, so the
// excerpt and the ranking agree on which words name the subject instead
// of keeping a second, differently tuned copy.
func IDF(n, df int) float64 {
	if n <= 0 || df <= 0 {
		return 0
	}
	fn, fd := float64(n), float64(min(df, n))
	return math.Log(1 + (fn-fd+0.5)/(fd+0.5))
}

// TermIDF is IDF of word w over the index's texts, w analysed as the
// index reads text (its heaviest term when it splits into several); 0
// when no text holds it.
func (ix *Index) TermIDF(w string) float64 {
	best := 0.0
	for _, t := range Terms(nil, w) {
		if ps := ix.post[t]; len(ps) > 0 {
			best = max(best, IDF(len(ix.lens), len(ps)))
		}
	}
	return best
}

// Cosine scores every text against the question's vector (0 for a text
// without a vector).
func (ix *Index) Cosine(q []float32) []float64 {
	out := make([]float64, len(ix.vecs))
	for i, v := range ix.vecs {
		if len(v) == len(q) {
			out[i] = float64(embed.Cosine(q, v))
		}
	}
	return out
}

// Fused is wsum50 of the query words' BM25 and the question vector's
// cosine, per text.
func (ix *Index) Fused(words []string, q []float32) []float64 {
	return Fuse(ix.BM25(words), ix.Cosine(q), Pool)
}

// Fuse is 0.5 × a/max(a) + 0.5 × c/max(c) over the first pool places of
// each source (a text outside both pools, or with no positive score,
// gets 0).
func Fuse(a, c []float64, pool int) []float64 {
	out := make([]float64, len(a))
	ina, inc := topPlaces(a, pool), topPlaces(c, pool)
	ma, mc := maxPos(a), maxPos(c)
	for i := range out {
		if !ina[i] && !inc[i] {
			continue
		}
		if ma > 0 && a[i] > 0 {
			out[i] += 0.5 * a[i] / ma
		}
		if mc > 0 && c[i] > 0 {
			out[i] += 0.5 * c[i] / mc
		}
	}
	return out
}

// topPlaces marks the texts among the first pool places by s (positive
// scores only; ties at the cut broken by place, so it is deterministic).
func topPlaces(s []float64, pool int) []bool {
	idx := make([]int, 0, len(s))
	for i, v := range s {
		if v > 0 {
			idx = append(idx, i)
		}
	}
	slices.SortStableFunc(idx, func(x, y int) int {
		switch {
		case s[x] > s[y]:
			return -1
		case s[x] < s[y]:
			return 1
		}
		return 0
	})
	out := make([]bool, len(s))
	for p, i := range idx {
		if p >= pool {
			break
		}
		out[i] = true
	}
	return out
}

// maxPos is the largest positive value of s, 0 when none.
func maxPos(s []float64) float64 {
	m := 0.0
	for _, v := range s {
		m = max(m, v)
	}
	return m
}

// Terms appends text's index terms to dst, the way lore's full-text index
// reads it (`porter unicode61 remove_diacritics 2 categories 'L* N* Co
// So'`): runs of letters, digits, private-use and other symbols (emoji)
// are tokens, each lower-cased without diacritics, then Porter-stemmed
// when it is 3..64 bytes of ASCII (SQLite's porter passes others through).
func Terms(dst []string, text string) []string {
	text = textnorm.SearchFold(text)
	start := -1
	for i, r := range text {
		if isTokRune(r) || unicode.Is(unicode.Mn, r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			dst = addTerm(dst, text[start:i])
			start = -1
		}
	}
	if start >= 0 {
		dst = addTerm(dst, text[start:])
	}
	return dst
}

// isTokRune is a token character of the index tokenizer (the skin-tone
// modifiers are token characters too, as the index's tokenchars say).
func isTokRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Co, r) || unicode.Is(unicode.So, r) || (r >= 0x1F3FB && r <= 0x1F3FF)
}

// addTerm folds and stems one token.
func addTerm(dst []string, w string) []string {
	w = fold(w)
	if w == "" {
		return dst
	}
	return append(dst, Stem(w))
}

// Stem is the index term of one folded (lower-case) token: its Porter
// stem when it is 3..64 bytes of ASCII, else the token itself, as
// lore's full-text index stems it. Why exported (bug hunt H-235): lore
// ask matches a question word to a row word by this stem too, so
// "validate" finds the row writing "validation" and "jobs" the one
// writing "job", as lore search does.
func Stem(w string) string {
	if len(w) >= 3 && len(w) <= 64 && isASCII(w) {
		return porter.StemString(w)
	}
	return w
}

// isASCII reports whether s is all ASCII.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// fold lower-cases w and drops its diacritics (NFD, combining marks out).
func fold(w string) string {
	if isASCII(w) {
		return strings.ToLower(w)
	}
	var sb strings.Builder
	for _, r := range norm.NFD.String(w) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		sb.WriteRune(unicode.ToLower(r))
	}
	return sb.String()
}
