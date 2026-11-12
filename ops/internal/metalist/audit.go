package metalist

// audit.go — the shared --created-by / --updated-by list filters
// (INTEG-AUDIT-1). Every synced row carries created_by / updated_by in its
// envelope (model/row, stamped by the engine write path from the calling
// actor), so the filter reads the envelope, never a table column: it means
// the same on every noun and needs no per-noun code. Flags embeds Audit, so
// every list that embeds Flags gets both flags and the REST query params
// (created_by, updated_by) from the same struct; Select applies them.

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// Audit is the pair of writer filters. A value is an actor id (act_…) or
// "unknown" (the reserved actor old rows are backfilled with, auditfill).
type Audit struct {
	CreatedBy string `json:"created_by,omitempty" flag:"created-by" desc:"only rows this actor created (act_ id, or unknown for rows from before audit fields)"`
	UpdatedBy string `json:"updated_by,omitempty" flag:"updated-by" desc:"only rows this actor changed last (act_ id, or unknown)"`
}

// auditMatcher is a parsed Audit; a zero id matches every row.
type auditMatcher struct {
	created, updated ids.ID[ids.Actor]
}

// matcher parses the flags (E_INVALID_ID for a value that is not an actor
// id; the flag is named in the message).
func (a Audit) matcher() (auditMatcher, error) {
	var m auditMatcher
	var err error
	if m.created, err = auditfill.ParseActor("--created-by", a.CreatedBy); err != nil {
		return m, err
	}
	m.updated, err = auditfill.ParseActor("--updated-by", a.UpdatedBy)
	return m, err
}

// keep reports whether r passes the filters. The row's audit actors are
// read through auditfill.Defaults, so an old row without the keys answers
// as the `unknown` actor, exactly as `lore migrate run` would write it.
// An actor merged into the one asked for (`lore actor merge`) counts as
// it (engine.SameActor): history stamps keep the old id.
func (m auditMatcher) keep(ctx context.Context, r row.Row) bool {
	if m.created.IsZero() && m.updated.IsZero() {
		return true
	}
	r, _ = auditfill.Defaults(r)
	if !m.created.IsZero() && !engine.SameActor(ctx, r.CreatedBy, m.created) {
		return false
	}
	return m.updated.IsZero() || engine.SameActor(ctx, r.UpdatedBy, m.updated)
}

// Keep is the filter for a list that embeds Audit without Flags (task
// list): the indexes of rows that pass, in order.
func (a Audit) Keep(rows []row.Row) ([]int, error) {
	m, err := a.matcher()
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(rows))
	for i, r := range rows {
		if m.keep(context.Background(), r) {
			out = append(out, i)
		}
	}
	return out, nil
}
