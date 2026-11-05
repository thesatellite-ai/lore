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

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/row"
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
	return leakRowsFrom(ctx, coreApp, client, names...)
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
		for _, t := range wire.CoreTables() {
			rows, err := app.List(ctx, si.Name, t)
			if err != nil {
				return nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()+" (store "+string(si.Name)+")").WithCause(err)
			}
			out = append(out, leakIndexRows(rows, t, si.Name, label)...)
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
		out = append(out, leakcheck.IndexRow{ID: r.ID.String(), Table: t.String(), Label: label, Source: string(src), Texts: texts})
	}
	return out
}
