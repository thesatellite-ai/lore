package wire

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
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
	if to == "" || strings.Contains(to, targetSep) {
		return d, nil // `<store>/<folder>` and `<store>/.` name their place explicitly
	}
	// TEAM-032 / side effect 4: `--to <org>` with several links to that
	// org and none the default is refused naming them, never guessed
	// (OrgEntryStore decides; P3-CMDLORE-10).
	if a.opts.OrgEntryStore != nil {
		if _, _, oerr := a.opts.OrgEntryStore(ctx, to, ""); errors.Is(oerr, orgresolve.ErrTwoLinksNoDefault) {
			return nil, oerr
		}
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		top = ""
	}
	if linked, ok := linkedFolderName(ctx, a.opts, top, to); ok {
		return aliasDecoder{inner: d, from: to, to: string(linked)}, nil
	}
	if to != attach.ReservedTeam && to != attach.ReservedCompany {
		if target, ok := a.bareFolderTarget(ctx, to); ok {
			// `--to <folder>`: a folder of one of the org's stores, named
			// alone (foldertarget.go); attachFolderTarget finds its store
			return aliasDecoder{inner: d, from: to, to: target}, nil
		}
		return d, nil
	}
	name, ok := orgAliases(ctx, a.opts, top)[to]
	if !ok {
		return nil, errs.New(errs.NotFound, "--to "+to+": this repository has no org store "+aliasWhat[to]).
			WithHint("link the repo to an org (`lore org link <org> --project <folder>`), or name the store: --to <store>/<folder>")
	}
	return aliasDecoder{inner: d, from: to, to: string(name)}, nil
}

// linkedFolderName maps `--to <org>` to the name the org's linked-folder
// entry is attached under when that is not the bare org name.
//
// Why: CONFIG_GUIDE says a write to a store goes to its root, or to its
// folder when one is attached; for the org store of a linked repo the
// attached folder is the repo's linked folder (v0.1.17 wrote
// `--to northwind` into northwind/acme). When stores.yaml also attaches the
// bare name at the store root (`lore store init northwind` in this repo),
// renameOrgClashes attaches the linked folder as "<org>/<folder>" and the
// bare name would reach the root instead; this rewrite keeps the v0.1.17
// place. `--to <org>/.` stays the explicit root and `--to company` the org
// folder (both untouched here). ok is false when to is not the org's bare
// name or no rename happened.
func linkedFolderName(ctx context.Context, opts Options, top, to string) (store.Source, bool) {
	raw := rawOrgStores(ctx, opts)
	named := renameOrgClashes(raw, top)
	for i, s := range raw {
		if string(s.Name) == to && named[i].Name != s.Name {
			return named[i].Name, true
		}
	}
	return "", false
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
