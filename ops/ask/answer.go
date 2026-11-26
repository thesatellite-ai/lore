package ask

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/logic/dates"
	"github.com/thesatellite-ai/lore/logic/hybrid"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/schema"
)

// taskRow is one open task as the lens holds it, with its debt metadata
// and its --ref pointers at resources.
type taskRow struct {
	ID, Place, Title, Status, Created string
	OwedBy, OwedTo                    string
	// Refs are the resources the task points at (knowledge_refs), with
	// the pointer's relation.
	Refs []taskRef
}

// taskRef is one --ref pointer of a task.
type taskRef struct{ Relation, ResID string }

// textRow is one row with text: a memory, a decision, a rule …
type textRow struct {
	Table, ID, Place, Title, Body string
	At                            time.Time
	// Imported marks a row brought in from a feed (source_kind imported):
	// noise next to what a person wrote, so it ranks after it.
	Imported bool
	// SupersededBy is the id of the row that replaced this one
	// (superseded_by_id), "" when none.
	SupersededBy string
	// ValidUntil is a memory's valid_until: from then on it is history
	// (lore memory invalidate sets it); zero when unset.
	ValidUntil time.Time
	// Day is the row's day metadata (YYYY-MM-DD), "" when none.
	Day string
	// Stale says the row's fact evidence changed after it was verified
	// (markStale, devco:D-941), "" when fresh or unchecked; Reverify is
	// the command that clears it (devco:D-953).
	Stale, Reverify string
	// FactState and Verified are the row's fact_state and
	// fact_verified_at (markFacts, devco D-963): a live or fixed fact
	// ranks above a planned or rejected one, a recently verified one
	// above an older one with the same match.
	FactState, Verified string
	// stored is the row's date as the lens stores it (metaDayOf reads
	// it), "" for a row not read from the lens.
	stored string
	// meaning marks a row that joined by meaning alone: among the rows
	// nearest the question in the whole corpus, holding none of its words
	// (corpus.go, devco D-902).
	meaning bool
	// Refs are the resources the row's text references (row_refs).
	Refs []string
	// words is the word set of Title and Body (tokens), built once per
	// row. Why: an everything-about answer scored a row, then tested it
	// against each topic, splitting the same text into words up to six
	// times, ~5 ms of a warm served "where does X work now" (devco
	// D-847).
	words map[string]bool
	// label is the row's label words (labelOf), built once per row when
	// labeled is set. Why (bug hunt H-213): markReplaced compares every
	// pair of candidate rows and split both rows' labels with a regexp for
	// each pair, ~30 ms of a warm ask over 500 rows.
	label   []string
	labeled bool
	// content is the row's words without filler (contentOf), built once.
	content []string
	// first is the word set of the title and the body's first line
	// (firstTokens), built once.
	first map[string]bool
	// split is the word sets of the kept corpus row this row was copied
	// from, shared by every copy (nil for a row read once). The fields
	// above stay this copy's own memo; split carries what one call built
	// to the next call's copy (bug hunt H-219).
	split *rowSplit
	// comments are the comments on the row, oldest first (comments.go,
	// devco D-942): history, which can make the row answer, never answer
	// in its place.
	comments []rowComment
	// identList is the parts of each code identifier of the row's text
	// (idents.go, bug hunt H-230), built once when identsBuilt is set.
	identList   [][]string
	identsBuilt bool
	// secs are the word sets of a long row's sections (sectionsOf,
	// longsec.go), built once when sectioned is set.
	secs      []rowSection
	sectioned bool
	// best is bestSection's answer for the question and resources named
	// by bestFor (bestSectionOf): the scorer and search's order ask it of
	// the same row in one call (devco D-984)
	best    *bestSec
	bestFor string
}

// tokens is the word set of r's title and body (tokensOf), built on the
// first call and kept on r.
func (r *textRow) tokens() map[string]bool {
	if r.words == nil {
		r.words = shareSplit(r.split, func(s *rowSplit) *splitSlot[map[string]bool] { return &s.words }, func() map[string]bool {
			return tokensOf(r.Title + "\n" + r.Body)
		})
	}
	return r.words
}

// firstTokens is the word set of r's title and its body's first line,
// built on the first call and kept on r (and on the kept corpus row r was
// copied from). Why (bug hunt H-214): score split that text again with a
// regexp for every row of every call (~4 ms of a warm ask over 500 rows).
func (r *textRow) firstTokens() map[string]bool {
	if r.first == nil {
		r.first = shareSplit(r.split, func(s *rowSplit) *splitSlot[map[string]bool] { return &s.first }, func() map[string]bool {
			return tokensOf(r.Title + "\n" + firstLine(r.Body))
		})
	}
	return r.first
}

// rowSplit is the word sets of one kept corpus row (corpusOf), built the
// first time any call needs one and read by every later call's copy of
// the row. Why (bug hunt H-219): H-214 built all of them for every row
// when the corpus was parsed, so a cold lore ask (the store just changed,
// a new corpus) split all ~6,000 rows of a personal store with a regexp,
// ~70 ms, though the answer reads the word sets of the few hundred rows it
// scores. Built on use and shared, the cold call pays for the rows it
// reads and a warm call still splits nothing. The lock makes the read
// server's concurrent calls safe; the text never changes after the parse.
type rowSplit struct {
	mu      sync.Mutex
	words   splitSlot[map[string]bool]
	first   splitSlot[map[string]bool]
	content splitSlot[[]string]
	label   splitSlot[[]string]
	idents  splitSlot[[][]string]
	secs    splitSlot[[]rowSection]
	// bases is the querywords.Base of every word of the row (coverOf)
	bases splitSlot[map[string]bool]
}

// splitSlot is one word set of a rowSplit and whether it is built (a
// built label can be nil: a row with no label words).
type splitSlot[T any] struct {
	v     T
	built bool
}

// shareSplit is the word set slot names in s, built by build when not
// built yet; with no shared split (a row read once) it is build's result.
// build runs outside the lock (contentOf's reads tokens, another slot),
// so two calls racing build it twice and keep the first.
func shareSplit[T any](s *rowSplit, slot func(*rowSplit) *splitSlot[T], build func() T) T {
	if s == nil {
		return build()
	}
	s.mu.Lock()
	p := slot(s)
	if p.built {
		v := p.v
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()
	v := build()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !p.built {
		p.v, p.built = v, true
	}
	return p.v
}

// data is everything one ask reads.
type data struct {
	resources []resourceRow
	tasks     []taskRow
	rows      []textRow
	// facts is every row's fact keys (loadFacts, devco:D-941), nil when
	// the ask read no rows.
	facts map[string]factItems
	// view is the question's view of every live row the call read
	// (corpus.go); nil builds it from rows (a test's data).
	view *meaningView
}

// Defaults of ask.
const (
	// DefaultMaxChars is --max-chars' default: an answer an agent reads in
	// one go, lighter than the files a grep would open. Why 8000 (devco
	// D-1013): at 4000 the answering row was found but printed cut or as
	// a candidate line, so an agent needed a lore show for most
	// questions; 8000 prints up to three rows whole at the same speed
	// and still stays below what a grep reads.
	DefaultMaxChars = 8000
	// DefaultTop is how many rows are printed in full (--top): about ten,
	// so an agent reading the answer has the evidence; --max-chars still
	// caps the text. Why (devco D-898, owner pick list item 3): three rows
	// cost an answering agent 11 points on two public QA sets.
	DefaultTop = 10
	// LatestHeading heads an everything-about answer's newest rows: the
	// newest row of each folder, then of each topic no folder row covers.
	LatestHeading = "latest (the newest row of each folder and the newest decision, status, budget and meeting row, word for word):"
	// maxLinked caps the resources listed under one resource.
	maxLinked = 12
	// maxSideTasks caps the open tasks listed under a question that is
	// not about debts.
	maxSideTasks = 5
)

// scored is one text row with its score and the terms it matched.
type scored struct {
	row     *textRow
	score   int
	matched []string
	// history says why the row is no longer the current answer, "" when
	// it is current (staleNote, replacedNote).
	history string
	// replacedBy is the id of the newer row that replaced this one on the
	// same thing (markReplaced), "" when none.
	replacedBy string
	// fused is the row's meaning-and-words score for the question
	// (fuseScores, wsum50 in 0..1): it orders rows of equal score.
	fused float64
	// meaning says the row answers by meaning alone (textRow.meaning).
	meaning bool
	// comments are the row's comments holding a word of the question
	// (devco D-942), printed under it as history.
	comments []Comment
	// heads are the question words and topic names the heading of a long
	// row's best section holds (longsec.go, devco D-969), each adding
	// headWord; distinctBonus takes back the common ones (D-972).
	heads []string
	// outside are the question words a long row holds only outside its
	// best section (devco D-972): matched, but at a point each, and never
	// distinctive.
	outside []string
}

// staleNote says why row r is no longer the current answer as lore
// stores it: superseded by another row, or past its valid_until at now;
// "" when it is current. Why (devco D-820, owner): a fact that changed
// must never be handed to an agent as the answer, only as history.
func staleNote(r *textRow, now time.Time) string {
	return schema.HistoryNote(r.SupersededBy, r.ValidUntil, now)
}

// markReplaced marks the rows of all that a newer current row replaced:
// the same folder and the same topics and words of the question (so the
// same thing: a budget, a gym, a status) but older, and the newer row
// repeats what the older one is about (restates). Each points at the
// newest such row, which says what it replaces. A row matching none of the
// question's words (an everything-about answer: the question is only a
// name) is the same thing as another by the topics both hold instead
// (sameThing). Rows holding neither are left alone: two rows that only
// name alice-ng say different things.
func markReplaced(all []scored) {
	keys := make([][]string, len(all))
	for i := range all {
		keys[i] = sameThing(&all[i])
	}
	for i := range all {
		s := &all[i]
		if s.history != "" || len(keys[i]) == 0 {
			continue
		}
		var newest *scored
		for j := range all {
			n := &all[j]
			if j == i || n.history != "" || n.row.Place != s.row.Place || !slices.Equal(keys[j], keys[i]) || !n.row.At.After(s.row.At) || !restates(n.row, s.row) {
				continue
			}
			if newest == nil || n.row.At.After(newest.row.At) {
				newest = n
			}
		}
		if newest == nil {
			continue
		}
		s.history = fmt.Sprintf("history: replaced by %s from %s", newest.row.ID, newest.row.At.UTC().Format(dates.DayLayout))
		s.replacedBy = newest.row.ID
	}
}

// sameThing is what row s is about for markReplaced: the question's words
// and topics it matched, or, when it matched none (a question that is only
// a name), the topics of topicTable it holds (status, budget, …), nil when
// it holds none. Why (devco D-898, bug hunt H-156): an everything-about
// answer matches no question word, so its older status and budget rows were
// never marked replaced and, once agents get ten rows instead of three,
// were printed as current next to the rows that replaced them.
func sameThing(s *scored) []string {
	if len(s.matched) > 0 {
		return s.matched
	}
	var out []string
	for _, t := range topicTable {
		if holdsTopic(s.row, t) {
			out = append(out, t.name)
		}
	}
	return out
}

// restates reports whether newer is about what older is about, so it
// can replace it: both carry the same label of two or more words
// (sameLabel: "Status update lumen: …" after "Status update lumen: …"),
// or newer holds at least half of older's content words (its words but
// the filler of skipWords; a plural counts as its singular), as "Budget
// for orbit moved to 55k (was 40k)." holds budget, orbit and 40k of
// "Budget for orbit is 40k for this year.". Why (devco
// D-893, bug hunt H-153): the folder and the question's words alone made
// a newer row that only repeated them ("Vela status: the office move is
// on track.") replace an older row holding the answer (a long weekly note
// with the move's date, or the booking line itself), so that row was
// printed as history and the status line as the answer. A long note is
// never restated by a short row that holds less of it. When in doubt both
// rows stay current: the newer one still ranks first, nothing is hidden.
func restates(newer, older *textRow) bool {
	if sameLabel(newer, older) {
		return true
	}
	if la, lo := labelOf(newer), labelOf(older); len(la) >= 2 && len(lo) >= 2 {
		// devco D-984: two rows that each carry a label of their own
		// ("As built: hookwire (webhook sender)", "As built: gaterate
		// (rate limiter)") are two things; two long notes sharing a
		// store's common words held half of each other's words, so every
		// as-built note but the newest was printed as history
		return false
	}
	nt := newer.tokens()
	content := contentOf(older)
	total, held := len(content), 0
	for i, w := range content {
		if nt[w] || nt[w+"s"] || (len(w) > 3 && nt[strings.TrimSuffix(w, "s")]) {
			held++
		}
		// bug hunt H-214: the answer is known before the last word (half
		// held, or half out of reach): markReplaced asks this of every
		// pair of a folder's candidate rows
		if left := total - i - 1; 2*held >= total || 2*(held+left) < total {
			break
		}
	}
	return 2*held >= total
}

// contentOf is r's words without the filler words (skipWords), built
// once per row and kept on r. Why (bug hunt H-214): restates
// walked r's whole word set and looked each word up in skipWords for
// every pair markReplaced compares, ~10 ms of a warm ask over 500 rows.
func contentOf(r *textRow) []string {
	if r.content == nil {
		r.content = shareSplit(r.split, func(s *rowSplit) *splitSlot[[]string] { return &s.content }, func() []string {
			out := make([]string, 0, len(r.tokens()))
			for w := range r.tokens() {
				if !skipWords[w] && !isStemKey(w) {
					out = append(out, w)
				}
			}
			return out
		})
	}
	return r.content
}

// maxLabel is how far into a row's first line its label's colon may be.
const maxLabel = 60

// sameLabel reports whether rows a and b carry the same label of two or
// more content words: the title, or the text before a colon early in the
// body's first line ("Status update lumen (2026-09-01): …"), its filler
// and words holding digits (a date) left out. One word (a project name,
// "Vela: …") says too little to make two rows one thing.
func sameLabel(a, b *textRow) bool {
	la, lb := labelOf(a), labelOf(b)
	return len(la) >= 2 && slices.Equal(la, lb)
}

// labelOf is r's label words (sameLabel), nil when it has none.
func labelOf(r *textRow) []string {
	if !r.labeled {
		r.label, r.labeled = shareSplit(r.split, func(s *rowSplit) *splitSlot[[]string] { return &s.label }, func() []string { return readLabel(r) }), true
	}
	return r.label
}

// readLabel splits r's label words (labelOf keeps them on r).
func readLabel(r *textRow) []string {
	text := r.Title
	if text == "" {
		first, _, _ := strings.Cut(strings.TrimSpace(r.Body), "\n")
		head, _, ok := strings.Cut(first, ":")
		if !ok || len([]rune(head)) > maxLabel {
			return nil
		}
		text = head
	}
	var out []string
	for _, w := range reWord.FindAllString(strings.ToLower(text), -1) {
		if !skipWords[w] && !hasDigit(w) {
			out = append(out, w)
		}
	}
	return out
}

// build answers q from d: the matched resources, the open tasks that
// answer it, then the rows (the window's rows for a date question, else
// the best top rows), ranked so the item that answers the question type
// comes first, cut to maxChars of text in that order.
func build(q question, d data, top, maxChars int) Out {
	if top <= 0 {
		top = DefaultTop
	}
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	out := Out{Query: q.text, When: q.when, Resources: []Resource{}, Tasks: []Task{}, Rows: []Row{}, Ranked: []Ranked{}, Cited: []string{}, Candidates: []Candidate{}}
	var more []Row // the rows past top, for the candidate list
	ms := matchResources(q, d.resources)
	identity := isIdentity(q.text) && len(ms) > 0
	q.about = !identity && len(ms) > 0 && (isAbout(q.text) || bareName(q, ms))
	out.identity, out.asked, out.q, out.top = identity, q.tokens, q, top
	for _, m := range ms {
		r := resourceOf(m, d.resources, q.owe != oweNone)
		if q.about && m.whole {
			// everything about a typo of a name ("alise-ng"): the card
			// leads, whole
			r.touched = false
		}
		out.Resources = append(out.Resources, r)
	}
	if !identity {
		out.Tasks = tasksFor(q, ms, d.tasks)
	}
	if q.owe != oweNone && len(out.Tasks) > 0 {
		// devco D-827: the owed tasks answer a debt question and their
		// lines name the person; the person's card under them only made
		// the answer heavier than the grep it replaces. A person the
		// lines name only by an alias (owed to Huey) keeps a one-line
		// card that says who that is; with no open task the person stays,
		// so the answer still says who was looked up.
		out.Resources = slices.DeleteFunc(out.Resources, func(r Resource) bool { return namedByTasks(r, out.Tasks) })
	}
	lead := 0
	switch {
	case q.owe != oweNone || identity:
		// a debt question is answered by the tasks, "who is X" by the
		// resource: rows would only make the answer heavier
	case !q.window.IsZero() && !asksBesidesDate(q, ms):
		// "what happened yesterday": the window's rows are the answer
		out.Rows = windowRows(q, ms, d.rows)
	case !q.window.IsZero():
		// "what did I buy last weekend": the date is a soft prior (prior.go)
		out.Rows, lead = priorRows(q, ms, d.rows, d.view, top)
		out.prior = true
	case q.file != "":
		// a change to a file: its context says what applies; a row is
		// added only when it holds two or more of the question's words
		// (one shared word, such as "invoice", is not about the change)
		rows, _ := topRows(q, ms, d.rows, d.view, top)
		out.Rows = slices.DeleteFunc(rows, func(r Row) bool { return len(r.Matched) < 2 })
		lead = len(out.Rows)
	default:
		q.search = &searchList{} // H-270: filled by topRows (plainOrder)
		out.q.search = q.search
		out.Rows, lead = topRows(q, ms, d.rows, d.view, top+maxCandidates)
		out.Rows, more, lead = pastTop(out.Rows, top, lead)
		if len(out.Rows) == 0 && len(ms) > 0 && !q.about {
			// no row is about the resources it names: the rows holding
			// two or more of its words and topics answer, the resources
			// follow (devco D-949, public issue #157: never the resource
			// cards alone while rows match). One shared word or topic
			// ("money" for a budget row of another project) is no match:
			// "who handles the money at birchwood-co" stays the company
			// and the people its fields link.
			rows, _ := topRows(q, nil, d.rows, d.view, top+maxCandidates)
			rows = slices.DeleteFunc(rows, func(r Row) bool { return len(r.Matched) < 2 })
			out.Rows, more, _ = pastTop(rows, top, 0)
			lead = 0
		}
	}
	names := nameIndex(d.resources)
	out.names = names // H-270: candidates past ask's own rows are read the same way
	for _, rs := range [][]Row{out.Rows, more} {
		for i := range rs {
			r := &rs[i]
			r.readTitle, r.readBody = readable(r.Title, names), readable(r.Body, names)
		}
	}
	if q.file == "" && q.owe == oweNone && !identity {
		out.Abstain = abstainOf(q, ms, d.rows) // abstain.go
	}
	if leadOf(q, identity, &out) == leadAbout {
		out.lead = lead
	}
	out.chunkLong()
	// H-245: cards stand in the candidate list when a row answers, and the
	// list counts in maxChars (candidates.go)
	order, cards := out.splitCards(out.rank(q, identity, lead))
	out.fitCandidates(order, maxChars, more, cards)
	// H-239: only the rows printed, after the cap cut them
	out.Conflicts = out.conflictsOf()
	comments := map[string][]Comment{}
	for _, r := range out.Rows {
		comments[r.ID] = r.Comments
	}
	for _, r := range out.Ranked {
		out.Cited = append(out.Cited, r.ID)
		if r.Kind == KindRow {
			// D-942: a comment printed under its row is cited after it
			for _, c := range comments[r.ID] {
				out.Cited = append(out.Cited, c.ID)
			}
		}
	}
	return out
}

// nameIndex maps every way a ${res:…} reference names a resource of rs
// (its id, its name or an alias, each also as <kind>/<name>), in lower
// case, to the resource's name.
func nameIndex(rs []resourceRow) map[string]string {
	out := make(map[string]string, 3*len(rs))
	for i := range rs {
		r := &rs[i]
		out[strings.ToLower(r.ID)] = r.Name
		for _, n := range r.names() {
			out[n] = r.Name
			out[strings.ToLower(r.Kind)+"/"+n] = r.Name
		}
	}
	return out
}

// readable is text with each ${res:…} reference that names a resource of
// names replaced by the resource's name; any other reference stays as
// written (refs.Readable).
func readable(text string, names map[string]string) string {
	return refs.Readable(text, func(r refs.Ref) (string, bool) {
		if r.Scheme != refs.SchemeRes {
			return "", false
		}
		n, ok := names[strings.ToLower(r.Target)]
		return n, ok
	})
}

// namedByTasks reports whether every task names resource r by its name:
// as the other side of the debt or in its title.
func namedByTasks(r Resource, ts []Task) bool {
	name := strings.ToLower(r.Name)
	for _, t := range ts {
		if !strings.EqualFold(t.OwedBy, name) && !strings.EqualFold(t.OwedTo, name) && !allWords(tokensOf(t.Title), name) {
			return false
		}
	}
	return true
}

// answerFirst is which list leads an answer.
type answerFirst int

const (
	leadResources answerFirst = iota // who is X, or nothing better
	leadTasks                        // what do I owe X, what does X owe me
	leadRows                         // a status, a decision, a budget, a date
	leadAbout                        // everything about X
)

// leadOf is which list answers q first. A debt question is answered by
// its owed tasks, a date question by the rows of its dates, a question
// asking for something the rows hold (a status, a decision, a budget) by
// those rows, everything about X by the resource itself; the resource the
// question names follows as context. Why: an agent reads the first item
// and often stops there (recall gate, top-1 bar), so the project's field
// list must not stand where its status update is the answer.
func leadOf(q question, identity bool, o *Out) answerFirst {
	switch {
	case identity || len(o.Resources) > 0 && q.about:
		if !identity {
			return leadAbout
		}
		return leadResources
	case q.owe != oweNone && len(o.Tasks) > 0:
		return leadTasks
	case !q.window.IsZero() && len(o.Rows) > 0:
		return leadRows
	case len(o.Rows) > 0 && len(o.Rows[0].Matched) > 0 && o.Rows[0].History == "":
		// the best row holds what the question asks for, not only the name
		return leadRows
	}
	return leadResources
}

// rank is the answer's reading order (see leadOf). For everything about
// X: the resource, then the lead rows (the newest row of each folder:
// the last meeting, the last journal day), then the open tasks, then the
// other rows; lead is how many of o.Rows are those lead rows.
//
// A resource the question only touches (a part of its name or a field
// value found it, Resource.touched) comes after every row and task when
// a row matched, the one touched by the most question words first: a
// resource the question does not name never outranks a matching row of
// any kind. Why (devco D-968, Sync benchmark set 5): code_repo resources
// named after the product ("shop_import_worker" for a "shop import"
// question) stood before the rows whenever the best row was not a plain
// match (a changed row leads with the resources), cap walked them first
// and up to six of them took the answer's items next to the rows.
func (o *Out) rank(q question, identity bool, lead int) []Ranked {
	res := make([]Ranked, 0, len(o.Resources))
	var touched []Resource
	for _, r := range o.Resources {
		if r.touched && len(o.Rows) > 0 && !identity {
			touched = append(touched, r)
			continue
		}
		res = append(res, Ranked{Kind: KindResource, ID: r.ID})
	}
	slices.SortStableFunc(touched, func(a, b Resource) int { return cmp.Compare(b.parts, a.parts) })
	tail := make([]Ranked, 0, len(touched))
	for _, r := range touched {
		tail = append(tail, Ranked{Kind: KindResource, ID: r.ID})
	}
	tsk := make([]Ranked, 0, len(o.Tasks))
	for _, t := range o.Tasks {
		tsk = append(tsk, Ranked{Kind: KindTask, ID: t.ID})
	}
	rows := make([]Ranked, 0, len(o.Rows))
	for _, r := range o.Rows {
		rows = append(rows, Ranked{Kind: KindRow, ID: r.ID})
	}
	o.answer = leadOf(q, identity, o)
	switch o.answer {
	case leadTasks:
		return slices.Concat(tsk, res, rows, tail)
	case leadRows:
		return slices.Concat(rows, res, tsk, tail)
	case leadAbout:
		lead = min(lead, len(rows))
		return slices.Concat(res, rows[:lead], tsk, rows[lead:], tail)
	}
	return slices.Concat(res, tsk, rows, tail)
}

// isAbout reports an "everything about X" question: the resource leads,
// then the newest rows that name it, then its open tasks.
func isAbout(text string) bool {
	low := " " + strings.ToLower(strings.TrimSpace(text))
	for _, p := range []string{" everything about ", " all about ", " tell me about ", " know about ", " anything about ", " everything on "} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

// bareName reports a question that is only the name of resources it
// matched ("hollow", "mara-petrov"): no topic, debt, date or file, every
// word a matched name. It asks for everything about them. Why (devco
// D-844, blind agent run): an agent asked "hollow" for "what did we
// decide for hollow and why" and got the three newest rows, none of
// them the decision; the everything-about answer holds the newest
// decision, status, budget and meeting row about the resource.
func bareName(q question, ms []match) bool {
	if len(q.topics) > 0 || q.owe != oweNone || !q.window.IsZero() || q.file != "" || len(q.words) == 0 {
		return false
	}
	for _, w := range q.words {
		if !namesResource(w, ms) {
			return false
		}
	}
	return true
}

// isIdentity reports a "who is X" question: the resource answers it.
func isIdentity(text string) bool {
	low := strings.ToLower(strings.TrimSpace(text))
	for _, p := range []string{"who is ", "who's ", "who are ", "what is the email", "what is the role"} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

// resourceOf is resource m as the answer prints it: its fields (a changed
// field with its former value and the day it changed) and the resources
// whose fields name it (the people of a company, the projects a person
// owns). brief keeps only the name line (a debt question).
func resourceOf(m match, all []resourceRow, brief bool) Resource {
	r := m.res
	out := Resource{ID: r.ID, Kind: r.Kind, Name: r.Name, Place: r.Place, Match: m.how, Aliases: r.Aliases, Fields: []Field{}, Linked: []Linked{}, touched: !m.named(), parts: len(m.said)}
	if brief {
		return out
	}
	for _, f := range r.Fields {
		fo := Field{Name: f.Name, Value: f.Value}
		for _, h := range slices.Backward(r.History) {
			if h.Field == f.Name && h.From != "" && h.To == f.Value {
				fo.Was, fo.ChangedAt = h.From, dayOf(h.At)
				break
			}
		}
		out.Fields = append(out.Fields, fo)
	}
	// a field that names it by its name or an alias links (company:
	// Acme for acme-co with the alias Acme)
	names := []string{strings.ToLower(r.Name)}
	for _, a := range r.Aliases {
		names = append(names, strings.ToLower(strings.TrimSpace(a)))
	}
	for i := range all {
		o := &all[i]
		if o.ID == r.ID {
			continue
		}
		for _, f := range o.Fields {
			if !slices.Contains(names, strings.ToLower(strings.TrimSpace(f.Value))) {
				continue
			}
			l := Linked{ID: o.ID, Kind: o.Kind, Name: o.Name, Via: f.Name}
			for _, g := range o.Fields {
				if g.Name != f.Name && g.Value != "" && !strings.Contains(g.Value, "@") {
					l.Fields = append(l.Fields, g.Name+" "+g.Value)
				}
			}
			out.Linked = append(out.Linked, l)
			break
		}
		if len(out.Linked) >= maxLinked {
			break
		}
	}
	return out
}

// keepBestRow makes room for the best row of order when cap kept none:
// the resource and task cards printed before it are dropped from the end
// until the row fits, cut to its best sections. Why (devco D-975, Sync
// benchmark set 6, S6-19): the full cards of three code_repos filled the
// cap and the answer was the cards alone while rows matched; a card says
// what a thing is, the row what the question asks.
func (o *Out) keepBestRow(order []Ranked, rowIdx map[string]int, maxChars int) {
	at := slices.IndexFunc(order, func(r Ranked) bool { return r.Kind == KindRow })
	if at < 0 || slices.ContainsFunc(o.Ranked, func(r Ranked) bool { return r.Kind == KindRow }) {
		return
	}
	best := order[at]
	o.Left-- // best was left out; it is printed now
	for len(o.Ranked) > 0 {
		o.Ranked = o.Ranked[:len(o.Ranked)-1]
		o.Left++
		if o.fitsWith(best, rowIdx[best.ID], maxChars) {
			return
		}
	}
	if !o.fitsWith(best, rowIdx[best.ID], maxChars) {
		// not even cut to one section: it stands alone, as cap's first
		// item does
		o.Ranked = append(o.Ranked, best)
	}
}

// fitsWith adds row r (at index i of o.Rows) to the answer when it fits
// in maxChars, cut to its best sections if need be.
func (o *Out) fitsWith(r Ranked, i, maxChars int) bool {
	if r.Kind == KindRow {
		o.chunkRow(i)
	}
	o.Ranked = append(o.Ranked, r)
	if len(o.body()) <= maxChars || o.cutRow(i, maxChars) {
		return true
	}
	o.Ranked = o.Ranked[:len(o.Ranked)-1]
	return false
}

// dayOf is the YYYY-MM-DD of an RFC 3339 time, or s itself.
func dayOf(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// tasksFor lists the open tasks that answer q: for a debt question every
// open task owed in that direction (to or by the resources it names), for
// any other question the open tasks of the resources it names.
func tasksFor(q question, ms []match, ts []taskRow) []Task {
	names, ids := map[string]bool{}, map[string]bool{}
	for _, m := range ms {
		for _, n := range m.res.names() { // owed_to Bobby is owed to robert-kim
			names[n] = true
		}
		ids[m.res.ID] = true
	}
	linked := func(t taskRow, relation string) bool {
		for _, r := range t.Refs {
			if ids[r.ResID] && (relation == "" || r.Relation == relation) {
				return true
			}
		}
		return false
	}
	var out []Task
	for _, t := range ts {
		by, to := strings.ToLower(t.OwedBy), strings.ToLower(t.OwedTo)
		keep := false
		switch q.owe {
		case oweIOwe:
			keep = by == Me && (len(ms) == 0 || names[to] || linked(t, "owed_to"))
		case oweOwesMe:
			keep = to == Me && (len(ms) == 0 || names[by] || linked(t, "owed_by"))
		default:
			keep = len(ms) > 0 && (names[by] || names[to] || linked(t, ""))
		}
		if keep {
			out = append(out, Task{ID: t.ID, Title: t.Title, Status: t.Status, Place: t.Place, OwedBy: t.OwedBy, OwedTo: t.OwedTo, Created: t.Created})
		}
	}
	slices.SortStableFunc(out, func(a, b Task) int { return byIDLast(cmp.Compare(b.Created, a.Created), a.ID, b.ID) })
	if q.owe == oweNone && len(out) > maxSideTasks {
		out = out[:maxSideTasks]
	}
	if out == nil {
		out = []Task{}
	}
	return out
}

// subjectOf reports whether row r is about one of the matched resources:
// its text names one (a name the words split, widget_api, by all its
// words), says the words the question found it by (match.said: the part
// widget, the role cfo), or references one.
func subjectOf(r *textRow, toks map[string]bool, ms []match) bool {
	for _, m := range ms {
		if slices.Contains(r.Refs, m.res.ID) {
			return true
		}
		for _, n := range m.res.names() {
			if allWords(toks, nameKey(n)) {
				return true
			}
		}
		for _, w := range m.said {
			if allWords(toks, w) {
				return true
			}
		}
	}
	return false
}

// touchedBonus is what a row about a resource the question only touches
// (a typo of its name, a part of it, a field value) scores for being
// about it: one question word, as a row holding the word scores.
const touchedBonus = 3

// namesWhole reports whether the question names one of ms by its name or
// an alias, said whole: the rows must then be about them. A typo, a part
// of a name or a field value only touches a resource. Why (devco D-975,
// Sync benchmark set 6, S6-19): "why is the <payment> webhook receiver
// not mounted" found code_repos by a typo of their name (webhooks) or a
// part of it, kept only the rows naming them, so the memory and task
// saying "receiver ... not mounted" were left out, and three code_repo
// cards answered.
func namesWhole(ms []match) bool {
	return slices.ContainsFunc(ms, func(m match) bool { return m.named() })
}

// allWords reports whether every word of name (one, or an alias's
// several) is in toks.
func allWords(toks map[string]bool, name string) bool {
	ws := strings.Fields(name)
	for _, w := range ws {
		if !toks[w] {
			return false
		}
	}
	return len(ws) > 0
}

// tokensOf is the word set of a row's text: each word, and the parts of
// each code identifier (ErrGroupNameTaken holds group, name and taken;
// idents.go, bug hunt H-230), so a question in plain words finds the row
// that names the thing in code; and the stem of each word whose stem is
// not the word (forms.go, bug hunt H-235), so hasWord finds the forms
// lore search finds (validation for validate, job for jobs).
func tokensOf(s string) map[string]bool {
	return addStems(wordsOf(s))
}

// wordsOf is tokensOf without the stems: the words and identifier parts
// of s.
func wordsOf(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range reWord.FindAllString(s, -1) {
		out[strings.TrimSuffix(strings.ToLower(w), "'s")] = true
		for _, p := range identParts(w) {
			out[p] = true
		}
	}
	return out
}

// hasWord reports whether toks holds w or a form of it (appointment for
// appointments, budgets for budget): a word three letters or fewer
// longer or shorter, or a word with the same stem as lore search stems
// it (validation for validate, job for jobs, enqueue for enqueuing;
// forms.go, bug hunt H-235).
func hasWord(toks map[string]bool, w string) bool {
	if toks[w] || sameStem(toks, w) {
		return true
	}
	if len(w) < 4 {
		return false
	}
	for t := range toks {
		if (strings.HasPrefix(t, w) && len(t)-len(w) <= 3) || (len(t) >= 4 && strings.HasPrefix(w, t) && len(w)-len(t) <= 3) {
			return true
		}
	}
	return false
}

// score rates row r for q: about a matched resource (required when the
// question names one), each topic and each word it holds, a little more
// for a topic in its title or first line; a feed row and a replaced row
// rank lower. ok false when it does not answer at all.
func score(q question, ms []match, r *textRow) (scored, bool) {
	toks := r.tokens()
	var first map[string]bool // the title and first line's words, built at the first topic hit
	s := scored{row: r}
	cmts := map[string]bool{} // the comments holding a word of q (D-942)
	// sm are the resources whose words are their name, not question
	// words, for r
	sm := ms
	loose := false // r is not about the resources the question touches
	if len(ms) > 0 {
		switch {
		case !namesWhole(ms):
			// devco D-975 (bug hunt H-234): no resource is named by its
			// name or an alias, so the question is not about them: a row
			// about one counts the word that found it once, as one
			// question word; any other row answers by its words, each
			// word counted (two or more, below)
			if subjectOf(r, toks, ms) {
				s.score += touchedBonus
			} else {
				sm, loose = nil, true
			}
		case !subjectOf(r, toks, ms):
			return s, false
		default:
			s.score += 10
		}
	}
	for _, t := range q.topics {
		for _, w := range t.row {
			if hasWord(toks, w) {
				s.score += 4
				s.matched = append(s.matched, t.name)
				if first == nil {
					first = r.firstTokens()
				}
				if hasWord(first, w) {
					s.score++
				}
				break
			}
		}
	}
	// devco D-972: a long row counts the words of its best section (and
	// title), not every word its whole text holds (longsec.go)
	sec := bestSectionOf(q, sm, r)
	if sec != nil {
		s.heads = sec.heldHeads
	}
	for _, w := range q.words {
		if namesResource(w, sm) {
			continue
		}
		if hasWord(toks, w) || identWord(q, toks, w) {
			if sec != nil && !sec.holds(q, r, w) {
				// held only by the long row's other sections: it says the
				// row is about the word a little, as a comment does
				s.score++
				s.matched = append(s.matched, w)
				s.outside = append(s.outside, w)
				continue
			}
			s.score += 3
			s.matched = append(s.matched, w)
			if len(r.comments) > 0 {
				commentWord(r, w, cmts)
			}
			continue
		}
		if len(r.comments) > 0 && commentWord(r, w, cmts) {
			// D-942: a word only a comment on the row holds makes the row
			// answer, below a row that holds it itself
			s.score++
			s.matched = append(s.matched, w)
		}
	}
	s.comments = commentsOf(r, cmts)
	if s.score > 0 {
		// devco D-969: a long row ranks by its best section too, its
		// heading naming the question's words (longsec.go); D-972: only
		// the distinctive heading words keep it (distinctBonus)
		s.score += headWord * len(s.heads)
	} else {
		s.heads = nil
	}
	if s.score == 0 && r.meaning {
		// nearest the question in meaning, sharing none of its words: the
		// lowest band, after every row holding a word (corpus.go, devco
		// D-902), so it fills places the words leave and never pushes a
		// word's answer out; the fused order places it among its kind
		s.score, s.meaning = 1, true
	}
	if s.score == 0 {
		return s, false
	}
	if len(ms) == 0 && len(s.matched) == 0 && !s.meaning {
		return s, false
	}
	if loose && len(s.matched) < 2 {
		// one shared word ("money" for a budget row of another project)
		// is no match, as in build's no-row fallback (devco D-949)
		return s, false
	}
	// H-230: the row naming the asked thing in one identifier
	// (ErrGroupNameTaken for "taken group name") ranks as a phrase
	s.score += identBonus(q, sm, r)
	// a row dated in (or near) the question's window ranks up (prior.go);
	// it never makes a row answer on its own
	s.score += timeBonus(q, r)
	if r.Imported {
		s.score -= 2
	}
	// devco D-963 (Sync benchmark set 4): the repo's own folder before
	// another folder or client with the same match, a built fact before a
	// planned or rejected one; neither outweighs one more question word
	s.score += factBonus(r.FactState)
	if q.home != "" && r.Place == q.home {
		s.score += homeBonus
	}
	if r.SupersededBy != "" {
		s.score -= 3
	}
	return s, true
}

// namesResource reports whether word w is (a typo of) a matched name, a
// word of a matched name the words split (api of widget_api), or a word
// the question found a resource by (match.said): a row is not more
// about the question for saying it, it is about the resource.
func namesResource(w string, ms []match) bool {
	for _, m := range ms {
		if slices.Contains(m.said, w) {
			return true
		}
		if ws := keyWords(nameKey(m.res.Name)); len(ws) > 1 && slices.Contains(ws, w) {
			return true
		}
		for i, n := range m.res.names() {
			// an alias (names()[1:]) is a typo only from four letters on:
			// cto is not the alias CFO
			if w == n || ((i == 0 || len([]rune(w)) >= 4) && osa(w, n) <= typoBudget(w)) {
				return true
			}
		}
	}
	return false
}

// homeBonus is what a row of the repo's own folder (question.home) adds
// to its score: less than one question word (3), so a row of another
// folder that holds more of the question still wins.
const homeBonus = 2

// factBonus is what a row's fact_state adds to its score (devco D-963):
// +1 for a fact that holds now (live, fixed, built, shipped), -2 for one
// that does not (planned, proposed, rejected, abandoned, retired), 0 for
// none or another value.
func factBonus(state string) int {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "live", "fixed", "built", "shipped":
		return 1
	case "planned", "proposed", "rejected", "abandoned", "retired":
		return -2
	}
	return 0
}

// staleOrder puts a fresh row before a stale one (devco:D-941); 0 when
// both are fresh or both stale.
func staleOrder(a, b scored) int {
	switch sa, sb := a.row.Stale != "", b.row.Stale != ""; {
	case sa == sb:
		return 0
	case sa:
		return 1
	}
	return -1
}

// firstLine is the first line of s.
func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// topRows picks the best top rows for q, newest first among equals; the
// first pick takes the best row of each folder among the best-scored rows,
// so one busy folder (a daily journal) does not hide the others (the
// meeting note about the same person). Those picks stay first, newest
// first, and lead says how many there are: a newer row of a folder already
// shown comes after the newest row of every other folder.
func topRows(q question, ms []match, rows []textRow, v *meaningView, top int) ([]Row, int) {
	var all []scored
	for i := range rows {
		if s, ok := score(q, ms, &rows[i]); ok {
			all = append(all, s)
		}
	}
	distinctBonus(q, all) // idents.go: the distinctive words count more (H-230, D-972)
	// an everything-about answer leads with the newest row of each folder
	// and topic (lore's structure): the fused order stays out of it
	if !q.about {
		if v == nil {
			v = viewOf(q, corpusOfRows(rows), nil)
		}
		fuseScores(q, all, v)
	}
	// subject.go: the words a cut row keeps first (H-232), weighed with the
	// IDF of the index the rows were scored with (H-244)
	var ix *hybrid.Index
	if v != nil && v.ci != nil {
		ix = v.ci.ix
	}
	subject := subjectTermsOf(all, ix)
	slices.SortStableFunc(all, func(a, b scored) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		if c := staleOrder(a, b); c != 0 {
			return c // devco:D-941: a fresh row before a stale one with the same match
		}
		if c := strings.Compare(b.row.Verified, a.row.Verified); c != 0 {
			return c // devco D-963: the more recently verified fact first
		}
		if c := cmp.Compare(b.fused, a.fused); c != 0 {
			return c
		}
		if c := b.row.At.Compare(a.row.At); c != 0 {
			return c
		}
		return strings.Compare(b.row.ID, a.row.ID)
	})
	// devco D-984: the rows in lore search's order (searchorder.go)
	if !q.about && q.window.IsZero() && q.file == "" {
		q.order = searchOrder(q, ms, rows, v)
		if q.search != nil && q.order != nil {
			// H-270: the candidate list follows search's own order
			q.search.ids = plainOrder(q, rows, v)
			q.search.rows = make(map[string]*textRow, len(rows))
			for i := range rows {
				q.search.rows[rows[i].ID] = &rows[i]
			}
		}
	}
	searched := q.order != nil
	if searched {
		all = searchOrdered(q, rows, all, len(ms) == 0)
	}
	if len(all) == 0 {
		return []Row{}, 0
	}
	if len(all[0].matched) > 0 {
		// some rows hold what the question asks (a topic, a word): rows
		// that only name the resource would pad the answer
		all = slices.DeleteFunc(all, func(s scored) bool { return len(s.matched) == 0 && !s.meaning })
	}
	for i := range all {
		all[i].history = staleNote(all[i].row, q.now)
	}
	markReplaced(all)
	thenCurrent(q, all) // prior.go: a row dated in the question's window is what was true then
	// history (superseded, expired, replaced) never stands where the
	// current answer goes: the current rows are picked first, history
	// only fills what is left of top, marked (devco D-820)
	var hist []scored
	cur := slices.DeleteFunc(slices.Clone(all), func(s scored) bool { return s.history != "" })
	for _, s := range all {
		if s.history != "" {
			hist = append(hist, s)
		}
	}
	// history reads as a timeline, newest first: the fused order is for
	// choosing the current answer, not for what came before it
	slices.SortStableFunc(hist, func(a, b scored) int {
		if searched {
			return cmp.Compare(searchPlace(q, a, len(all)), searchPlace(q, b, len(all)))
		}
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		if c := b.row.At.Compare(a.row.At); c != 0 {
			return c
		}
		return strings.Compare(b.row.ID, a.row.ID)
	})
	all = cur
	if len(all) == 0 {
		all, hist = hist, nil
	}
	var pick []scored
	taken := map[string]bool{}
	places := map[string]bool{}
	for _, s := range all {
		if searched || s.score < all[0].score || len(pick) >= top {
			// devco D-984: in search's order the first rows are search's
			// first rows, not the best row of each folder
			break
		}
		if !places[s.row.Place] {
			places[s.row.Place], taken[s.row.ID] = true, true
			pick = append(pick, s)
		}
	}
	if q.about && len(all[0].matched) == 0 {
		pick = append(pick, topicPicks(all, taken)...)
	}
	lead := len(pick)
	for _, s := range all {
		if len(pick) >= top {
			break
		}
		if !taken[s.row.ID] {
			taken[s.row.ID] = true
			pick = append(pick, s)
		}
	}
	byScoreThenNewest := func(a, b scored) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		if c := staleOrder(a, b); c != 0 {
			return c
		}
		if c := cmp.Compare(b.fused, a.fused); c != 0 {
			return c
		}
		return b.row.At.Compare(a.row.At)
	}
	if !searched {
		slices.SortStableFunc(pick[:lead], byScoreThenNewest)
		slices.SortStableFunc(pick[lead:], byScoreThenNewest)
	}
	if !q.about && q.file == "" {
		pick, hist = wasSecond(pick, hist, top)
	}
	for _, s := range hist {
		if len(pick) >= top {
			break
		}
		pick = append(pick, s)
	}
	out := make([]Row, 0, len(pick))
	for _, s := range pick {
		r := rowOf(s.row, s.matched)
		r.History = s.history
		r.Comments = s.comments
		r.subject = subject
		// a fact that changed: the newest row on the same thing says
		// which of the rows shown with it it replaces (the newest of them)
		var last *textRow
		for _, h := range pick {
			if h.replacedBy == s.row.ID && (last == nil || h.row.At.After(last.At)) {
				last = h.row
			}
		}
		if last != nil {
			r.Note = fmt.Sprintf("newest: it replaces %s from %s", last.ID, last.At.UTC().Format(dates.DayLayout))
			r.replaces = last.ID
		}
		out = append(out, r)
	}
	return out, lead
}

// searchOrdered is all in lore search's order for q (q.order, devco
// D-984): every row search ranked comes first, in search's order, with
// addRanked also one ask's own scorer did not take (it gets the words it
// holds, so its excerpt is cut around them); the rows only ask found (by
// meaning, by a topic word) follow in ask's order.
func searchOrdered(q question, rows []textRow, all []scored, addRanked bool) []scored {
	have := make(map[string]bool, len(all))
	for _, s := range all {
		have[s.row.ID] = true
	}
	// a question naming a resource (by name, alias or a typo of it) keeps
	// only the rows about it: search, which reads words, finds rows that
	// share the other words and is not about the resource at all
	for i := 0; addRanked && i < len(rows); i++ {
		r := &rows[i]
		if _, ok := q.order[r.ID]; !ok || have[r.ID] {
			continue
		}
		s, _ := score(q, nil, r)
		s.row = r
		have[r.ID] = true
		all = append(all, s)
	}
	n := len(all)
	place := make(map[string]int, n)
	for i, s := range all {
		place[s.row.ID] = i
	}
	slices.SortStableFunc(all, func(a, b scored) int {
		pa, pb := searchPlace(q, a, n), searchPlace(q, b, n)
		if pa != pb {
			return cmp.Compare(pa, pb)
		}
		return cmp.Compare(place[a.row.ID], place[b.row.ID])
	})

	return all
}

// searchPlace is s's place in search's hits, after (n past) every hit
// for a row search did not rank.
func searchPlace(q question, s scored, n int) int {
	if p, ok := q.order[s.row.ID]; ok {
		return p
	}
	return len(q.order) + n
}

// topicPicks is, for an everything-about answer, the newest row of each
// topic (decision, status, budget, meeting) that is not picked yet, best
// scored first (a row you wrote before an imported one). Why (devco
// D-844, blind agent run): the newest row of each folder is the last
// status and the last journal day; the project's decision, months older
// in the same folder, was never shown, and grep, which prints every line
// naming the project, answered the question lore did not.
func topicPicks(all []scored, taken map[string]bool) []scored {
	var out []scored
	for _, t := range topicTable {
		for _, s := range all {
			if !holdsTopic(s.row, t) {
				continue
			}
			if !taken[s.row.ID] {
				taken[s.row.ID] = true
				out = append(out, s)
			}
			break
		}
	}
	return out
}

// holdsTopic reports whether row r holds a row word of topic t.
func holdsTopic(r *textRow, t topic) bool {
	toks := r.tokens()
	for _, w := range t.row {
		if hasWord(toks, w) {
			return true
		}
	}
	return false
}

// windowRows lists the rows of q's date window, oldest first: the rows
// with a day (a journal) when any has one, else every written row dated
// in it, else the imported ones; only the rows about the matched
// resources when the question names any.
func windowRows(q question, ms []match, rows []textRow) []Row {
	var withDay, written, imported []*textRow
	for i := range rows {
		r := &rows[i]
		if len(ms) > 0 && !subjectOf(r, r.tokens(), ms) {
			continue
		}
		if r.Day != "" {
			if t, err := time.Parse(dates.DayLayout, r.Day); err == nil && q.window.Keeps(t) {
				withDay = append(withDay, r)
			}
			continue
		}
		if !q.window.Keeps(r.At) {
			continue
		}
		if r.Imported {
			imported = append(imported, r)
		} else {
			written = append(written, r)
		}
	}
	pick := withDay
	if len(pick) == 0 {
		pick = written
	}
	if len(pick) == 0 {
		pick = imported
	}
	slices.SortStableFunc(pick, func(a, b *textRow) int {
		return cmp.Or(strings.Compare(rowDay(a), rowDay(b)), a.At.Compare(b.At), strings.Compare(a.ID, b.ID))
	})
	out := make([]Row, 0, len(pick))
	for _, r := range pick {
		row := rowOf(r, nil)
		row.History = staleNote(r, q.now) // what happened that day stays, marked when no longer true
		out = append(out, row)
	}
	return out
}

// rowDay is the day a row is about: its day metadata, else its date.
func rowDay(r *textRow) string {
	if r.Day != "" {
		return r.Day
	}
	return r.At.UTC().Format(dates.DayLayout)
}

// rowOf is row r as the answer prints it.
func rowOf(r *textRow, matched []string) Row {
	out := Row{ID: r.ID, Table: r.Table, Place: r.Place, Title: r.Title, Body: r.Body, Matched: matched, Stale: r.Stale, Reverify: r.Reverify, factState: r.FactState, verified: r.Verified}
	if r.Day != "" {
		out.At = r.Day
	} else if !r.At.IsZero() {
		out.At = r.At.UTC().Format(dates.DayLayout)
	}
	if out.Matched == nil {
		out.Matched = []string{}
	}
	return out
}

// cap keeps the answer within maxChars of text, walking order (the
// reading order): items are kept whole while they fit, an item that does
// not fit is left out and the walk goes on, so a lower item that fits is
// still printed; Left counts what was left out. The best row (the first
// row of order) is never left out for its length: when it does not fit
// whole it is cut to the part of it that answers the question (cutRow),
// so the answer is never empty while a row matched. It sets Resources,
// Tasks, Rows and Ranked to what is kept.
//
// Why (devco D-859, public issue #86): the walk stopped at the first item
// that did not fit, so a best row longer than --max-chars left the answer
// at "(no answer)" and dropped the short rows under it too.
func (o *Out) cap(order []Ranked, maxChars int) {
	o.maxChars = maxChars
	o.Ranked = []Ranked{}
	o.Left = 0
	rowIdx := map[string]int{}
	for i, r := range o.Rows {
		rowIdx[r.ID] = i
	}
	bestRow := true // no row of order seen yet
	// devco D-963/D-968 gave the resources a question only touches a
	// share of the cap under rows; since H-245 (devco D-998) they never
	// reach cap while a row answers: they are candidate lines
	// (candidates.go, splitCards)
	for _, r := range order {
		if r.Kind == KindRow && o.unchunked(rowIdx[r.ID]) {
			if !bestRow && maxChars-len(o.body()) < minCut {
				// no cut of it fits what is left (chunkLong)
				o.Left++
				if rowIdx[r.ID] < o.lead {
					o.lead--
				}
				continue
			}
			o.chunkRow(rowIdx[r.ID])
		}
		o.Ranked = append(o.Ranked, r)
		fits := len(o.body()) <= maxChars
		if !fits && r.Kind == KindRow && bestRow {
			fits = o.cutRow(rowIdx[r.ID], maxChars)
		}
		if r.Kind == KindRow {
			bestRow = false
		}
		if fits {
			continue
		}
		o.Ranked = o.Ranked[:len(o.Ranked)-1]
		o.Left++
		if r.Kind == KindRow && rowIdx[r.ID] < o.lead {
			// one fewer newest-of-a-folder row is printed compact
			o.lead--
		}
	}
	if len(o.Ranked) == 0 && len(order) > 0 {
		// never empty while something matched: the first item whole
		// (a resource or task card; a row was cut above)
		o.Ranked = append(o.Ranked, order[0])
		o.Left--
	}
	o.keepBestRow(order, rowIdx, maxChars)
	kept := map[Ranked]bool{}
	for _, r := range o.Ranked {
		kept[r] = true
	}
	o.Resources = slices.DeleteFunc(o.Resources, func(r Resource) bool { return !kept[Ranked{KindResource, r.ID}] })
	o.Tasks = slices.DeleteFunc(o.Tasks, func(t Task) bool { return !kept[Ranked{KindTask, t.ID}] })
	o.Rows = slices.DeleteFunc(o.Rows, func(r Row) bool { return !kept[Ranked{KindRow, r.ID}] })
}

// cutRow cuts row i of o.Rows, the last item of o.Ranked, to the part of
// its text that answers the question (cutBody: a markdown row's best
// sections by heading, else its best paragraph) in the room the answer has left,
// at most half of maxChars and at least minCut characters, and reports whether it was cut: false
// when what is above it leaves less than minCut characters (the row is
// then left out whole, as before).
func (o *Out) cutRow(i, maxChars int) bool {
	r := &o.Rows[i]
	whole, wholeRead := *r, r.readBody
	// a long row chunkLong already cut keeps its whole length
	r.Body, r.readBody, r.FullChars = "", "", cmp.Or(whole.FullChars, len(whole.Body))
	// at most half the cap: the rows under it that fit are printed too
	room := min(maxChars-len(o.body()), max(maxChars/2, minCut))
	alone := false // the answer's only item: kept at minCut even when over
	if room < minCut {
		if len(o.Ranked) > 1 {
			*r = whole
			return false
		}
		room, alone = minCut, true
	}
	words := cutWords(r.Matched, o.asked)
	text, cut := cutBody(cmp.Or(wholeRead, whole.Body), words, room, r.subject)
	if !cut {
		// its text fits: what is above it leaves no room, unless nothing
		// is above it (a long row chunkLong already cut to its best
		// sections, whose cut note alone fills a small --max-chars): the
		// answer is never empty while a row matched
		*r = whole
		return alone
	}
	if wholeRead == "" || wholeRead == whole.Body {
		r.Body = text // the same text: cut once (devco D-984)
	} else {
		r.Body, _ = cutBody(whole.Body, words, room, r.subject)
	}
	if wholeRead != "" {
		r.readBody = text
	}
	r.LeftOut = leftOutOf(o.q, r.Matched, r.Title, r.Body, r.Comments) // leftout.go
	return true
}

// NoAnswer is the line an answer that holds nothing prints.
const NoAnswer = "(no answer)"

// Text is the answer as an agent reads it: the date window (a date
// question), the items in reading order (resources, open tasks, rows in
// full, grouped where they follow each other), then the ids it cites.
func (o Out) Text() string {
	var b strings.Builder
	body := o.body()
	if o.File != "" && (o.Context != "" || o.note != "") {
		// the file's rules and hotfixes come before the ranked items
		h := o.header()
		f := "\napplies to " + o.File + " (lore context --for-file):\n"
		if o.Context != "" {
			f += o.Context + "\n"
		}
		if o.note != "" {
			f += o.note + "\n"
		}
		body = h + f + strings.TrimPrefix(body, h)
	}
	if hint := o.Abstain.line(); hint != "" && len(o.Ranked) > 0 {
		// the memory names no such thing: the rows are the nearest it
		// holds, not the answer (abstain.go)
		body = hint + body
	}
	b.WriteString(body)
	if len(o.Ranked)+len(o.Applies) == 0 {
		// devco D-821: the empty line every empty read prints; the
		// composition root adds what was searched and what to try under
		// it (wire/emptysearch.go), as for every search
		b.WriteString(NoAnswer + "\n")
	}
	b.WriteString(o.conflictText())
	if o.Left > 0 {
		fmt.Fprintf(&b, "\n%s left out to stay under %d characters; --max-chars %d prints more\n", plural.Count(o.Left, "more item"), o.maxChars, o.maxChars*2)
	}
	b.WriteString(o.reverifyText())
	if cited := o.textCited(); len(cited) > 0 {
		b.WriteString("cited: " + strings.Join(cited, " ") + "\n")
	}
	return b.String()
}

// reverifyText is one line per stale row the answer printed (a ranked
// row or a row the file's context applied) with the command that clears
// it, as lore lint and lore show print it (devco:D-953, bug hunt H-212):
// the answer says what to do about a stale fact, not only that it is.
func (o Out) reverifyText() string {
	printed := map[string]bool{}
	for _, id := range o.Cited {
		printed[id] = true
	}
	var b strings.Builder
	add := func(id, fix string) {
		if fix != "" && printed[id] {
			b.WriteString("  " + id + ": " + fix + "\n")
			printed[id] = false // once, when a row is both applied and ranked
		}
	}
	for _, a := range o.Applies {
		add(a.ID, a.Reverify)
	}
	for _, r := range o.Rows {
		add(r.ID, r.Reverify)
	}
	if b.Len() == 0 {
		return ""
	}
	return "\nre-verify each stale row (its evidence changed after fact_verified_at):\n" + b.String()
}

// textCited is the ids the text's cited line names: every cited item but
// the resources of a who-is answer, whose card names them by kind and
// name (lore resource show --kind <kind> --name <name> reads one; --json
// keeps every id in cited). Why (devco D-827, recall gate G-4): a who-is
// answer must stay at least 10x lighter than the files grep opens, and a
// 30-character id per card was a third of it.
func (o Out) textCited() []string {
	if !o.identity || o.answer != leadResources {
		return o.Cited
	}
	out := []string{}
	for _, r := range o.Ranked {
		if r.Kind != KindResource {
			out = append(out, r.ID)
		}
	}
	return out
}

// cards reports an answer whose resources print as one-line cards: a
// who-is question (the resource is the answer, not context) and a debt
// question (the person under the tasks, only when a line names them by
// an alias).
func (o Out) cards() bool { return o.identity && o.answer == leadResources || o.answer == leadTasks }

// body is the header, the items of o.Ranked in that order: a run of
// tasks under one "open tasks" heading, a run of rows under one rows
// heading (a second run of rows, after the tasks of an everything-about
// answer, under "more rows"), then the candidate list (candidates.go).
func (o Out) body() string { return o.items() + candText(o.Candidates) + dropText(o.Dropped) }

// items is body without the candidate list.
func (o Out) items() string {
	res, tsk, rows := map[string]Resource{}, map[string]Task{}, map[string]Row{}
	for _, r := range o.Resources {
		res[r.ID] = r
	}
	for _, t := range o.Tasks {
		tsk[t.ID] = t
	}
	ranked := map[string]bool{}
	for _, it := range o.Ranked {
		if it.Kind == KindRow {
			ranked[it.ID] = true
		}
	}
	for _, r := range o.Rows {
		// only the rows the answer holds: while cap walks, o.Rows still
		// holds the rows not yet kept, and the now line quoted one of them
		// in its "before that", so a row that fits measured as too long
		// (devco D-859)
		if ranked[r.ID] {
			rows[r.ID] = r
		}
	}
	about := o.lead > 0
	if !about && !o.cards() {
		return o.compactBody(res, tsk, rows)
	}
	var b strings.Builder
	b.WriteString(o.header())
	b.WriteString(o.nowLine(rows))
	runs := 0 // how many runs of rows the answer has
	for i, it := range o.Ranked {
		if it.Kind == KindRow && (i == 0 || o.Ranked[i-1].Kind != KindRow) {
			runs++
		}
	}
	rowRuns, n := 0, 0
	for i, it := range o.Ranked {
		first := i == 0 || o.Ranked[i-1].Kind != it.Kind
		switch it.Kind {
		case KindResource:
			if o.cards() {
				b.WriteString(res[it.ID].card(o.asked))
				continue
			}
			if about {
				b.WriteString(res[it.ID].who())
				continue
			}
			b.WriteString(res[it.ID].text())
		case KindTask:
			if o.answer == leadTasks {
				// a debt answer is its task lines: no heading, the
				// person named once per line (devco D-827)
				b.WriteString(tsk[it.ID].line(true))
				continue
			}
			if first {
				run := 0
				for _, x := range o.Ranked[i:] {
					if x.Kind != KindTask {
						break
					}
					run++
				}
				fmt.Fprintf(&b, "\nopen tasks (%d):\n", run)
			}
			if about {
				b.WriteString(tsk[it.ID].bare())
				continue
			}
			b.WriteString(tsk[it.ID].line(false))
		case KindRow:
			compact := n < o.lead
			if first || n == o.lead && o.lead > 0 {
				b.WriteString(o.rowsHeader(rowRuns, runs, compact))
				rowRuns++
			}
			if about {
				b.WriteString(rows[it.ID].facts())
			} else {
				b.WriteString(rows[it.ID].text(n - o.lead))
			}
			n++
		}
	}
	return b.String()
}

// header is the first line: the date window a date question was read
// as, "" for every other question. The question itself is not echoed:
// the agent wrote it, and every echoed character is read again (devco
// D-827); --json keeps it as query.
func (o Out) header() string {
	if o.When != "" && o.prior {
		// the rows are the best rows, not only those of the dates
		return "dates: " + o.When + "; rows dated near them rank higher\n"
	}
	if o.When != "" {
		return "dates: " + o.When + "\n"
	}
	return ""
}

// rowsHeader heads the run'th of runs runs of rows (0 the first), or the
// first row after an everything-about answer's newest rows: those newest
// rows (compact) one line each, then the other rows after its open tasks.
func (o Out) rowsHeader(run, runs int, compact bool) string {
	switch {
	case o.When != "" && o.prior:
		return "\nbest rows, a row dated near those dates ranked higher:\n"
	case o.When != "":
		return "\nrows dated " + o.When + ", oldest first:\n"
	case compact:
		return "\n" + LatestHeading + "\n"
	case o.lead > 0:
		return "\nmore rows (word for word):\n"
	case run > 0:
		return "\nmore rows, in full:\n"
	case runs > 1:
		return "\nnewest row of each folder, in full:\n"
	}
	return "\nbest rows, in full:\n"
}

// card is one resource as the answer to "who is X": one line led by its
// name, its kind and aliases in brackets after it, then the fields that
// say who it is (a changed one with what it was and the day it changed):
// "jonah-park (person; aka Jonny): role: support lead". The name leads so
// it survives a summary: with "person jonah-park:" first a small model
// read the kind and name as a label and answered with the fields alone
// (devco D-844, blind agent run: 3 of 9 who-is answers lost the name).
// Contact values (an email, a link) are left out unless the question names that field
// ("what is the email of X"), and so are the resources that link to it;
// `lore resource show` and --json print them. Why (devco D-827, recall
// gate G-4): a who-is answer must be at least 10x lighter than the
// person's notes file grep reads, and the full field list, the linked
// projects and the id were most of it.
func (r Resource) card(asked []string) string {
	var b strings.Builder
	b.WriteString(r.Name + " (" + r.Kind)
	if len(r.Aliases) > 0 {
		b.WriteString("; aka " + strings.Join(r.Aliases, ", "))
	}
	b.WriteString(")")
	var parts []string
	for _, f := range r.Fields {
		if f.Value == "" || isContact(f.Value) && !slices.Contains(asked, strings.ToLower(f.Name)) {
			continue
		}
		p := f.Name + ": " + f.Value
		if f.Was != "" {
			p += fmt.Sprintf(" (changed %s, was %s)", f.ChangedAt, f.Was)
		}
		parts = append(parts, p)
	}
	if len(parts) > 0 {
		b.WriteString(": " + strings.Join(parts, "; "))
	}
	return b.String() + "\n"
}

// isContact reports a value that is a way to reach someone (an email, a
// link), not who they are.
func isContact(v string) bool {
	return strings.Contains(v, "@") || strings.Contains(v, "://")
}

// text is one resource: its line, its fields and what links to it.
func (r Resource) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s %s  %s  (%s)\n", r.Kind, r.Name, r.ID, r.Place)
	if len(r.Aliases) > 0 {
		fmt.Fprintf(&b, "  aliases: %s\n", strings.Join(r.Aliases, ", "))
	}
	for _, f := range r.Fields {
		if f.Value == "" {
			continue
		}
		fmt.Fprintf(&b, "  %s: %s", f.Name, f.Value)
		if f.Was != "" {
			fmt.Fprintf(&b, " (changed %s, was %s)", f.ChangedAt, f.Was)
		}
		b.WriteString("\n")
	}
	if len(r.Linked) > 0 {
		b.WriteString("  linked:\n")
		for _, l := range r.Linked {
			fmt.Fprintf(&b, "    %s %s (%s)", l.Kind, l.Name, l.Via)
			if len(l.Fields) > 0 {
				b.WriteString(": " + strings.Join(l.Fields, ", "))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// line is one open task: its title, its status when it is not plain
// todo, and the debt (who owes whom). The id is cited once at the end of
// a debt answer, not on the line. debt is a debt answer: the question fixed
// the direction, so a title that already names the other side ("send the
// contract to keiko-sato", "robert-kim owes me the deck") says it all.
// Why (devco D-827, recall gate G-4): the owe answer was three times the
// grep line it replaces (status, id, place and the debt on every line,
// the person's card under them).
func (t Task) line(debt bool) string {
	s := t.summary(debt)
	if !debt {
		// a task under another answer (everything about X) is a list item
		// under its heading and keeps its id on its line, as the rows
		// around it do; a debt answer's lines are the whole answer, bare
		s = "  - " + s + "  " + t.ID
	}
	return s + "\n"
}

// summary is the task's title, its status when it is not plain todo and
// the debt (left out of a debt answer's line when the title names the
// other side).
func (t Task) summary(debt bool) string {
	s := t.Title
	if t.Status != "" && t.Status != schema.TaskStatusTodo {
		s += " [" + t.Status + "]"
	}
	if d := t.debt(); d != "" && !(debt && t.namesOther()) {
		s += " (" + d + ")"
	}
	return s
}

// debt says who owes whom from the person's side: "owed to X" (I owe X),
// "owed by X" (X owes me), "owed by A to B" between two others; "" for a
// task that is not a debt.
func (t Task) debt() string {
	switch {
	case t.OwedBy == "" && t.OwedTo == "":
		return ""
	case strings.EqualFold(t.OwedBy, Me):
		return "owed to " + cmp.Or(t.OwedTo, "?")
	case strings.EqualFold(t.OwedTo, Me):
		return "owed by " + cmp.Or(t.OwedBy, "?")
	}
	return fmt.Sprintf("owed by %s to %s", cmp.Or(t.OwedBy, "?"), cmp.Or(t.OwedTo, "?"))
}

// namesOther reports a title that names the other side of the debt.
func (t Task) namesOther() bool {
	other := t.OwedTo
	if strings.EqualFold(t.OwedTo, Me) {
		other = t.OwedBy
	}
	if other == "" || strings.EqualFold(other, Me) || strings.EqualFold(t.OwedBy, t.OwedTo) {
		return false
	}
	return allWords(tokensOf(t.Title), strings.ToLower(other))
}

// text is one row in full, numbered i+1, its references read as names.
func (r Row) text(i int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n%d. %s  %s %s  (%s)", i+1, r.At, r.Table, r.ID, r.Place)
	if r.Note != "" {
		b.WriteString("  " + r.Note)
	}
	if r.History != "" {
		b.WriteString("  [" + r.History + "]")
	}
	if r.Stale != "" {
		b.WriteString("  [" + r.Stale + "]")
	}
	b.WriteString("\n")
	title, body := r.readable()
	if title != "" && title != body {
		b.WriteString("   " + title + "\n")
	}
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString(strings.TrimRight("   "+line, " ") + "\n")
	}
	if r.FullChars > 0 {
		fmt.Fprintf(&b, "   (%s; lore show --id %s prints it whole)\n", r.cutNote(), r.ID)
	}
	b.WriteString(r.commentLines())
	return b.String()
}

// cutNote says a row was cut (to fit --max-chars, or a long markdown row
// to its best sections, chunkLong) and how long it is whole.
func (r Row) cutNote() string {
	if len(r.LeftOut) > 0 {
		// devco D-974: the part not printed holds words of the question
		return fmt.Sprintf("cut to the part that answers the question; the whole row is %d characters and the part left out holds %s", r.FullChars, strings.Join(r.LeftOut, ", "))
	}
	return fmt.Sprintf("cut to the part that answers the question; the whole row is %d characters", r.FullChars)
}

// readable is r's title and body as the text answer prints them: the
// stored text with its resource references read as names when build
// filled them, else the stored text.
func (r Row) readable() (string, string) {
	return cmp.Or(r.readTitle, r.Title), cmp.Or(r.readBody, r.Body)
}

// nowLine is the line a changed fact leads the answer with: the newest
// row's text, the day it changed and what it was before (the older row it
// replaces, when the answer holds it); "" when no row of the answer
// replaces another. Why (devco D-836, blind agent run H-93): a small
// model summarising two numbered rows wrote "updated on <day>, previously
// tuesdays" and lost the row's own "changed to thursdays (was tuesdays)";
// one line that states the current value first survives the summary.
func (o Out) nowLine(rows map[string]Row) string {
	for _, it := range o.Ranked {
		r, ok := rows[it.ID]
		if it.Kind != KindRow || !ok || r.replaces == "" {
			continue
		}
		line := "now: " + oneLine(r) + " (changed " + r.At
		if old, ok := rows[r.replaces]; ok {
			line += "; before that: " + oneLine(old)
		}
		line += ")"
		if r.Stale != "" { // devco:D-953: the current value may no longer hold either
			line += "  [" + r.Stale + "]"
		}
		return line + "\n"
	}
	return ""
}

// oneLine is r's readable text on one line: the title, then the body's
// lines, joined by spaces.
func oneLine(r Row) string {
	return strings.Join(r.factLines(), " ")
}

// who is one resource as an everything-about answer prints it: a plain
// "who:" heading with its kind, name and folder, then one line per alias
// list, field and linked resource, no id (the cited line names it). Why
// (devco D-836): an id and a nested "linked:" block in the middle of the
// answer were read as noise by a small model, which then summarised the
// lines around them too.
func (r Resource) who() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nwho: %s %s (%s)\n", r.Kind, r.Name, r.Place)
	if len(r.Aliases) > 0 {
		fmt.Fprintf(&b, "  aliases: %s\n", strings.Join(r.Aliases, ", "))
	}
	for _, f := range r.Fields {
		if f.Value == "" {
			continue
		}
		fmt.Fprintf(&b, "  %s: %s", f.Name, f.Value)
		if f.Was != "" {
			fmt.Fprintf(&b, " (changed %s, was %s)", f.ChangedAt, f.Was)
		}
		b.WriteString("\n")
	}
	for _, l := range r.Linked {
		fmt.Fprintf(&b, "  %s %s (%s)", l.Kind, l.Name, l.Via)
		if len(l.Fields) > 0 {
			b.WriteString(": " + strings.Join(l.Fields, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// bare is one open task as an everything-about answer lists it: a list
// item with its title, status and debt, no id (the cited line names it).
func (t Task) bare() string {
	return "  - " + t.summary(false) + "\n"
}

// facts is one row as an everything-about answer prints it: one line
// with its day and folder, then its text word for word in quotes (the day
// the line already says dropped from the text, its lines joined), then
// the note or history of a changed fact; no id (the cited line names it).
// Why (devco D-836, blind agent run H-93): a small model kept "Saw
// hugo-berg about yarrow" and dropped "sorted old photos in the rain"
// from "Journal 2026-11-06: sorted old photos in the rain. Saw hugo-berg
// about yarrow.  [memories mem_…]"; cut into one sentence per line it
// dropped the activity more often, since that line no longer named the
// person. The whole row as one quoted line, under a heading that says
// "word for word", keeps the activity next to the name and reads as a
// quote to copy, not a list to summarise.
func (r Row) facts() string {
	line := fmt.Sprintf("  %s, %s: \"%s\"", r.At, r.Place, oneLine(r))
	if r.Note != "" {
		line += "  (" + r.Note + ")"
	}
	if r.History != "" {
		line += "  [" + r.History + "]"
	}
	if r.Stale != "" {
		line += "  [" + r.Stale + "]"
	}
	if r.FullChars > 0 {
		line += "  (" + r.cutNote() + ")"
	}
	return line + "\n"
}

// factLines is r's readable title and body, one item per line, with the
// row's own day dropped where the text repeats it (a leading
// "Journal <day>:" or a "(<day>)").
func (r Row) factLines() []string {
	title, body := r.readable()
	var out []string
	add := func(text string) {
		for _, line := range strings.Split(text, "\n") {
			if line = dropDay(strings.TrimSpace(line), r.At); line != "" {
				out = append(out, line)
			}
		}
	}
	if title != "" && title != body {
		add(title)
	}
	add(strings.TrimRight(body, "\n"))
	return out
}

// dropDay removes day from text where it only repeats the day the answer
// already prints for the row: a leading "<day>: " or "<word> <day>: "
// (a journal's "Journal 2026-11-06: ") and a " (<day>)". Any other
// mention of the day stays.
func dropDay(text, day string) string {
	if day == "" {
		return text
	}
	if rest, ok := strings.CutPrefix(text, day+":"); ok {
		text = strings.TrimSpace(rest)
	} else if i := strings.Index(text, " "+day+":"); i > 0 && !strings.Contains(text[:i], " ") {
		text = strings.TrimSpace(text[i+len(day)+2:])
	}
	return strings.Replace(text, " ("+day+")", "", 1)
}
