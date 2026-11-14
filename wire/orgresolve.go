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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/logic/repourl"
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

	// waited names the store clones whose waiting commits this process
	// already reported (pushreport.go, B2-5): a store and its folders share
	// one clone, so its commits are named once, not once per folder.
	waited waitedClones
	// heldSaid names the store clones whose held push (store.SyncReport.Held,
	// F-9) this process already said, once per clone like waited.
	heldSaid waitedClones

	// routed is the F14 rule of the routed create this process made, for
	// the "filed: <rule>" line (Routed).
	routed string

	// mu guards the gathered answer below. A plain mutex, not a sync.Once:
	// a following resolution (FollowConfig) gathers again after the config
	// changed, and a Once cannot be re-armed safely.
	mu   sync.Mutex
	done bool
	sit  *orgcfg.Situation
	err  error
	// follow: gather again when config.yaml changed since the last gather
	// (FollowConfig); stamp is that file's size and time at that gather.
	follow bool
	stamp  string
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
	r := &OrgResolution{home: home, dir: dir, links: links}
	if g, ok := links.(*orgcfg.GitLinks); ok && g.Now == nil {
		// the clone fetch stamps (`lore store show` last fetch) follow the
		// same clock as the resolution's live decisions: r.Now, set by
		// cmd/lore after this (LORE_NOW in a test build), read at each call
		g.Now = r.now
	}
	return r
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

// FollowConfig makes this resolution gather again whenever
// ~/.lore/config.yaml changed since its last gather, re-reading the org
// entries the store links come from. A server that outlives the config
// it started with calls it: the setup page saves a whole setup, its team
// step creates the org, and the next call on that same page must resolve
// that org and its person, not the empty answer read before the setup
// (devco J-16: team.invite refused "this repo has no org" until the page
// was restarted). A CLI command keeps its one answer for its whole run.
func (r *OrgResolution) FollowConfig() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.follow = true
}

// Situation is the gathered inputs and F10's decision, computed once (it
// may fetch the org stores); with FollowConfig, again after config.yaml
// changed.
func (r *OrgResolution) Situation(ctx context.Context) (*orgcfg.Situation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stamp := ""
	if r.follow {
		stamp = configStamp(r.home)
		if r.done && stamp != r.stamp {
			r.done = false
			r.reloadLinkConfig()
		}
	}
	if !r.done {
		// the stamp is taken before the gather: a write made during it is
		// seen as a change on the next call
		r.sit, r.err = r.gatherWithSetup(ctx)
		r.err = maskOrgErr(r.err)
		r.done, r.stamp = true, stamp
	}
	return r.sit, r.err
}

// configStamp is config.yaml's size and modification time under home
// ("-" when it does not exist): a stat, no read.
func configStamp(home string) string {
	st, err := os.Stat(filepath.Join(home, ConfigFile))
	if err != nil {
		return "-"
	}
	return strconv.FormatInt(st.Size(), 10) + " " + strconv.FormatInt(st.ModTime().UnixNano(), 10)
}

// reloadLinkConfig gives the store link reader the config as it is now
// (StandaloneOrgResolution loaded it once at start). A config that does
// not read gives it none, as at start; Gather then reports the error.
func (r *OrgResolution) reloadLinkConfig() {
	g, ok := r.links.(*orgcfg.GitLinks)
	if !ok {
		return
	}
	cfg, _, err := orgcfg.LoadConfig(r.home)
	if err != nil {
		cfg = nil
	}
	g.Config = cfg
}

// maskOrgErr masks only the credentials in a lore error's message, hint
// and cause (repourl.MaskText). A gather error such as E_NOT_A_STORE names
// the store url, and that url is printed as it is, in a pipe and in CI
// too: a store's plain address is no secret, and the person needs it to
// act on the error. A non-lore error is returned as it is.
func maskOrgErr(err error) error {
	var e *errs.Error
	if err == nil || !errors.As(err, &e) {
		return err
	}
	c := *e
	c.Message, c.Hint = repourl.MaskText(c.Message), repourl.MaskText(c.Hint)
	if c.Cause != nil {
		if m := repourl.MaskText(c.Cause.Error()); m != c.Cause.Error() {
			c.Cause = errors.New(m)
		}
	}
	if c.Message == e.Message && c.Hint == e.Hint && c.Cause == e.Cause {
		return err
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
	if o.RepoLinkedByRemote == nil {
		o.RepoLinkedByRemote = r.LinkedByRemote
	}
	if o.ClientRepo == nil {
		o.ClientRepo = r.ClientRepo
	}
	if o.OrgEntryStore == nil {
		o.OrgEntryStore = r.OrgEntryStore
	}
	if o.OrgSigners == nil {
		o.OrgSigners = r.OrgSigners
	}
	if o.OrgStoreURLs == nil {
		o.OrgStoreURLs = r.OrgStoreURLs
	}
	if o.DefaultTarget == nil {
		o.DefaultTarget = r.DefaultTarget
	}
	if o.GuardRepoWrite == nil {
		o.GuardRepoWrite = r.GuardRepoWrite
	}
}

// ClientRepo is Options.ClientRepo: the org this checkout is a client repo
// of by a match (a client link, or the org's repos: pattern), and its
// linked folder (the default link, else the first client link). Zero for a
// plain folder, a repo with no hosted remote, a repo that is client only
// by the fail-closed default (nothing lists it as own), an own repo, no
// org, or an org that cannot be resolved.
func (r *OrgResolution) ClientRepo(ctx context.Context) ClientRepo {
	s, err := r.Situation(ctx)
	if err != nil || s == nil || s.ResolveErr != nil || s.NotGitRepo || len(s.Remotes) == 0 {
		return ClientRepo{}
	}
	res := s.Result
	if res.Role != orgresolve.RoleClient || !orgresolve.ClientByMatch(res.Org, res.Links, s.Remotes) {
		return ClientRepo{} // not a client by default alone (an own repo never listed under own:)
	}
	out := ClientRepo{Org: res.Org.Name}
	for _, l := range res.Links {
		if l.Own {
			continue
		}
		if out.Folder == "" || l.Default {
			out.Folder = l.Folder
		}
	}
	return out
}

// LinkedByRemote is Options.RepoLinkedByRemote: the remote a store link
// names this repo by when it holds no project id of its own (orgcfg
// Situation.LinkedByRemote); "" when it has one, or nothing links it.
func (r *OrgResolution) LinkedByRemote(ctx context.Context) string {
	s, err := r.Situation(ctx)
	if err != nil || s == nil {
		return ""
	}
	return s.LinkedByRemote
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

// linkUnknownHint is the hint of E_LINK_UNKNOWN (B1-22): the store named
// in the message was never fetched (or not reachable), and `lore sync`
// fetches every store of the org.
const linkUnknownHint = "run `lore sync` to fetch the org's stores, then retry; `lore whoami` shows the link status"

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
	links := s.Result.Links
	if s.Result.LinkUnknown {
		// W-15: offline with a copy older than the link index max age,
		// the folders the last fetched links name stay attached, so the
		// rows fetched then still read; the role still fails closed
		// (Result.LinkUnknown refuses an org write that names no store)
		if !slices.ContainsFunc(s.OldLinks, func(l orgresolve.Link) bool { return l.Entry == org.Name }) {
			return nil, errs.New(errs.LinkUnknown, "the store links of this repo cannot be read: "+s.Result.LinkDetail).
				WithHint(linkUnknownHint).WithNext("lore", "sync")
		}
		links = s.OldLinks
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
	for i := range links {
		l := &links[i]
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
	case s.Result.MovedLink != nil && s.Result.MovedLink.Entry == org.Name && org.StoreFor(s.Result.MovedLink.StoreURL) != nil:
		// The client repo moved: its old link's folder is attached so its
		// notes still read and the leak check still indexes them; writes
		// stay refused until it is linked here (no entry in Result.Links).
		ml := s.Result.MovedLink
		sc := org.StoreFor(ml.StoreURL)
		out = append(out, attach.Store{Name: store.Source(org.Name), URL: sc.URL, Folder: ml.Folder, Branch: attachBranch(sc)})
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
			return attach.Store{}, false, errs.Wrap(errs.InvalidInput, fmt.Sprintf("%v: candidates %s; pass --to %s/<folder> or mark one with `lore org link --org %s --project <folder> --default`",
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

// OrgStoreURLs is Options.OrgStoreURLs: every configured entry's store
// urls, whether or not it has an identity (INTEG-CFG7-1).
func (r *OrgResolution) OrgStoreURLs(ctx context.Context) ([]OrgStoreURL, error) {
	cfg, _, err := orgcfg.LoadConfig(r.home)
	if err != nil || cfg == nil {
		return nil, err
	}
	var out []OrgStoreURL
	for _, org := range cfg.Orgs {
		for _, st := range org.Stores {
			out = append(out, OrgStoreURL{Org: org.Name, URL: st.URL})
		}
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
	// D-12: every other kind of note a person writes follows the same
	// rule (docs: "tasks, memories and other kinds: the linked project");
	// left out, `lore plan add` in a client repo wrote internal planning
	// text into the client's .lore/data, or was refused there.
	schema.TablePlans:             "plan",
	schema.TableTasklists:         "tasklist",
	schema.TablePatterns:          "pattern",
	schema.TablePlaybooks:         "playbook",
	schema.TablePrompts:           "prompt",
	schema.TableHotfixes:          "hotfix",
	schema.TableReminders:         "reminder",
	schema.TableIncidents:         "incident",
	schema.TableArchitectureNotes: "architecture_note",
	schema.TableCookbookRecipes:   "cookbook_recipe",
	schema.TableHandoffs:          "handoff",
	schema.TableTechDocs:          "tech_doc",
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
	if s.Result.Org == nil {
		return "", nil
	}
	if dec.Repo {
		// H-2: a repo linked by its remote holds no lore files, and its repo
		// store is a read-only cache under ~/.lore: the write goes to the
		// folder the link names, as a person would expect from "linked".
		if s.LinkedByRemote == "" {
			return "", nil
		}
		return r.linkedFolderTarget(ctx, s)
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

// linkedFolderTarget is the attached store of the folder a link by remote
// names (OrgStores attaches it under the entry's name); "" when it is not
// attached, and then the write fails on the read-only repo cache as before.
func (r *OrgResolution) linkedFolderTarget(ctx context.Context, s *orgcfg.Situation) (store.Source, error) {
	attached, err := r.OrgStores(ctx)
	if err != nil {
		return "", err
	}
	linked := map[string]bool{} // the folders the link names
	for _, l := range s.Result.Links {
		if l.Entry == s.Result.Org.Name {
			linked[l.Folder] = true
		}
	}
	for _, a := range attached {
		// the org store attached at the linked folder; one attached at its
		// root (the link's store is not the entry's) is not that folder
		if a.Name == store.Source(s.Result.Org.Name) && linked[a.Folder] {
			r.routed = "this repo holds no lore files; its store link by remote (" + s.LinkedByRemote + ") names " + string(a.Name) + "/" + a.Folder
			return a.Name, nil
		}
	}
	// L-4: the linked folder is not attached (its store is not one of the
	// org entry's stores here): the read-only repo cache would refuse the
	// write with E_STORE_READ_ONLY, naming nothing to do. Refuse here with
	// the exact --to that reaches the linked folder.
	for _, l := range s.Result.Links {
		if l.Entry == s.Result.Org.Name && l.Folder != "" {
			to := s.Result.Org.Name + "/" + l.Folder
			return "", errs.New(errs.ReadOnly, "this repo holds no lore files and its store link by remote ("+s.LinkedByRemote+") names folder "+l.Folder+" of "+l.StoreURL+", which is not attached here, so a write has nowhere to go").
				WithHint("name the folder: --to " + to + " (or add that store to the org entry in ~/.lore/config.yaml so writes go there by themselves)")
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
	if r.IsAgent != nil && res.Role == orgresolve.RoleClient && !explicit {
		in.Agent = r.IsAgent(ctx) // read only where F14 uses it (identity resolution walks processes)
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
