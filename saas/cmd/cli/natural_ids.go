// natural_ids.go — deterministic ids for rows with a natural unique key.
//
// Why: lore data is shared through git (LORE_SYNC_SPEC.md, E2). Two clones
// that each create tag "auth" in the same project would otherwise mint two
// random ids; their files collide on the (project_id, name) unique index
// when the branches merge. Deriving the id from the natural key makes both
// clones write the SAME file, which git merges as an identical add.
//
// The sync engine still merges pre-existing duplicates (rows created before
// this hook, or by writers that bypass it such as `lore tui`); this hook
// just stops new ones from appearing.
package main

import (
	"context"
	"fmt"
	"strings"

	"dbent/gen/ent"

	"saas/pkg/aicoder/ids"
)

// naturalIDSpec says how one entity type derives its id.
type naturalIDSpec struct {
	// table is the SQL table, used by the drift test against lsync's
	// registry of natural keys.
	table  string
	prefix string
	// fields are the natural-key columns in schema (index) order.
	fields []string
}

// naturalIDSpecs is keyed by ent mutation type name. natural_ids_test.go
// fails when a synced table gains a unique key that is missing here.
var naturalIDSpecs = map[string]naturalIDSpec{
	"Actor":           {table: "actors", prefix: ids.PrefixActor, fields: []string{"stable_key"}},
	"Tag":             {table: "tags", prefix: ids.PrefixTag, fields: []string{"project_id", "name"}},
	"Repo":            {table: "repos", prefix: ids.PrefixRepo, fields: []string{"project_id", "mount_name"}},
	"Prompt":          {table: "prompts", prefix: ids.PrefixPrompt, fields: []string{"project_id", "name"}},
	"ProjectConfig":   {table: "project_configs", prefix: ids.PrefixProjectConfig, fields: []string{"project_id", "key"}},
	"CommitLink":      {table: "commit_links", prefix: ids.PrefixCommitLink, fields: []string{"entity_table", "entity_id", "sha"}},
	"EntityTag":       {table: "entity_tags", prefix: ids.PrefixEntityTag, fields: []string{"entity_table", "entity_id", "tag_id"}},
	"RuleVerifierRef": {table: "rule_verifier_refs", prefix: ids.PrefixRuleVerifierRef, fields: []string{"rule_id", "verifier_kind", "verifier_ref"}},
}

// idSetter is implemented by every generated create mutation whose id is a
// string (ent generates SetID for user-defined id fields).
type idSetter interface {
	SetID(id string)
}

// naturalIDHook overrides the random UUIDv7 default with the deterministic
// id on CREATE of a natural-key entity. Updates are untouched (ids are
// immutable). If any key field is unset the random id is kept — the create
// would fail its NOT NULL / unique checks anyway.
func naturalIDHook() ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if !m.Op().Is(ent.OpCreate) {
				return next.Mutate(ctx, m)
			}
			spec, ok := naturalIDSpecs[m.Type()]
			if !ok {
				return next.Mutate(ctx, m)
			}
			setter, ok := m.(idSetter)
			if !ok {
				return next.Mutate(ctx, m)
			}
			parts := make([]string, 0, len(spec.fields))
			for _, f := range spec.fields {
				v, set := m.Field(f)
				if !set {
					return next.Mutate(ctx, m)
				}
				parts = append(parts, fmt.Sprint(v))
			}
			id, err := ids.Deterministic(spec.prefix, parts...)
			if err != nil {
				return nil, fmt.Errorf("natural id for %s: %w", m.Type(), err)
			}
			setter.SetID(id)
			v, err := next.Mutate(ctx, m)
			if err == nil || !isPrimaryKeyCollision(err, spec.table) {
				return v, err
			}
			// The natural-key fields are editable (`prompt edit --name`,
			// tag renames in the TUI): a row created under this key and
			// renamed since still holds the deterministic id. The key itself
			// is free (its unique index did not fire), so fall back to a
			// random id rather than refuse a valid create.
			random, rerr := ids.New(spec.prefix)
			if rerr != nil {
				return nil, fmt.Errorf("natural id fallback for %s: %w", m.Type(), rerr)
			}
			setter.SetID(random)
			return next.Mutate(ctx, m)
		})
	}
}

// isPrimaryKeyCollision reports whether err is SQLite rejecting a duplicate
// id in table (as opposed to a natural-key unique index, which must still
// fail loudly).
func isPrimaryKeyCollision(err error, table string) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed: "+table+".id")
}
