package settings

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/rowref"
	"github.com/thesatellite-ai/lore/schema"
)

// Everything about one resource in one read (devco D-792, owner P-19):
// `lore resource show --kind person --name alice-ng --with-refs` prints the
// resource, then every row whose text references it (a ${res:…} or its
// res_ id, the row_refs lines graph --up lists), each with its body in
// full, newest first, so "what do I know about alice" needs no second
// call per note. --store <store> covers every folder of the store.

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
	Fields    []string `json:"fields" desc:"the text fields holding the reference (${res:…} or the res_ id)"`
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
}

// withRefs fills out's ReferencedBy with the rows referencing resource id,
// newest first, at most max (defaultMaxRefs when 0): the rows whose text
// names it (row_refs, as graph --up) and the rows whose --ref points at
// it (knowledge_refs), each row once.
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
		rows, ok := live[p.SrcTable]
		if !ok {
			list, lerr := liveRows(ctx, o.env, srcs, p.SrcTable)
			if lerr != nil {
				return ResOut{}, lerr
			}
			rows = make(map[string]located, len(list))
			for _, l := range list {
				if _, dup := rows[l.Row.ID.String()]; !dup { // nearest store first
					rows[l.Row.ID.String()] = l
				}
			}
			live[p.SrcTable] = rows
		}
		if l, ok := rows[p.SrcID]; ok { // a deleted row's pointer is not a reference
			add(l, p.SrcTable.String(), "", p.Relation)
		}
	}
	slices.SortFunc(bodies, func(a, b RefBody) int {
		if c := b.when.Compare(a.when); c != 0 {
			return c // newest first
		}
		return strings.Compare(b.Row, a.Row) // ids are time ordered: newest first too
	})
	out.withRefs = true
	out.ReferencedTotal = len(bodies)
	out.ReferencedBy = bodies[:min(max, len(bodies))]
	return out, nil
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
	b := RefBody{Row: r.ID.String(), Table: table, Fields: []string{}, Store: string(l.Src), Title: title, Body: body, when: schema.RowDate(r)}
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
	fmt.Fprintf(&b, "referenced by %s, newest first:\n", plural.Count(o.ReferencedTotal, "row"))
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
		fmt.Fprintf(&b, "\n  %s left out; --max-refs %d prints them all\n", plural.Count(left, "older row"), o.ReferencedTotal)
	}
	return b.String()
}
