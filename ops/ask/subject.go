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
	"math"
	"strings"
)

// subjectTerms is how much each question word a row matched says about
// the question's subject across the store: weight is ln(1 + n/df) for a
// word df of the n scored rows hold, and rare holds the words at most a
// quarter of them hold (distinctBonus's distinctive words). Shared by every
// row of one answer; nil (every word 1, none rare) when fewer than
// rareFloor rows scored, where every word is rare.
type subjectTerms struct {
	weight map[string]float64
	rare   map[string]bool
}

// subjectTermsOf is the subject terms of q's words and topics among the scored
// rows all (wordSpread); nil below rareFloor rows.
func subjectTermsOf(all []scored) *subjectTerms {
	if len(all) < rareFloor {
		return nil
	}
	df := wordSpread(all)
	st := &subjectTerms{weight: make(map[string]float64, len(df)), rare: map[string]bool{}}
	n := float64(len(all))
	for w, d := range df {
		st.weight[w] = math.Log(1 + n/float64(d))
		if d*4 <= len(all) {
			st.rare[w] = true
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

// isRare reports whether w is one of the question's distinctive words.
func (st *subjectTerms) isRare(w string) bool {
	return st != nil && st.rare[strings.ToLower(w)]
}
