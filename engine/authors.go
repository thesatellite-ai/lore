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
	return BindListAuthors(ctx, e.reg, in)
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
func keepAuthor(ctx context.Context, r row.Row) bool {
	s, ok := ctx.Value(authorsKey{}).(authorsScope)
	if !ok || s.table != r.Table {
		return true
	}
	if !r.Deleted {
		r, _ = auditfill.Defaults(r)
	}
	if !s.created.IsZero() && r.CreatedBy != s.created {
		return false
	}
	return s.updated.IsZero() || r.UpdatedBy == s.updated
}
