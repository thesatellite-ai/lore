package wire

// outsideclones.go — store clones a machine kept from a store list it
// should not have followed (devco R-7 leftover). Before the org file check
// (adapters/orgcfg orgauthority.go), a member's hand edit of lore-org.yaml
// made every teammate's lore clone an outside repo under ~/.lore/stores,
// and a refused `lore store add` left its clone behind too. The check now
// stops new ones; this file finds the old ones:
//
//	~/.lore/stores/<a>/<b> (a git clone) ─▶ its origin url
//	  known (an org's store in ~/.lore/config.yaml, a store of a setup,
//	  a trusted store, the clone of a store this repo attaches) ─▶ fine
//	  unknown, and on a host or owner no org store uses, or not a lore
//	  store at all (no lore-store.yaml) ─▶ named by `lore doctor`
//	    (store_trust); `lore fix --yes` removes it when it holds no
//	    commit of its own; one that does is named, never removed
//
// Nothing here ever deletes without --yes, and nothing is pushed or
// fetched.

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// outsideClone is one leftover clone under ~/.lore/stores.
type outsideClone struct {
	dir      string
	url      string
	why      string // why it is not a store of this machine
	unpushed int    // commits on its branches that its remote lacks
}

// outsideClones lists the leftover clones (none on a machine with no org:
// there is no org whose stores tell what belongs).
func (a *App) outsideClones(ctx context.Context) []outsideClone {
	home := a.opts.Home
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil || len(cfg.Orgs) == 0 {
		return nil
	}
	known := readTrustedStores(home).URLs
	var orgSlugs []string
	for i := range cfg.Orgs {
		for _, sc := range cfg.Orgs[i].Stores {
			known = append(known, sc.URL)
			if s, serr := repourl.Normalize(sc.URL); serr == nil {
				orgSlugs = append(orgSlugs, s)
			}
		}
	}
	if entries, lerr := opsstores.ListEntries(ctx, a); lerr == nil {
		for _, e := range entries {
			known = append(known, e.URL)
		}
	}
	var out []outsideClone
	for _, dir := range storeCloneDirs(filepath.Join(home, git.StoresDir)) {
		u, gerr := gitRun{dir: dir, args: []string{"config", "--get", "remote.origin.url"}}.output(ctx)
		url := strings.TrimSpace(string(u))
		if gerr != nil || url == "" || strings.HasPrefix(url, folderURLScheme) || filepath.IsAbs(url) {
			continue // a local store (personal): nothing outside
		}
		_, serr := os.Stat(filepath.Join(dir, rowfs.MarkerFile))
		why := leftoverWhy(known, orgSlugs, url, serr == nil)
		if why == "" {
			continue // under an org's own host and owner: maybe a store attached by hand; not guessed
		}
		n := 0
		if c, cerr := (gitRun{dir: dir, args: []string{"rev-list", "--count", "--branches", "--not", "--remotes"}}).output(ctx); cerr == nil {
			n, _ = strconv.Atoi(strings.TrimSpace(string(c)))
		}
		out = append(out, outsideClone{dir: dir, url: url, why: why, unpushed: n})
	}
	return out
}

// storeCloneDirs are the git clones one or two folders below root.
func storeCloneDirs(root string) []string {
	var out []string
	isClone := func(d string) bool {
		_, err := os.Stat(filepath.Join(d, ".git"))
		return err == nil
	}
	first, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range first {
		d := filepath.Join(root, e.Name())
		if !e.IsDir() {
			continue
		}
		if isClone(d) {
			out = append(out, d)
			continue
		}
		second, rerr := os.ReadDir(d)
		if rerr != nil {
			continue
		}
		for _, s := range second {
			if dd := filepath.Join(d, s.Name()); s.IsDir() && isClone(dd) {
				out = append(out, dd)
			}
		}
	}
	return out
}

// sameAny reports whether url names the same store as one of urls.
func sameAny(urls []string, url string) bool {
	for _, k := range urls {
		if k != "" && storeaddr.Same(k, url) {
			return true
		}
	}
	return false
}

// sameOwner reports whether slug (host/owner/repo) shares host and owner
// with one of slugs.
func sameOwner(slugs []string, slug string) bool {
	for _, s := range slugs {
		if path.Dir(s) == path.Dir(slug) {
			return true
		}
	}
	return false
}

// line is what doctor prints for c, with its next step.
func (c outsideClone) line() (msg, fix string) {
	msg = "store clone " + c.dir + " (" + repourl.Mask(c.url) + ") is not a store any org, setup or repo on this machine uses: " + c.why + "; an earlier lore cloned it from a store list it should not have followed, and lore reads nothing from it"
	if c.unpushed > 0 {
		return msg + "; it holds " + strconv.Itoa(c.unpushed) + " commit(s) its remote lacks",
			"check them (git -C " + c.dir + " log --branches --not --remotes), then remove the folder by hand"
	}
	return msg, "lore fix --yes removes it (it holds no commit of its own)"
}

// outsideCloneFindings are the doctor findings (store_trust).
func (a *App) outsideCloneFindings(ctx context.Context) []DoctorFinding {
	var out []DoctorFinding
	for _, c := range a.outsideClones(ctx) {
		msg, fix := c.line()
		out = append(out, DoctorFinding{Check: DoctorCheckStoreTrust, Level: DoctorWarning, Message: msg, Fix: fix})
	}
	return out
}

// fixOutsideClones is lore fix's step: with yes (the global --yes,
// op.Confirmed), remove every leftover
// clone that holds no commit of its own; without, say what --yes removes.
func (a *App) fixOutsideClones(ctx context.Context, dry, yes bool) opsdoctor.FixStep {
	st := opsdoctor.FixStep{Step: opsdoctor.StepOutsideClones, Where: filepath.Join(a.opts.Home, git.StoresDir), Result: opsdoctor.ResultCurrent}
	var removable []outsideClone
	kept := 0
	for _, c := range a.outsideClones(ctx) {
		if c.unpushed > 0 {
			kept++
			continue
		}
		removable = append(removable, c)
	}
	if len(removable) == 0 {
		if kept > 0 {
			st.Result = strconv.Itoa(kept) + " outside clone(s) hold commits of their own: lore doctor names them; remove them by hand after checking"
		}
		return st
	}
	for _, c := range removable {
		st.Detail = append(st.Detail, c.dir)
	}
	if dry || !yes {
		st.Result = "would remove " + strconv.Itoa(len(removable)) + " outside store clone(s) no org, setup or repo here uses; run lore fix --yes to remove them"
		return st
	}
	for _, c := range removable {
		if err := os.RemoveAll(c.dir); err != nil {
			st.Failed, st.Result = true, "remove "+c.dir+": "+err.Error()
			return st
		}
	}
	st.Changed = true
	st.Result = "removed " + strconv.Itoa(len(removable)) + " outside store clone(s) no org, setup or repo here uses"
	return st
}

// withOutsideClones appends the outside clones step to steps when it has
// something to say (a machine with none prints no extra line).
func (a *App) withOutsideClones(ctx context.Context, dry bool, steps []opsdoctor.FixStep) []opsdoctor.FixStep {
	if st := a.fixOutsideClones(ctx, dry, op.Confirmed(ctx)); st.Result != opsdoctor.ResultCurrent {
		steps = append(steps, st)
	}
	return steps
}

// leftoverWhy is why the clone of url is a leftover ("" when it is not):
// unknown to every list of this machine (known), and either not a lore
// store (hasMarker false) or on a host and owner no org store uses.
func leftoverWhy(known, orgSlugs []string, url string, hasMarker bool) string {
	if sameAny(known, url) {
		return ""
	}
	if !hasMarker {
		return "it is not a lore store (no " + rowfs.MarkerFile + ")"
	}
	if slug, err := repourl.Normalize(url); err == nil && !sameOwner(orgSlugs, slug) {
		return "it is on a host or owner none of your orgs' stores use"
	}
	return ""
}
