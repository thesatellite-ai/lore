// orgresolve.go — the org hooks of Options from the core's org resolution
// (adapters/orgcfg, logic/orgresolve), so both binaries answer "which org,
// which role, which org stores" with the same code (FA-OLDCODE-1).
//
// Flow (once per process, the first hook that asks):
//
//	OrgResolution.situation ──▶ orgcfg.Gather(home, dir, LORE_ORG, links)
//	   Org          ─▶ the resolved entry's identity (adapters/identity input)
//	   RepoIsClient ─▶ Situation.IsClient (fail closed)
//	   OrgStores    ─▶ the linked folder of the resolved entry's store + its
//	                   org folder ("<org>/<folder>", F13)
//	   OrgEntryStore─▶ any configured entry's store (`--from <org>/<folder>`)
//	   OrgSigners   ─▶ every entry with an identity signs its stores' commits
//
// v0.1.17 did this in saas/cmd/cli (coreOrg, coreRepoIsClient,
// coreOrgStores, coreOrgEntryStore, coreOrgSigners); those now call these
// methods.

package wire

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// OrgResolution answers the org hooks of one process from the machine's
// config. Build it with NewOrgResolution; the zero value is not usable.
type OrgResolution struct {
	home  string
	dir   string
	links orgcfg.LinkSource

	once sync.Once
	sit  *orgcfg.Situation
	err  error
}

// NewOrgResolution resolves the org of the project at dir from the config
// under home (~/.lore). links reads the org stores' links (F11); nil reads
// none, which fails closed for any org with stores.
func NewOrgResolution(home, dir string, links orgcfg.LinkSource) *OrgResolution {
	return &OrgResolution{home: home, dir: dir, links: links}
}

// Situation is the gathered inputs and F10's decision, computed once (it
// may fetch the org stores).
func (r *OrgResolution) Situation(ctx context.Context) (*orgcfg.Situation, error) {
	r.once.Do(func() {
		r.sit, r.err = orgcfg.Gather(ctx, orgcfg.In{Home: r.home, Dir: r.dir, EnvOrg: os.Getenv(orgcfg.EnvOrg), Links: r.links})
	})
	return r.sit, r.err
}

// Apply sets the org hooks of o from r (the hooks o already sets win, so
// an embedder can still replace one).
func (r *OrgResolution) Apply(o *Options) {
	if o.Org == nil {
		o.Org = r.Org
	}
	if o.RepoIsClient == nil {
		o.RepoIsClient = r.RepoIsClient
	}
	if o.OrgStores == nil {
		o.OrgStores = r.OrgStores
	}
	if o.OrgEntryStore == nil {
		o.OrgEntryStore = r.OrgEntryStore
	}
	if o.OrgSigners == nil {
		o.OrgSigners = r.OrgSigners
	}
}

// Org is Options.Org: the resolved entry's name and identity; nil when no
// org resolves. A refusal (ambiguous, unknown LORE_ORG entry) is an error:
// a write must never be stamped with a guessed person.
func (r *OrgResolution) Org(ctx context.Context) (*identity.Org, error) {
	s, err := r.Situation(ctx)
	if err != nil {
		return nil, err
	}
	if s.ResolveErr != nil {
		return nil, orgcfg.ResolveError(s.ResolveErr)
	}
	o := s.Result.Org
	if o == nil {
		return nil, nil
	}
	return &identity.Org{Name: o.Name, Identity: identity.Entry{Key: o.Identity.Key, Name: o.Identity.Name, Handle: o.Identity.Handle}}, nil
}

// RepoIsClient is Options.RepoIsClient (fail closed: any error answers
// client).
func (r *OrgResolution) RepoIsClient(ctx context.Context) (bool, error) {
	s, err := r.Situation(ctx)
	if err != nil {
		return true, err
	}
	return s.IsClient()
}

// OrgStores is Options.OrgStores (CONFIG_GUIDE section 4 step 5b): the
// resolved entry's store attached under the entry's name at the folder
// this repo is linked to (the default link, else the first; else the
// committed .lore/org.yaml project; else the store root), and the org
// folder store under "<org>/<org folder>". Nothing is written to the repo
// (CFG7). An org refusal or unreadable links is an error (fail closed).
func (r *OrgResolution) OrgStores(ctx context.Context) ([]attach.Store, error) {
	s, err := r.Situation(ctx)
	if err != nil {
		return nil, err
	}
	if s.ResolveErr != nil {
		return nil, orgcfg.ResolveError(s.ResolveErr)
	}
	org := s.Result.Org
	if org == nil || len(org.Stores) == 0 {
		return nil, nil
	}
	if s.Result.LinkUnknown {
		return nil, errs.New(errs.LinkUnknown, "the store links of this repo cannot be read: "+s.Result.LinkDetail)
	}
	var out []attach.Store
	var linked *orgresolve.Link
	for i := range s.Result.Links {
		l := &s.Result.Links[i]
		if l.Entry != org.Name {
			continue
		}
		if linked == nil || (l.Default && !linked.Default) {
			linked = l
		}
	}
	switch {
	case linked != nil:
		if sc := org.StoreFor(linked.StoreURL); sc != nil {
			out = append(out, attach.Store{Name: store.Source(org.Name), URL: sc.URL, Folder: linked.Folder, Branch: attachBranch(sc)})
		}
	case s.Committed != nil && s.Committed.Org == org.Name && s.Committed.Project != "":
		sc := &org.Stores[0]
		out = append(out, attach.Store{Name: store.Source(org.Name), URL: sc.URL, Folder: s.Committed.Project, Branch: attachBranch(sc)})
	default:
		sc := &org.Stores[0]
		out = append(out, attach.Store{Name: store.Source(org.Name), URL: sc.URL, Branch: attachBranch(sc)})
	}
	for i := range org.Stores {
		sc := &org.Stores[i]
		if sc.OrgFolder == "" {
			continue
		}
		company := attach.Store{Name: store.Source(org.Name + orgconf.RefSep + sc.OrgFolder), URL: sc.URL, Folder: sc.OrgFolder, Branch: attachBranch(sc)}
		// org_folder "." is a dedicated company store (F13), attached as
		// "<org>/."; skipped only when the entry above already attaches
		// that same root (one store folder keeps one name).
		if sc.OrgFolder == orgconf.OrgFolderRoot && slices.ContainsFunc(out, func(o attach.Store) bool { return sameStoreRoot(o, company) }) {
			break
		}
		out = append(out, company)
		break // one org folder per entry (F13)
	}
	return out, nil
}

// attachBranch is the attach branch of an entry store: "" for the default.
func attachBranch(sc *orgconf.Store) string {
	if b := sc.EffectiveBranch(); b != orgconf.DefaultStoreBranch {
		return b
	}
	return ""
}

// sameStoreRoot reports whether a and b both attach the root folder of the
// same store url on the same branch.
func sameStoreRoot(a, b attach.Store) bool {
	isRoot := func(f string) bool { f = strings.Trim(f, "/"); return f == "" || f == orgconf.OrgFolderRoot }
	return a.URL == b.URL && a.Branch == b.Branch && isRoot(a.Folder) && isRoot(b.Folder)
}

// OrgEntryStore is Options.OrgEntryStore: entry name's store at folder.
// With no folder it is this repo's link folder for that entry (its default
// link first), else the repo's default link folder; with neither it is
// v0.1.17's "name the folder" refusal. An unknown entry is not found.
func (r *OrgResolution) OrgEntryStore(ctx context.Context, name, folder string) (attach.Store, bool, error) {
	s, err := r.Situation(ctx)
	if err != nil {
		return attach.Store{}, false, err
	}
	org := s.Config.FindOrg(name)
	if org == nil || len(org.Stores) == 0 {
		return attach.Store{}, false, nil
	}
	sc := &org.Stores[0]
	if folder == "" {
		var link, fallback *string
		for i := range s.Result.Links {
			l := &s.Result.Links[i]
			switch {
			case l.Entry == name && (link == nil || l.Default):
				link = &l.Folder
				if st := org.StoreFor(l.StoreURL); st != nil {
					sc = st
				}
			case l.Default && fallback == nil:
				fallback = &l.Folder
			}
		}
		switch {
		case link != nil:
			folder = *link
		case fallback != nil:
			folder = *fallback
		default:
			return attach.Store{}, false, errs.New(errs.InvalidInput, "--from "+name+": name the folder (<org>/<folder>); this repo has no default link")
		}
	}
	return attach.Store{Name: store.Source(name), URL: sc.URL, Folder: folder, Branch: attachBranch(sc)}, true, nil
}

// OrgSigners is Options.OrgSigners: every entry with an identity and
// stores signs its stores' commits with that identity (Q-R2-1). The name
// is the identity's name, else the entry's; the email is the key's.
func (r *OrgResolution) OrgSigners(ctx context.Context) ([]OrgSigner, error) {
	cfg, _, err := orgcfg.LoadConfig(r.home)
	if err != nil || cfg == nil {
		return nil, err
	}
	var out []OrgSigner
	for i := range cfg.Orgs {
		org := &cfg.Orgs[i]
		if strings.TrimSpace(org.Identity.Key) == "" || len(org.Stores) == 0 {
			continue
		}
		name := org.Identity.Name
		if name == "" {
			name = org.Name
		}
		s := OrgSigner{Name: name, Email: emailOfKey(org.Identity.Key)}
		for _, st := range org.Stores {
			s.StoreURLs = append(s.StoreURLs, st.URL)
		}
		out = append(out, s)
	}
	return out, nil
}

// emailOfKey is the email of a `human:<email>` key, "" for another shape.
func emailOfKey(key string) string {
	_, rest, ok := strings.Cut(key, ":")
	if !ok || !strings.Contains(rest, "@") {
		return ""
	}
	return rest
}
