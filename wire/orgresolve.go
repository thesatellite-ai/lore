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
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// OrgResolution answers the org hooks of one process from the machine's
// config. Build it with NewOrgResolution; the zero value is not usable.
type OrgResolution struct {
	home  string
	dir   string
	links orgcfg.LinkSource

	// Interactive reports whether a person can answer (stdin is a
	// terminal); nil = no. F14 refuses a rule or decision written into a
	// client repo without a terminal unless --to names the target.
	Interactive func() bool
	// IsAgent reports whether the caller is an agent (F14: an agent must
	// name the target of a rule or decision in a client repo); nil = no.
	IsAgent func(ctx context.Context) bool
	// RepoWriteCheck is the F16 write-time leak check of a row about to be
	// written into a client repo (root, the row kind, its text); nil skips
	// it (the commit hooks still check). The old binary passes its
	// v0.1.17 check (P3-ORG-3).
	RepoWriteCheck func(ctx context.Context, root, kind, text string) error
	// Now is the clock of live decisions (a store clone's live.fetch_ttl,
	// storePush); nil = time.Now. cmd/lore passes its live clock, so a
	// test build's LORE_NOW decides the fetch as it stamps the rows (the
	// old binary's liveClock).
	Now func() time.Time

	// fetchAlways: this process fetches the store clones whatever the
	// fetch stamp says (FetchLinksAlways).
	fetchAlways bool

	// routed is the F14 rule of the routed create this process made, for
	// the "filed: <rule>" line (Routed).
	routed string

	once sync.Once
	sit  *orgcfg.Situation
	err  error
}

// now is r.Now, or the wall clock.
func (r *OrgResolution) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// NewOrgResolution resolves the org of the project at dir from the config
// under home (~/.lore). links reads the org stores' links (F11); nil reads
// none, which fails closed for any org with stores.
func NewOrgResolution(home, dir string, links orgcfg.LinkSource) *OrgResolution {
	return &OrgResolution{home: home, dir: dir, links: links}
}

// FetchLinksAlways makes the link read of this process fetch every org
// store now, ignoring live.fetch_ttl: `lore sync` refreshes the link index
// (F11, v0.1.17 refreshLinkIndex). The attached store clones fetch now too
// (storePush, v0.1.17 fetchKind: FetchAlways for sync). Call it before the
// first Situation.
func (r *OrgResolution) FetchLinksAlways() {
	r.fetchAlways = true
	if g, ok := r.links.(*orgcfg.GitLinks); ok {
		g.Always = true
	}
}

// Situation is the gathered inputs and F10's decision, computed once (it
// may fetch the org stores).
func (r *OrgResolution) Situation(ctx context.Context) (*orgcfg.Situation, error) {
	r.once.Do(func() {
		r.sit, r.err = orgcfg.Gather(ctx, orgcfg.In{Home: r.home, Dir: r.dir, EnvOrg: os.Getenv(orgcfg.EnvOrg), Links: r.links})
		r.err = redactQuietErr(r.home, r.sit, r.err)
	})
	return r.sit, r.err
}

// redactQuietErr hides every configured org store url in err when output
// must name nothing (no terminal, CI; quietOutput): a gather error such as
// E_NOT_A_STORE names the store url, and every command of a client repo
// prints it, so a client's CI log or an agent's transcript would carry the
// agency's internal store url (v0.1.17 redactIfQuiet in stores_wiring.go,
// B-FLOW4-4). The code, hint and the rest of the text stay; a non-lore
// error or an unreadable config is returned as it is.
func redactQuietErr(home string, sit *orgcfg.Situation, err error) error {
	if err == nil || !quietOutput() {
		return err
	}
	var e *errs.Error
	if !errors.As(err, &e) {
		return err
	}
	if sit == nil {
		cfg, _, cerr := orgcfg.LoadConfig(home)
		if cerr != nil || cfg == nil {
			return err
		}
		sit = &orgcfg.Situation{Config: cfg}
	}
	red := newWhoamiRedactor(sit, false)
	c := *e
	c.Message, c.Hint = red.str(c.Message), red.str(c.Hint)
	if c.Cause != nil {
		c.Cause = errors.New(red.str(c.Cause.Error()))
	}
	return &c
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
	if o.DefaultTarget == nil {
		o.DefaultTarget = r.DefaultTarget
	}
	if o.GuardRepoWrite == nil {
		o.GuardRepoWrite = r.GuardRepoWrite
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
	if s.Result.Disagreement != nil {
		// F10: LORE_ORG / org use contradicting a link. Nothing of the
		// chosen entry is attached, so opening never reaches a store of
		// the wrong org; reads keep the repo and personal rows, and every
		// org-scoped write is refused by decide (E_ORG_DISAGREES), as
		// v0.1.17 refused before touching any store (P3-CMDLORE-10).
		return nil, nil
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
		var entryFolders []string
		entryDefault := false
		for i := range s.Result.Links {
			l := &s.Result.Links[i]
			if l.Entry == name {
				entryFolders = append(entryFolders, l.Folder)
				entryDefault = entryDefault || l.Default
			}
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
		if len(entryFolders) > 1 && !entryDefault {
			// TEAM-032 / side effect 4: several links, none the default:
			// refuse rather than pick one (P3-CMDLORE-10).
			return attach.Store{}, false, errs.Wrap(errs.InvalidInput, fmt.Sprintf("%v: candidates %s; pass --to %s/<folder> or mark one with `lore org link %s --project <folder> --default`",
				orgresolve.ErrTwoLinksNoDefault, strings.Join(entryFolders, ", "), name, name), orgresolve.ErrTwoLinksNoDefault)
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

// routeKinds maps a core table to the kind name F14's routing rules
// (defaults.client_repos, routing.client_repos) use for it.
var routeKinds = map[row.Table]string{
	schema.TableMemories:  "memory",
	schema.TableRules:     "rule",
	schema.TableDecisions: "decision",
	schema.TableComments:  "comment",
	schema.TableTasks:     "task",
	schema.TableMissions:  "mission",
}

// Routed is the F14 rule that chose the target of this process's routed
// create ("" when none did), for v0.1.17's "  filed: <rule>" line.
func (r *OrgResolution) Routed() string { return r.routed }

// DefaultTarget is Options.DefaultTarget: a create of a routed kind that
// names no --to goes where F14 sends it. In a client repo that is the
// linked folder of the org store (attached under the org's name by
// OrgStores) or the folder a client-repo default names; in the org's own
// repo, or with no org, the repo store (""), where GuardRepoWrite still
// runs. A folder this process has not attached answers "" too, so the
// guard refuses it naming the --to to use (fail closed).
func (r *OrgResolution) DefaultTarget(ctx context.Context, q WriteQuery) (store.Source, error) {
	kind, ok := routeKinds[q.Table]
	if !ok {
		return "", nil
	}
	s, dec, err := r.decide(ctx, false, kind, q)
	if err != nil {
		return "", err
	}
	if dec.Repo || s.Result.Org == nil {
		return "", nil
	}
	attached, err := r.OrgStores(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range attached {
		if a.Folder == dec.Folder {
			r.routed = dec.Rule
			return a.Name, nil
		}
	}
	return "", nil
}

// GuardRepoWrite is Options.GuardRepoWrite: a create of a routed kind that
// lands in the repo store is refused when F14 sends it elsewhere (a
// client repo: internal knowledge never lands in it, D8) or when the
// links cannot be read (E_LINK_UNKNOWN); a write F14 keeps here gets the
// F16 leak check (RepoWriteCheck). explicit is `--to repo`.
func (r *OrgResolution) GuardRepoWrite(ctx context.Context, q WriteQuery, explicit bool) error {
	kind, ok := routeKinds[q.Table]
	if !ok {
		return nil
	}
	s, dec, err := r.decide(ctx, explicit, kind, q)
	if err != nil {
		return err
	}
	if !dec.Repo {
		hint := "write it to a store listed in .lore/stores.yaml with --to <name>, or choose this repo with --to repo"
		if org := s.Result.Org; org != nil {
			hint = "write it to " + org.Name + "'s store with --to " + org.Name + "/" + dec.Folder + " (the folder " + dec.Folder + "/), or choose this repo with --to repo"
		}
		return errs.New(errs.Unsupported, "this repository's lore routing sends a "+kind+" to a store folder lore has not attached ("+dec.Rule+"); name the target with --to").
			WithHint(hint)
	}
	needs := s.Result.Role == orgresolve.RoleClient || s.Result.LinkUnknown
	if r.RepoWriteCheck == nil || !needs || strings.TrimSpace(q.Text) == "" {
		return nil
	}
	return r.RepoWriteCheck(ctx, s.Dir, kind, q.Text)
}

// decide is F14 for one create of kind: the org situation, --to repo when
// explicit, the team's client-repo defaults (lore-org.yaml until migrated)
// joined with the routing.client_repos policy (q.Routes; per kind a store
// target beats repo, CFG8) and the entry's personal defaults.
func (r *OrgResolution) decide(ctx context.Context, explicit bool, kind string, q WriteQuery) (*orgcfg.Situation, orgresolve.TargetDecision, error) {
	none := orgresolve.TargetDecision{}
	s, err := r.Situation(ctx)
	if err != nil {
		return nil, none, err
	}
	if s.ResolveErr != nil {
		return nil, none, orgcfg.ResolveError(s.ResolveErr)
	}
	if cerr := s.Result.CheckWrite(false, explicit); cerr != nil {
		return nil, none, orgcfg.ResolveError(cerr)
	}
	res := s.Result
	in := orgresolve.TargetInputs{Org: res.OrgName(), Kind: kind, Role: res.Role, LinkUnknown: res.LinkUnknown}
	if r.Interactive != nil {
		in.TTY = r.Interactive()
	}
	if r.IsAgent != nil {
		in.Agent = r.IsAgent(ctx)
	}
	if explicit {
		in.To = orgresolve.ToRepo
	}
	links := res.Links
	if res.Org != nil && len(links) == 0 && s.ProjectID != "" && r.links != nil {
		// A staging entry chosen on purpose has its own links on its own
		// store branch (F10 skips them only for automatic matching).
		el, ok, lerr := r.links.EntryLinks(ctx, res.Org, s.ProjectID, s.Remotes)
		if lerr != nil {
			return nil, none, lerr
		}
		if ok {
			links = el
		}
	}
	for _, l := range links {
		in.Links = append(in.Links, orgresolve.TargetLink{Folder: l.Folder, Default: l.Default, Own: l.Own})
	}
	if s.Committed != nil {
		in.CommittedProject = s.Committed.Project
	}
	if res.Org != nil {
		in.PersonalDefaults = res.Org.Defaults.ClientRepos
		if r.links != nil {
			d, derr := r.links.ClientRepoDefaults(ctx, res.Org)
			if derr != nil {
				return nil, none, errs.Wrap(errs.StoreUnreachable, "read "+res.Org.Name+"'s lore-org.yaml", derr)
			}
			in.StoreDefaults = d
		}
	}
	in.StoreDefaults = joinRoutes(in.StoreDefaults, q.Routes)
	dec, err := orgresolve.ResolveTarget(in)
	if err != nil {
		return nil, none, targetError(err)
	}
	return s, dec, nil
}

// joinRoutes joins the routing.client_repos policy into the team defaults:
// per kind a store target beats repo (CFG8).
func joinRoutes(team, routes map[string]string) map[string]string {
	if len(routes) == 0 {
		return team
	}
	out := make(map[string]string, len(team)+len(routes))
	for k, v := range team {
		out[k] = v
	}
	for k, v := range routes {
		if cur, seen := out[k]; !seen || (cur == orgresolve.DefaultRepo && v != orgresolve.DefaultRepo) {
			out[k] = v
		}
	}
	return out
}

// targetError maps F14 refusals to codes, as v0.1.17's targetCLIError.
func targetError(err error) error {
	switch {
	case errors.Is(err, orgresolve.ErrLinkUnknown):
		return errs.New(errs.LinkUnknown, err.Error()+"; nothing written").
			WithHint("pass --to (repo, <org> or <org>/<folder>) or retry when the store is reachable")
	case errors.Is(err, orgresolve.ErrNeedsExplicitTo), errors.Is(err, orgresolve.ErrTwoLinksNoDefault), errors.Is(err, orgresolve.ErrNoLinkedProject):
		return errs.New(errs.InvalidInput, err.Error())
	}
	return errs.Wrap(errs.Internal, "decide the write target", err)
}
