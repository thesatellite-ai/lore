// Package knscope wires --scope and --overrides (CONTEXT_BUDGET 6.2.3,
// D10) into the knowledge nouns' add and edit (rule, hotfix, decision),
// on top of ops/internal/knowledge and ops/scope.
//
//	add:  ForAdd  ─► scope.ForWrite ─► kn.Create{To, Repo, Texts[scope_id]}
//	                 └─ --overrides ─► scope.CheckOverrides ─► Texts[overrides_id]
//	edit: ForEdit ─► kn.Edit{RebindRepo|RebindMaster, Texts[scope_id]}
//	                 └─ After: null scope_id / overrides_id when cleared
package knscope

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/dirscope"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	kn "github.com/thesatellite-ai/lore/ops/internal/knowledge"
	"github.com/thesatellite-ai/lore/ops/scope"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// ClearOverrides is the --overrides value that removes the link.
const ClearOverrides = "none"

// ForAdd applies --scope (raw) and --overrides to c before kn.Add. label is
// the scope the add line prints ("" = none given, keep the noun's own).
func ForAdd(ctx context.Context, env kn.Env, n kn.Noun, raw, overrides string, c *kn.Create) (label string, warns []kn.Warning, err error) {
	t, err := scope.ForWrite(ctx, env, raw, c.To, c.Repo, false)
	if err != nil {
		return "", nil, err
	}
	if c.Texts == nil {
		c.Texts = map[string]string{}
	}
	if t.To != "" {
		c.To = t.To
	}
	if t.Repo != "" {
		c.Repo = t.Repo
	}
	if t.ScopeID != "" {
		c.Texts[schema.FieldScope] = t.ScopeID
	}
	if o := strings.TrimSpace(overrides); o != "" && o != ClearOverrides {
		cur := dirscope.Place{}
		if c.Repo != "" {
			rid, rerr := env.RepoID(ctx, c.Repo)
			if rerr != nil {
				return "", nil, rerr
			}
			cur.Repo = rid.String()
		}
		id, oerr := scope.CheckOverrides(ctx, env, n.Name, n.Table, n.Prefix, o, t.Place(cur), "")
		if oerr != nil {
			return "", nil, oerr
		}
		c.Texts[schema.FieldOverrides] = id
	}
	if t.Given() {
		label = t.Spec.Label()
	}
	return label, warnings(t), nil
}

// Plan is what ForEdit adds to an edit.
type Plan struct {
	// After nulls scope_id (project or repo scope) and overrides_id
	// (--overrides none), which a text patch cannot clear; nil when none.
	After func(ctx context.Context) error
	// Only is set when After is the whole edit (no other field changes):
	// the caller then skips kn.Apply, which would refuse "nothing to change".
	Only     bool
	Warnings []kn.Warning
}

// Run runs the edit: apply (kn.Apply) unless the plan is After alone, then
// After. Apply's result is returned; with Only, the stamped row's.
func (p Plan) Run(ctx context.Context, env kn.Env, n kn.Noun, id string, apply func() (store.Source, engine.Written, error)) (store.Source, engine.Written, error) {
	var src store.Source
	var w engine.Written
	var err error
	if !p.Only {
		if src, w, err = apply(); err != nil {
			return src, w, err
		}
	}
	if p.After == nil {
		return src, w, nil
	}
	if err = p.After(ctx); err != nil {
		return src, w, err
	}
	it, err := kn.Show(ctx, env, n, id)
	if err != nil {
		return src, w, err
	}
	if p.Only {
		w = engine.Written{Row: it.Row}
	}
	return it.Source, w, nil
}

// ForEdit applies --scope (raw) and --overrides to e before kn.Apply of
// the row whose id is id.
func ForEdit(ctx context.Context, env kn.Env, n kn.Noun, id, raw, overrides string, e *kn.Edit) (Plan, error) {
	o := strings.TrimSpace(overrides)
	if strings.TrimSpace(raw) == "" && o == "" {
		return Plan{}, nil
	}
	t, err := scope.ForWrite(ctx, env, raw, "", "", true)
	if err != nil {
		return Plan{}, err
	}
	if t.Given() && (e.RebindRepo != "" || e.RebindMaster) {
		return Plan{}, errs.New(errs.InvalidInput, n.Name+" edit: give --scope or --rebind-repo/--rebind-master, not both")
	}
	it, err := kn.Show(ctx, env, n, id)
	if err != nil {
		return Plan{}, err
	}
	if e.Texts == nil {
		e.Texts = map[string]string{}
	}
	switch {
	case t.Master:
		e.RebindMaster = true
	case t.Repo != "":
		e.RebindRepo = t.Repo
	}
	if t.ScopeID != "" {
		e.Texts[schema.FieldScope] = t.ScopeID
	}
	var nulls []string
	if t.ClearScope && kn.Field(it.Row, schema.FieldScope) != "" {
		nulls = append(nulls, schema.FieldScope)
	}
	switch {
	case o == ClearOverrides:
		nulls = append(nulls, schema.FieldOverrides)
	case o != "":
		narrow := t.Place(scope.PlaceOf(ctx, env, it.Row))
		oid, oerr := scope.CheckOverrides(ctx, env, n.Name, n.Table, n.Prefix, o, narrow, it.Row.ID.String())
		if oerr != nil {
			return Plan{}, oerr
		}
		e.Texts[schema.FieldOverrides] = oid
	}
	p := Plan{Warnings: warnings(t)}
	if len(nulls) > 0 {
		p.After = func(ctx context.Context) error {
			for _, f := range nulls {
				if _, err := kn.Stamp(ctx, env, n, id, f, value.Null{}); err != nil {
					return err
				}
			}
			return nil
		}
		p.Only = !hasText(e.Texts) && e.RebindRepo == "" && !e.RebindMaster && len(e.Tags) == 0 && len(e.Untags) == 0
	}
	return p, nil
}

// hasText reports whether any text field is set (an empty value is "not given").
func hasText(m map[string]string) bool {
	for _, v := range m {
		if v != "" {
			return true
		}
	}
	return false
}

// warnings turns t's lines into the write's warnings.
func warnings(t scope.Target) []kn.Warning {
	var out []kn.Warning
	for _, w := range t.Warnings {
		out = append(out, kn.Warning{Code: "W_SCOPE_PATH", Field: schema.FieldScope, Message: w})
	}
	return out
}
