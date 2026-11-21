package settings

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/rowref"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Everything about one resource in one read (devco D-792, owner P-19):
// `lore resource show --kind person --name alice-ng --with-refs` prints the
// resource, then every row whose text references it (a ${res:…} or its
// res_ id, the row_refs lines graph --up lists), each with its body in
// full, newest first, so "what do I know about alice" needs no second
// call per note. --store <store> covers every folder of the store. A row
// that names it in plain words ("Met alice-ng about orbit") is one of
// them too (devco D-822), as lore ask reads it.

// defaultMaxRefs is --max-refs' default: enough for a person's recent
// history, small enough that one read stays a few screens.
const defaultMaxRefs = 20

// RefBody is one row that references the resource, as --with-refs prints
// it: the row, where it is, its date and its text in full.
type RefBody struct {
	Row   string `json:"row" desc:"the id of the row whose text references the resource"`
	Table string `json:"table" desc:"its table (memories, tasks, decisions, …)"`
	// Fields are the text fields holding the reference (one row once);
	// Relations the --ref pointers at it (task add --ref <resource>).
	Fields    []string `json:"fields" desc:"the text fields holding the reference (${res:…}, the res_ id, or its name or an alias as a word)"`
	Relations []string `json:"relations,omitempty" desc:"the relation of each --ref pointer the row holds at it (mentions, …)"`
	Store     string   `json:"store,omitempty" desc:"where the row is stored"`
	// At is the date the row is about (schema.RowDate: valid_at, else
	// created_at), the order of the list.
	At    string `json:"at,omitempty" desc:"the date the row is about (valid_at, else when it was written), RFC 3339"`
	Title string `json:"title,omitempty" desc:"the row's title or name, when it has one"`
	Body  string `json:"body,omitempty" desc:"the row's text in full: its body, else its description, else its title"`
	// History is set on a row that is no longer current (devco D-820):
	// superseded by another row or past its valid_until, so the newest
	// row about the resource is not taken for the current fact when it is
	// not.
	History string `json:"history,omitempty" desc:"set when the row is no longer current, read it as history: superseded by <id> or valid until <day>"`
	when    time.Time
	// imported marks a row a feed brought in (source_kind imported): it
	// lists after the rows people wrote, as lore ask ranks it.
	imported bool
}

// sourceImported is the source_kind of a row a feed or an import brought
// in (memory import, feeds), not written by a person.
const sourceImported = "imported"

// namingTables are the tables whose rows --with-refs reads for the
// resource's name in plain words, with the text fields read: the tables
// lore ask answers from, and tasks (a task titled "send the deck to
// alice-ng").
var namingTables = []struct {
	table  row.Table
	fields []string
}{
	{schema.TableMemories, []string{"title", "body"}},
	{schema.TableDecisions, []string{"title", "body"}},
	{schema.TableRules, []string{"title", "body"}},
	{schema.TablePatterns, []string{"title", "body"}},
	{schema.TableIncidents, []string{"title", "body"}},
	{schema.TableTasks, []string{"title", "body"}},
}

// minNamedLen is the shortest name or alias --with-refs looks for in
// plain words: a one- or two-letter name ("go") is in too many rows.
const minNamedLen = 3

// withRefs fills out's ReferencedBy with the rows referencing resource id,
// newest first, at most max (defaultMaxRefs when 0): the rows whose text
// references it (row_refs, as graph --up), the rows whose --ref points at
// it (knowledge_refs) and the rows whose text names it or an alias of it
// as a word, in any letter case (namingTables), each row once. Why the
// names: a note written without ${res:…} ("Met alice-ng about orbit") is
// about the person all the same; lore ask finds it by the name, and an
// "everything about X" read that left it out lost the newest meeting
// (devco D-822).
func (o ops) withRefs(ctx context.Context, out ResOut, id string, max int) (ResOut, error) {
	if max <= 0 {
		max = defaultMaxRefs
	}
	hits, err := o.mentionHits(ctx, []string{id})
	if err != nil {
		return ResOut{}, err
	}
	ptrs, err := rowref.Referrers(ctx, o.env, schema.TableResources, id)
	if err != nil {
		return ResOut{}, err
	}
	now := opclock.Now(o.env)
	byRow := map[string]int{}
	bodies := []RefBody{}
	add := func(l located, table, field, relation string) {
		i, dup := byRow[l.Row.ID.String()]
		if !dup {
			i = len(bodies)
			byRow[l.Row.ID.String()] = i
			b := refBodyOf(l, table)
			b.History = schema.RowHistory(l.Row, now)
			bodies = append(bodies, b)
		}
		b := &bodies[i]
		if field != "" && !slices.Contains(b.Fields, field) {
			b.Fields = append(b.Fields, field)
			slices.Sort(b.Fields)
			// a reference in another text field (a decision's rationale)
			// is part of what the row says about it: print that too
			if t := text(l.Row, field); t != "" && !strings.Contains(b.Body, t) && t != b.Title {
				b.Body = strings.TrimSpace(b.Body + "\n" + field + ": " + t)
			}
		}
		if relation != "" && !slices.Contains(b.Relations, relation) {
			b.Relations = append(b.Relations, relation)
			slices.Sort(b.Relations)
		}
	}
	for _, h := range hits {
		add(h.at, h.ref.Table.String(), h.ref.Field, "")
	}
	live := map[row.Table]map[string]located{}
	srcs := engine.ReadStoresOf(o.env)
	for _, p := range ptrs {
		rows, err := liveTable(ctx, o.env, srcs, live, p.SrcTable)
		if err != nil {
			return ResOut{}, err
		}
		if l, ok := rows[p.SrcID]; ok { // a deleted row's pointer is not a reference
			add(l, p.SrcTable.String(), "", p.Relation)
		}
	}
	names, err := o.ownNames(ctx, srcs, live, id, out.Data)
	if err != nil {
		return ResOut{}, err
	}
	for _, t := range namingTables {
		if len(names) == 0 {
			break
		}
		rows, err := liveTable(ctx, o.env, srcs, live, t.table)
		if err != nil {
			return ResOut{}, err
		}
		for _, l := range rows {
			if l.Row.ID.String() == id {
				continue
			}
			for _, f := range t.fields {
				if namesAny(text(l.Row, f), names) {
					add(l, t.table.String(), f, "")
				}
			}
		}
	}
	slices.SortFunc(bodies, func(a, b RefBody) int {
		if a.imported != b.imported {
			// a daily feed naming the person would bury the last meeting
			if a.imported {
				return 1
			}
			return -1
		}
		if c := b.when.Compare(a.when); c != 0 {
			return c // newest first
		}
		return strings.Compare(b.Row, a.Row) // ids are time ordered: newest first too
	})
	out.withRefs = true
	out.feedLast = slices.ContainsFunc(bodies, func(r RefBody) bool { return r.imported })
	out.ReferencedTotal = len(bodies)
	out.ReferencedBy = bodies[:min(max, len(bodies))]
	return out, nil
}

// liveTable is table's live rows of srcs by id (the nearest store's copy
// of a row), read once per show: live keeps what was read.
func liveTable(ctx context.Context, env Env, srcs []store.Source, live map[row.Table]map[string]located, table row.Table) (map[string]located, error) {
	if rows, ok := live[table]; ok {
		return rows, nil
	}
	list, err := liveRows(ctx, env, srcs, table)
	if err != nil {
		return nil, err
	}
	rows := make(map[string]located, len(list))
	for _, l := range list {
		if _, dup := rows[l.Row.ID.String()]; !dup { // nearest store first
			rows[l.Row.ID.String()] = l
		}
	}
	live[table] = rows
	return rows, nil
}

// ownNames are the names and aliases of resource id (shown as r) that a
// plain word in a row can mean: those no resource of another kind has as
// its name or alias (a person and a team both called alice-ng: the word
// alone cannot say which, only ${res:person/alice-ng} can). The same name
// in another folder of the same kind is the same thing, a person kept in
// two folders.
func (o ops) ownNames(ctx context.Context, srcs []store.Source, live map[row.Table]map[string]located, id string, r Resource) ([]string, error) {
	names := namedAs(r)
	if len(names) == 0 {
		return nil, nil
	}
	res, err := liveTable(ctx, o.env, srcs, live, schema.TableResources)
	if err != nil {
		return nil, err
	}
	kind := ""
	if l, ok := res[id]; ok {
		kind = text(l.Row, rKind)
	}
	for _, l := range res {
		if l.Row.ID.String() == id || text(l.Row, rKind) == kind {
			continue
		}
		other := append([]string{text(l.Row, rName)}, texts(l.Row, rAliases)...)
		names = slices.DeleteFunc(names, func(n string) bool {
			return slices.ContainsFunc(other, func(x string) bool { return strings.EqualFold(strings.TrimSpace(x), n) })
		})
	}
	return names, nil
}

// namedAs is r's name and aliases in lower case, those of at least
// minNamedLen letters, each once.
func namedAs(r Resource) []string {
	var out []string
	for _, n := range append([]string{r.Name}, r.Aliases...) {
		if n = strings.ToLower(strings.TrimSpace(n)); utf8.RuneCountInString(n) >= minNamedLen && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// namesAny reports whether s holds one of names (lower case) as a word in
// any letter case: not inside a longer word or name, so "alice-ng" is not
// found in "alice-nguyen" nor "orbit" in "orbital".
func namesAny(s string, names []string) bool {
	if s == "" {
		return false
	}
	low := strings.ToLower(s)
	for _, n := range names {
		for i := 0; ; {
			j := strings.Index(low[i:], n)
			if j < 0 {
				break
			}
			at, end := i+j, i+j+len(n)
			before, _ := utf8.DecodeLastRuneInString(low[:at])
			after, _ := utf8.DecodeRuneInString(low[end:])
			if !nameRune(before) && !nameRune(after) {
				return true
			}
			i = at + 1
		}
	}
	return false
}

// nameRune reports whether r can be part of a name: a letter, a digit,
// "-" or "_" (utf8.RuneError, outside the text, is not).
func nameRune(r rune) bool {
	return r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// refBodyOf is row l of table as --with-refs prints it: its title or name,
// its text in full (body, else description, else title) and its date.
func refBodyOf(l located, table string) RefBody {
	r := l.Row
	title := cmp.Or(text(r, "title"), text(r, kName))
	body := cmp.Or(text(r, "body"), text(r, "description"), text(r, "text"))
	if body == "" || body == title {
		body, title = cmp.Or(body, title), ""
	}
	b := RefBody{Row: r.ID.String(), Table: table, Fields: []string{}, Store: string(l.Src), Title: title, Body: body, when: schema.RowDate(r), imported: text(r, "source_kind") == sourceImported}
	if !b.when.IsZero() {
		b.At = b.when.UTC().Format(time.RFC3339)
	}
	return b
}

// refsText prints --with-refs' rows under the resource: a header with the
// count, then each row's date, table, id and store, its title and its
// body indented; "" without --with-refs.
func (o ResOut) refsText() string {
	if !o.withRefs {
		return ""
	}
	var b strings.Builder
	if o.ReferencedTotal == 0 {
		b.WriteString("referenced by: no row references it\n")
		return b.String()
	}
	order := "newest first"
	if o.feedLast {
		order += ", the rows a feed imported after the rest"
	}
	fmt.Fprintf(&b, "referenced by %s, %s:\n", plural.Count(o.ReferencedTotal, "row"), order)
	for _, r := range o.ReferencedBy {
		day := ""
		if !r.when.IsZero() {
			day = r.when.UTC().Format("2006-01-02") + "  "
		}
		fmt.Fprintf(&b, "\n  %s%s %s", day, r.Table, r.Row)
		if r.Store != "" {
			fmt.Fprintf(&b, "  (%s)", r.Store)
		}
		if len(r.Relations) > 0 {
			fmt.Fprintf(&b, "  --ref %s", strings.Join(r.Relations, ", "))
		}
		if r.History != "" {
			b.WriteString("  [" + r.History + "]")
		}
		b.WriteString("\n")
		if r.Title != "" {
			b.WriteString("    " + r.Title + "\n")
		}
		for _, line := range strings.Split(strings.TrimRight(r.Body, "\n"), "\n") {
			b.WriteString(strings.TrimRight("    "+line, " ") + "\n")
		}
	}
	if left := o.ReferencedTotal - len(o.ReferencedBy); left > 0 {
		what := "older row"
		if o.feedLast {
			what = "more row" // an imported row left out may be newer
		}
		fmt.Fprintf(&b, "\n  %s left out; --max-refs %d prints them all\n", plural.Count(left, what), o.ReferencedTotal)
	}
	return b.String()
}
