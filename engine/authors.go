package engine

// authors.go — the --created-by / --updated-by list filters at the one
// read path (INTEG-AUDIT-1). Run binds them from any `<noun>.list` input
// that has the created_by / updated_by fields (ops/internal/metalist.Audit
// is the one definition every list embeds) the way it binds --meta and
// --live, and List keeps only the rows of the list's own table whose
// envelope names the actor. So a list gets the filter by embedding the
// struct, with no code of its own, and the meaning is the same everywhere.
//
//	Run ─ bindInputAuthors(op, input) ─ ctx{table, created, updated}
//	List(table) ─ auditfill.Defaults(row) ─ keep when created/updated match

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// Input field names of the writer filters (metalist.Audit's json names).
const (
	inputCreatedBy = "created_by"
	inputUpdatedBy = "updated_by"
	listVerb       = ".list"
)

type authorsKey struct{}

// authorsScope is a writer filter bound to one table (the list's own, so
// lookups of other tables on the way stay whole).
type authorsScope struct {
	table            row.Table
	created, updated ids.ID[ids.Actor]
}

// bindInputAuthors puts a list input's writer filter on ctx. Inputs of
// other verbs (an add's --created-by) are never read here.
func (e *Engine) bindInputAuthors(ctx context.Context, in op.Input) (context.Context, error) {
	ctx, err := BindListAuthors(ctx, e.reg, in)
	if err != nil {
		return ctx, err
	}
	if _, ok := ctx.Value(authorsKey{}).(authorsScope); ok {
		ctx = context.WithValue(ctx, sameActorKey{}, &mergedResolver{e: e})
	}
	return ctx, nil
}

// sameActorKey is the context key of the run's merged-actor resolver.
type sameActorKey struct{}

// mergedResolver reads the merged actors once per run, on first use.
type mergedResolver struct {
	e    *Engine
	once sync.Once
	m    map[ids.ID[ids.Actor]]ids.ID[ids.Actor]
}

// SameActor reports whether a and b name one actor once `lore actor
// merge` marks are followed (merged_into): a list's --created-by and
// --updated-by filters (here and ops/internal/metalist) match the rows a
// person made under an id merged into the one given, whose history
// stamps keep the old id. Outside a list run with a writer filter it is
// a == b.
func SameActor(ctx context.Context, a, b ids.ID[ids.Actor]) bool {
	if a == b {
		return true
	}
	r, ok := ctx.Value(sameActorKey{}).(*mergedResolver)
	if !ok || r == nil {
		return false
	}
	r.once.Do(func() { r.m = r.e.mergedActors(ctx) })
	return mergedEnd(r.m, a) == mergedEnd(r.m, b)
}

// BindListAuthors is bindInputAuthors for a host that runs an operation
// without Engine.Run (wire's direct ops: the roster lists of team
// profiles and invites read their store through an engine of their own,
// so Run never sees their input). Without it their --created-by and
// --updated-by would be accepted and silently ignored.
func BindListAuthors(ctx context.Context, reg *schema.Registry, in op.Input) (context.Context, error) {
	if !strings.HasSuffix(in.Op(), listVerb) {
		return ctx, nil
	}
	var s authorsScope
	for _, tv := range in.Texts() {
		var err error
		switch tv.Field {
		case inputCreatedBy:
			s.created, err = auditfill.ParseActor("--created-by", tv.Text)
		case inputUpdatedBy:
			s.updated, err = auditfill.ParseActor("--updated-by", tv.Text)
		}
		if err != nil {
			return nil, err
		}
	}
	if s.created.IsZero() && s.updated.IsZero() {
		return ctx, nil
	}
	s.table = nounTable(reg, strings.TrimSuffix(in.Op(), listVerb))
	if s.table == "" {
		return ctx, nil
	}
	return context.WithValue(ctx, authorsKey{}, s), nil
}

// nounTable is the table a command noun names: a table that declares
// the noun (schema.Noun: "link" → commit_links), else the plural rule
// ("pii_pattern" → pii_patterns, "repo" → repos, "memory" →
// memories); "" for none.
func nounTable(reg *schema.Registry, noun string) row.Table {
	for _, def := range reg.Tables() {
		if def.Noun != "" && def.Noun == noun {
			return def.Name
		}
	}
	n := strings.ReplaceAll(noun, "_", "")
	want := []string{n + "s", n + "es", strings.TrimSuffix(n, "y") + "ies"}
	for _, def := range reg.Tables() {
		if slices.Contains(want, strings.ReplaceAll(string(def.Name), "_", "")) {
			return def.Name
		}
	}
	return ""
}

// keepAuthor reports whether List keeps r under ctx's writer filter. An
// old row without audit keys answers as `unknown` (auditfill.Defaults).
// A merged actor (`lore actor merge`: its row carries merged_into) is the
// actor it was merged into, on the row's stamps and in the filter alike:
// history stamps keep the old id, and `--created-by <kept id>` still
// finds the rows the person made under it (owner report 2026-11-12).
func keepAuthor(ctx context.Context, r row.Row) bool {
	s, ok := ctx.Value(authorsKey{}).(authorsScope)
	if !ok || s.table != r.Table {
		return true
	}
	if !r.Deleted {
		r, _ = auditfill.Defaults(r)
	}
	if !s.created.IsZero() && !SameActor(ctx, r.CreatedBy, s.created) {
		return false
	}
	return s.updated.IsZero() || SameActor(ctx, r.UpdatedBy, s.updated)
}

// mergedEnd follows a's merged_into chain to the actor that stays (a
// itself when it was never merged; a cycle stops where it closes).
func mergedEnd(m map[ids.ID[ids.Actor]]ids.ID[ids.Actor], a ids.ID[ids.Actor]) ids.ID[ids.Actor] {
	seen := map[ids.ID[ids.Actor]]bool{}
	for !seen[a] {
		seen[a] = true
		next, ok := m[a]
		if !ok {
			return a
		}
		a = next
	}
	return a
}

// mergedActors maps every actor row that carries merged_into, in any
// attached store, to the actor it was merged into.
func (e *Engine) mergedActors(ctx context.Context) map[ids.ID[ids.Actor]]ids.ID[ids.Actor] {
	out := map[ids.ID[ids.Actor]]ids.ID[ids.Actor]{}
	for _, name := range e.order {
		rows, err := e.readTable(ctx, name, schema.TableActors)
		if err != nil {
			continue // an unreadable store merges nothing here
		}
		for _, sr := range rows {
			r, err := sr.Decode()
			if err != nil || r.Deleted {
				continue
			}
			v, ok := r.Fields[schema.FieldActorMergedInto]
			if !ok || v == nil || v.String() == "" {
				continue
			}
			from, ferr := ids.Parse[ids.Actor](r.ID.String())
			to, terr := ids.Parse[ids.Actor](v.String())
			if ferr == nil && terr == nil {
				out[from] = to
			}
		}
	}
	return out
}
