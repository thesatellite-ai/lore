package pattern

// --scope on add and edit (CONTEXT_BUDGET 6.2.1/6.2.3, CQ12): a folder
// scope ranks the row in lore context only; it is never pinned and never
// written into a generated file.
//
//	add:  scopeAdd  ─► scope.ForWrite ─► To, Repo, fields[scope_id]
//	edit: scopeEdit ─► scope.ForWrite ─► RebindRepo|RebindMaster, patch[scope_id]

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/content"
	opsscope "github.com/thesatellite-ai/lore/ops/scope"
	"github.com/thesatellite-ai/lore/schema"
)

// scopeAdd applies --scope to an add: the store, the repo and the folder.
func scopeAdd(ctx context.Context, env content.Env, raw string, to, repo *string, fields map[string]value.Value) error {
	st, err := opsscope.ForWrite(ctx, env, raw, *to, *repo, false)
	if err != nil {
		return err
	}
	if st.To != "" {
		*to = st.To
	}
	if st.Repo != "" {
		*repo = st.Repo
	}
	if st.ScopeID != "" {
		fields[schema.FieldScope], err = scopeRef(st.ScopeID)
	}
	return err
}

// scopeEdit applies --scope to an edit; it moves the repo binding the same
// way --rebind-repo/--rebind-master do, so giving both is refused.
func scopeEdit(ctx context.Context, env content.Env, raw string, rebindRepo *string, rebindMaster *bool, patch map[string]value.Value) error {
	st, err := opsscope.ForWrite(ctx, env, raw, "", "", true)
	if err != nil || !st.Given() {
		return err
	}
	if *rebindRepo != "" || *rebindMaster {
		return errs.New(errs.InvalidInput, Noun+" edit: give --scope or --rebind-repo/--rebind-master, not both")
	}
	*rebindRepo, *rebindMaster = st.Repo, st.Master
	if st.ClearScope {
		patch[schema.FieldScope] = value.Null{}
	}
	if st.ScopeID != "" {
		patch[schema.FieldScope], err = scopeRef(st.ScopeID)
	}
	return err
}

// scopeRef is a scp_ id as a ref value.
func scopeRef(id string) (value.Value, error) {
	sid, err := ids.ParseAny(id)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, Noun+": scope id", err)
	}
	return value.RefOf(sid)
}
