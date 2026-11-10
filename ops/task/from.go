package task

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/store"
)

// SourceName implements op.SourceNamer: `task show --from`.
func (i ShowIn) SourceName() string { return sourceToAttach(i.From) }

// SourceName implements op.SourceNamer: `task done --from`.
func (i DoneIn) SourceName() string { return sourceToAttach(i.From) }

// sourceToAttach is the --from value the runner may need to attach: every
// value but "" and repo (the repo store is always open).
func sourceToAttach(from string) string {
	if from == fromRepo {
		return ""
	}
	return from
}

// getSource is the lookup of a write that takes --from: the normal lookup
// when from is "", else the source it names (getFrom).
func (o ops) getSource(ctx context.Context, from string, id ids.AnyID) (store.Source, row.Row, error) {
	if from == "" {
		return work.Get(ctx, o.env, Noun, table, id)
	}
	return o.getFrom(ctx, from, id)
}

// patchAt is work.Patch in a store already chosen (--from), so an id two
// sources hold is written where the caller named instead of answering
// E_AMBIGUOUS_ID.
func (o ops) patchAt(ctx context.Context, env work.Env, src store.Source, id ids.AnyID, patch map[string]value.Value, human string) (work.WriteOut, error) {
	w, err := env.Update(ctx, engine.Ref{Store: src, Table: table, ID: id}, patch)
	if err != nil {
		return work.WriteOut{}, err
	}
	return work.Out(src, w, human), nil
}
