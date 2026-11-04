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
	"memories": true,
}

// CoreOwned reports whether the new core owns table's row files.
func CoreOwned(table string) bool { return coreOwned[table] }

// isCoreOwned is CoreOwned for this pass (Options.coreOwned when set).
func (e *engine) isCoreOwned(table string) bool {
	if e.o.coreOwned != nil {
		return e.o.coreOwned[table]
	}
	return CoreOwned(table)
}

// CoreOwnedTables lists the core-owned tables, sorted (for messages).
func CoreOwnedTables() []string {
	out := make([]string, 0, len(coreOwned))
	for t := range coreOwned {
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
	out, conflict, err := merge.Driver{Registry: reg}.Merge(base, ours, theirs)
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
	return MergeOutcome{Conflicted: conflict}, nil
}

// syncCoreOwned is syncOne for a core-owned row: the file side always wins
// and lore.db is only its mirror. A file change is imported (a deleted file
// trashes the row); a lore.db-only change is not exported (warned); a
// zero-length file is reported, never repaired from lore.db (that repair
// would write a v1 document over the core's file).
func (e *engine) syncCoreOwned(ctx context.Context, k rowKey, t *Table, fc fileChange, fileChanged bool) error {
	rel := k.rel()
	if !fileChanged {
		cols := liveColumns(t, e.present)
		dbDoc, err := readRowDoc(ctx, e.tx, t, cols, k.id)
		if err != nil {
			return err
		}
		return e.exportDoc(ctx, k, t, dbDoc) // guarded: warns, writes nothing
	}
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
	return e.observe(ctx, k, t, ChangeImport, baseDoc)
}
