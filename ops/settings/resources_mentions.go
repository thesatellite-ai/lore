package settings

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/row"
)

// Notes that mention a resource (devco D-764): a memory, task, decision or
// any row whose text holds ${res:<name>}, ${res:<kind>/<name>…} or the
// res_ id gets a row_refs line in the cache when its text is expanded
// (spec 4.5). resource graph --up and resource impact list those rows
// under the tree, so "what uses this resource" answers with the notes
// too, not only the resources whose ref fields point at it.

// RefRow is one text field that references an id (a row_refs line).
type RefRow struct {
	Table row.Table
	ID    string
	Field string
	RefID string
}

// RefFinder is the Env upgrade that reads row_refs: the rows whose text
// references one of refIDs, in every cache file of the view. wire.App
// implements it; an Env without it lists no mentions.
type RefFinder interface {
	RowsReferencing(ctx context.Context, refIDs []string) ([]RefRow, error)
}

// GraphMention is one row whose text mentions a node of the walk (or the
// root) through a reference.
type GraphMention struct {
	Row   string `json:"row" desc:"the id of the row whose text holds the reference"`
	Table string `json:"table" desc:"its table (memories, tasks, decisions, …)"`
	Field string `json:"field" desc:"the text field holding the reference"`
	Node  string `json:"node" desc:"the resource it mentions (the root or a node of the walk)"`
	Title string `json:"title" desc:"the row's title, name or first line of its body"`
	Store string `json:"store,omitempty" desc:"where the row is stored"`
}

// mentionTitleMax cuts a mention's title in the printed list.
const mentionTitleMax = 60

// mentions lists the live rows of the view whose text references one of
// nodes, sorted by node, table and row; nil when the Env cannot read
// row_refs.
func (o ops) mentions(ctx context.Context, nodes []string) ([]GraphMention, error) {
	hits, err := o.mentionHits(ctx, nodes)
	if err != nil {
		return nil, err
	}
	var out []GraphMention
	for _, h := range hits {
		out = append(out, GraphMention{Row: h.ref.ID, Table: h.ref.Table.String(), Field: h.ref.Field, Node: h.ref.RefID, Title: mentionTitle(h.at.Row), Store: string(h.at.Src)})
	}
	slices.SortFunc(out, func(a, b GraphMention) int {
		return strings.Compare(a.Node+"\x00"+a.Table+"\x00"+a.Row+"\x00"+a.Field, b.Node+"\x00"+b.Table+"\x00"+b.Row+"\x00"+b.Field)
	})
	return out, nil
}

// mentionHit is one row_refs line whose row is live in this view, with
// that row (graph --up prints its title, resource show --with-refs its
// body: devco D-792).
type mentionHit struct {
	ref RefRow
	at  located
}

// mentionHits are the row_refs lines of the view that reference one of
// nodes and whose row is live here (nearest store first), each line once;
// nil when the Env cannot read row_refs.
func (o ops) mentionHits(ctx context.Context, nodes []string) ([]mentionHit, error) {
	f, ok := o.env.(RefFinder)
	if !ok || len(nodes) == 0 {
		return nil, nil
	}
	refRows, err := f.RowsReferencing(ctx, nodes)
	if err != nil {
		return nil, err
	}
	byTable := map[row.Table]map[string]located{}
	srcs := engine.ReadStoresOf(o.env)
	var out []mentionHit
	seen := map[string]bool{}
	for _, rr := range refRows {
		rows, ok := byTable[rr.Table]
		if !ok {
			list, lerr := liveRows(ctx, o.env, srcs, rr.Table)
			if lerr != nil {
				return nil, lerr
			}
			rows = make(map[string]located, len(list))
			for _, l := range list {
				if _, dup := rows[l.Row.ID.String()]; !dup { // nearest store first
					rows[l.Row.ID.String()] = l
				}
			}
			byTable[rr.Table] = rows
		}
		l, live := rows[rr.ID]
		key := rr.RefID + "\x00" + rr.ID + "\x00" + rr.Field
		if !live || seen[key] {
			continue // deleted, out of this view, or a second cache file's copy
		}
		seen[key] = true
		out = append(out, mentionHit{ref: rr, at: l})
	}
	return out, nil
}

// mentionTitle is a row's title, its name, or the first line of its body,
// cut to mentionTitleMax runes.
func mentionTitle(r row.Row) string {
	t := ""
	for _, f := range []string{"title", kName, "body"} {
		if t = text(r, f); t != "" {
			break
		}
	}
	t, _, _ = strings.Cut(t, "\n")
	if rs := []rune(t); len(rs) > mentionTitleMax {
		t = string(rs[:mentionTitleMax-1]) + "…"
	}
	return t
}

// mentionLines prints mentions under a graph or impact, each with the
// node it mentions (label gives "<kind> <name>").
func mentionLines(ms []GraphMention, indent string, label func(id string) string) string {
	if len(ms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(indent + "mentioned in:\n")
	for _, m := range ms {
		b.WriteString(indent + "  " + m.Table + " " + m.Row + " (" + m.Field + ", mentions " + label(m.Node) + ")")
		if m.Title != "" {
			b.WriteString("  " + m.Title)
		}
		b.WriteString("\n")
	}
	return b.String()
}
