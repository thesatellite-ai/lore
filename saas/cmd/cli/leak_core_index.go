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
// Fails closed: a store that cannot be listed makes the check refuse
// (E_LEAK_BLOCKED, "cannot check"), like an unreadable v0.1.17 store.
package main

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/direction"
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
	return leakRowsFrom(ctx, coreApp, client)
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
func leakRowsFrom(ctx context.Context, app leakStores, client bool) ([]leakcheck.IndexRow, error) {
	idents, err := app.StoreIdentities(ctx)
	if err != nil {
		return nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()).WithCause(err)
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
		out = append(out, leakcheck.IndexRow{ID: r.ID.String(), Table: t.String(), Label: label, Source: string(src), Texts: texts})
	}
	return out
}
