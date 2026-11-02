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
	"strings"

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
				if b, err := os.ReadFile(local); err == nil && strings.Count(strings.TrimSpace(string(b)), "\n") > 0 {
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
	return cfg.Personal.Store != "" && cfg.Personal.Store != lorecfg.PersonalStoreLocal
}
