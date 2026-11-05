// stores_reads.go — how list, show and search commands include rows that
// live outside the checkout (store projects, global, personal; F1 order,
// F15, F20). A command prints its repo rows as today, then calls one of
// these with the same table / text; each external row carries its label
// ([northwind/acme], [me]) so nobody mistakes it for a client row.
//
//	printExternalRows(ctx, &b, "memories", "")         // after the repo rows
//	extra := externalJSONRows(ctx, "tasks", "")               // append to the --json data array
//
// Reads never fail because of a store: problems are printed as one
// muted note (offline, missing branch) and the repo rows still show.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"strings"

	"saas/pkg/aicoder/cachedb"
	"saas/pkg/aicoder/style"

	"github.com/thesatellite-ai/lore/store"
)

// Keys added to an external row in --json output.
const (
	jsonKeySource = "_source"
	jsonKeyLabel  = "_label"
	jsonKeyLive   = "_live_row"
)

// printExternalRows prints external rows of table (or matching text; ""
// = no filter) as "id  text  [label]" lines. Returns how many it printed.
func printExternalRows(ctx context.Context, w *strings.Builder, table, text string) int {
	return printExternalRowsExcept(ctx, w, table, text, nil)
}

// printExternalRowsExcept is printExternalRows leaving out the ids in skip
// (rows the caller already listed from the core, r2-source).
func printExternalRowsExcept(ctx context.Context, w *strings.Builder, table, text string, skip map[string]bool) int {
	rows, notes := externalRowsHere(ctx, cachedb.Query{Table: table, Text: text})
	rows = slices.DeleteFunc(rows, func(r cachedb.Row) bool { return skip[r.ID] })
	for _, n := range notes {
		fmt.Fprintln(os.Stderr, style.Muted("lore: "+n))
	}
	for _, r := range rows {
		if r.Table == tableProjects {
			continue
		}
		t := r.Title
		if t == "" {
			t = r.Body
		} else if r.Body != "" && r.Body != r.Title {
			t += " — " + r.Body
		}
		status := ""
		if r.Status != "" {
			status = "  " + r.Status
		}
		fmt.Fprintf(w, "%s  %s%s  %s\n", r.ID, oneLine(t), status, style.Muted(r.Label))
	}
	return len(rows)
}

// externalJSONRows returns external rows as JSON objects (the row's own
// fields plus _source, _label, _live_row) for --json list output.
func externalJSONRows(ctx context.Context, table, text string) []map[string]any {
	return externalJSONRowsExcept(ctx, table, text, nil)
}

// externalJSONRowsExcept is externalJSONRows leaving out the ids in skip.
func externalJSONRowsExcept(ctx context.Context, table, text string, skip map[string]bool) []map[string]any {
	rows, _ := externalRowsHere(ctx, cachedb.Query{Table: table, Text: text})
	rows = slices.DeleteFunc(rows, func(r cachedb.Row) bool { return skip[r.ID] })
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		var doc map[string]any
		if err := json.Unmarshal(r.Doc, &doc); err != nil {
			continue
		}
		delete(doc, "_live")
		doc[jsonKeySource], doc[jsonKeyLabel], doc[jsonKeyLive] = externalSource(r.Source), r.Label, r.Live
		out = append(out, doc)
	}
	return out
}

// externalSource is the `_source` object of a v0.1.17 cache row whose
// Source is "store:<org>/<folder>" or "personal:<folder>".
//
// FLAG1 (owner C): `_source` is {store, store_id, folder, folder_id}, the
// same object the core prints, so scripts read one shape. The cache row
// carries only names; the ids come from the core when it attaches the same
// store (withCoreIDs: store_id from the store's marker, folder_id when the
// core attached that very folder). A v0.1.17 store the core does not
// attach keeps the names alone. The personal store is "me" (its attached
// name, personalStoreEntry). Any other Source is kept as the store name
// alone.
func externalSource(src string) store.SourceRef {
	kind, rest, _ := strings.Cut(src, ":")
	switch kind {
	case cachedb.SourceStore:
		org, folder, _ := strings.Cut(rest, "/")
		return withCoreIDs(store.SourceRef{Store: org, Folder: folder})
	case cachedb.SourcePersonal:
		return withCoreIDs(store.SourceRef{Store: personalStoreEntry, Folder: rest})
	}
	return store.SourceRef{Store: src}
}

// withCoreIDs fills ref's store_id and folder_id from the core's
// description of the same store (FLAG1, r2-source): the attached name
// "<store>/<folder>" first, then the bare store name. The store id is
// taken from either; the folder id only when the described folder is
// ref's folder (another folder's prj_ id would be wrong).
func withCoreIDs(ref store.SourceRef) store.SourceRef {
	if coreApp == nil {
		return ref
	}
	names := []store.Source{store.Source(ref.Store)}
	if ref.Folder != "" {
		names = append([]store.Source{store.Source(ref.Store + "/" + ref.Folder)}, names...)
	}
	for _, n := range names {
		d := coreApp.DescribeSource(n)
		if d.Store != ref.Store {
			continue
		}
		if ref.StoreID.IsZero() && !d.StoreID.IsZero() {
			ref.StoreID = d.StoreID
		}
		if ref.FolderID.IsZero() && d.Folder == ref.Folder && !d.FolderID.IsZero() {
			ref.FolderID = d.FolderID
		}
	}
	return ref
}
