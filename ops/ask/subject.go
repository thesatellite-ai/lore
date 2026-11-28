package ask

// subject.go — the question's subject terms in a cut row's excerpt (bug
// hunt H-232, devco D-973, Sync benchmark set 6 ask 2: S6-01, 04, 26, 35,
// 40).
//
// When ask cuts a long note to its best sections (bestSections) or a row
// to its best sentences (keepSentences), it weighed each question word by
// how few of the note's own sections or sentences hold it. A word most
// rows of the store hold (job, run, first, return) weighed as much as the
// word only the asked thing holds (duplicate, an idempotency key, a
// Dockerfile name, a type name) when both sat in as few sections of the
// note, so the section repeating three common words beat the one holding
// the subject and the subject section was left out (S6-01 lost "a
// duplicate while the first run is non-terminal returns the first run's
// id"). The words the scored rows of the store rarely hold are the
// question's subject: they weigh more in the excerpt too, the same spread
// distinctBonus ranks rows by, read once per ask.

import (
	"strings"

	"github.com/thesatellite-ai/lore/logic/hybrid"
)

// subjectTerms is how much each question word a row matched says about
// the question's subject across the store: weight is the word's BM25 IDF
// (hybrid.IDF) over the index the rows were scored with, and rare holds
// the words at most a quarter of its texts hold (distinctBonus's
// distinctive words). Shared by every row of one answer; nil (every word
// 1, none rare) when fewer than rareFloor rows scored, where every word is
// rare.
//
// Why the index's IDF (devco D-985, hunt H-244, owner decision
// 2026-11-25 item 2): the excerpt picks a long row's sections and
// sentences by these weights; read from the same index and the same
// function the ranking scores rows with, the excerpt keeps the words that
// made the row rank instead of a second count over the scored rows.
type subjectTerms struct {
	weight map[string]float64
	rare   map[string]bool
	// key are the question's key words (keyFrac): the words a long
	// note must hold to take a full slot (slots.go).
	key map[string]bool
}

// subjectTermsOf is the subject terms of q's words and topics among the
// scored rows all (wordSpread), weighed over ix (the answer's word index;
// over all itself when nil, or for a word ix does not hold); nil below
// rareFloor rows.
func subjectTermsOf(all []scored, ix *hybrid.Index) *subjectTerms {
	if len(all) < rareFloor {
		return nil
	}
	df := wordSpread(all)
	st := &subjectTerms{weight: make(map[string]float64, len(df)), rare: map[string]bool{}, key: map[string]bool{}}
	best := 0.0 // the heaviest word's weight
	for w, d := range df {
		n, wt := len(all), 0.0
		if ix != nil {
			n, wt = ix.Len(), ix.TermIDF(w)
		}
		if wt == 0 {
			n, wt = len(all), hybrid.IDF(len(all), d)
		}
		st.weight[w] = wt
		if wt >= hybrid.IDF(n, max(n/4, 1)) {
			st.rare[w] = true
		}
		best = max(best, wt)
	}
	for w, wt := range st.weight {
		if wt >= keyFrac*best {
			st.key[w] = true
		}
	}
	return st
}

// wordSpread is, for each term a scored row matched, how many of all
// match it.
func wordSpread(all []scored) map[string]int {
	df := map[string]int{}
	for _, s := range all {
		for _, w := range s.matched {
			df[strings.ToLower(w)]++
		}
	}
	return df
}

// of is word w's weight, 1 for a word the scored rows did not match or
// with no subject terms.
func (st *subjectTerms) of(w string) float64 {
	if st == nil {
		return 1
	}
	if v, ok := st.weight[strings.ToLower(w)]; ok {
		return v
	}
	return 1
}

// isKey reports whether w is one of the question's key words (keyFrac).
func (st *subjectTerms) isKey(w string) bool {
	return st != nil && st.key[strings.ToLower(w)]
}

// isRare reports whether w is one of the question's distinctive words.
func (st *subjectTerms) isRare(w string) bool {
	return st != nil && st.rare[strings.ToLower(w)]
}
