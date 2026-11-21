package wire

// emptysearch.go — devco D-821 (hunt H-77, owner HIGH): a search that
// finds nothing is never silently empty. `lore search`, `lore ask` and
// every `lore <kind> search` answered "(no matches)" (ask: one fixed
// sentence) and --json an empty list, so an agent could not tell a store
// that holds nothing about the question from a search that read the wrong
// stores, a question read wrong, or a filter (--tables, --since) that left
// the answer out; it gave up or asked again blindly. An empty answer now
// says, under its empty line and in --json:
//
//	searched  the stores and folders read, the kinds, the words the query
//	          was read as (ask: the question type and its dates)
//	try       1-3 next commands, each one runnable where it is printed:
//	          the same --store, the store a folder belongs to, your
//	          personal store, every kind, the query without its filters,
//	          ask for a search and search for an ask
//
// The composition root adds it (as personalunread.go adds its note), so
// every search op gets it from one place and a new `<kind> search` too.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/metaquery"
	"github.com/thesatellite-ai/lore/logic/searchrank"
	"github.com/thesatellite-ai/lore/op"
	opsask "github.com/thesatellite-ai/lore/ops/ask"
	contextop "github.com/thesatellite-ai/lore/ops/context"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/store"
)

// The top-level --json fields of an empty search's answer.
const (
	// SearchedField is the object saying what an empty search read.
	SearchedField = "searched"
	// TryField is the list of next commands an empty search prints.
	TryField = "try"
)

// emptyTryMax is how many next commands an empty answer lists: enough
// for the likely causes (scope, kind, wording), few enough to read.
const emptyTryMax = 3

// searchVerb is the verb of every per-kind search op (<noun>.search).
const searchVerb = "search"

// taskSearchOp is task search, which reads open tasks only unless --all.
const taskSearchOp = "task.search"

// everyKind is the kinds entry of a search over every kind of row.
const everyKind = "every kind"

// emptySearched is the "searched" object of an empty answer.
type emptySearched struct {
	Stores []string `json:"stores"`
	Kinds  []string `json:"kinds"`
	Words  []string `json:"words"`
	Type   string   `json:"type,omitempty"`
	Dates  string   `json:"dates,omitempty"`
	// lore context (devco D-824): the file it matched, the folder scopes
	// covering it, or the topic
	Path    string   `json:"path,omitempty"`
	Folders []string `json:"folders,omitempty"`
	Topic   string   `json:"topic,omitempty"`
}

// emptySearchInfo declares the two fields for the OpenAPI document.
var emptySearchInfo = []op.FieldInfo{
	{Name: SearchedField, Type: op.TypeObject, Desc: "on an empty answer: what was searched (absent otherwise)", Fields: []op.FieldInfo{
		{Name: "stores", Type: op.TypeStrings, Required: true, Desc: "the stores and folders read"},
		{Name: "kinds", Type: op.TypeStrings, Required: true, Desc: "the kinds of rows searched (every kind, or the ones named)"},
		{Name: "words", Type: op.TypeStrings, Required: true, Desc: "the words the query was read as (question words and filler left out)"},
		{Name: "type", Type: op.TypeString, Desc: "ask: how the question was read (who is, what I owe, a date, decision, status, words)"},
		{Name: "dates", Type: op.TypeString, Desc: "the dates read from the question or from --since / --until"},
		{Name: "path", Type: op.TypeString, Desc: "context --for-file: the path matched against globs, folder scopes and rows naming it"},
		{Name: "folders", Type: op.TypeStrings, Desc: "context --for-file: the folder scopes covering the path, innermost first"},
		{Name: "topic", Type: op.TypeString, Desc: "context --topic: the topic read"},
	}},
	{Name: TryField, Type: op.TypeStrings, Desc: "on an empty answer: 1-3 next commands, each runnable where it was printed (absent otherwise)"},
}

// emptySearchSpec reports whether spec is a search whose empty answer
// says what it searched: search, ask and every <kind> search.
func emptySearchSpec(spec op.Spec) bool {
	if spec.Access != op.AccessRead {
		return false
	}
	switch spec.Name {
	case opssearch.OpRun, opsask.OpRun, contextop.OpFetch:
		return true
	}
	_, verb, ok := strings.Cut(spec.Name, ".")
	return ok && verb == searchVerb
}

// emptySearchFields is the top fields of spec: emptySearchInfo for a
// search, nil otherwise.
func emptySearchFields(spec op.Spec) []op.FieldInfo {
	if emptySearchSpec(spec) {
		return emptySearchInfo
	}
	return nil
}

// emptySearchInput is what an empty search was asked, read from its input.
type emptySearchInput struct {
	op           string // the op name (search.run, ask.run, decision.search)
	query, store string
	tables       []string
	since, until string
}

// emptyResult reports whether res is an empty read's answer (emptyAnswer
// of its text), read before any note is added under it.
func emptyResult(res op.Result) bool {
	text, ok := res.Text()
	return ok && emptyAnswer(text)
}

// noteEmptySearch returns res with what was searched and what to try when
// e is a search; Run calls it only for an empty answer (emptyResult), read
// before the personal-unread note is added.
func (a *App) noteEmptySearch(ctx context.Context, e op.Entry, d op.Decoder, res op.Result) op.Result {
	if !emptySearchSpec(e.Spec()) {
		return res
	}
	in, err := e.Decode(d)
	if err != nil {
		return res
	}
	q := emptySearchInput{op: e.Spec().Name}
	n := scopeOf(in)
	q.store = n.store
	if q.store == "" {
		q.store = n.to
	}
	for _, tv := range in.Texts() {
		switch tv.Field {
		case "query":
			q.query = strings.TrimSpace(tv.Text)
		case "tables":
			q.tables = append(q.tables, tv.Text)
		case "since":
			q.since = tv.Text
		case "until":
			q.until = tv.Text
		}
	}
	if q.op == contextop.OpFetch {
		return a.noteEmptyContext(ctx, q.store, res) // emptycontext.go (devco:D-824)
	}
	if q.query == "" {
		return res
	}
	s := a.searchedOf(ctx, q)
	return withEmptyNote(res, s, a.emptyTries(ctx, q, s))
}

// withEmptyNote is res with s and try under its empty line and as the
// top-level --json fields searched and try.
func withEmptyNote(res op.Result, s emptySearched, try []string) op.Result {
	sj, serr := json.Marshal(s)
	tj, terr := json.Marshal(try)
	if serr != nil || terr != nil {
		return res
	}
	lines := emptySearchLines(s, try)
	return res.WithTextEdit(func(t string) string {
		return strings.TrimRight(t, "\n") + "\n" + lines
	}).WithTopJSON(SearchedField, sj).WithTopJSON(TryField, tj)
}

// searchedOf is what q read: the stores it read (one name per place, as a
// cross-store read walks them, --store's alone), the kinds and the words.
func (a *App) searchedOf(ctx context.Context, q emptySearchInput) emptySearched {
	s := emptySearched{Stores: []string{}, Kinds: []string{}, Words: []string{}}
	only := store.Source(q.store)
	for _, src := range a.readStores() {
		if opsmemory.FromStore(only, src) {
			s.Stores = append(s.Stores, string(src))
		}
	}
	if len(s.Stores) == 0 && q.store != "" {
		// a read naming its store runs on an App opened on that store
		// (routed reads, the sql lens), not on a: name what it read
		s.Stores = a.namedStoreReads(ctx, q.store)
	}
	noun, _, _ := strings.Cut(q.op, ".")
	switch q.op {
	case opsask.OpRun:
		s.Kinds = append(s.Kinds, "resources", "open tasks", everyKind+" of row")
		r := opsask.Read(q.query, a.Now().UTC())
		s.Words = append(s.Words, r.Words...)
		s.Type, s.Dates = r.Type, r.Dates
		return s
	case opssearch.OpRun:
		if len(q.tables) == 0 {
			s.Kinds = append(s.Kinds, everyKind)
		}
		s.Kinds = append(s.Kinds, q.tables...)
	default:
		s.Kinds = append(s.Kinds, noun)
	}
	s.Words = append(s.Words, searchrank.Plan(q.query, metaquery.Parse(q.query, func(string) bool { return false })).Terms...)
	switch {
	case q.since != "" && q.until != "":
		s.Dates = "since " + q.since + ", until " + q.until
	case q.since != "":
		s.Dates = "since " + q.since
	case q.until != "":
		s.Dates = "until " + q.until
	}
	return s
}

// namedStoreReads is what a read naming --store name reads: the store
// and, for the personal store, each of its folders; another store's top
// name reads every folder of it.
func (a *App) namedStoreReads(ctx context.Context, name string) []string {
	if name != attach.ReservedMe {
		if !strings.Contains(name, "/") && name != string(store.SourceRepo) {
			return []string{name + " (every folder)"}
		}
		return []string{name}
	}
	out := []string{name}
	if me, found, _, err := a.baseStore(ctx, attach.ReservedMe); err == nil && found {
		for _, f := range a.personalFolders(ctx, me) {
			out = append(out, string(f.Name))
		}
	}
	return out
}

// readStores is the attached stores a read across stores walks
// (engine.ReadStores); nil before the engine is open.
func (a *App) readStores() []store.Source {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil {
		return nil
	}
	return a.st.eng.ReadStores()
}

// emptyTries is the next commands of an empty search, likeliest first:
// a wider store (the store a folder belongs to, or the personal store a
// plain read left out), then the same words over every kind or without the
// filters that may have left the answer out, then ask for a search (typos,
// nicknames, dates) or search for an ask (word forms, partial matches).
// Each keeps the --store the search named, so it runs where it is printed.
func (a *App) emptyTries(ctx context.Context, q emptySearchInput, s emptySearched) []string {
	storeArg := ""
	if q.store != "" {
		storeArg = " --store " + q.store
	}
	cmd := "lore search"
	if q.op == opsask.OpRun {
		cmd = "lore ask"
	}
	query := " --query " + shellArg(q.query)
	var out []string
	add := func(c string) {
		for _, have := range out {
			if have == c {
				return
			}
		}
		out = append(out, c)
	}
	// a wider store first: an answer in a sibling folder or the personal
	// store is the likeliest miss (H-30, D-771)
	same := cmd
	if noun, verb, ok := strings.Cut(q.op, "."); ok && verb == searchVerb {
		same = "lore " + noun + " " + searchVerb
	}
	if top, _, isFolder := strings.Cut(q.store, "/"); isFolder && top != "" {
		add(same + query + " --store " + top)
	} else if q.store == "" && !inStore(a.opts.RepoDir) && a.personalUnread(ctx, s.Stores) {
		add(same + query + " --store " + attach.ReservedMe)
	}
	if q.op == taskSearchOp {
		// task search reads open tasks only; done and cancelled ones need --all
		add(same + query + " --all" + storeArg)
	}
	switch {
	case q.op == opsask.OpRun:
		if len(s.Words) > 0 {
			add("lore search --query " + shellArg(strings.Join(s.Words, " ")) + storeArg)
		}
	case q.op != opssearch.OpRun:
		add("lore search" + query + storeArg) // every kind, not only this one
	case len(q.tables) > 0 || q.since != "" || q.until != "":
		add("lore search" + query + storeArg) // without the filters
	}
	if q.op != opsask.OpRun {
		add("lore ask" + query + storeArg)
	}
	switch {
	case q.op == opsask.OpRun && s.Type == opsask.TypeIdentity:
		add("lore resource list" + storeArg) // the names the store knows
	case q.op == opsask.OpRun && s.Type == opsask.TypeDate && !strings.Contains(s.Dates, "month"):
		// a day or a week read: the month around it (ask reads "last month")
		add("lore ask --query " + shellArg(widerDateQuestion) + storeArg)
	case q.op == opsask.OpRun && len(s.Words) > 0:
		add("lore search --query " + shellArg(strings.Join(s.Words, " ")) + " --include-archived" + storeArg)
	case q.op == opssearch.OpRun:
		add("lore search" + query + " --include-archived" + storeArg)
	}
	if len(out) > emptyTryMax {
		out = out[:emptyTryMax]
	}
	return out
}

// widerDateQuestion is the date question an empty day or week question
// tries next: a window ask reads (question.go windowOf) and wider.
const widerDateQuestion = "what happened last month"

// personalUnread reports whether a personal store is set up and the read
// of stores left out the personal store or one of its folders.
func (a *App) personalUnread(ctx context.Context, stores []string) bool {
	read := map[string]bool{}
	for _, s := range stores {
		read[s] = true
	}
	if !a.hasPersonalStore(ctx) {
		return false
	}
	if !read[attach.ReservedMe] {
		return true
	}
	for _, f := range a.personalUnreadFolders(ctx) {
		if !read[f] {
			return true
		}
	}
	return false
}

// emptySearchLines is the text under an empty answer's empty line:
//
//	searched: repo, acme/eng (every kind)
//	read as: words: orbit, deploy; question: who is; dates: 2026-11-19 (yesterday)
//	try: lore ask --query "orbit deploy"
func emptySearchLines(s emptySearched, try []string) string {
	var b strings.Builder
	stores := "no attached store"
	if len(s.Stores) > 0 {
		stores = strings.Join(s.Stores, ", ")
	}
	b.WriteString("  searched: " + stores + " (" + strings.Join(s.Kinds, ", ") + ")\n")
	var read []string
	if s.Path != "" {
		at := "globs"
		if len(s.Folders) > 0 {
			at += "; folder scopes " + strings.Join(s.Folders, ", ")
		}
		read = append(read, "path: "+s.Path+" ("+at+"; rows naming it or its folder)")
	}
	if s.Topic != "" {
		read = append(read, "topic: "+s.Topic)
	}
	switch {
	case len(s.Words) > 0:
		read = append(read, "words: "+strings.Join(s.Words, ", "))
	case s.Path != "":
		read = append(read, "no topic word in the path (its names are generic)")
	case s.Topic == "":
		read = append(read, "no word to search for")
	}
	if s.Type != "" {
		read = append(read, "question: "+s.Type)
	}
	if s.Dates != "" {
		read = append(read, "dates: "+s.Dates)
	}
	b.WriteString("  read as: " + strings.Join(read, "; ") + "\n")
	for _, t := range try {
		b.WriteString("  try: " + t + "\n")
	}
	return b.String()
}

// shellArg is s as one shell word: in double quotes, or in single quotes
// when it holds a character double quotes would expand or end on.
func shellArg(s string) string {
	if !strings.ContainsAny(s, "\"$`\\!") {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
