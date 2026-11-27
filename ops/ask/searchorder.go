package ask

// searchorder.go — lore ask ranks its rows as lore search does (devco
// D-984, owner decision 2026-11-25 item 1).
//
// Why: ask kept a scorer of its own, tuned per blind question (as-built
// notes first, the linked folder first, live and recently verified facts
// first, section and identifier bonuses …). Each tune passed its own
// cases and missed the next blind set, while search, judged on every
// question, held. Ask now orders the rows it read the way search orders
// its hits, with search's own pieces (logic/searchrank), plus the inputs
// the gates prove (the linked folder, fact state, a long row's best
// section, identifiers, topics):
//
//	question ─▶ searchrank.Of: the same topic words search plans
//	         ─▶ per row: words held = words one of whose forms
//	            (searchrank.Forms, search's per-word match) the row holds
//	            in the index's analysis (hybrid.Terms, FTS5's tokenizer and
//	            porter stemmer), or its text covers (opsmemory.Held)
//	         ─▶ per kind, in bm25 order: searchrank.Order (words held,
//	            names, title share, newest among full-title rows) ─▶ Score
//	         ─▶ every kind merged by Score, a stale fact at 0.9 of it
//	            (opssearch.RankScore) and its words held counted by their
//	            rarity (opssearch.Rerank, devco:D-1007), equal scores in
//	            kind order
//
// Calling `lore search` itself costs 200-300 ms a call (a full-text query
// per word per kind and every hit's row file read), five times ask's
// 50 ms budget; ask has every row's text and a BM25 index of the same
// parameters already (hybrid, Lucene's k1/b as FTS5's bm25()), so the
// order costs a few postings walks.

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/hybrid"
	"github.com/thesatellite-ai/lore/logic/querywords"
	"github.com/thesatellite-ai/lore/logic/searchrank"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	"github.com/thesatellite-ai/lore/schema"
)

// searchStaleRank is the share of its score a stale fact ranks with, as
// in search (opssearch.RankScore; devco:D-941).
const searchStaleRank = 0.9

// searchOrder is lore search's order of rows for q: row id to its place
// (0 first), a word naming a resource ms matched read as its name
// (resourceTerms). A row holding none of the question's words has no place;
// nil when the question has no word search ranks by. v is the call's
// meaning view (its BM25 index weighs words by the whole store, as a
// kind's index does); without one an index of rows alone is built.
func searchOrder(q question, ms []match, rows []textRow, v *meaningView) map[string]int {
	w := searchrank.Of(q.text)
	if len(w.Terms) == 0 || len(rows) == 0 {
		return nil
	}
	if len(q.topics) > 0 && !slices.ContainsFunc(w.Terms, func(t string) bool { _, ok := topicOf(t); return !ok }) {
		return nil // only topic words: ask's own reading of the topics orders the rows
	}
	w.Terms = resourceTerms(w.Terms, ms)
	if len(q.topics) > 0 {
		// a word that names a topic ("along" of how far along, "decide")
		// is read as the topic below, not as itself: "ran along the river"
		// does not answer how far along a project is
		w.Terms = slices.DeleteFunc(w.Terms, func(t string) bool { _, ok := topicOf(t); return ok })
	}
	ix, at := searchIndex(rows, v)
	// a row holds a word when it holds every index term of one of the
	// word's forms (searchrank.Forms: search's match of the word; a form
	// of several terms, e_auth_failed or on-call, is a phrase there)
	present := q.search.bm25Of(ix) // index term → its BM25 per text
	termsOf := func(form string) []string {
		ts := hybrid.Terms(nil, form)
		for _, t := range ts {
			if _, ok := present[t]; !ok {
				present[t] = ix.BM25([]string{t})
			}
		}
		return ts
	}
	forms := make([][][]string, len(w.Terms), len(w.Terms)+len(q.topics)) // word → form → index terms
	for i, t := range w.Terms {
		for _, f := range searchrank.Forms(t) {
			forms[i] = append(forms[i], termsOf(f))
		}
	}
	// a topic the question names (decision, status, budget …: question.go)
	// is one more word, held by any of its row words: "what did we decide
	// and why" is answered by "we chose … because", which no form of
	// decide finds (kept from ask's reading of the question: it lifts the
	// held-out why and status questions, see docs/knowledge/ask.md)
	for _, tp := range q.topics {
		var fs [][]string
		for _, rw := range tp.row {
			fs = append(fs, termsOf(rw))
		}
		forms = append(forms, fs)
	}
	// held reports whether text p (row r) holds word and adds the bm25
	// of each form it holds to *bm, as FTS5's bm25 sums the phrases of
	// the any-word match a row holds
	held := func(word, p int, r *textRow, seq *[]string, bm *float64) (bool, int) {
		ok, size := false, 0
		for _, ts := range forms[word] {
			if len(ts) == 0 || slices.ContainsFunc(ts, func(t string) bool { return present[t][p] == 0 }) {
				continue
			}
			if len(ts) > 1 {
				// a phrase: its terms next to each other, in order
				if *seq == nil {
					// the index terms the corpus keeps by text (corpus.go):
					// a long note is stemmed once per process, not per call
					t := docText(r)
					*seq = keptTerms(textHash(t), t)
				}
				if !hasRun(*seq, ts) {
					continue
				}
			}
			for _, t := range ts {
				*bm += present[t][p]
			}
			ok, size = true, max(size, len(ts))
		}
		return ok, size
	}
	type cand struct {
		row   *textRow
		bm25  float64
		held  int
		fact  int // factOrder
		heads int // question words its best section's heading holds
		// got is which words (terms, then topics) it holds; headNames the
		// words and topic names its best section's heading holds
		got       []bool
		headNames []string
	}
	byTable := map[string][]cand{}
	var tables []string
	// df is how many rows hold each question word in the index, as each
	// word's own search counts them in lore search (devco:D-1007)
	df := make([]int, len(w.Terms))
	for i := range rows {
		r := &rows[i]
		p, ok := at(r.ID)
		if !ok {
			continue
		}
		c := cand{row: r, fact: factOrder(r)}
		var seq []string // r's index terms in order, read for a phrase only
		got := make([]bool, len(forms))
		phraseExtra := 0
		for k := range forms {
			var size int
			got[k], size = held(k, p, r, &seq, &c.bm25)
			if k >= len(w.Terms) || !got[k] {
				continue
			}
			df[k]++
			// a question term naming several words (E_QUOTA_MAX: several
			// index terms; JobTimeLimit: one index term, its parts in
			// q.parts) held as written names the asked thing as one thing:
			// as many words more less one, the identifier rule below
			if size > 1 {
				phraseExtra += size - 1
			} else if ps := q.parts[w.Terms[k]]; len(ps) > 1 {
				phraseExtra += len(ps) - 1
			}
		}
		identExtra, identBM := identHeld(q, r, w.Terms, got, ix, p)
		identExtra += phraseExtra
		c.bm25 += identBM
		sec := rankSectionOf(q, ms, r)
		if sec != nil {
			// a long row holds a word only when its best section or, with
			// no sections, its best passage (or its title and first line)
			// does (longsec.go, devco D-972, D-1030)
			for k := range got {
				if got[k] && !sectionHolds(q, r, sec, w.Terms, k) {
					got[k] = false
				}
			}
		}
		byIndex := 0
		for _, g := range got {
			if g {
				byIndex++
			}
		}
		c.held = byIndex
		if sec == nil {
			// opsmemory.Held: the words the row's own text covers count too
			c.held = max(byIndex, querywords.Coverage(docText(r), w.Terms))
		} else if c.held > 0 {
			// the heading of the best section naming the asked words says
			// the section answers them, as a short row's title does (D-969);
			// a tie-break, never one more word: a "Commands" heading over a
			// note's common words is no reason to pass the short row naming
			// the asked command (D-972)
			c.heads = len(sec.heldHeads)
			c.headNames = sec.heldHeads
		}
		c.got = got
		if c.held == 0 {
			continue // search's any-word match does not find it
		}
		c.held += identExtra
		if _, seen := byTable[r.Table]; !seen {
			tables = append(tables, r.Table)
		}
		byTable[r.Table] = append(byTable[r.Table], c)
	}
	distinctHeads(q, w.Terms, byTable, func(c *cand) ([]bool, []string, *int) { return c.got, c.headNames, &c.held })
	// a stale fact's words count by their rarity, as in search
	// (opssearch.Rerank, devco:D-1007): a stale row holding the question's
	// common words does not pass a fresh row holding its rare one
	share := searchrank.Share(df)
	type hit struct {
		id    string
		held  float64
		fact  int
		heads int
		home  bool // a row of the repo's linked folder (q.home)
		span  bool // it writes a code span of the question (spans.go)
		// rare is the rarity share (searchrank.Share) of the rarest
		// question word it holds, 1 for a word the fewest rows hold
		rare  float64
		stale bool
		score float64
		table int
	}
	// equal scores keep search's kind order (opssearch.Targets: by entity
	// name, the same order as the table names ask reads)
	slices.Sort(tables)
	var hits []hit
	for ti, t := range tables {
		cs := byTable[t]
		// bm25 order within the kind, as the index returns its hits
		// (ties by id: one order every run)
		slices.SortStableFunc(cs, func(a, b cand) int {
			if c := cmp.Compare(b.bm25, a.bm25); c != 0 {
				return c
			}
			return cmp.Compare(a.row.ID, b.row.ID)
		})
		items := make([]searchrank.Item, len(cs))
		for i, c := range cs {
			items[i] = searchrank.Item{Label: searchrank.Label(c.row.Title, c.row.Body), Matched: c.held, At: searchDate(c.row)}
		}
		for _, rk := range w.Order(items, len(items)) {
			c := cs[rk.Index]
			s := rk.Score
			if c.row.Stale != "" {
				s *= searchStaleRank
			}
			home := q.home != "" && c.row.Place == q.home
			held := float64(c.held)
			if c.row.Stale != "" {
				held *= searchrank.Weight(c.got[:len(w.Terms)], make([]bool, len(w.Terms)), share)
			}
			rare := 0.0
			for k, g := range c.got[:len(w.Terms)] {
				if g {
					rare = max(rare, share[k])
				}
			}
			hits = append(hits, hit{id: c.row.ID, held: held, fact: c.fact, heads: c.heads, home: home, span: holdsSpan(c.row, q.spans), rare: rare, stale: c.row.Stale != "", score: s, table: ti})
		}
	}
	// a row writing a code span of the question first (spans.go), then
	// words held (one more word always wins, as in Score; a stale
	// fact's words by their rarity, devco:D-1007), then
	// the repo's linked folder before another folder or client, then the
	// row holding the rarer question word (devco D-1028, bug hunt H-288:
	// a large as-built note holding as many of the question's common
	// words led over the short decision holding the one word only it
	// held; realeval TUNE answer first 119 -> 123 of 198), then
	// what the row is (factOrder: an as-built note or a live fact before
	// a planned one), then a fresh fact before a stale one, then Score.
	// Why the folder (devco D-963, kept by D-984): the owner's set-4 rule
	// "rows of the repo's linked folder first, other clients' folders
	// only when nothing local matches" scopes the order, it is not a
	// relevance boost; it never outweighs one more question word.
	slices.SortStableFunc(hits, func(a, b hit) int {
		if a.span != b.span {
			// the row writing the command the question quotes names the
			// asked thing (spans.go, devco D-1028)
			if a.span {
				return -1
			}
			return 1
		}
		if c := cmp.Compare(b.held, a.held); c != 0 {
			return c
		}
		if a.home != b.home {
			if a.home {
				return -1
			}
			return 1
		}
		if c := cmp.Compare(b.rare, a.rare); c != 0 {
			return c
		}
		if c := cmp.Compare(b.fact, a.fact); c != 0 {
			return c
		}
		if c := cmp.Compare(b.heads, a.heads); c != 0 {
			return c
		}
		if a.stale != b.stale {
			if a.stale {
				return 1
			}
			return -1
		}
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		return cmp.Compare(a.table, b.table)
	})
	out := make(map[string]int, len(hits))
	for i, h := range hits {
		out[h.id] = i
	}
	if q.search != nil {
		q.search.held = make(map[string]float64, len(hits))
		for _, h := range hits {
			q.search.held[h.id] = h.held
		}
	}
	return out
}

// identHeld marks in got (by terms' index) the words row r holds as an
// identifier, which the index reads as one word, and says how much that
// adds: extra words held and bm25. A row writing ErrGroupNameTaken holds
// "taken group name" (two or more of the words in one of its identifiers,
// idents.go); a row spelling out "the job time limit" holds the
// question's JobTimeLimit (identWord). An identifier naming k >= 2 of the
// question's words names the asked thing as one thing: k-1 words more, as
// ask's identifier bonus weighed it (H-230, devco D-972), and its index
// term's bm25 (the identifier is rare where the words are common). Kept
// (coordinator decision on H-243): the set-5 identifier cases of the
// recall gate need it, and search finds neither.
func identHeld(q question, r *textRow, terms []string, got []bool, ix *hybrid.Index, p int) (extra int, bm float64) {
	if len(q.parts) > 0 {
		var toks map[string]bool
		for k, t := range terms {
			ps := q.parts[t]
			if len(ps) < 2 {
				continue
			}
			if toks == nil {
				toks = r.tokens()
			}
			if !got[k] && identWord(q, toks, t) && spellsOut(r, ps) {
				got[k] = true
			}
		}
	}
	ids := r.idents()
	if len(ids) == 0 || len(terms) < 2 {
		return extra, 0
	}
	set := map[string]bool{}
	var best []int
	var bestID []string
	for _, ps := range ids {
		clear(set)
		for _, p := range ps {
			set[p] = true
		}
		var in []int
		for k, t := range terms {
			if hasWord(set, t) {
				in = append(in, k)
			}
		}
		if len(in) > len(best) {
			best, bestID = in, ps
		}
	}
	if len(best) < 2 {
		return extra, 0
	}
	for _, k := range best {
		got[k] = true
	}
	return extra + len(best) - 1, ix.BM25([]string{strings.Join(bestID, "")})[p]
}

// distinctHeads adds to each long row's words held (*held) one word for
// each word or topic its best section's heading holds that at most a
// quarter of the candidate rows hold (every one below rareFloor rows):
// a heading naming the asked thing says the section answers it, as a
// short row's title does (devco D-969: S5-29/33/39 of the recall gate),
// while a "Commands" heading over a note's common words is no reason to
// pass the short row naming the asked command (D-972: S6-06).
func distinctHeads[C any](q question, terms []string, byTable map[string][]C, of func(*C) ([]bool, []string, *int)) {
	n := 0
	var df []int
	for _, cs := range byTable {
		for i := range cs {
			got, _, _ := of(&cs[i])
			if df == nil {
				df = make([]int, len(got))
			}
			for k, g := range got {
				if g {
					df[k]++
				}
			}
			n++
		}
	}
	index := map[string]int{}
	for k, t := range terms {
		index[t] = k
	}
	for i, tp := range q.topics {
		index[tp.name] = len(terms) + i
	}
	for _, cs := range byTable {
		for i := range cs {
			_, heads, held := of(&cs[i])
			for _, h := range heads {
				k, ok := index[strings.ToLower(h)]
				if ok && (n < rareFloor || df[k]*4 <= n) {
					*held++
				}
			}
		}
	}
}

// spellsOut reports whether r writes the parts of an identifier next to
// each other, in order ("the job time limit" for JobTimeLimit), as
// search's phrase would find them: a long note holding gaterate, quota
// and max far apart does not hold GATERATE_QUOTA_MAX.
func spellsOut(r *textRow, parts []string) bool {
	run := hybrid.Terms(nil, strings.Join(parts, " "))
	if len(run) < 2 {
		return false
	}
	t := docText(r)
	return hasRun(keptTerms(textHash(t), t), run)
}

// sectionHolds reports whether long row r's best section sec (or its
// title and first line) holds word k: a question term (terms), or a
// topic past them (q.topics, in searchOrder's order).
func sectionHolds(q question, r *textRow, sec *bestSec, terms []string, k int) bool {
	if k < len(terms) {
		return sec.holds(q, r, terms[k])
	}
	return slices.ContainsFunc(q.topics[k-len(terms)].row, func(w string) bool { return sec.holds(q, r, w) })
}

// factOrder is what a row is, for search's order among rows holding as
// many of the question's words: 2 for a fact that holds now (live, fixed,
// built, shipped: factBonus > 0), 1 more for an as-built architecture
// note, -2 for a fact that does not (planned, proposed, rejected …), 0 for
// none. Why kept (devco D-963, coordinator decision on H-243): the
// linked-stores shape of the recall gate needs the as-built note before
// another client's row and a planned memory with the same words; it never
// outweighs one more question word.
func factOrder(r *textRow) int {
	n := factBonus(r.FactState) * 2
	if n < 0 {
		return -2
	}
	if r.Table == "architecture_notes" {
		n++
	}
	return n
}

// resourceTerms is terms with each word that names a matched resource
// by an alias or a typo ("alise-ng" for alice-ng) replaced by the
// resource's name: search reads the words as written and finds no row
// for the typo, while ask knows whom the question names (match.go), so
// the rows naming the resource hold that word as they would had the
// question spelled it.
func resourceTerms(terms []string, ms []match) []string {
	if len(ms) == 0 {
		return terms
	}
	out := slices.Clone(terms)
	for i, t := range out {
		for _, m := range ms {
			if namesResource(t, []match{m}) {
				out[i] = strings.ToLower(m.res.Name)
				break
			}
		}
	}
	return out
}

// hasRun reports whether run occurs in seq as consecutive items.
func hasRun(seq, run []string) bool {
	for i := 0; i+len(run) <= len(seq); i++ {
		if slices.Equal(seq[i:i+len(run)], run) {
			return true
		}
	}
	return false
}

// searchIndex is the BM25 index searchOrder reads and where a row is in
// it: the meaning view's (the whole store) when the call has one, else
// one built from rows.
func searchIndex(rows []textRow, v *meaningView) (*hybrid.Index, func(id string) (int, bool)) {
	if v != nil && v.ci != nil {
		return v.ci.ix, func(id string) (int, bool) { p, ok := v.at[id]; return p, ok }
	}
	docs := make([]hybrid.Doc, len(rows))
	at := make(map[string]int, len(rows))
	for i := range rows {
		docs[i] = hybrid.Doc{Text: docText(&rows[i])}
		if _, dup := at[rows[i].ID]; !dup {
			at[rows[i].ID] = i
		}
	}
	return hybrid.Build(docs), func(id string) (int, bool) { p, ok := at[id]; return p, ok }
}

// searchDate is the day search orders a full-title tie by: the row's own
// date (a memory's valid_at, else when it was written).
func searchDate(r *textRow) time.Time { return r.At }

// searchList is lore search's own order of the rows for a question, the
// one the candidate list follows (plainOrder, bug hunt H-270, devco
// D-1008). build sets it on the question before topRows fills it, so
// every copy of the question shares it.
type searchList struct {
	// ids are the row ids in search's order (search's first hit first).
	ids []string
	// rows are the rows the order was read from, by id.
	rows map[string]*textRow
	// ix and present are the BM25 index both orders read and each index
	// term's BM25 per text in it (bm25Of): searchOrder and plainOrder
	// read the same terms, and each walk of a term's postings costs a
	// slice as long as the store.
	ix      *hybrid.Index
	present map[string][]float64
	// held is the question words each row holds in search's order
	// (searchOrder), by id: grow's yieldRows reads it
	held map[string]float64
}

// bm25Of is the per-term BM25 cache of index ix: s's own when s read ix
// before, else a new one (kept on s when s is set).
func (s *searchList) bm25Of(ix *hybrid.Index) map[string][]float64 {
	if s == nil {
		return map[string][]float64{}
	}
	if s.ix != ix || s.present == nil {
		s.ix, s.present = ix, map[string][]float64{}
	}
	return s.present
}

// plainOrder is lore search's own order of rows for q, without what ask
// adds to rank its full rows (topics read as words, a long row's best
// section, identifiers, the linked folder, fact state): per kind, the
// rows holding a form of a question word in bm25 order, each scored by
// searchrank (words held, its text's coverage of the words as
// opsmemory.Held, names, title share, rank), a stale fact at 0.9 of its
// score, history after every current row, then every kind merged by
// score, equal scores in kind order (wire's GlobalSearch). Why (bug hunt
// H-270, a benchmark of 200 questions on a team store): ask's candidate
// list followed ask's own order, which kept search's first hits out of
// the list on 151 of 200 questions, so an agent reading search and ask
// saw two different "next" rows. nil when the question has no word
// search ranks by.
func plainOrder(q question, rows []textRow, v *meaningView) []string {
	w := searchWords(q.text)
	if len(w.Terms) == 0 || len(rows) == 0 {
		return nil
	}
	ix, at := searchIndex(rows, v)
	present := q.search.bm25Of(ix) // the terms searchOrder read are read once
	// each word's forms, each form its index terms and their BM25 per
	// text, looked up once per call, not once per row
	type form struct {
		terms []string
		bm    [][]float64
	}
	forms := make([][]form, len(w.Terms))
	for i, t := range w.Terms {
		for _, f := range searchrank.Forms(t) {
			fm := form{terms: hybrid.Terms(nil, f)}
			for _, x := range fm.terms {
				b, ok := present[x]
				if !ok {
					b = ix.BM25([]string{x})
					present[x] = b
				}
				fm.bm = append(fm.bm, b)
			}
			forms[i] = append(forms[i], fm)
		}
	}
	bases := coverBases(w.Terms)
	type cand struct {
		row  *textRow
		bm25 float64
		held int
	}
	byTable := map[string][]cand{}
	var tables []string
	for i := range rows {
		r := &rows[i]
		p, ok := at(r.ID)
		if !ok {
			continue
		}
		c := cand{row: r}
		var seq []string
		for k := range forms {
			got := false
			for _, fm := range forms[k] {
				if len(fm.terms) == 0 || slices.ContainsFunc(fm.bm, func(b []float64) bool { return b[p] == 0 }) {
					continue
				}
				if len(fm.terms) > 1 {
					if seq == nil {
						t := docText(r)
						seq = keptTerms(textHash(t), t)
					}
					if !hasRun(seq, fm.terms) {
						continue
					}
				}
				for _, b := range fm.bm {
					c.bm25 += b[p]
				}
				got = true
			}
			if got {
				c.held++
			}
		}
		if c.held == 0 {
			continue // search's any-word match does not find it
		}
		if _, seen := byTable[r.Table]; !seen {
			tables = append(tables, r.Table)
		}
		byTable[r.Table] = append(byTable[r.Table], c)
	}
	// the words a row's text covers count too (opsmemory.Held), read for
	// the rows that can reach the head of the order: coverage only adds
	// a word or two the index missed, so a row holding fewer words by the
	// index than the coverHead-th best row less one stays past it (the
	// list reads the first rows; reading every row's coverage cost 10 ms
	// of a warm ask on a 450-row folder)
	var helds []int
	for _, cs := range byTable {
		for _, c := range cs {
			helds = append(helds, c.held)
		}
	}
	if len(helds) == 0 {
		return nil
	}
	slices.SortFunc(helds, func(a, b int) int { return cmp.Compare(b, a) })
	floor := helds[min(coverHead, len(helds)-1)] - 1
	for _, cs := range byTable {
		for i := range cs {
			if c := &cs[i]; c.held < len(w.Terms) && c.held >= floor {
				c.held = max(c.held, coverOf(c.row, w.Terms, bases))
			}
		}
	}
	type hit struct {
		id      string
		history bool
		score   float64
		table   int
	}
	slices.Sort(tables)
	var hits []hit
	for ti, t := range tables {
		cs := byTable[t]
		slices.SortStableFunc(cs, func(a, b cand) int {
			if c := cmp.Compare(b.bm25, a.bm25); c != 0 {
				return c
			}
			return cmp.Compare(a.row.ID, b.row.ID)
		})
		items := make([]searchrank.Item, len(cs))
		for i, c := range cs {
			items[i] = searchrank.Item{Label: searchLabel(c.row), Matched: c.held, At: searchDate(c.row)}
		}
		for _, rk := range w.Order(items, len(items)) {
			c := cs[rk.Index]
			s := rk.Score
			if c.row.Stale != "" {
				s *= searchStaleRank
			}
			hits = append(hits, hit{id: c.row.ID, history: staleNote(c.row, q.now) != "", score: s, table: ti})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		if a.history != b.history {
			if a.history {
				return 1
			}
			return -1
		}
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		return cmp.Compare(a.table, b.table)
	})
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.id
	}
	return out
}

// bodyLabelled are the kinds search labels by their body's first 80
// bytes as written (opssearch.Targets: Label{Field: "body", Trunc: true}).
var bodyLabelled = map[string]bool{
	string(schema.TableComments): true, string(schema.TableHandoffs): true,
	string(schema.TableRules): true, string(schema.TableTastePrefs): true,
}

// searchLabel is what names row r in search's ranking (wire's hitLabel):
// a body-labelled kind's first line of its body's first 80 bytes; else
// its title (or name) on one line; a row without one its body on one
// line, cut to 80 bytes (the hit's fallback text). A memory is named by
// its body's first words, not its first line: a note opening "Feed app
// note 1." still names the words of the sentence after it.
func searchLabel(r *textRow) string {
	if bodyLabelled[r.Table] {
		return searchrank.Label("", textcut.Cut(r.Body, labelBytes, labelBytes-len(textcut.Ellipsis)))
	}
	if t := strings.Join(strings.Fields(r.Title), " "); t != "" {
		return searchrank.Label(t, "")
	}
	// the label is the first 80 bytes on one line: a long note's first
	// kilobyte holds them (whitespace aside), not its whole text
	body := r.Body
	if len(body) > labelRead {
		body = strings.ToValidUTF8(body[:labelRead], "")
	}
	flat := strings.Join(strings.Fields(body), " ")
	return searchrank.Label("", textcut.Cut(flat, labelBytes, labelBytes-len(textcut.Ellipsis)))
}

// coverHead is how many of search's first rows plainOrder reads the
// coverage of (with the rows holding as many words by the index).
const coverHead = 30

// labelRead is how much of a body searchLabel reads for its label.
const labelRead = 1024

// labelBytes is the length search cuts a body label to (opssearch.BodyMax).
const labelBytes = 80

// searchWords is how lore search reads question text: its words; a
// question holding FTS5 syntax (a parenthesis, a quote: "why is the
// master table (acme_masters) its own table?") that FTS5 refuses, as the
// plain words search falls back to then (searchrank.Words.Plain,
// opsmemory.MatchCounting). FTS5 refuses a question mark, comma, full
// stop, semicolon or exclamation mark outside a phrase; a query FTS5
// reads (deploy -friday, "exact phrase") stays syntax, as in search.
// Why (bug hunt H-270): read as syntax the words are not natural, and
// searchrank.Order counts every row as holding every word, so a row
// holding one word of eleven ranked with the row holding seven, by its
// title's share alone.
func searchWords(text string) searchrank.Words {
	w := searchrank.Of(text)
	if !w.Syntax || !refusedByFTS(text) {
		return w
	}
	if p, ok := w.Plain(); ok {
		return p
	}
	return w
}

// refusedByFTS reports whether FTS5 refuses text as a query: it holds a
// character no FTS5 bareword or operator may (? , . ; !) outside a
// double-quoted phrase.
func refusedByFTS(text string) bool {
	quoted := false
	for _, r := range text {
		switch {
		case r == '"':
			quoted = !quoted
		case !quoted && strings.ContainsRune("?,.;!", r):
			return true
		}
	}
	return false
}
