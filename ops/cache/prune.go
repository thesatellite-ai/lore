// Package cache defines `lore cache prune` (layer L5, CLI_REFERENCE 3.25,
// FLAG3): remove the cache files under ~/.lore/cache that no repo on this
// machine attaches any more, so the folder does not grow forever and old
// client data does not linger. Caches are derived: a repo that attaches
// the store again rebuilds its cache from the row files on its next
// command.
//
//	cache.prune ─▶ Env.LoreHome ─▶ Plan (use records, store caches, v0.1.17 caches)
//	            ─▶ nothing listed: "nothing to prune…" (+ drop dead use records)
//	            ─▶ --dry-run: list + "--dry-run: nothing removed"
//	            ─▶ not confirmed: E_NEEDS_CONFIRM (the surface asks [y/N] and runs again)
//	            ─▶ delete each file, drop dead use records, "✓ removed …"
//
// It is a Local operation (op.Spec.Local, P3-CFG-1): it works outside a
// repo and never opens a store; it touches only files inside the cache
// root (the sqlite adapter's helpers refuse any other path). Ported from
// the old binary's cache_cmd.go (PASS3 ONE-PATH, P3-MISC-2).
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore cache prune`).
const (
	Noun    = "cache"
	OpPrune = "cache.prune"
)

// restNoun keeps the REST collection "cache" (the default plural would be
// "caches").
const restNoun = "cache"

// Layout under the lore home and inside a repo.
const (
	// CacheDir is the cache root under the lore home (~/.lore/cache).
	CacheDir = "cache"
	// v017CacheExt ends a v0.1.17 per-project cache (<cache>/<prj_…>.db).
	v017CacheExt = ".db"
	// v017MetaRel is a v0.1.17 checkout's project marker, relative to its
	// top; its project_id keeps that project's v0.1.17 cache.
	v017MetaRel = ".lore/data/_meta.json"
	// v017MetaProjectKey is the project id key of v017MetaRel.
	v017MetaProjectKey = "project_id"
	// repoMarkerRel is the folder whose presence makes a recorded repo live.
	repoMarkerRel = ".lore"
)

// v017Sidecars are a v0.1.17 cache file and its WAL files ("" = the file).
var v017Sidecars = []string{"", "-wal", "-shm"}

// Lines of the v0.1.17 command (CLI_REFERENCE 3.25 sample).
const (
	lineNothing   = "nothing to prune: every cache file belongs to a store a repo on this machine attaches"
	lineHeader    = "cache files no repo on this machine attaches:"
	lineDryRun    = "--dry-run: nothing removed"
	question      = "Delete these cache files?"
	confirmHint   = "run `lore cache prune --yes` to delete them, or --dry-run to only list them"
	deadRecordFmt = "removed %d cache use record(s) of repos no longer on this machine"
)

// byteUnits are the size suffixes HumanBytes prints, smallest first.
var byteUnits = []string{"B", "KB", "MB", "GB", "TB"}

// byteStep is the factor between two byteUnits.
const byteStep = 1024

// Env is what cache.prune needs; *wire.App implements it.
type Env interface {
	// LoreHome is the lore home (~/.lore) whose cache folder is pruned.
	LoreHome() string
}

// PruneIn is the input of cache.prune.
type PruneIn struct {
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"list the cache files prune would delete, with their sizes; delete nothing"`
}

// File is one cache file prune lists.
type File struct {
	Path string `json:"path" desc:"absolute path of the cache file"`
	Size int64  `json:"size" desc:"bytes, WAL files included"`
}

// PruneOut is the output of cache.prune.
type PruneOut struct {
	Files   []File `json:"files"`
	Count   int    `json:"count"`
	Bytes   int64  `json:"bytes"`
	DryRun  bool   `json:"dry_run,omitempty"`
	Removed bool   `json:"removed" desc:"true when the listed files were deleted"`
	// Records is how many use records of repos gone from this machine were
	// dropped (FLAG3 round 2: without asking, never silently).
	Records int `json:"records_removed,omitempty"`
}

// Text prints the v0.1.17 lines (CLI_REFERENCE 3.25).
func (o PruneOut) Text() string {
	var b strings.Builder
	if o.Count == 0 {
		b.WriteString(lineNothing + "\n")
	} else {
		b.WriteString(listing(o.Files, o.Bytes))
		switch {
		case o.DryRun:
			b.WriteString(lineDryRun + "\n")
		case o.Removed:
			fmt.Fprintf(&b, "✓ removed %d file(s), %s\n", o.Count, HumanBytes(o.Bytes))
		}
	}
	if o.Records > 0 {
		fmt.Fprintf(&b, deadRecordFmt+"\n", o.Records)
	}
	return b.String()
}

// listing is the header, one line per file and the total line.
func listing(files []File, total int64) string {
	var b strings.Builder
	b.WriteString(lineHeader + "\n")
	for _, f := range files {
		fmt.Fprintf(&b, "  %s  %s\n", f.Path, HumanBytes(f.Size))
	}
	fmt.Fprintf(&b, "%d file(s), %s\n", len(files), HumanBytes(total))
	return b.String()
}

// Ops returns the cache operations bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpPrune, Summary: "Remove cache files of stores no repo on this machine attaches any more (lists them and asks first)",
			Permission: op.PermWrite, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors: []errs.Code{errs.NeedsConfirm, errs.InvalidInput, errs.Internal},
			// MayAsk: prune lists first and asks first (FLAG3); the surface
			// offers --yes and runs it again confirmed (op.Confirmed).
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "list what prune would delete", CLI: "lore cache prune --dry-run", Want: ""}},
		}, func(ctx context.Context, in PruneIn) (PruneOut, error) {
			return Prune(ctx, filepath.Join(env.LoreHome(), CacheDir), in.DryRun, op.Confirmed(ctx))
		}),
	}
}

// Plan is what one prune would delete under a cache root.
type Plan struct {
	// Stores are store caches (relative to the root) no live record lists.
	Stores []sqlite.File
	// V017 are leftover v0.1.17 per-project caches no live repo is.
	V017 []sqlite.File
	// Dead are use records of repos that are gone (or unreadable).
	Dead []sqlite.RecordFile
}

// Prune plans and, unless dryRun, deletes; without yes it refuses with
// E_NEEDS_CONFIRM before deleting anything.
func Prune(_ context.Context, root string, dryRun, yes bool) (PruneOut, error) {
	plan, err := MakePlan(root)
	if err != nil {
		return PruneOut{}, err
	}
	out := PruneOut{DryRun: dryRun, Files: []File{}}
	for _, f := range slices.Concat(plan.Stores, plan.V017) {
		out.Files = append(out.Files, File{Path: filepath.Join(root, filepath.FromSlash(f.Rel)), Size: f.Size})
		out.Bytes += f.Size
	}
	out.Count = len(out.Files)
	if dryRun {
		return out, nil
	}
	if out.Count == 0 {
		// Nothing to ask about: a gone repo's record is lore's own
		// bookkeeping and goes without asking (FLAG3 round 2).
		n, derr := dropDead(root, plan.Dead)
		out.Records = n
		return out, derr
	}
	if !yes {
		return PruneOut{}, errs.New(errs.NeedsConfirm, strings.TrimSuffix(listing(out.Files, out.Bytes), "\n")+"\n"+question).
			WithQuestion(question).WithHint(confirmHint).WithNext("lore", "cache", "prune", "--yes")
	}
	for _, f := range plan.Stores {
		if rerr := sqlite.RemoveCache(root, f.Rel); rerr != nil {
			return PruneOut{}, rerr
		}
	}
	for _, f := range plan.V017 {
		if rerr := removeV017Cache(root, f.Rel); rerr != nil {
			return PruneOut{}, rerr
		}
	}
	n, err := dropDead(root, plan.Dead)
	out.Records, out.Removed = n, err == nil
	return out, err
}

// MakePlan decides what prune deletes under the cache root.
//
// FLAG3: a cache is kept while a repo on this machine attaches it, judged
// from the use records every core open writes (sqlite.RecordUse,
// <root>/repos/): a record is live while its repo top still holds a .lore
// folder; a live record's cache stays, except a stores.yaml entry the
// repo's stores.yaml no longer names (`lore store remove` counts at once;
// org and personal attachments are refreshed by the repo's next command).
// A v0.1.17 per-project cache stays while a live repo has that project id.
// Everything else is derived data of a store nobody here uses.
func MakePlan(root string) (Plan, error) {
	var plan Plan
	records, err := sqlite.ReadUses(root)
	if err != nil {
		return plan, err
	}
	keep := map[string]bool{}
	keepProjects := map[string]bool{}
	for _, rf := range records {
		rec := rf.Record
		if !repoAlive(rec.Repo) {
			plan.Dead = append(plan.Dead, rf)
			continue
		}
		named, trusted := committedStoreNames(rec.Repo)
		for _, u := range rec.Caches {
			if u.Committed && trusted && !named[u.Store] {
				continue
			}
			keep[u.Path] = true
		}
		if p := v017Project(rec.Repo); p != "" {
			keepProjects[p] = true
		}
	}
	files, err := sqlite.StoreCaches(root)
	if err != nil {
		return plan, err
	}
	for _, f := range files {
		if !keep[f.Rel] {
			plan.Stores = append(plan.Stores, f)
		}
	}
	old, err := v017Caches(root)
	if err != nil {
		return plan, err
	}
	for _, f := range old {
		if !keepProjects[strings.TrimSuffix(f.Rel, v017CacheExt)] {
			plan.V017 = append(plan.V017, f)
		}
	}
	return plan, nil
}

// repoAlive reports whether top is still a lore repo on this machine.
func repoAlive(top string) bool {
	if top == "" || !filepath.IsAbs(top) {
		return false
	}
	fi, err := os.Stat(filepath.Join(top, repoMarkerRel))
	return err == nil && fi.IsDir()
}

// committedStoreNames is the set of store names top's .lore/stores.yaml
// names now (an absent file names none). trusted is false when the file
// cannot be read or parsed: the caller then keeps every committed entry
// (fail safe: prune never deletes a cache on a guess that a store was
// detached).
func committedStoreNames(top string) (names map[string]bool, trusted bool) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(top, filepath.FromSlash(attach.FileName))))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]bool{}, true
	}
	if err != nil {
		return nil, false
	}
	cfg, err := attach.Parse(b)
	if err != nil {
		return nil, false
	}
	out := map[string]bool{}
	for _, s := range cfg.Stores {
		out[string(s.Name)] = true
	}
	return out, true
}

// v017Project is the project id of top's v0.1.17 marker (.lore/data/
// _meta.json), "" when there is none or it does not hold a valid one.
func v017Project(top string) string {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(top, filepath.FromSlash(v017MetaRel))))
	if err != nil {
		return ""
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(b, &doc) != nil {
		return ""
	}
	var id string
	if json.Unmarshal(doc[v017MetaProjectKey], &id) != nil || !isProjectID(id) {
		return ""
	}
	return id
}

// isProjectID reports whether s is a prj_ id (v0.1.17 and 0.1.18 share the
// <prefix>_<32 hex> shape).
func isProjectID(s string) bool {
	_, err := ids.Parse[ids.Project](s)
	return err == nil
}

// v017Caches lists the v0.1.17 per-project caches (<prj_…>.db) directly in
// the cache root (the machine store's cache, machine.db, is not one).
func v017Caches(root string) ([]sqlite.File, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "list "+root, err)
	}
	var out []sqlite.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, v017CacheExt) || !isProjectID(strings.TrimSuffix(name, v017CacheExt)) {
			continue
		}
		var size int64
		for _, s := range v017Sidecars {
			if fi, serr := os.Stat(filepath.Join(root, name+s)); serr == nil {
				size += fi.Size()
			}
		}
		out = append(out, sqlite.File{Rel: name, Size: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

// removeV017Cache deletes one v0.1.17 cache (name directly in root) and its
// WAL files. It never touches ~/.lore/render: prune deletes only inside
// the cache folder (FLAG3).
func removeV017Cache(root, name string) error {
	if filepath.Base(name) != name {
		return errs.New(errs.Internal, "refusing to remove "+name+": not a cache file in "+root)
	}
	for _, s := range v017Sidecars {
		if err := os.Remove(filepath.Join(root, name+s)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return errs.Wrap(errs.Internal, "remove the cache "+name, err)
		}
	}
	return nil
}

// dropDead removes the use records of repos that are gone and returns how
// many it removed.
func dropDead(root string, dead []sqlite.RecordFile) (int, error) {
	for i, rf := range dead {
		if err := sqlite.RemoveUseRecord(root, rf.Path); err != nil {
			return i, err
		}
	}
	return len(dead), nil
}

// HumanBytes prints n as B/KB/MB/... with one decimal above bytes.
func HumanBytes(n int64) string {
	if n < byteStep {
		return fmt.Sprintf("%d %s", n, byteUnits[0])
	}
	f := float64(n)
	i := 0
	for f >= byteStep && i < len(byteUnits)-1 {
		f /= byteStep
		i++
	}
	return fmt.Sprintf("%.1f %s", f, byteUnits[i])
}
