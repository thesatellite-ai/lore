// leak_core_index.go — the core's rows in the F16 leak index.
//
// Why it exists: the leak index (leakIndexFor) was built only from the
// v0.1.17 external cache, which reads v0.1.17 stores. Rows the 0.1.18 core
// writes into a new-format store (lore-store.yaml _v 2) never reach that
// cache, so every check that compares text against "what is internal"
// (pre-commit, commit-msg, pre-push, pr-body, the write-time check of a
// client repo write) saw nothing internal and passed internal text into a
// client repo. This file adds the rows of every store the core attached
// to the same index.
//
// Which stores count (same split as v0.1.17's index):
//   - in a client repo: every attached store of class internal or personal
//     that is not shareable (CFG6: a shareable store's rows are meant for
//     repos);
//   - in any other repo that is checked at all (no org, or an own repo whose
//     link status is unknown): personal stores only.
//
// The repo store and client-class stores are never in the index: they are
// what the client may read.
//
// Fails closed: a store that cannot be listed, or an org store the link
// names that is not attached (left out at open as unreachable), makes the
// check refuse (E_LEAK_BLOCKED, "cannot check"), like an unreadable
// v0.1.17 store.
package main

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/leakcheck"
)

// leakTextFields are the prose fields of core rows the text rule compares
// against (v0.1.17's index used the row's title and body). Only prose: a
// field holding ids (tag_ids, task_id, …) would make an internal row that
// legitimately points at a client row (F15 allows agency → repo) match the
// client row's own id.
var leakTextFields = []string{"title", "name", "body", "description", "reason", "notes", "message"}

// leakStoreLabelPrefix starts the label a finding names for a core row's
// store (v0.1.17 labels named the store project).
const leakStoreLabelPrefix = "store "

// coreLeakRows returns the index rows of the core's internal stores (see
// the file comment for which). client is the repo's role. With no core
// mounted (unit tests that drive only v0.1.17 code) there is nothing to add.
func coreLeakRows(ctx context.Context, client bool) ([]leakcheck.IndexRow, error) {
	if coreApp == nil {
		return nil, nil
	}
	// The org's stores this repo must see (the link). One that is not
	// attached was left out at open as unreachable (no local copy,
	// offline): its rows are unknown, so the check cannot decide.
	expected, err := coreOrgStores(ctx)
	if err != nil {
		return nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()).WithCause(err)
	}
	names := make([]store.Source, 0, len(expected))
	for _, e := range expected {
		names = append(names, e.Name)
	}
	out, err := leakRowsFrom(ctx, coreApp, client, names...)
	if err != nil || !client {
		return out, err
	}
	// METADATA 3.11 item 1 / 3.15: the client repo's own key rows feed the
	// foreign-key rule, and an internal key name the repo itself defines is
	// not a leak there (its own key of that name).
	repoKeys, err := coreApp.List(ctx, store.SourceRepo, schema.TableMetaKeys)
	if err != nil {
		return nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()+" (the repo's key rows)").WithCause(err)
	}
	return withRepoKeys(out, repoKeys), nil
}

// withRepoKeys adds the repo's live key rows as RepoKey index rows and
// drops their names from the internal key names of out.
func withRepoKeys(out []leakcheck.IndexRow, repoKeys []row.Row) []leakcheck.IndexRow {
	own := map[string]bool{}
	for _, k := range repoKeys {
		if k.Deleted {
			continue
		}
		own[renderField(k, metaKeyNameField)] = true
		out = append(out, leakcheck.IndexRow{ID: k.ID.String(), Table: string(schema.TableMetaKeys), Source: string(store.SourceRepo),
			RepoKey: true, RepoKeyScope: renderField(k, metaKeyScopeField)})
	}
	for i := range out {
		if len(out[i].KeyNames) > 0 {
			out[i].KeyNames = slices.DeleteFunc(slices.Clone(out[i].KeyNames), func(n string) bool { return own[n] })
		}
	}
	return out
}

// leakStores is the part of the core the leak index reads: which stores are
// attached, each one's class, and its live rows. *wire.App implements it;
// the seam exists so the class choice and the fail-closed paths are tested
// without a real store on disk.
type leakStores interface {
	StoreIdentities(ctx context.Context) ([]wire.StoreIdentity, error)
	StoreClass(ctx context.Context, name store.Source) (direction.Class, error)
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
}

// leakRowsFrom is coreLeakRows over any leakStores. Every error (the store
// list, a class, a table listing) refuses with E_LEAK_BLOCKED "cannot
// check": an internal store that cannot be read must never count as empty.
// expected are stores that must be attached (the org's, from the link); a
// missing one refuses the same way.
func leakRowsFrom(ctx context.Context, app leakStores, client bool, expected ...store.Source) ([]leakcheck.IndexRow, error) {
	idents, err := app.StoreIdentities(ctx)
	if err != nil {
		return nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()).WithCause(err)
	}
	attached := make(map[store.Source]bool, len(idents))
	for _, si := range idents {
		attached[si.Name] = true
	}
	for _, name := range expected {
		if !attached[name] {
			return nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()+" (store "+string(name)+")")
		}
	}
	var out []leakcheck.IndexRow
	for _, si := range idents {
		if si.Shareable {
			continue
		}
		class, err := app.StoreClass(ctx, si.Name)
		if err != nil {
			return nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()).WithCause(err)
		}
		if !leakIndexesClass(class, client) {
			continue
		}
		label := leakStoreLabelPrefix + string(si.Name)
		keyRows, err := app.List(ctx, si.Name, schema.TableMetaKeys)
		if err != nil {
			return nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()+" (store "+string(si.Name)+")").WithCause(err)
		}
		keys := leakKeysOf(keyRows)
		for _, t := range wire.CoreTables() {
			rows, err := app.List(ctx, si.Name, t)
			if err != nil {
				return nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()+" (store "+string(si.Name)+")").WithCause(err)
			}
			out = append(out, leakIndexRowsWith(rows, t, si.Name, label, keys)...)
		}
	}
	return out, nil
}

// leakIndexesClass reports whether a store of class is internal for a repo
// of the given role: personal always, internal only seen from a client
// repo, never repo/client-class stores.
func leakIndexesClass(class direction.Class, client bool) bool {
	switch class {
	case direction.Personal:
		return true
	case direction.Internal:
		return client
	case direction.Client:
		return false
	}
	return false // the repo store, or a class this binary does not know
}

// Key row fields the leak index reads (schema meta_keys).
const (
	metaKeyNameField      = "name"
	metaKeyScopeField     = "scope"
	metaKeyTypeField      = "type"
	metaKeyInternalField  = "internal"
	metaKeySensitiveField = "sensitive"
)

// leakKey is what the metadata leak rules need of one key row.
type leakKey struct {
	name, typ           string
	internal, sensitive bool
}

// leakKeysOf maps a store's live key rows by id.
func leakKeysOf(rows []row.Row) map[string]leakKey {
	out := make(map[string]leakKey, len(rows))
	for _, k := range rows {
		if k.Deleted {
			continue
		}
		out[k.ID.String()] = leakKey{name: renderField(k, metaKeyNameField), typ: renderField(k, metaKeyTypeField),
			internal: rowBool(k, metaKeyInternalField), sensitive: rowBool(k, metaKeySensitiveField)}
	}
	return out
}

// rowBool is a bool field of r (false when absent).
func rowBool(r row.Row, f string) bool {
	b, ok := r.Fields[f].(value.Bool)
	return ok && b.Bool()
}

// leakMetaTokens are the exact-token / key-pair inputs of an internal row
// (METADATA 3.15 step 6): every string leaf of each entry whose key is
// internal or sensitive (by the key row or the entry's label), or whose key
// row is not known here (R2 item 4: unknown ids count as internal).
func leakMetaTokens(r row.Row, keys map[string]leakKey) []leakcheck.Token {
	var out []leakcheck.Token
	for kid, j := range r.Meta {
		en, err := meta.DecodeEntry(j)
		if err != nil {
			continue
		}
		k, known := keys[kid.String()]
		if known && !k.internal && !k.sensitive && !en.Sensitive {
			continue
		}
		for _, v := range meta.Strings(en.V) {
			if v != "" {
				out = append(out, leakcheck.Token{Value: v, Key: en.Name, Type: k.typ})
			}
		}
	}
	slices.SortFunc(out, func(a, b leakcheck.Token) int { return strings.Compare(a.Key+"\x00"+a.Value, b.Key+"\x00"+b.Value) })
	return out
}

// leakMetaTexts is the metadata of an internal row as index text (META
// 3.15 step 1, FA-META-9): each value's string leaves, and "name: value"
// per leaf so the text rule also sees the pair. Entries that do not decode
// are skipped (lint reports them).
func leakMetaTexts(r row.Row) []string {
	var out []string
	for _, j := range r.Meta {
		en, err := meta.DecodeEntry(j)
		if err != nil {
			continue
		}
		for _, v := range meta.Strings(en.V) {
			if v == "" {
				continue
			}
			out = append(out, v, en.Name+": "+v)
		}
	}
	slices.Sort(out)
	return out
}

// leakIndexRows turns live rows of one table into index rows (tombstones
// carry no text and are skipped; their ids stay internal through any live
// copy only).
func leakIndexRows(rows []row.Row, t row.Table, src store.Source, label string) []leakcheck.IndexRow {
	return leakIndexRowsWith(rows, t, src, label, nil)
}

// leakIndexRowsWith is leakIndexRows with the store's key rows (keys), so
// metadata values feed the exact-token rule (nil: every value counts as
// internal, R2 item 4).
func leakIndexRowsWith(rows []row.Row, t row.Table, src store.Source, label string, keys map[string]leakKey) []leakcheck.IndexRow {
	out := make([]leakcheck.IndexRow, 0, len(rows))
	for _, r := range rows {
		if r.Deleted {
			continue
		}
		var texts []string
		for _, f := range leakTextFields {
			if s := renderField(r, f); s != "" {
				texts = append(texts, s)
			}
		}
		texts = append(texts, leakMetaTexts(r)...)
		ir := leakcheck.IndexRow{ID: r.ID.String(), Table: t.String(), Label: label, Source: string(src), Texts: texts, Tokens: leakMetaTokens(r, keys)}
		if t == schema.TableMetaKeys && rowBool(r, metaKeyInternalField) {
			ir.KeyNames = []string{renderField(r, metaKeyNameField)} // 3.15 step 1
		}
		out = append(out, ir)
	}
	return out
}
