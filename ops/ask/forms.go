package ask

// forms.go — word forms in ask's matching (bug hunt H-235, devco D-976,
// Sync benchmark set 6 item 5: S6-05, S6-28, S6-34 and the sibling
// questions S6-27/28/29).
//
// lore search stems every word of the question and of the rows (the
// full-text index's Porter stemmer), so "which fields does the catalog
// validate" finds the note on "catalog validation rules" first, as does
// "how are catalog validations run". lore ask matched a question word to
// a row word only exactly or as a prefix three letters off (rule for
// rules), so validate missed validation, jobs missed job, enqueuing
// missed enqueue and re-encrypted missed re-encrypts. A row about
// something else that wrote the question's other words in the
// question's own form (one writer per catalog table, writing "product
// fields and rules") then ranked first, and sibling questions worded
// differently got different rows.
//
// Every word set tokensOf builds also holds the stem of each of its
// words (under stemMark) whose stem is not the word itself, and hasWord
// matches a question word whose stem one of them holds: the forms search
// joins, ask joins. The stems are built with the word set, once per row
// and shared by the kept corpus rows (rowSplit), so a warm ask pays one
// map lookup per question word and row, no stemming.

import (
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/logic/hybrid"
)

// stemMark starts the key under which a word set holds a stem of its
// words: no word reWord finds starts with it, so a stem key is never
// taken for a word (hasWord's prefix forms, contentOf, heldByAny skip
// it).
const stemMark = "\x00"

// isStemKey reports whether word-set key k is a stem key (stemMark).
func isStemKey(k string) bool { return strings.HasPrefix(k, stemMark) }

// maxStems bounds the process's stem cache: past it the cache starts
// again, so a long-lived read server reading many stores stays small.
const maxStems = 200_000

var (
	stemMu    sync.RWMutex
	stemCache = map[string]string{}
)

// stemOf is the index stem of lower-case word w (hybrid.Stem: Porter
// for 3..64 bytes of ASCII), and false for a word that has no form to
// match: shorter than three letters, or holding a hyphen or a dot (its
// parts are matched instead, identWord). Cached per process: a store
// repeats its words in every row.
func stemOf(w string) (string, bool) {
	if len(w) < 3 || strings.ContainsAny(w, "-.'’") {
		return "", false
	}
	stemMu.RLock()
	s, ok := stemCache[w]
	stemMu.RUnlock()
	if ok {
		return s, true
	}
	s = hybrid.Stem(w)
	stemMu.Lock()
	if len(stemCache) >= maxStems {
		clear(stemCache)
	}
	stemCache[w] = s
	stemMu.Unlock()
	return s, true
}

// addStems adds to word set toks the stem key of each of its words whose
// stem is not the word itself (a word that is its own stem is found as
// itself) and returns toks.
func addStems(toks map[string]bool) map[string]bool {
	var keys []string
	for w := range toks {
		if s, ok := stemOf(w); ok && s != w {
			keys = append(keys, stemMark+s)
		}
	}
	for _, k := range keys {
		toks[k] = true
	}
	return toks
}

// sameStem reports whether toks holds a word with the stem of question
// word w: a word whose stem key toks holds (addStems), or the stem
// itself written as a word that is its own stem (job for jobs).
func sameStem(toks map[string]bool, w string) bool {
	s, ok := stemOf(w)
	if !ok {
		return false
	}
	if toks[stemMark+s] {
		return true
	}
	if s == w || !toks[s] {
		return false
	}
	own, _ := stemOf(s)
	return own == s
}
