package ask

// idents.go — code identifiers and distinctive words in ask's ranking
// (bug hunt H-230, devco D-971, Sync benchmark set 5 S5-06 and S5-11).
//
// A short memory that says "group.Create returns ErrGroupNameTaken" is
// the answer to "what happens when a custom group uses a taken group
// name", but its words were one token, errgroupnametaken, which holds
// none of the question's words; a decision and filler notes sharing the
// common words (group, name, builtin) ranked above it, and the on-topic
// hotfix naming JobTimeLimit lost to an off-topic hotfix sharing
// "sync workflow run". Three parts fix the class:
//
//   - a row's word set also holds the parts of every code identifier it
//     writes (ErrGroupNameTaken: err group name taken; group.Create;
//     job-time-limit); snake_case already splits at the underscore
//   - a row whose one identifier names two or more of the question's
//     words names the asked thing (identBonus), as a phrase would
//   - a question word few of the scored rows hold is distinctive and
//     counts a point more than one most rows hold (distinctBonus): a row
//     matching the question's rare words beats one matching only the
//     common ones
//
// A question that writes an identifier (JobTimeLimit) finds a row that
// spells its parts out (the job time limit) the same way (identWord).

import (
	"slices"
	"strings"
	"unicode"
)

// identParts is the lower-case parts of identifier w when it is one:
// split at dots and hyphens and where the case changes (ErrGroupNameTaken,
// group.Create, HTTPServer, job-time-limit), parts without a letter left
// out (v0.1.47 is no identifier of words); nil for a plain word, a word
// with an apostrophe, or when fewer than two parts are left.
func identParts(w string) []string {
	if !mayBeIdent(w) || strings.ContainsAny(w, "'’") {
		return nil // a plain word, or one with an apostrophe (don't, falcon's)
	}
	var parts []string
	rs := []rune(w)
	start := 0
	flush := func(end int) {
		if end > start {
			p := strings.ToLower(string(rs[start:end]))
			if strings.IndexFunc(p, unicode.IsLetter) >= 0 {
				parts = append(parts, p)
			}
		}
		start = end
	}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '.' || r == '-':
			flush(i)
			start = i + 1
		case i > start && unicode.IsUpper(r) && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])):
			// errRole: a capital after a small letter starts a part
			flush(i)
		case i > start && unicode.IsUpper(r) && unicode.IsUpper(rs[i-1]) && i+1 < len(rs) && unicode.IsLower(rs[i+1]):
			// HTTPServer: the last capital of a run starts the next part
			flush(i)
		}
	}
	flush(len(rs))
	if len(parts) < 2 {
		return nil
	}
	return parts
}

// mayBeIdent reports whether w can be an identifier: it holds a dot or a
// hyphen, or a capital after its first letter. Why: tokensOf runs on every
// word of every row a cold ask reads; a plain word (the most of them)
// costs one scan, no allocation.
func mayBeIdent(w string) bool {
	for i, r := range w {
		if r == '.' || r == '-' || i > 0 && unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// hasIdent reports whether text s can hold an identifier: a small letter
// or digit followed by a capital, or a dot or hyphen between two letters
// or digits. A byte scan, so a row of prose skips identsOf's word split.
func hasIdent(s string) bool {
	alnum := func(b byte) bool {
		return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b >= 0x80
	}
	for i := 1; i < len(s); i++ {
		b, p := s[i], s[i-1]
		switch {
		case b >= 'A' && b <= 'Z' && (p >= 'a' && p <= 'z' || p >= '0' && p <= '9' || p >= 'A' && p <= 'Z' && i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z'):
			return true
		case (b == '.' || b == '-') && alnum(p) && i+1 < len(s) && alnum(s[i+1]):
			return true
		case b >= 0x80:
			return true // not ASCII: leave it to identParts
		}
	}
	return false
}

// identsOf is the parts of every identifier in s (identParts), each once.
func identsOf(s string) [][]string {
	if !hasIdent(s) {
		return nil
	}
	var out [][]string
	seen := map[string]bool{}
	for _, w := range reWord.FindAllString(s, -1) {
		if seen[w] {
			continue
		}
		seen[w] = true
		if ps := identParts(w); ps != nil {
			out = append(out, ps)
		}
	}
	return out
}

// idents is the identifiers of r's title and body (identsOf), built on
// the first call and kept on r (and on the kept corpus row it was copied
// from).
func (r *textRow) idents() [][]string {
	if !r.identsBuilt {
		r.identList = shareSplit(r.split, func(s *rowSplit) *splitSlot[[][]string] { return &s.idents }, func() [][]string {
			return identsOf(r.Title + "\n" + r.Body)
		})
		r.identsBuilt = true
	}
	return r.identList
}

// identStep is what each question word past the first that one of a
// row's identifiers names adds to its score: ErrGroupNameTaken naming
// "taken group name" adds 4, more than one shared common word (3), so
// the row naming the asked thing beats a row holding one more common
// word.
const identStep = 2

// identBonus is what r gains for naming the asked thing in one
// identifier: identStep for each question word past the first that the
// best of its identifiers names; 0 when none names two. Words that name
// a matched resource do not count (namesResource).
func identBonus(q question, ms []match, r *textRow) int {
	ids := r.idents()
	if len(ids) == 0 || len(q.words) < 2 {
		return 0
	}
	best := 0
	// devco D-972: a question that writes an identifier (vault-rekey,
	// set-when-blank) names the asked thing as a phrase, and a row writing
	// the same identifier names it too
	toks := r.tokens()
	for _, w := range q.words {
		if ps := q.parts[w]; len(ps) >= 2 && toks[w] && !namesResource(w, ms) {
			best = max(best, len(ps))
		}
	}
	set := map[string]bool{}
	for _, ps := range ids {
		clear(set)
		for _, p := range ps {
			set[p] = true
		}
		n := 0
		for _, w := range q.words {
			if !namesResource(w, ms) && hasWord(set, w) {
				n++
			}
		}
		best = max(best, n)
	}
	if best < 2 {
		return 0
	}
	return identStep * (best - 1)
}

// identWord reports whether toks hold every part of question word w when
// the question wrote it as an identifier (JobTimeLimit, q.parts): the
// row that spells out "the job time limit" answers it.
func identWord(q question, toks map[string]bool, w string) bool {
	ps := q.parts[w]
	if len(ps) < 2 {
		return false
	}
	for _, p := range ps {
		if !hasWord(toks, p) {
			return false
		}
	}
	return true
}

// rareFloor is the fewest scored rows distinctBonus reads a word's spread
// from: among a handful every word is rare.
const rareFloor = 8

// distinctBonus adds a point to each scored row for each question word
// it holds that at most a quarter of the scored rows hold: the
// distinctive words (taken, limit) say more than the common ones
// every row shares (group, workflow). Less than one question word (3), so
// a row holding one more of the question's words still wins (H-230).
//
// It also takes back the headWord a long row's best section heading
// added (scored.heads) for each heading word or topic that more than a
// quarter of the scored rows hold (devco D-972, bug hunt H-231): a
// heading "Commands" or "Mirror" over a note's common words is no reason
// to rank the note above the short row naming the asked command, which
// lore search ranks first. A word a long row holds only outside its best
// section (scored.outside) is never distinctive for that row. Among
// fewer than rareFloor scored rows every word is distinctive and nothing
// changes.
func distinctBonus(q question, all []scored) {
	if len(all) < rareFloor {
		return
	}
	df := wordSpread(all) // subject.go: the same spread a cut row's excerpt weighs words by
	rare := func(w string) bool { return df[strings.ToLower(w)]*4 <= len(all) }
	for i := range all {
		s := &all[i]
		for _, w := range s.matched {
			if isWord(q, w) && rare(w) && !slices.Contains(s.outside, w) {
				s.score++
			}
		}
		for _, h := range s.heads {
			if !rare(h) {
				s.score -= headWord
			}
		}
	}
}

// isWord reports whether w is one of q's words (not a topic name).
func isWord(q question, w string) bool {
	return slices.Contains(q.words, w)
}
