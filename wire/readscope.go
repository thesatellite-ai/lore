package wire

// readscope.go — lists and searches in a code repo read what the repo
// attaches (devco D-484). A repo attaching a whole store (acme) and one
// folder of a second org (acmeops/infra): `lore sql` read every folder of
// acme (lensFolders, D-443), while `rule list`, `task list` and `search`
// read acme's root folder only, which holds no project rows, so they showed
// acmeops/infra alone, and `rule list --store acme` said "(no rules)". A
// store attached whole is every folder of it, for every read: lists and
// search now take their folders from the same resolver as the sql lens.
//
//	stores.yaml: acme (whole), acmeops/infra
//	  sql / list / search ─▶ acme/app1, acme/app2, …, acmeops/infra
//	  --store acme        ─▶ acme/app1, acme/app2, … (its folders)
//
// Writes, context and render keep the repo's own attachments: a write goes
// where --to says, and the rendered files hold what the repo links, not a
// whole company store.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/op"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
)

// listReadSuffixes are the op name endings of the reads that list or
// search rows (rule.list, task.search, …): the reads whose scope is every
// folder the repo attaches.
var listReadSuffixes = []string{".list", ".search"}

// listRead reports whether spec lists or searches rows: a row read named
// <kind>.list or <kind>.search, or the cross-kind search (search.run).
// Those read the folders of a store attached whole (readScopeFolders).
func listRead(spec op.Spec) bool {
	if !spec.OnlyReads() || !rowRead(spec.Name) {
		return false
	}
	if spec.Name == opssearch.OpRun {
		return true
	}
	for _, s := range listReadSuffixes {
		if strings.HasSuffix(spec.Name, s) {
			return true
		}
	}
	return false
}

// readScopeFolders is what a list or search in a code repo attaches beside
// the repo's stores for its one run: every folder of each store the repo
// attaches whole, the sql lens's resolver (lensFolders), so sql, lists and
// search see one scope. None for any other operation, and none where
// lensFolders adds none (a store clone, a store-only open).
func (a *App) readScopeFolders(ctx context.Context, e op.Entry) []attach.Store {
	if !listRead(e.Spec()) {
		return nil
	}
	return a.lensFolders(ctx)
}
