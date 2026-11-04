package wire

import (
	"context"
	"reflect"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// The org shortcut targets of STORE TARGETS (CONFIG_GUIDE section 4):
//
//	--to team     this repo's org store, the folder the repo is linked to
//	--to company  this repo's org store, its org/ folder (O1, CFG1)
//
// Options.OrgStores attaches exactly those two folders (the org's name at
// the linked folder, "<org>/<org folder>" at the company folder), so each
// shortcut is rewritten to that attached name before the engine resolves
// the target: one name per data folder, no second backend for an alias.

// orgAliases maps team and company to the names Options.OrgStores attached
// for this repo. A shortcut with no org folder behind it is absent; the
// caller refuses it.
func orgAliases(ctx context.Context, opts Options, top string) map[string]store.Source {
	out := map[string]store.Source{}
	// Classify by the name Options.OrgStores gave (company = "<org>/<org
	// folder>", team = the bare org name at the linked folder), then use
	// the name the entry is attached under (renameOrgClashes may have
	// made the team entry "<org>/<folder>").
	raw := rawOrgStores(ctx, opts)
	named := renameOrgClashes(raw, top)
	for i, s := range raw {
		alias := attach.ReservedTeam
		if strings.Contains(string(s.Name), targetSep) {
			alias = attach.ReservedCompany
		}
		if _, done := out[alias]; !done {
			out[alias] = named[i].Name
		}
	}
	return out
}

// resolveAlias rewrites a decoder whose input names `--to team` or
// `--to company` so the engine and the operation see the attached org
// store name instead. With no org (or no org folder) the shortcut is
// refused with the explicit form to use.
func (a *App) resolveAlias(ctx context.Context, e op.Entry, d op.Decoder) (op.Decoder, error) {
	in, err := e.Decode(d)
	if err != nil {
		return d, nil // the engine's Run reports the decode error itself
	}
	to := ""
	for _, tv := range in.Texts() {
		if tv.Field == engine.InputTarget {
			to = tv.Text
		}
	}
	if to != attach.ReservedTeam && to != attach.ReservedCompany {
		return d, nil
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		top = ""
	}
	name, ok := orgAliases(ctx, a.opts, top)[to]
	if !ok {
		return nil, errs.New(errs.NotFound, "--to "+to+": this repository has no org store "+aliasWhat[to]).
			WithHint("link the repo to an org (`lore org link <org> --project <folder>`), or name the store: --to <store>/<folder>")
	}
	return aliasDecoder{inner: d, from: to, to: string(name)}, nil
}

// aliasWhat says which org folder a shortcut needs, for its refusal.
var aliasWhat = map[string]string{
	attach.ReservedTeam:    "folder linked to it",
	attach.ReservedCompany: "org folder",
}

// aliasDecoder decodes through inner, then replaces the input's `to` field
// (json name engine.InputTarget) when it equals from.
type aliasDecoder struct {
	inner    op.Decoder
	from, to string
}

// Decode implements op.Decoder.
func (d aliasDecoder) Decode(dst reflect.Value, fields []op.FieldInfo) error {
	if err := d.inner.Decode(dst, fields); err != nil {
		return err
	}
	setTarget(dst, d.from, d.to)
	return nil
}

// setTarget walks a decoded input struct (embedded structs included) and
// sets every string field tagged json:"to" holding from to to.
func setTarget(v reflect.Value, from, to string) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := range t.NumField() {
		f, fv := t.Field(i), v.Field(i)
		if f.Anonymous {
			setTarget(fv, from, to)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == engine.InputTarget && fv.Kind() == reflect.String && fv.CanSet() && fv.String() == from {
			fv.SetString(to)
		}
	}
}
