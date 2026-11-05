package settings

// `lore resource field edit <kind> <field> --rename-value <old>=<new>`
// (CLI_REFERENCE 3.2, RES 3.14). Flow:
//
//	fieldEdit ─► renamePairs (parse, old must be allowed, new must be free)
//	          ─► the definition's values list, renamed in place (patchRow)
//	          ─► rewriteValueRows: every live resource whose field holds old
//	             gets new (parsed by the field's type), one Env.Update each
//
// The build stores allowed values as plain text (no val_ ids yet), so a
// rename rewrites the rows that hold the value, as CLI_REFERENCE 3.2 says.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// valueRename is one --rename-value pair.
type valueRename struct{ old, new string }

// renamePairs parses the --rename-value pairs against the allowed values
// vals (after --add-value / --remove-value) and returns vals renamed.
func renamePairs(field string, raw []string, vals []string) ([]valueRename, []string, error) {
	out := slices.Clone(vals)
	pairs := make([]valueRename, 0, len(raw))
	for _, p := range raw {
		oldV, newV, ok := strings.Cut(p, "=")
		if !ok || oldV == "" || newV == "" {
			return nil, nil, errs.New(errs.InvalidInput, "--rename-value takes <old>=<new>, not "+quote(p))
		}
		i := slices.Index(out, oldV)
		if i < 0 {
			return nil, nil, errs.New(errs.InvalidInput, field+" has no allowed value "+quote(oldV)).
				WithHint("allowed: " + strings.Join(out, ", "))
		}
		if slices.Contains(out, newV) {
			return nil, nil, errs.New(errs.InvalidInput, field+" already allows "+quote(newV)).
				WithHint("remove one of them first (--remove-value), or pick another name")
		}
		out[i] = newV
		pairs = append(pairs, valueRename{old: oldV, new: newV})
	}
	return pairs, out, nil
}

// rewriteValueRows writes the renamed value into every live resource of
// kind k whose field f holds an old value; it returns how many rows changed.
func (o ops) rewriteValueRows(ctx context.Context, k, f located, pairs []valueRename) (int, error) {
	if len(pairs) == 0 {
		return 0, nil
	}
	kind, err := value.ParseKind(text(f.Row, fType))
	if err != nil {
		return 0, errs.Wrap(errs.ConfigInvalid, "kind field "+f.Row.ID.String()+" has an unknown type", err)
	}
	using, err := o.resourcesUsing(ctx, k, f)
	if err != nil {
		return 0, err
	}
	d, err := o.kindDef(ctx, k.Row.ID.String())
	if err != nil {
		return 0, err
	}
	fid, n := f.Row.ID.String(), 0
	for _, r := range using {
		stored, verr := decodeValues(r)
		if verr != nil {
			return n, verr
		}
		cur := display(fromJSONRaw(stored.vals[fid]))
		at := slices.IndexFunc(pairs, func(p valueRename) bool { return p.old == cur })
		if at < 0 {
			continue
		}
		v, perr := parseAs(kind, pairs[at].new)
		if perr != nil {
			return n, errs.Wrap(errs.ResourceType, fmt.Sprintf("%s value %s is not a %s", text(f.Row, kName), quote(pairs[at].new), kind), perr)
		}
		b, merr := v.MarshalJSON()
		if merr != nil {
			return n, errs.Wrap(errs.Internal, "resource: encode "+fid, merr)
		}
		stored.vals[fid] = b
		patch, perr := valuesPatch(d, stored.vals, stored)
		if perr != nil {
			return n, perr
		}
		if _, err = o.env.Update(ctx, engine.Ref{Store: r.Src, Table: schema.TableResources, ID: r.Row.ID}, patch); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
