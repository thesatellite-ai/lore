// cache_cmd.go — `lore cache prune` (§1 Commands, side effect 40, FLAG3):
// remove the cache files under ~/.lore/cache that no repo on this machine
// attaches any more. Caches are derived: a repo that attaches the store
// again rebuilds its cache from the row files on its next command.
package main

import (
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

	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/stores"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

// cachePruneQuestion is the confirmation `lore cache prune` asks before it
// deletes anything (FLAG3: lists first, asks first).
const cachePruneQuestion = "Delete these cache files? [y/N] "

// v017CacheExt ends a v0.1.17 per-project cache file (~/.lore/cache/<prj_…>.db).
const v017CacheExt = ".db"

// byteUnits are the size suffixes humanBytes prints, smallest first.
var byteUnits = []string{"B", "KB", "MB", "GB", "TB"}

// byteStep is the factor between two byteUnits.
const byteStep = 1024

func newCacheCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Maintain lore's caches outside the checkout (~/.lore/cache, ~/.lore/render)",
	}
	cmd.AddCommand(newCachePruneCommand())
	return cmd
}

// prunePlan is what one `lore cache prune` would delete.
type prunePlan struct {
	// stores are store caches (relative to the cache root) no live repo
	// record lists.
	stores []sqlite.File
	// v017 are leftover v0.1.17 per-project caches no live repo is.
	v017 []sqlite.File
	// deadRecords are use records of repos that are gone (or unreadable).
	deadRecords []sqlite.RecordFile
}

func newCachePruneCommand() *cobra.Command {
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove cache files of stores no repo on this machine attaches any more (lists them and asks first)",
		Example: `  lore cache prune --dry-run
  lore cache prune
  lore cache prune --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := loreHome()
			if err != nil {
				return err
			}
			root := filepath.Join(home, stores.CacheDir)
			plan, err := planCachePrune(root)
			if err != nil {
				return err
			}
			if len(plan.stores)+len(plan.v017) == 0 {
				fmt.Println("nothing to prune: every cache file belongs to a store a repo on this machine attaches")
				return pruneDeadRecords(root, plan.deadRecords, dryRun)
			}
			var total int64
			fmt.Println("cache files no repo on this machine attaches:")
			for _, f := range slices.Concat(plan.stores, plan.v017) {
				fmt.Printf("  %s  %s\n", filepath.Join(root, filepath.FromSlash(f.Rel)), humanBytes(f.Size))
				total += f.Size
			}
			n := len(plan.stores) + len(plan.v017)
			fmt.Printf("%d file(s), %s\n", n, humanBytes(total))
			if dryRun {
				fmt.Println("--dry-run: nothing removed")
				return nil
			}
			if !yes {
				// FLAG3: prune asks before it deletes; with no terminal to
				// ask on, it refuses with the exact command that confirms.
				if !stdinIsTTY() {
					return errs.New(errs.NeedsConfirm, "lore cache prune deletes the cache files listed above and there is no terminal to ask on: run `lore cache prune --yes` to delete them, or --dry-run to only list them").
						WithQuestion(strings.TrimSuffix(cachePruneQuestion, " [y/N] ")).
						WithHint("run `lore cache prune --yes` to delete them, or --dry-run to only list them").
						WithNext("lore", "cache", "prune", "--yes")
				}
				if !confirmYes(cachePruneQuestion) {
					fmt.Println("nothing removed")
					return nil
				}
			}
			for _, f := range plan.stores {
				if err := sqlite.RemoveCache(root, f.Rel); err != nil {
					return err
				}
			}
			for _, f := range plan.v017 {
				if err := removeV017Cache(root, f.Rel); err != nil {
					return err
				}
			}
			if err := pruneDeadRecords(root, plan.deadRecords, false); err != nil {
				return err
			}
			fmt.Printf("✓ removed %d file(s), %s\n", n, humanBytes(total))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, constants.FlagDryRun, false, "list the cache files prune would delete, delete nothing")
	cmd.Flags().BoolVar(&yes, flagYes, false, "delete without asking")
	return cmd
}

// planCachePrune decides what prune deletes under the cache root.
//
// FLAG3: a cache is kept while a repo on this machine attaches it, judged
// from the use records every core open writes (sqlite.RecordUse,
// ~/.lore/cache/repos/): a record is live while its repo top still holds a
// .lore folder; a live record's cache stays, except a stores.yaml entry the
// repo's stores.yaml no longer names (`lore store remove` counts at once;
// org and personal attachments are refreshed by the repo's next command).
// A v0.1.17 per-project cache stays while a live repo has that project id.
// Everything else is derived data of a store nobody here uses, and old
// client data must not linger.
func planCachePrune(root string) (prunePlan, error) {
	var plan prunePlan
	records, err := sqlite.ReadUses(root)
	if err != nil {
		return plan, err
	}
	keep := map[string]bool{}
	keepProjects := map[string]bool{}
	for _, rf := range records {
		rec := rf.Record
		if !repoAlive(rec.Repo) {
			plan.deadRecords = append(plan.deadRecords, rf)
			continue
		}
		named, trusted := committedStoreNames(rec.Repo)
		for _, u := range rec.Caches {
			if u.Committed && trusted && !named[u.Store] {
				continue
			}
			keep[u.Path] = true
		}
		if m, merr := lsync.ReadProjectMeta(filepath.Join(rec.Repo, filepath.FromSlash(dataDirRel))); merr == nil && m.ProjectID != "" {
			keepProjects[m.ProjectID] = true
		}
	}
	files, err := sqlite.StoreCaches(root)
	if err != nil {
		return plan, err
	}
	for _, f := range files {
		if !keep[f.Rel] {
			plan.stores = append(plan.stores, f)
		}
	}
	old, err := v017Caches(root)
	if err != nil {
		return plan, err
	}
	for _, f := range old {
		if !keepProjects[strings.TrimSuffix(f.Rel, v017CacheExt)] {
			plan.v017 = append(plan.v017, f)
		}
	}
	return plan, nil
}

// dataDirRel is a repo's row-file folder relative to its top.
const dataDirRel = ".lore/data"

// repoMarkerRel is the folder whose presence makes a recorded repo live.
const repoMarkerRel = ".lore"

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

// v017Caches lists the v0.1.17 per-project caches (<prj_…>.db) directly in
// the cache root.
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
		if e.IsDir() || !strings.HasSuffix(name, v017CacheExt) ||
			ids.Validate(strings.TrimSuffix(name, v017CacheExt), ids.PrefixProject) != nil {
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

// v017Sidecars are a v0.1.17 cache file and its WAL files ("" = the file).
var v017Sidecars = []string{"", "-wal", "-shm"}

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

// pruneDeadRecords removes the use records of repos that are gone; with
// dryRun it only says how many there are.
func pruneDeadRecords(root string, dead []sqlite.RecordFile, dryRun bool) error {
	if len(dead) == 0 || dryRun {
		return nil
	}
	for _, rf := range dead {
		if err := sqlite.RemoveUseRecord(root, rf.Path); err != nil {
			return err
		}
	}
	return nil
}

// humanBytes prints n as B/KB/MB/... with one decimal above bytes.
func humanBytes(n int64) string {
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
