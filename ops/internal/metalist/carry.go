package metalist

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// Carry is the metadata a --supersedes add copies from the replaced row
// (METADATA 3.19, CLI_REFERENCE 2.4; --no-meta turns it off). Every value
// goes through the engine's normal --meta write, so it is checked against
// the key rules of today (a retired key, a type or allowed-value change
// refuses with the --meta write code, hint --no-meta).
//
// Flow:
//
//	PlanCarry(old, explicit, noMeta)
//	  ├ no --meta given: Before(ctx) → the create itself writes the
//	  │   carried values (defaults and required keys see them)
//	  └ --meta given: the create takes the flags (Run bound them) and
//	      Before binds an engine.MetaCarry: the same create fills in the
//	      carried keys the flags did not set, before the required-key
//	      check (OV30-CARRY-REQ); After only reads how many it filled
type Carry struct {
	terms []meta.Term
	table row.Table
	after bool
	bound engine.MetaCarry
	// Count is how many keys were carried over ("carried over N keys").
	Count int
}

// PlanCarry reads old's metadata for a create of table. explicit are the
// add's own --meta flags; noMeta (--no-meta) carries nothing.
func PlanCarry(old row.Row, table row.Table, explicit engine.MetaFlags, noMeta bool) (Carry, error) {
	c := Carry{table: table, after: !explicit.Empty()}
	if noMeta || len(old.Meta) == 0 {
		return c, nil
	}
	keys := make([]ids.ID[ids.MetaKey], 0, len(old.Meta))
	for id := range old.Meta {
		keys = append(keys, id)
	}
	slices.SortFunc(keys, func(a, b ids.ID[ids.MetaKey]) int { return strings.Compare(a.String(), b.String()) })
	for _, id := range keys {
		en, err := meta.DecodeEntry(old.Meta[id])
		if err != nil {
			return Carry{}, errs.Prefix("--supersedes "+old.ID.String()+": its metadata cannot be read", err).
				WithHint("rerun with --no-meta, or fix it with lore meta lint")
		}
		t, err := meta.ParseWrite(id.String() + "=" + string(en.V))
		if err != nil {
			return Carry{}, err
		}
		c.terms = append(c.terms, t)
	}
	return c, nil
}

// Before is ctx carrying the values for the create (no --meta given).
func (c *Carry) Before(ctx context.Context) context.Context {
	if len(c.terms) == 0 {
		return ctx
	}
	if c.after {
		ctx, c.bound = engine.WithMetaCarry(ctx, engine.MetaCarry{Terms: c.terms, Table: c.table})
		return ctx
	}
	c.Count = len(c.terms)
	return engine.WithMeta(ctx, engine.MetaWrite{JSON: c.terms, Table: c.table})
}

// Updater is the row update After writes with.
type Updater interface {
	Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error)
}

// After sets the carried keys the new row does not hold yet (--meta was
// given, so its values win). An error leaves the new row for the caller
// to remove.
func (c *Carry) After(ctx context.Context, upd Updater, ref engine.Ref, newRow row.Row) error {
	if !c.after || len(c.terms) == 0 {
		return nil
	}
	c.Count = c.bound.Filled()
	var todo []meta.Term
	for _, t := range c.terms {
		if _, has := newRow.Meta[t.KeyID]; !has {
			todo = append(todo, t)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	if _, err := upd.Update(engine.WithMeta(ctx, engine.MetaWrite{JSON: todo, ID: ref.ID}), ref, map[string]value.Value{}); err != nil {
		return err
	}
	c.Count += len(todo)
	return nil
}

// Hint adds the --no-meta hint to a metadata error of a carrying create.
func (c *Carry) Hint(err error) error {
	if err == nil || len(c.terms) == 0 || !strings.HasPrefix(string(errs.CodeOf(err)), "E_META") {
		return err
	}
	var e *errs.Error
	if !errors.As(err, &e) {
		return err
	}
	return e.WithHint("the replaced row's metadata fails a current rule: rerun with --no-meta, or fix it with lore meta set")
}

// Line is the "carried over N keys" note ("" for none).
func (c Carry) Line() string {
	switch c.Count {
	case 0:
		return ""
	case 1:
		return "carried over 1 key\n"
	}
	return "carried over " + strconv.Itoa(c.Count) + " keys\n"
}
