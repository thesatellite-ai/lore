// Package addkey is the one --key flag (idempotency key) of the adds that
// had none: rule, decision, hotfix, pattern and playbook (devco D-439: a
// script or agent filling a store could not retry an add safely, as it can
// `memory add --key` and `task add --key`).
//
// The key is never stored as a column: it is hashed with the noun and the
// project into the new row's id (engine.NaturalID), so a retry with the
// same key names the same row. The first run creates it; a retry finds it
// and writes nothing; two runs racing on one key collide on the id (the
// later create is E_STALE) and the later one answers with the row the
// earlier one wrote.
//
//	Retry.Key ─▶ ID(prefix, noun, project, key) ─▶ Existing? ─▶ that row
//	                                             └▶ Create(ctx, id) ─▶ stale? ─▶ Existing
package addkey

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// Retry is embedded (with no json tag) in an add's input: op.Define
// flattens it into --key and "key".
type Retry struct {
	Key string `json:"key,omitempty" flag:"key" desc:"idempotency key: a retry with the same key returns the row already added and writes nothing"`
}

// MaxLen bounds a key (real keys are a uuid or a short tag), as memory's.
const MaxLen = 200

// AlreadyLine is what a keyed retry says instead of writing.
const AlreadyLine = "already added with this --key; nothing written"

// idPart separates a keyed add's natural-id input from every other
// natural id; the noun keeps two nouns' equal keys apart.
const idPart = "add.key"

// ID is the natural id of a keyed add of noun in project (the zero id
// when raw is empty). The same key in another project is another row.
func ID(prefix ids.Prefix, noun, project, raw string) (ids.AnyID, error) {
	if raw == "" {
		return ids.AnyID{}, nil
	}
	key := strings.TrimSpace(raw)
	if key == "" || len(key) > MaxLen || strings.ContainsAny(key, "\n\r") {
		return ids.AnyID{}, errs.New(errs.InvalidInput, noun+" add: --key must be one line of 1 to "+strconv.Itoa(MaxLen)+" characters").
			WithHint("pass a short stable value, e.g. --key import-2026-11-15-row-7")
	}
	return engine.NaturalID(prefix, idPart, noun, project, key)
}

// Getter is the slice of an ops Env Existing reads.
type Getter interface {
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
}

// Creator is the optional Env upgrade a keyed add needs
// (engine.Engine.CreateWithID); an Env without it answers --key with
// E_UNSUPPORTED instead of writing a row the key cannot find again.
type Creator interface {
	CreateWithID(ctx context.Context, target store.Source, table row.Table, id ids.AnyID, fields map[string]value.Value) (engine.Written, error)
}

// Existing returns the row a previous keyed add stored (found), with
// nothing written. A row deleted since is refused: its key is spent, and
// re-adding it silently would undo the delete.
func Existing(ctx context.Context, env Getter, noun string, table row.Table, id ids.AnyID) (store.Source, row.Row, bool, error) {
	src, r, err := env.Get(ctx, engine.Ref{Table: table, ID: id})
	if errs.Has(err, errs.NotFound) {
		return "", row.Row{}, false, nil
	}
	if err != nil {
		return "", row.Row{}, false, err
	}
	if r.Deleted {
		return "", row.Row{}, false, errs.New(errs.InvalidInput, noun+" add: the "+noun+" stored with this --key ("+id.String()+") was deleted").
			WithHint("use another --key to add it again")
	}
	return src, r, true, nil
}

// Create creates the row under its key's id. A concurrent run with the
// same key that created it first makes this create E_STALE: the row it
// wrote is returned (found true) instead.
func Create(ctx context.Context, env Getter, noun string, target store.Source, table row.Table, id ids.AnyID, fields map[string]value.Value) (store.Source, engine.Written, bool, error) {
	c, ok := env.(Creator)
	if !ok {
		return "", engine.Written{}, false, errs.New(errs.Unsupported, noun+" add: --key is not supported here")
	}
	w, err := c.CreateWithID(ctx, target, table, id, fields)
	if errs.Has(err, errs.Stale) {
		src, r, found, ferr := Existing(ctx, env, noun, table, id)
		if found || ferr != nil {
			return src, engine.Written{Row: r}, found, ferr
		}
	}
	return target, w, false, err
}
