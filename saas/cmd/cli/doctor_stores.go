// doctor_stores.go — the stores rows of `lore doctor` (§9.4, §6.6, F20):
// stores configured on a machine whose hostname matches untrusted_hosts,
// store clones holding unpushed work, and the checkout's CLAUDE.local.md
// (one import line, excluded, not a symlink, not tracked).
package main

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"

	"saas/pkg/aicoder/lorecfg"
)

// doctorStores adds the stores findings to the doctor report. Every
// finding degrades health; none is fatal (doctor reports, it never blocks).
func doctorStores(ctx context.Context, r *doctorReport) {
	cfg, err := loadTeamConfig(ctx)
	if err != nil {
		return // the config problem is `lore config check`'s to report
	}
	warn := func(msg string) {
		r.Warnings = append(r.Warnings, msg)
		if r.Status == doctorHealthy {
			r.Status = doctorDegraded
		}
	}
	if host, err := os.Hostname(); err == nil && hasStores(cfg) {
		for _, pat := range cfg.UntrustedHosts {
			if ok, _ := path.Match(strings.ToLower(pat), strings.ToLower(host)); ok {
				warn("this machine (" + host + ") matches untrusted_hosts " + pat + " but org stores are configured here: anyone with access to it can read ~/.lore/clones (§6.6); remove the stores from this machine's config")
				break
			}
		}
	}
	if m, err := newStoreManager(cfg, nil); err == nil {
		seen := map[string]bool{}
		for _, o := range cfg.Orgs {
			for _, sc := range o.Stores {
				st, err := m.Lookup(ctx, sc.URL)
				if err != nil || seen[st.Dir] {
					continue
				}
				seen[st.Dir] = true
				if un, err := st.Unpushed(ctx); err == nil && len(un) > 0 {
					warn(redactIfQuiet(st.Norm) + ": unpushed store work (" + strings.Join(un, ", ") + "); `lore sync` pushes it")
				}
			}
		}
	}
	if coreApp != nil {
		if list, err := coreApp.ListStores(ctx); err == nil && len(list) > wire.MaxStoreCaches {
			r.Notes = append(r.Notes, strconv.Itoa(len(list))+" stores attached (more than "+strconv.Itoa(wire.MaxStoreCaches)+
				"): this repo uses one combined store cache (CFG3) instead of one cache file per store")
		}
	}
	if root, err := os.Getwd(); err == nil {
		local := filepath.Join(root, claudeLocalFile)
		if fi, err := os.Lstat(local); err == nil {
			switch {
			case fi.Mode()&os.ModeSymlink != 0:
				warn(claudeLocalFile + " is a symlink; lore will not write through it (delete it and run `lore render`)")
			default:
				if tracked, _ := gitTracks(ctx, root, claudeLocalFile); tracked {
					warn(claudeLocalFile + " is tracked by git: its content would be committed (`git rm --cached " + claudeLocalFile + "`)")
				}
				if b, err := os.ReadFile(filepath.Clean(local)); err == nil && strings.Count(strings.TrimSpace(string(b)), "\n") > 0 {
					warn(claudeLocalFile + " holds more than the one import line; internal context belongs in ~/.lore/render")
				}
			}
		}
	}
}

// hasStores reports whether any org entry (or the personal store) names a
// git store on this machine.
func hasStores(cfg *lorecfg.Config) bool {
	for _, o := range cfg.Orgs {
		if len(o.Stores) > 0 {
			return true
		}
	}
	ps := personalStoreV017(cfg)
	return ps != "" && ps != lorecfg.PersonalStoreLocal
}

// doctorDataDir is a store's row folder relative to the store root
// (rowfs: <folder>/.lore/data, the root folder at .lore/data).
const doctorDataDir = ".lore/data"

// resurrectedFindings names every live row file of a purged row an old
// clone brought back (DQ4, DELETION_DESIGN 2.7). The engine already hides
// such a file from every read; doctor says where it is so a person deletes
// it. A failed check is itself a finding (doctor never hides a problem).
func resurrectedFindings(ctx context.Context) []string {
	eng, err := coreApp.Engine(ctx)
	if err != nil {
		return []string{"purged rows not checked: " + err.Error()}
	}
	res, err := eng.Resurrected(ctx)
	if err != nil {
		return []string{"purged rows not checked: " + err.Error()}
	}
	if len(res) == 0 {
		return nil
	}
	folders := map[store.Source]string{}
	if idents, err := coreApp.StoreIdentities(ctx); err == nil {
		for _, si := range idents {
			folders[si.Name] = si.Folder
		}
	}
	out := make([]string, 0, len(res))
	for _, x := range res {
		p := path.Join(folders[x.Store], doctorDataDir, rowfs.RowPath(x.Table, x.ID))
		out = append(out, "store "+string(x.Store)+": "+p+" is a live copy of a purged row (an old clone brought it back); lore hides it from every read; remove the file (git rm it and commit)")
	}
	return out
}
