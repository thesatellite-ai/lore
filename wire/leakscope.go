package wire

// leakscope.go — which org stores the leak guard reads in a client repo
// (issues #133 and #135, devco:D-910).
//
// One rule: in a client repo, "internal" is every row of every org store
// whose audience is not the client's team, whatever the repo links to.
// The client's team is the team audience (`audience: team:@x` in
// lore-org.yaml) of the store this repo's link points at: the default
// link, else the first (the folder `lore whoami` lists under reads).
//
//	linked store audience team:@client ─▶ client team @client
//	  org store, audience team:@client ─▶ shared: never indexed (#133)
//	  org store, any other audience    ─▶ internal: every folder of its
//	                                      clone on this machine indexed (#135)
//	linked store with no team audience ─▶ no client team: every org store
//	                                      is internal (the old rule)
//
// Why every folder and not only the attached ones: an internal decision
// about the client in internal/client_ops/ was never attached in a repo
// linked to shared/client_app/, so its id and text committed and pushed
// with `lore check leaks` passing (#135). A store not cloned on this
// machine is skipped: its rows are not here to copy from, and a person
// outside its audience never clones it.

import (
	"context"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/store"
)

// leakScope is what the leak guard of one client repo reads beside the
// stores the repo attaches: Extra, the folders of the org's internal
// stores no attachment reads yet (attached read-only for the one check),
// and Shared, the attached sources whose store is shared with the
// client's team (never indexed).
type leakScope struct {
	Extra  []attach.Store
	Shared map[store.Source]bool
}

// primaryLink is the link of entry org a repo reads by default: its
// default link, else its first (OrgStores attaches its folder; the leak
// guard takes the client's team from its store). nil when none.
func primaryLink(org string, links []orgresolve.Link) *orgresolve.Link {
	var out *orgresolve.Link
	for i := range links {
		l := &links[i]
		if l.Entry != org {
			continue
		}
		if out == nil || (l.Default && !out.Default) {
			out = l
		}
	}
	return out
}

// leakClientAudience is the client's team of a client repo: the team
// audience of the store its primary link points at; "" when that store
// has no team audience (everyone, admins) or the repo has no link.
func leakClientAudience(org *orgconf.Org, links []orgresolve.Link) orgconf.Audience {
	if org == nil {
		return ""
	}
	l := primaryLink(org.Name, links)
	if l == nil {
		return ""
	}
	sc := org.StoreFor(l.StoreURL)
	if sc == nil {
		return ""
	}
	if _, ok := sc.Audience.Team(); !ok {
		return ""
	}
	return sc.Audience
}

// leakSharedStore reports whether sc is shared with the client's team
// client: its audience is that same team. With no client team nothing is.
func leakSharedStore(sc *orgconf.Store, client orgconf.Audience) bool {
	if sc == nil {
		return false
	}
	want, ok := client.Team()
	if !ok {
		return false
	}
	got, ok := sc.Audience.Team()
	return ok && got == want
}

// leakLinks is the links a client repo's leak scope reads: the resolved
// ones, else the last fetched ones OrgStores attaches while offline.
func leakLinks(s *orgcfg.Situation) []orgresolve.Link {
	if s.Result.LinkUnknown && len(s.Result.Links) == 0 {
		return s.OldLinks
	}
	return s.Result.Links
}

// leakScopeOf builds the leak scope of a client repo (see the file
// comment); attached is every store the repo attaches (stores.yaml, the
// org's, the extra targets). Not a client repo, or no org: empty.
func (a *App) leakScopeOf(s *orgcfg.Situation, client bool, attached []attach.Store) leakScope {
	if !client || s.Result.Org == nil {
		return leakScope{Shared: map[store.Source]bool{}}
	}
	return a.leakScopeFor(s.Result.Org, leakClientAudience(s.Result.Org, leakLinks(s)), attached)
}

// leakScopeFor is the leak scope over org's stores for the client's team
// audience ("" = none: every org store is internal, as for a hand-over,
// whose future client's team no link names yet).
func (a *App) leakScopeFor(org *orgconf.Org, audience orgconf.Audience, attached []attach.Store) leakScope {
	sc := leakScope{Shared: map[store.Source]bool{}}
	seen := map[string]bool{}
	names := map[store.Source]bool{}
	for _, at := range attached {
		names[at.Name] = true
		if loc, err := attachLocation(at, at.Folder); err == nil {
			seen[loc] = true
		}
		if leakSharedStore(orgStoreOf(org, at.URL), audience) {
			sc.Shared[at.Name] = true
		}
	}
	for i := range org.Stores {
		st := &org.Stores[i]
		if leakSharedStore(st, audience) {
			continue
		}
		branch := attachBranch(st)
		dir, _, err := gitbackend.CloneDirOf(a.opts.Home, st.URL, branch)
		if err != nil || !exists(filepath.Join(dir, ".git")) {
			continue // not cloned here: no rows of it to copy from
		}
		folders, err := rowfs.StoreFolders(dir)
		if err != nil {
			continue
		}
		if exists(filepath.Join(dir, filepath.FromSlash(rowfs.LayoutStore.StorePath(rowfs.FolderRoot, rowfs.MetaFileName)))) {
			folders = append([]string{rowfs.FolderRoot}, folders...) // rows kept in the store root (an older store)
		}
		for _, f := range folders {
			base := attach.Store{URL: st.URL, Folder: f, Branch: branch, ReadOnly: true}
			loc, err := attachLocation(base, f)
			if err != nil || seen[loc] {
				continue
			}
			base.Name = leakScopeName(org.Name, st, f, names)
			seen[loc], names[base.Name] = true, true
			sc.Extra = append(sc.Extra, base)
		}
	}
	return sc
}

// orgStoreOf is the org's store whose url is raw (any spelling), or nil.
func orgStoreOf(org *orgconf.Org, raw string) *orgconf.Store {
	if raw == "" {
		return nil
	}
	n, err := repourl.NormalizeStore(raw)
	if err != nil {
		return nil
	}
	return org.StoreFor(n)
}

// leakScopeName names an extra folder <org>/<folder> (the label a finding
// shows, "store acme/client_ops"), or <org>/<store>/<folder> when the
// short name is taken (a folder of the same name in another store).
func leakScopeName(org string, st *orgconf.Store, folder string, taken map[store.Source]bool) store.Source {
	name := store.Source(org + targetSep + folder)
	if !taken[name] {
		return name
	}
	short := strings.TrimSuffix(path.Base(strings.TrimSuffix(st.URL, "/")), ".git")
	for i := 0; ; i++ {
		n := store.Source(org + targetSep + short + targetSep + folder)
		if i > 0 {
			n = store.Source(string(n) + "-" + strconv.Itoa(i))
		}
		if !taken[n] {
			return n
		}
	}
}

// leakAttached is every store this repo attaches: the committed
// stores.yaml entries, the org's (expected) and the extra targets.
func (a *App) leakAttached(ctx context.Context, expected []attach.Store) []attach.Store {
	out := slices.Clone(expected)
	if top, err := a.storeRepoTop(ctx); err == nil && top != "" {
		if cfg, cerr := readStoresFile(top); cerr == nil {
			out = append(out, cfg.Stores...)
		}
	}
	a.mu.Lock()
	out = append(out, a.extra...)
	a.mu.Unlock()
	return out
}
