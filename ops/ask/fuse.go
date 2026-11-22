package ask

import (
	"hash/fnv"
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
// The fusion runs over the rows ask scored (the rows holding a question
// word), the same set whether the read server or a one-shot call answers,
// so both print the same answer byte for byte. A row's vector is kept
// per process by its text's hash: the read server embeds a row once,
// not per question; a one-shot call embeds at most maxEmbedded rows.

// maxEmbedded bounds the rows a question's vectors are computed for: the
// first ones by lore's score, then BM25 (a one-shot call with a word held
// by thousands of rows embeds these only, ~40 µs each). Twice the fusion
// pool: a row past it could not enter the vector source's pool anyway
// unless BM25 ranked it below a hundred tied rows.
const maxEmbedded = 2 * hybrid.Pool

// maxKeptVectors bounds the process's vector cache (~1 KB each): past it
// the cache starts again, so a long-lived read server stays under its
// heap limit on a big store.
const maxKeptVectors = 50_000

var (
	vecMu   sync.Mutex
	vecKept = map[uint64][]float32{}
)

// vectorOf is text's vector from the process cache, embedded on a miss.
func vectorOf(m *embed.Model, text string) []float32 {
	h := fnv.New64a()
	h.Write([]byte(text))
	k := h.Sum64()
	vecMu.Lock()
	v, ok := vecKept[k]
	vecMu.Unlock()
	if ok {
		return v
	}
	v = m.Embed(text)
	vecMu.Lock()
	if len(vecKept) >= maxKeptVectors {
		clear(vecKept)
	}
	vecKept[k] = v
	vecMu.Unlock()
	return v
}

// fuseScores sets each scored row's fused score for q (wsum50 of BM25
// over the scored rows and the cosine to the question). Vectors are
// computed only for the rows that can reach the answer: lore's score
// bands down to the 2×top-th row (a margin for rows history and the
// folder picks move), at most maxEmbedded; a row below gets its BM25
// share alone. Why (devco D-894): embedding every scored row made the
// first call after a change ~7 ms slower on the recall gate. Without the
// model (it cannot be read) every fused score stays 0 and the order is
// the one before: newest first among equals.
func fuseScores(q question, all []scored, top int) {
	if len(all) < 2 {
		return
	}
	m, err := embed.Default()
	if err != nil {
		return
	}
	words := append(slices.Clone(q.words), topicWords(q)...)
	docs := make([]hybrid.Doc, len(all))
	for i := range all {
		docs[i].Text = all[i].row.Title + "\n" + all[i].row.Body
	}
	bm := hybrid.Build(docs).BM25(words)
	// the rows whose vectors are worth computing: lore's best first, then
	// BM25's
	order := make([]int, len(all))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		if all[a].score != all[b].score {
			return all[b].score - all[a].score
		}
		switch {
		case bm[a] > bm[b]:
			return -1
		case bm[a] < bm[b]:
			return 1
		}
		return 0
	})
	cutoff := all[order[min(2*top, len(order))-1]].score
	qv := m.Embed(q.text)
	cos := make([]float64, len(all))
	for n, i := range order {
		if n >= maxEmbedded || all[i].score < cutoff {
			break
		}
		cos[i] = float64(embed.Cosine(qv, vectorOf(m, docs[i].Text)))
	}
	fused := hybrid.Fuse(bm, cos, hybrid.Pool)
	for i := range all {
		all[i].fused = fused[i]
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
