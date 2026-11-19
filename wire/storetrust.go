package wire

// storetrust.go — what lore read from a store but did not take, named
// where a person sees it (devco R-7, C30): a change to the org's
// lore-org.yaml made by someone who is not an org admin is held back
// (adapters/orgcfg orgauthority.go); `lore sync` and `lore doctor` say
// which commit, who made it and the step that settles it.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

// storeTrustLines are the lines `lore sync` adds for what it did not take.
// The org this folder resolves to is worked out once, and only when an
// org of the config lists a store: each resolution runs git in the repo,
// and doctor --all asks this in every repo (devco D-332, its git budget).
func (a *App) storeTrustLines(ctx context.Context) []string {
	var org []string
	if cfg := a.storeOrgConfig(); cfg != nil {
		only := a.syncOrg(ctx)
		org = slices.Concat(a.heldOrgChangeLines(ctx, cfg, only), a.strayFileLines(ctx, cfg, only))
	}
	return slices.Concat(org, a.untrustedLines(ctx))
}

// storeOrgConfig is this machine's org config when an org in it lists a
// store, nil otherwise (no home, no config, or no org store to check).
func (a *App) storeOrgConfig() *orgconf.Config {
	if a.opts.Home == "" {
		return nil
	}
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil || cfg == nil {
		return nil
	}
	for i := range cfg.Orgs {
		if len(cfg.Orgs[i].Stores) > 0 {
			return cfg
		}
	}
	return nil
}

// strayFileHint is the step a stray-file line names: drop them in the
// store's clone and send the commit (the store itself is fine).
const strayFileHint = "lore reads the rest of the store as before; to drop them: git -C %s rm -r -- <file> && git -C %s commit -m \"drop files lore does not own\" && lore sync"

// strayFileLines is one line per org store (of the org this folder
// resolves to, or every org) whose clone here holds files lore does not
// own: which files, the commit and author that added them, and the step.
// only is the org this folder resolves to ("" = every org of cfg).
func (a *App) strayFileLines(ctx context.Context, cfg *orgconf.Config, only string) []string {
	g := &orgcfg.GitLinks{Held: orgStoreHeld, Home: a.opts.Home, Config: cfg}
	var out []string
	seen := map[string]bool{}
	for i := range cfg.Orgs {
		o := &cfg.Orgs[i]
		if only != "" && o.Name != only {
			continue
		}
		for _, sc := range o.Stores {
			strays, dir := g.LocalStrays(ctx, sc.URL, sc.Branch)
			if len(strays) == 0 || seen[dir] {
				continue
			}
			seen[dir] = true
			shown := make([]string, 0, len(strays))
			for j, st := range strays {
				if j == strayShown {
					shown = append(shown, fmt.Sprintf("and %d more", len(strays)-strayShown))
					break
				}
				shown = append(shown, st.String())
			}
			d := tildeHome(dir, filepath.Dir(a.opts.Home))
			out = append(out, fmt.Sprintf("lore: store %s: %d file(s) lore does not own were skipped: %s; "+strayFileHint,
				orgStoreShown(o, sc), len(strays), strings.Join(shown, ", "), d, d))
		}
	}
	return out
}

// orgStoreShown is the name lore shows for store sc of org o: the org's
// name for the store holding the org folder, else its short name.
func orgStoreShown(o *orgconf.Org, sc orgconf.Store) string {
	if f := o.OrgFolderStore(); f != nil && storeaddr.Same(f.URL, sc.URL) {
		return o.Name
	}
	return sc.ShortName()
}

// strayShown caps the stray files a line names.
const strayShown = 5

// heldOrgChangeLines is one line per held-back change to the
// lore-org.yaml of the org this folder resolves to (every org of the
// config when it resolves to none), as this machine's store clone holds
// it (no network). only is the org this folder resolves to ("" = every
// org of cfg).
func (a *App) heldOrgChangeLines(ctx context.Context, cfg *orgconf.Config, only string) []string {
	var out []string
	for i := range cfg.Orgs {
		o := &cfg.Orgs[i]
		if only != "" && o.Name != only {
			continue
		}
		sc := o.OrgFolderStore()
		if sc == nil {
			if len(o.Stores) == 0 {
				continue
			}
			sc = &o.Stores[0]
		}
		g := &orgcfg.GitLinks{Held: orgStoreHeld, Home: a.opts.Home, Config: cfg}
		sg, _ := g.LocalSuggestion(ctx, sc.URL, sc.Branch) // an error still carries what was held back
		for _, h := range sg.Held {
			out = append(out, "lore: org "+o.Name+": "+h.String())
		}
	}
	return out
}

// trustedStoresFile is the machine-local record of the stores the person
// trusted with `lore store trust` (R-9), under ~/.lore.
const trustedStoresFile = "state/trusted-stores.json"

// trustedStores is the record's shape: normalized store urls.
type trustedStores struct {
	URLs []string `json:"urls"`
}

// readTrustedStores reads the record (none or unreadable = empty).
func readTrustedStores(home string) trustedStores {
	var t trustedStores
	b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(trustedStoresFile)))
	if err == nil {
		_ = json.Unmarshal(b, &t) // an unreadable record trusts nothing more
	}
	return t
}

// untrustedStore is one store the committed stores.yaml attaches that
// lore left out (heldCommittedStores): its name, url and the org whose
// patterns it is outside of, with the commit that last changed the file.
type untrustedStore struct {
	Name   store.Source
	URL    string
	Org    string
	Commit string
}

// line is what sync and doctor print for it.
func (u untrustedStore) line() string {
	by := ""
	if u.Commit != "" {
		by = " (.lore/stores.yaml last changed in commit " + u.Commit + ")"
	}
	return "store " + string(u.Name) + " (" + repourl.Mask(u.URL) + ") is attached by this repo's .lore/stores.yaml" + by +
		" on a host outside your org (" + u.Org + "), and nobody attached it on this machine: lore has not cloned it and its notes are not read; if you trust it: lore store trust --name " + string(u.Name)
}

// heldCommittedStores splits the stores of the repo's committed
// stores.yaml (read from top) into those to open and those left out until
// the person trusts them (devco R-9). Anyone who can push to a repo can
// add a store line, and a store on a host the org does not own, that
// nobody attached on this machine, would be cloned and fed to the agent
// with no question. Only a machine with an org in ~/.lore/config.yaml is
// checked (a machine with no org reads what it has always read). A store
// is kept when it is local (a path or file://), is a store of a
// configured org or under the same host and owner as one, matches an
// org's own: or repos: patterns, already has a clone here (it was
// attached on this machine), or was trusted.
func heldCommittedStores(ctx context.Context, home, top string, stores []attach.Store) ([]attach.Store, []untrustedStore) {
	if home == "" || top == "" || len(stores) == 0 {
		return stores, nil
	}
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil || len(cfg.Orgs) == 0 {
		return stores, nil
	}
	trusted := readTrustedStores(home)
	var keep []attach.Store
	var held []untrustedStore
	for _, s := range stores {
		if committedStoreKnown(home, cfg, trusted, s.URL) {
			keep = append(keep, s)
			continue
		}
		held = append(held, untrustedStore{Name: s.Name, URL: s.URL, Org: orgNames(cfg), Commit: lastCommitOf(ctx, top, attach.FileName)})
	}
	return keep, held
}

// orgNames is the configured orgs' names, comma separated.
func orgNames(cfg *orgconf.Config) string {
	names := make([]string, 0, len(cfg.Orgs))
	for i := range cfg.Orgs {
		names = append(names, cfg.Orgs[i].Name)
	}
	return strings.Join(names, ", ")
}

// committedStoreKnown is heldCommittedStores' test for one store url.
func committedStoreKnown(home string, cfg *orgconf.Config, trusted trustedStores, raw string) bool {
	norm, err := repourl.NormalizeStore(raw)
	if err != nil || strings.HasPrefix(raw, folderURLScheme) || filepath.IsAbs(raw) {
		return true // local, or not a url lore clones: nothing outside is reached
	}
	slug, serr := repourl.Normalize(raw)
	for i := range cfg.Orgs {
		o := &cfg.Orgs[i]
		if serr == nil && firstMatch(append(append([]string{}, o.Own...), o.Repos...), slug) != "" {
			return true
		}
		for _, sc := range o.Stores {
			if storeaddr.Same(sc.URL, raw) {
				return true
			}
			if on, oerr := repourl.Normalize(sc.URL); oerr == nil && serr == nil && path.Dir(on) == path.Dir(slug) {
				return true // the same host and owner as the org's own store
			}
		}
	}
	for _, u := range trusted.URLs {
		if storeaddr.Same(u, norm) {
			return true
		}
	}
	if dir, _, derr := gitbackend.CloneDirFor(home, gitbackend.Remote{Norm: norm}, ""); derr == nil {
		if _, serr := os.Stat(filepath.Join(dir, ".git")); serr == nil {
			return true // attached on this machine before (by the person, or trusted)
		}
	}
	return false
}

// firstMatch is the first of patterns that matches the normalized repo
// slug, "" when none does (an invalid pattern matches nothing).
func firstMatch(patterns []string, slug string) string {
	for _, p := range patterns {
		if cp, err := repourl.Compile(p); err == nil && cp.Match(slug) {
			return p
		}
	}
	return ""
}

// lastCommitOf is "<short sha> by <author>" of the commit that last
// changed rel in the repo at top, "" when git cannot say.
func lastCommitOf(ctx context.Context, top, rel string) string {
	out, err := gitRun{dir: top, args: []string{"log", "-1", "--format=%h by %an <%ae>", "--", rel}}.output(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// untrustedLines are the lines for the stores left out at this open.
func (a *App) untrustedLines(ctx context.Context) []string {
	st, err := a.state(ctx)
	if err != nil || st == nil {
		return nil
	}
	out := make([]string, 0, len(st.untrusted))
	for _, u := range st.untrusted {
		out = append(out, "lore: "+u.line())
	}
	return out
}

// TrustStore implements opsstores.Truster: the store named in the repo's
// stores.yaml is recorded as trusted on this machine.
func (a *App) TrustStore(ctx context.Context, in opsstores.TrustIn) (opsstores.TrustOut, error) {
	const verb = "store trust"
	top, err := a.repoTop(ctx)
	if err != nil || top == "" {
		return opsstores.TrustOut{}, errs.New(errs.NotAStore, verb+": it trusts a store of a repo's .lore/stores.yaml, and this folder is not inside a git repo").
			WithHint("run it in the repo whose lore sync named the store")
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return opsstores.TrustOut{}, err
	}
	var s *attach.Store
	names := make([]string, 0, len(cfg.Stores))
	for i := range cfg.Stores {
		names = append(names, string(cfg.Stores[i].Name))
		if string(cfg.Stores[i].Name) == in.Name {
			s = &cfg.Stores[i]
		}
	}
	if s == nil {
		return opsstores.TrustOut{}, errs.New(errs.NotFound, verb+": this repo's .lore/stores.yaml attaches no store named "+in.Name+" (it attaches: "+strings.Join(names, ", ")+")").
			WithHint("lore store trust --name <one of those>")
	}
	norm, err := repourl.NormalizeStore(s.URL)
	if err != nil {
		return opsstores.TrustOut{}, errs.Wrap(errs.InvalidInput, verb+": store "+in.Name+" has a url lore cannot read", err)
	}
	out := opsstores.TrustOut{Store: in.Name, URL: repourl.Mask(s.URL)}
	t := readTrustedStores(a.opts.Home)
	for _, u := range t.URLs {
		if storeaddr.Same(u, norm) {
			return out, nil
		}
	}
	t.URLs = append(t.URLs, norm)
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return out, errs.Wrap(errs.Internal, verb, err)
	}
	p := filepath.Join(a.opts.Home, filepath.FromSlash(trustedStoresFile))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return out, errs.Wrap(errs.Internal, verb+": "+p, err)
	}
	if err := writeFileAtomic(p, append(b, '\n'), 0o600); err != nil {
		return out, errs.Wrap(errs.Internal, verb+": "+p, err)
	}
	out.Changed = true // the next command's open attaches it
	return out, nil
}

// DoctorCheckStoreTrust names the check of what lore did not take from
// the stores (devco R-2, R-7, R-9).
const DoctorCheckStoreTrust DoctorCheck = "store_trust"

// DoctorStoreTrust is the check (registered in doctor_op.go): the held
// back org file changes, the files lore does not own in the org stores and
// the committed stores waiting for `lore store trust`, as warnings. Each
// line names its own step.
func (a *App) DoctorStoreTrust(ctx context.Context) ([]DoctorFinding, error) {
	var out []DoctorFinding
	for _, l := range a.storeTrustLines(ctx) {
		out = append(out, DoctorFinding{Check: DoctorCheckStoreTrust, Level: DoctorWarning, Message: strings.TrimPrefix(l, "lore: ")})
	}
	out = append(out, a.outsideCloneFindings(ctx)...) // outsideclones.go: R-7 leftovers
	return out, nil
}

// untrustedStoreErr refuses a store command on src when src is a store of
// the repo's committed stores.yaml that open leaves out until the person
// trusts it (heldCommittedStores): opening it here would clone it (devco
// R-9: `lore doctor` cloned it through the push-mode view). nil for every
// other store.
func (a *App) untrustedStoreErr(ctx context.Context, src attach.Store) error {
	top, err := a.repoTop(ctx)
	if err != nil || top == "" {
		return nil
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return nil
	}
	for _, s := range cfg.Stores {
		if s.Name != src.Name || !storeaddr.Same(s.URL, src.URL) {
			continue
		}
		if _, held := heldCommittedStores(ctx, a.opts.Home, top, []attach.Store{s}); len(held) > 0 {
			return errs.New(errs.NeedsConfirm, held[0].line()).WithHint("lore store trust --name " + string(s.Name))
		}
	}
	return nil
}
