package ask

import (
	"hash/fnv"
	"hash/maphash"
	"maps"
	"runtime"
	"slices"
	"sync"

	"github.com/thesatellite-ai/lore/logic/embed"
	"github.com/thesatellite-ai/lore/logic/hybrid"
	"github.com/thesatellite-ai/lore/model/value"
)

// corpus.go — the whole set of rows a question is asked of, indexed once
// (devco D-902, hunt H-157). Before, BM25 and the static vectors ran only
// over the rows that held a question word (the SQL LIKE prefilter), so a
// note answering a question asked in other words ("which physician do I
// see" → "My doctor is Dr Patel …") was never read at all, and BM25's word
// weights came from that small set instead of the store. Now every live
// row of the stores the call reads is indexed: BM25 over the whole corpus
// (IDF of the store) and every row's vector, so the rows nearest in
// meaning can join the rows holding a word.
//
// Cost, kept small: the parsed rows and their index are kept per process
// by a fingerprint of the lens cells (corpusOf), so a served call with no
// change parses and indexes nothing; a change re-reads the rows, but each
// row's terms and vector are kept by its text's hash, so only the changed
// rows are stemmed and embedded. Vectors are also kept between processes
// through Env's VectorFile (a file under the lore home), so a one-shot
// call embeds only the rows new since the last call (~80 µs each).

// meaningRows is how many rows nearest the question in meaning may join
// the rows holding its words: the answer's place count (DefaultTop), so
// a question asked in other words can fill a whole answer, never more.
const meaningRows = DefaultTop

// meaningFloor is the least cosine to the question a row needs to join by
// meaning. Why this value: with the static vectors (potion-base-8M) a note
// answering a question in other words scores ~0.26-0.46 against it, the
// unrelated notes of the same person at most ~0.2 (TestMeaningFloorSeparates);
// a word-free paraphrase the model reads as unrelated (~0.12) stays out,
// which is the limit of static vectors.
const meaningFloor = 0.25

// corpusIndex is a corpus's BM25 index and row vectors, by row place.
type corpusIndex struct {
	ix   *hybrid.Index
	vecs [][]float32
}

// corpus is every live text row a call reads (nearest store first; Refs
// and Day unset: they depend on the question), kept per process by the
// fingerprint of the lens cells it was read from.
type corpus struct {
	rows []textRow
	at   map[string]int // row id → its place in rows
	once sync.Once
	ci   *corpusIndex // built on first use (index)
	used uint64       // corpusTick at its last use (maxKeptCorpora)
}

// maxKeptCorpora bounds the corpora kept per process (one per set of
// stores a call reads: --store me, the repo's stores …); past it the
// least recently used goes.
const maxKeptCorpora = 4

var (
	corpusMu   sync.Mutex
	corpusKept = map[uint64]*corpus{}
	corpusTick uint64 // the use clock of corpus.used
	corpusSeed = maphash.MakeSeed()
)

// KeptBytes is the heap lore ask keeps across calls on purpose: the kept
// corpora with their indexes, row terms and vectors (devco D-902). The
// read server adds it to what it may keep (app serverLimits), so a big
// store's index does not make it exit after every call.
func KeptBytes() int64 {
	corpusMu.Lock()
	var n int64
	for _, c := range corpusKept {
		for i := range c.rows {
			n += int64(len(c.rows[i].Title)+len(c.rows[i].Body)) + 256 // the row, its strings and its map entry
		}
		if c.ci != nil {
			n += c.ci.ix.Bytes() + int64(len(c.ci.vecs))*24
		}
	}
	corpusMu.Unlock()
	return n + vectorBytes() + termBytes()
}

// corpusOf is the corpus of a text statement's answer (lines of textCols
// cells; ref lines left out): the kept one when the row lines are the same
// cells in the same order, else parsed now and kept.
func corpusOf(lines [][]value.Value) *corpus {
	var h maphash.Hash
	h.SetSeed(corpusSeed)
	for _, row := range lines {
		if len(row) < textCols || cell(row[colKind]) != "row" {
			continue
		}
		for _, c := range row[:colLower] {
			h.WriteString(cell(c))
			h.WriteByte(0)
		}
	}
	key := h.Sum64()
	corpusMu.Lock()
	c, ok := corpusKept[key]
	if ok {
		corpusTick++
		c.used = corpusTick
	}
	corpusMu.Unlock()
	if ok {
		return c
	}
	c = &corpus{at: map[string]int{}}
	for i := range pickLines(lines) {
		id := cell(lines[i][colID])
		c.at[id] = len(c.rows)
		r := rowOfLine(lines[i], id)
		// bug hunt H-214: every call copies its rows out of the kept
		// corpus (ask.go), so a word set or label built on the copy was
		// built again by the next call: a warm ask split all ~550 rows of
		// a four-store repo with a regexp each time (~15 ms). Built here
		// once, on the kept row, they travel with each copy (read only).
		contentOf(&r) // the word set too (tokens)
		r.firstTokens()
		labelOf(&r)
		c.rows = append(c.rows, r)
	}
	corpusMu.Lock()
	if len(corpusKept) >= maxKeptCorpora {
		var oldKey uint64
		var oldest *corpus
		for k, o := range corpusKept {
			if oldest == nil || o.used < oldest.used {
				oldKey, oldest = k, o
			}
		}
		delete(corpusKept, oldKey)
	}
	corpusTick++
	c.used = corpusTick
	corpusKept[key] = c
	corpusMu.Unlock()
	return c
}

// index is c's index, built on the first call (nil without the model);
// vf keeps vectors between processes (nil for none).
func (c *corpus) index(vf VectorFile) *corpusIndex {
	c.once.Do(func() { c.ci = buildIndex(c.rows, vf) })
	return c.ci
}

// textHash is the key a row's terms and vector are kept by: its text's
// FNV-64a (stable across processes: the vector file is keyed by it).
func textHash(text string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(text))
	return h.Sum64()
}

// docText is the text of r that BM25 and the vectors read.
func docText(r *textRow) string { return r.Title + "\n" + r.Body }

// buildIndex indexes rows (BM25 and vectors, by place); nil without the
// model. Terms and vectors kept by text hash are reused; the others are
// computed (vectors on up to 8 goroutines) and kept, in the process and,
// for vectors, in vf.
func buildIndex(rows []textRow, vf VectorFile) *corpusIndex {
	m, err := embed.Default()
	if err != nil {
		return nil
	}
	loadVectorFile(vf, m)
	docs := make([]hybrid.Doc, len(rows))
	hashes := make([]uint64, len(rows))
	ci := &corpusIndex{vecs: make([][]float32, len(rows))}
	var miss []int
	for i := range rows {
		docs[i].Text = docText(&rows[i])
		hashes[i] = textHash(docs[i].Text)
		docs[i].Terms = keptTerms(hashes[i], docs[i].Text)
		if v, hit := keptVector(hashes[i]); hit {
			ci.vecs[i] = v
		} else {
			miss = append(miss, i)
		}
	}
	embedAll(m, docs, miss, ci.vecs)
	all := make(map[uint64][]float32, len(rows))
	terms := make(map[uint64][]string, len(rows))
	for i := range docs {
		docs[i].Vec = ci.vecs[i]
		all[hashes[i]] = ci.vecs[i]
		terms[hashes[i]] = docs[i].Terms
	}
	keepCorpusVectors(all)
	keepCorpusTerms(terms)
	ci.ix = hybrid.Build(docs)
	if len(miss) > 0 && vf != nil {
		vf.Keep(m.Name(), m.Dim(), all) // best effort: a write that fails only costs the next call the embedding
	}
	return ci
}

var (
	termMu   sync.Mutex
	termKept = map[uint64][]string{}
)

// keptTerms is text's index terms (hybrid.Terms), kept by its hash: a
// rebuilt index stems only the rows that changed.
func keptTerms(k uint64, text string) []string {
	termMu.Lock()
	t, ok := termKept[k]
	termMu.Unlock()
	if ok {
		return t
	}
	t = hybrid.Terms(nil, text)
	if t == nil {
		t = []string{} // kept as no terms, not as "read the text"
	}
	return t
}

// keepCorpusTerms adds a corpus's terms (by text hash) to the process
// cache; past maxKeptVectors the cache becomes that corpus's terms alone.
func keepCorpusTerms(terms map[uint64][]string) {
	termMu.Lock()
	defer termMu.Unlock()
	maps.Copy(termKept, terms)
	if len(termKept) > maxKeptVectors {
		termKept = maps.Clone(terms)
	}
}

// termBytes is the heap the kept terms take (KeptBytes).
func termBytes() int64 {
	termMu.Lock()
	defer termMu.Unlock()
	var n int64
	for _, t := range termKept {
		n += 48 + int64(len(t))*16
		for _, w := range t {
			n += int64(len(w))
		}
	}
	return n
}

// meaningView is one question's view of a corpus: the index and each
// row's cosine to the question (by place), computed once per call and
// read by both the meaning join and the fusion.
type meaningView struct {
	ci  *corpusIndex
	at  map[string]int // row id → its place in the index
	cos []float64
}

// viewOf is q's view of c, indexed with vf; nil when the model cannot be
// read.
func viewOf(q question, c *corpus, vf VectorFile) *meaningView {
	ci := c.index(vf)
	if ci == nil {
		return nil
	}
	m, err := embed.Default()
	if err != nil {
		return nil
	}
	return &meaningView{ci: ci, at: c.at, cos: ci.ix.Cosine(m.Embed(q.text))}
}

// corpusOfRows is a corpus of rows not kept (a test's data, built
// straight from its rows).
func corpusOfRows(rows []textRow) *corpus {
	c := &corpus{rows: rows, at: map[string]int{}}
	for i := range rows {
		if _, dup := c.at[rows[i].ID]; !dup {
			c.at[rows[i].ID] = i
		}
	}
	return c
}

// nearest is the places of the rows nearest the question in meaning, at
// most n, each with a cosine of at least floor, best first, leaving out
// the places in skip.
func (v *meaningView) nearest(n int, floor float64, skip map[int]bool) []int {
	var out []int
	for i, c := range v.cos {
		if c >= floor && !skip[i] {
			out = append(out, i)
		}
	}
	slices.SortStableFunc(out, func(a, b int) int {
		switch {
		case v.cos[a] > v.cos[b]:
			return -1
		case v.cos[a] < v.cos[b]:
			return 1
		}
		return 0
	})
	return out[:min(n, len(out))]
}

// embedAll sets vecs[i] to the vector of docs[i] for each i of miss, on
// up to GOMAXPROCS goroutines (at most 8). Why: the first call on a store
// of thousands of rows embeds every row, ~80 µs each; split across cores
// the read server's first answer stays near its usual time.
func embedAll(m *embed.Model, docs []hybrid.Doc, miss []int, vecs [][]float32) {
	workers := min(runtime.GOMAXPROCS(0), 8, (len(miss)+63)/64)
	if workers <= 1 {
		for _, i := range miss {
			vecs[i] = m.Embed(docs[i].Text)
		}
		return
	}
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := w; n < len(miss); n += workers {
				i := miss[n]
				vecs[i] = m.Embed(docs[i].Text)
			}
		}()
	}
	wg.Wait()
}
