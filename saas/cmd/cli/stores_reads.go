// stores_reads.go — how list, show and search commands include rows that
// live outside the checkout (store projects, global, personal; F1 order,
// F15, F20). A command prints its repo rows as today, then calls one of
// these with the same table / text; each external row carries its label
// ([northwind/acme], [me]) so nobody mistakes it for a client row.
//
//	printExternalRows(ctx, os.Stdout, "memories", "")         // after the repo rows
//	extra := externalJSONRows(ctx, "tasks", "")               // append to the --json data array
//
// Reads never fail because of a store: problems are printed as one
// muted note (offline, missing branch) and the repo rows still show.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"saas/pkg/aicoder/cachedb"
	"saas/pkg/aicoder/style"
)

// Keys added to an external row in --json output.
const (
	jsonKeySource = "_source"
	jsonKeyLabel  = "_label"
	jsonKeyLive   = "_live_row"
)

// printExternalRows prints external rows of table (or matching text; ""
// = no filter) as "id  text  [label]" lines. Returns how many it printed.
func printExternalRows(ctx context.Context, w io.Writer, table, text string) int {
	rows, notes := externalRowsHere(ctx, cachedb.Query{Table: table, Text: text})
	for _, n := range notes {
		fmt.Fprintln(os.Stderr, style.Muted("lore: "+n))
	}
	for _, r := range rows {
		if r.Table == "projects" {
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
	rows, _ := externalRowsHere(ctx, cachedb.Query{Table: table, Text: text})
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		var doc map[string]any
		if err := json.Unmarshal(r.Doc, &doc); err != nil {
			continue
		}
		delete(doc, "_live")
		doc[jsonKeySource], doc[jsonKeyLabel], doc[jsonKeyLive] = r.Source, r.Label, r.Live
		out = append(out, doc)
	}
	return out
}
