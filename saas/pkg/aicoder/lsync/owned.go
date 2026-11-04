package lsync

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/schema"
)

// coreOwned is THE single switch naming the tables whose row files belong to
// the 0.1.18 core (github.com/thesatellite-ai/lore, row format `_v: 2`).
// For these tables this package is import-only: row files flow into lore.db
// (so render, search and the TUI keep seeing them), but no path here ever
// writes, repairs, re-exports, removes or diverts their files, and the git
// merge driver hands them to logic/merge.Driver. Every old writer (sync
// pass, adopt after `lore restore`, bootstrap, `sync export --all`,
// empty-file repair, natural-key merge, purge, live divert) checks
// CoreOwned and nothing else, so extending this set is the whole migration
// of a noun's file side. The coordinator adds one line per migrated noun.
var coreOwned = map[string]bool{
	"memories":           true,
	"patterns":           true,
	"playbooks":          true,
	"prompts":            true,
	"rules":              true,
	"decisions":          true,
	"hotfixes":           true,
	"tags":               true,
	"comments":           true,
	"commit_links":       true,
	"kinds":              true,
	"kind_fields":        true,
	"resources":          true,
	"vars":               true,
	"policies":           true,
	"meta_keys":          true,
	"meta_values":        true,
	"machines":           true, // MACHINE PROFILE (OV3): written only by the core
	"task_templates":     true, // TASK 11 (OV15c): written only by the core
	"task_views":         true, // TASK 16.2 (OV22): saved views, written only by the core
	"scopes":             true, // directory scopes (OV25, CONTEXT_BUDGET 6.2): written only by the core
	"secret_rules":       true,
	"secret_allows":      true,
	"tasks":              true,
	"task_lists":         true,
	"missions":           true,
	"plans":              true,
	"reminders":          true,
	"knowledge_refs":     true,
	"rule_verifier_refs": true,
	"architecture_notes": true,
	"behaviours":         true,
	"cookbook_recipes":   true,
	"incidents":          true,
	"suggestions":        true,
	"taste_prefs":        true,
	"workflows":          true,
	"workspaces":         true,
	"handoffs":           true,
	"snapshots":          true,
	"tech_docs":          true,
	"external_sources":   true,
}

// repoOwned are core-owned in the checkout's own .lore/data only. In a
// store mirror (Options.StoreMirror) the old code stays their one writer:
// the roster writes actors and teams there, ensureGroupRepo writes repos,
// and store-folder project rows are written by the store code, while the
// core never writes these four tables into a store folder (phase 5 g9).
var repoOwned = map[string]bool{
	"actors":   true,
	"projects": true,
	"repos":    true,
	"teams":    true,
}

// CoreOwned reports whether the new core owns table's row files in a
// checkout (the TUI guard, the merge driver and the file checks use it).
func CoreOwned(table string) bool { return coreOwned[table] || repoOwned[table] }

// isCoreOwned is CoreOwned for this pass (Options.coreOwned when set; a
// store-mirror pass leaves the repoOwned tables to the old code).
func (e *engine) isCoreOwned(table string) bool {
	if e.o.coreOwned != nil {
		return e.o.coreOwned[table]
	}
	if e.o.StoreMirror && repoOwned[table] {
		return false
	}
	return CoreOwned(table)
}

// CoreOwnedTables lists the core-owned tables, sorted (for messages).
func CoreOwnedTables() []string {
	out := make([]string, 0, len(coreOwned)+len(repoOwned))
	for t := range coreOwned {
		out = append(out, t)
	}
	for t := range repoOwned {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// skipCoreOwnedExport records that a lore.db change to a core-owned row was
// not written to its file, once per path per pass.
func (e *engine) skipCoreOwnedExport(rel string) {
	if e.coreSkipped == nil {
		e.coreSkipped = map[string]bool{}
	}
	if e.coreSkipped[rel] {
		return
	}
	e.coreSkipped[rel] = true
	e.rep.Warnings = append(e.rep.Warnings, rel+": lore.db change not written (this table's files are owned by the 0.1.18 core; change it with the core's commands, e.g. `lore memory`)")
}

// coreOwnedRepoPath reports the core-owned table a repo-relative row path
// (".lore/data/<table>/<id>.json") belongs to, "" when none.
func coreOwnedRepoPath(repoPath string) string {
	dir := path.Base(path.Dir(filepath.ToSlash(repoPath)))
	if CoreOwned(dir) {
		return dir
	}
	return ""
}

// mergeCoreOwned runs the git merge driver for a core-owned row file
// through the core's own merge (logic/merge.Driver), so the result stays in
// the core's format; the v1 field merge here would upcast and write `_v: 1`.
func mergeCoreOwned(ctx context.Context, basePath, oursPath, theirsPath string) (MergeOutcome, error) {
	read := func(p string) ([]byte, error) {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("lsync: read merge side: %w", err)
		}
		return b, nil
	}
	base, err := read(basePath)
	if err != nil {
		return MergeOutcome{}, err
	}
	ours, err := read(oursPath)
	if err != nil {
		return MergeOutcome{}, err
	}
	theirs, err := read(theirsPath)
	if err != nil {
		return MergeOutcome{}, err
	}
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return MergeOutcome{}, fmt.Errorf("lsync: core registry: %w", err)
	}
	// Report carries the field decisions out of the core merge, so the
	// merge driver can still tell a teammate which of their edits lost
	// (sync_cmd prints NewerWins), as v0.1.17's field merge did.
	var res merge.Result
	out, conflict, err := merge.Driver{Registry: reg, Report: func(r merge.Result) { res = r }}.Merge(base, ours, theirs)
	if err != nil {
		// Not a core row on some side (a v1 copy from an old branch, a
		// hand edit): git's line merge, which writes no format of its own.
		return textMerge(ctx, basePath, oursPath, theirsPath)
	}
	if !bytes.Equal(out, ours) {
		if err := os.WriteFile(oursPath, out, dataFileMode); err != nil {
			return MergeOutcome{}, fmt.Errorf("lsync: write merge result: %w", err)
		}
	}
	auto := append([]string(nil), res.NewerWins...)
	if !res.EditAfterDelete.IsZero() {
		auto = append(auto, keyDeletedVsEditedTombstoneKept)
	}
	return MergeOutcome{Conflicted: conflict, AutoResolved: auto}, nil
}

// keyDeletedVsEditedTombstoneKept is reported in AutoResolved when a
// core-owned row was deleted on one side and edited later on the other:
// the core keeps the tombstone (rows are never resurrected, STORE_SERVER_SPEC
// 11 rule 3), unlike v0.1.17's keyDeletedVsEdited where the edit survived.
const keyDeletedVsEditedTombstoneKept = "(deleted on one side, edited on the other: the delete was kept)"

// syncCoreOwned is syncOne for a core-owned row: the file side always wins
// and lore.db is only its mirror. A file change is imported (a deleted file
// trashes the row); a lore.db-only change is not exported (warned); a
// zero-length file is reported, never repaired from lore.db (that repair
// would write a v1 document over the core's file).
func (e *engine) syncCoreOwned(ctx context.Context, k rowKey, t *Table, fc fileChange, fileChanged bool) error {
	rel := k.rel()
	var baseDoc map[string]any
	if _, hasBase := e.bases[rel]; hasBase {
		raw, err := e.baseBytes(ctx, rel)
		if err != nil {
			return err
		}
		if d, err := decodeDoc(raw); err == nil {
			baseDoc = d
		}
	}
	if !fileChanged {
		cols := liveColumns(t, e.present)
		dbDoc, err := readRowDoc(ctx, e.tx, t, cols, k.id)
		if err != nil {
			return err
		}
		// A lore.db-only change to a core-owned row is never exported, but
		// it is still audited (as DBChangeKind: an external write in the
		// catch-up pass) BEFORE exportDoc clears its dirty mark. Without
		// this, a sqlite3 edit lost its dirty mark unrecorded and the next
		// `lore audit verify` reported the tampered DB as intact (v0.1.17
		// recorded every such change as <table>.external-write).
		if err := e.observe(ctx, k, t, e.dbChangeKind(), baseDoc); err != nil {
			return err
		}
		return e.exportDoc(ctx, k, t, dbDoc) // guarded: warns, writes nothing
	}
	var fileDoc map[string]any
	if !fc.deleted {
		if len(fc.bytes) == 0 {
			e.rep.Warnings = append(e.rep.Warnings, rel+": empty file; not restored from lore.db (owned by the 0.1.18 core: restore it from git)")
			e.deferred[k] = true
			return nil
		}
		d, err := e.parseRowFile(ctx, rel, k, fc.bytes)
		if err != nil {
			e.deferred[k] = true
			return nil // reported by parseRowFile
		}
		fileDoc = d
	}
	if err := e.applyFile(ctx, k, t, fileDoc, fc); err != nil {
		return err
	}
	if err := e.bridgeTags(ctx, k.table, k.id, fileDoc); err != nil {
		return err
	}
	return e.observe(ctx, k, t, ChangeImport, baseDoc)
}
