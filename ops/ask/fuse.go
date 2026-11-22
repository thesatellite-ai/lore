package ask

import (
	"maps"
	"slices"
	"sync"

	"github.com/thesatellite-ai/lore/logic/embed"
	"github.com/thesatellite-ai/lore/logic/hybrid"
)

// fuse.go — the meaning-and-words order of ask's free-text rows (devco
// D-894, hunt H-149). Lore's integer score (subject, topics, words held)
// still decides the bands; inside a band the rows were newest first, so
// a busy folder's newest row that merely shared a word outranked the
// older row that answered. Now a band is ordered by wsum50 (logic/hybrid)
// of stemmed BM25 and the static-vector cosine to the question, newest
// first only among rows the fusion cannot tell apart.
//
// The fusion runs over every live row the call reads (corpus.go, devco
// D-902), the same set whether the read server or a one-shot call
// answers, so both print the same answer byte for byte.

// maxKeptVectors bounds the process's vector cache (~1 KB each) beyond
// the corpus in use: past it the cache keeps only the vectors of the
// corpus just indexed (keepCorpusVectors), so a long-lived read server
// drops the vectors of rows gone or changed, never the ones it reads.
const maxKeptVectors = 50_000

// VectorFile keeps row vectors between processes, by text hash (devco
// D-902): a one-shot ask reads the vectors of the rows it saw before and
// embeds only the new ones. Env may implement it (wire keeps a file under
// the lore home); without it vectors live as long as the process.
type VectorFile interface {
	// Vectors are the kept vectors of model (name, width); empty when
	// none are kept or the file is of another model.
	Vectors(model string, dim int) map[uint64][]float32
	// Keep adds vecs, the vectors of the corpus a call just indexed, to
	// the file (best effort); a file past its bound keeps vecs alone, so
	// the rows in use are never dropped.
	Keep(model string, dim int, vecs map[uint64][]float32)
}

var (
	vecMu     sync.Mutex
	vecKept   = map[uint64][]float32{}
	vecLoaded bool
)

// loadVectorFile seeds the process cache from vf once per process.
func loadVectorFile(vf VectorFile, m *embed.Model) {
	if vf == nil {
		return
	}
	vecMu.Lock()
	done := vecLoaded
	vecLoaded = true
	vecMu.Unlock()
	if done {
		return
	}
	got := vf.Vectors(m.Name(), m.Dim())
	vecMu.Lock()
	maps.Copy(vecKept, got)
	vecMu.Unlock()
}

// keptVector is the process's vector of a text hash.
func keptVector(k uint64) ([]float32, bool) {
	vecMu.Lock()
	defer vecMu.Unlock()
	v, ok := vecKept[k]
	return v, ok
}

// keepCorpusVectors adds a corpus's vectors (by text hash) to the
// process cache; past maxKeptVectors the cache becomes that corpus's
// vectors alone.
func keepCorpusVectors(vecs map[uint64][]float32) {
	vecMu.Lock()
	defer vecMu.Unlock()
	maps.Copy(vecKept, vecs)
	if len(vecKept) > maxKeptVectors {
		vecKept = maps.Clone(vecs)
	}
}

// vectorBytes is the heap the process's vectors take (KeptBytes).
func vectorBytes() int64 {
	vecMu.Lock()
	defer vecMu.Unlock()
	var n int64
	for _, v := range vecKept {
		n += int64(4*len(v)) + 48 // the floats, the slice header and map entry
	}
	return n
}

// fuseScores sets each scored row's fused score for q: wsum50 of BM25 over
// the whole corpus (the store's word weights, not the scored rows') and
// the row's cosine to the question (meaningView, computed once per call;
// devco D-902). The pools are the corpus's: a scored row outside both
// gets 0. Without the view (the model cannot be read) every fused score
// stays 0 and the order is the one before: newest first among equals.
func fuseScores(q question, all []scored, v *meaningView) {
	if len(all) < 2 || v == nil {
		return
	}
	words := append(slices.Clone(q.words), topicWords(q)...)
	fused := hybrid.Fuse(v.ci.ix.BM25(words), v.cos, hybrid.Pool)
	for i := range all {
		if p, ok := v.at[all[i].row.ID]; ok {
			all[i].fused = fused[p]
		}
	}
}

// topicWords are the row words of q's topics (a status question's
// "status", "update" …): BM25 weighs them like the question's own words.
func topicWords(q question) []string {
	var out []string
	for _, t := range q.topics {
		out = append(out, t.row...)
	}
	return out
}
