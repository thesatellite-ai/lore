package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// MigrateCommand is the command that answers the O6 question.
const MigrateCommand = "lore migrate run"

// formatState is the DOCS O6 answer for one open App (FORMAT_EVOLUTION
// rule 2, F5): the first 0.1.18 write in a repo whose rows were written by
// someone else with v0.1.17 asks first, because the `_v: 2` rows it writes
// cannot be read by a teammate still on v0.1.17.
//
// No hidden state: "first" is read from the rows themselves. A repo that
// already holds a `_v: 2` row has had its first write; a repo with only
// the caller's own (or unattributed) v1 rows never asks.
type formatState struct {
	// ask: v1 rows by another writer and no v2 row yet.
	ask bool
	// written: a write landed since open (no more asking).
	written bool
}

// check refuses a write while the question is open. The answer is running
// MigrateCommand (the explicit "the team has upgraded"), after which every
// row is v2 and nobody is asked again.
func (f formatState) check() error {
	if !f.ask || f.written {
		return nil
	}
	return errs.New(errs.NeedsConfirm, "wire: this repository's rows were written by teammates with lore 0.1.17; "+
		"the first 0.1.18 write changes the file format they would need 0.1.18 to read").
		WithQuestion("has everyone who shares this repository upgraded to lore 0.1.18?").
		WithHint("once they have, run `" + MigrateCommand + "` (it rewrites the files as _v: 2), then retry").
		WithNext(MigrateCommand)
}

// readFormat computes the format state from the repo store's rows.
func readFormat(ctx context.Context, repo store.Backend, caller ids.ID[ids.Actor]) (formatState, error) {
	rows, _, err := repo.Read(ctx, "")
	if err != nil {
		return formatState{}, err
	}
	var v1 []row.Row
	for _, sr := range rows {
		r, err := sr.Decode()
		if err != nil {
			return formatState{}, err
		}
		if r.SourceVersion == row.FormatVersion {
			return formatState{}, nil // a 0.1.18 write already happened
		}
		v1 = append(v1, r)
	}
	return formatState{ask: engine.HasOtherWriter(v1, caller)}, nil
}
