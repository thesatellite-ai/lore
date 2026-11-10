package wire

// doctor_repostores.go — opsdoctor.RepoStoresEnv: the stores each known
// repo attaches in its .lore/stores.yaml, for `lore doctor --all` (#16,
// ONBOARDING_DESIGN 6.1 store.read "each .lore/stores.yaml store of
// scanned repos"). Read-only: the file is parsed, no repo store is opened
// (opening one would write .lore/ wiring), no remote is contacted here.
//
//	roots ─▶ readStoresFile(root) ─▶ per entry: CleanURL (no credential),
//	   branch (`branch:` key or ?branch=), folder/branch query dropped
//	   (the probe reads the remote), storeKey (every spelling of one
//	   remote), Org (the org whose roster store this url is)

import (
	"context"
	"net/url"
	"slices"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// RepoStores implements opsdoctor.RepoStoresEnv.
func (a *App) RepoStores(_ context.Context, roots []string) ([]opsdoctor.RepoStore, []string) {
	orgStores := orgRosterKeys(a.opts.Home)
	var out []opsdoctor.RepoStore
	var warnings []string
	for _, root := range roots {
		cfg, err := readStoresFile(root)
		if err != nil {
			warnings = append(warnings, root+": "+attach.FileName+" could not be read, so its stores were not checked: "+err.Error())
			continue
		}
		for _, st := range cfg.Stores {
			rs, ok := repoStoreOf(root, st)
			if !ok {
				warnings = append(warnings, root+": store "+string(st.Name)+" has a url lore cannot read, so it was not checked (lore store list shows it)")
				continue
			}
			rs.Org = orgStores[rs.Key]
			out = append(out, rs)
		}
	}
	return out, warnings
}

// repoStoreOf is one stores.yaml entry as doctor checks it: the cleaned url
// without its query, the pinned branch and the store's identity key.
// false when the url cannot be read safely.
func repoStoreOf(root string, st attach.Store) (opsdoctor.RepoStore, bool) {
	clean, err := attach.CleanURL(st.URL)
	if err != nil {
		return opsdoctor.RepoStore{}, false
	}
	u, err := url.Parse(clean)
	if err != nil {
		return opsdoctor.RepoStore{}, false
	}
	branch := st.Branch
	if q, qerr := url.ParseQuery(u.RawQuery); qerr == nil && branch == "" {
		branch = q.Get(git.BranchParam) // the `<url>?branch=<b>` spelling
	}
	u.RawQuery, u.Fragment = "", ""
	key, ok := storeKey(clean)
	if !ok {
		key = ""
	}
	return opsdoctor.RepoStore{Repo: root, Name: string(st.Name), URL: u.String(), Key: key, Branch: branch, ReadOnly: st.ReadOnly}, true
}

// orgRosterKeys maps the storeKey of each org's roster store (the store
// "store <org>" resolves to: the one holding the org folder, else its
// first) to the org's name. An unreadable config.yaml maps nothing: then
// the store is checked as an attached store too, which is only a repeat.
func orgRosterKeys(home string) map[string]string {
	out := map[string]string{}
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil {
		return out
	}
	for _, o := range cfg.Orgs {
		if len(o.Stores) == 0 {
			continue
		}
		roster := o.Stores[0].URL
		if i := slices.IndexFunc(o.Stores, func(s orgconf.Store) bool { return s.OrgFolder != "" }); i >= 0 {
			roster = o.Stores[i].URL
		}
		if k, ok := storeKey(roster); ok {
			out[k] = o.Name
		}
	}
	return out
}
