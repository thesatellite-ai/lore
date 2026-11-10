package wire

// setupdiscover.go — setup.discover's Prober side (opssetup.AccountDiscoverer,
// owner request 2026-11-06): gh's logins, each login's GitHub profile,
// emails, avatar and store-like repos read with THAT login's own token,
// git's global and includeIf identities, and the store urls lore already
// uses. The ssh alias/key half is setup.aliases (setupaliases.go), which
// the op calls itself.
//
//	DiscoverFacts ─▶ GHLogins (hosts.yml)
//	              ─▶ per login (‖): GHToken (gh auth token --user, memory only)
//	                                ─▶ ghapi User ─▶ Emails (when the scope allows) ‖ Repos ‖ Avatar
//	              ‖  GlobalIdentity, IncludeIdentities (git)
//	              ‖  config.yaml setups + orgs (store urls, setup names)
//	              ‖  known repos' stores.yaml; repos lore has not initialised
//	                 under the includeIf and setup folders (importscan.FolderStores)
//
// The token lives in one local variable of discoverAccount and is passed
// only to ghapi; Detail strings are fixed text, so no failure path can
// echo it (TestDiscoverNeverLeaksToken).

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/ghapi"
	"github.com/thesatellite-ai/lore/adapters/importscan"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

var _ opssetup.AccountDiscoverer = setupProber{}

// discoverTimeout bounds one account's API calls (each call also has
// ghapi.DefaultTimeout).
const discoverTimeout = 8 * time.Second

// maxStoreRepos caps the store-like repos offered per account.
const maxStoreRepos = 10

// Fixed details of an account that could not be read (never gh's or the
// API's own text).
const (
	detailAPIUnreachable = "the GitHub API could not be reached"
	detailAPIDenied      = "GitHub refused this login's token (expired or revoked): gh auth login again"
	detailAPIFailed      = "the GitHub API answer could not be read"
)

// ghAPIClient is the GitHub API client: api.github.com, or the test-only
// ghapi.EnvBaseURL (LORE_GITHUB_API) the setup page's headless checks
// point at a local fake.
func ghAPIClient() ghapi.Client {
	return ghapi.Client{BaseURL: os.Getenv(ghapi.EnvBaseURL), Observe: recordAPICall}
}

// recordAPICall puts one GitHub API request in the request's trace (the
// page's Details and debug log; nothing without a trace). The url has no
// token (ghapi sends it only as a header) and the token was registered
// with probe.Secret when gh printed it, so the trace redacts it anyway.
func recordAPICall(ctx context.Context, c ghapi.Call) {
	pc := probe.Call{Kind: probe.CallHTTP, Command: c.Method + " " + c.URL, TookMS: c.Took.Milliseconds(), Start: c.Start}
	if c.Status != 0 {
		pc.Output = "HTTP " + strconv.Itoa(c.Status)
		if c.Status/100 != 2 {
			pc.Exit = c.Status
		}
	}
	if c.Err != nil {
		pc.Error = c.Err.Error() // ghapi errors are fixed text
		if pc.Exit == 0 {
			pc.Exit = 1
		}
	}
	probe.Record(ctx, pc)
}

// DiscoverFacts implements opssetup.AccountDiscoverer.
func (sp setupProber) DiscoverFacts(ctx context.Context, in opssetup.DiscoverFactsIn) (opssetup.DiscoverFacts, error) {
	return discoverFacts(ctx, sp.p, ghAPIClient(), sp.a, in.Folders)
}

// discoverFacts is DiscoverFacts over an explicit client (tests pass a
// fake API) and an optional App for config.yaml (nil: no known stores).
// named are the folders the person asked to scan (--folder), walked
// before the includeIf and setup folders.
func discoverFacts(ctx context.Context, p *probe.Prober, api ghapi.Client, a *App, named []string) (opssetup.DiscoverFacts, error) {
	var (
		f  opssetup.DiscoverFacts
		mu sync.Mutex
		wg sync.WaitGroup
	)
	logins := p.GHLogins()
	f.GH = make([]opssetup.GHAccountFacts, len(logins.Accounts))
	n := len(logins.Accounts)
	ghDone := 0
	opssetup.ReportDiscoverStep(ctx, opssetup.DiscoverStep{ID: opssetup.StepGitHub, Label: "Checking GitHub accounts", Total: n, Finished: n == 0})
	for i, acct := range logins.Accounts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.GH[i] = discoverAccount(ctx, p, api, acct)
			mu.Lock()
			ghDone++
			opssetup.ReportDiscoverStep(ctx, opssetup.DiscoverStep{ID: opssetup.StepGitHub, Label: "Checking GitHub accounts", Done: ghDone, Total: n, Finished: ghDone == n})
			mu.Unlock()
		}()
	}
	var (
		known       []opssetup.KnownStore
		knownRoots  []string
		setupFolder []string
	)
	if a != nil {
		stores, names := knownStores(a.opts.Home)
		repo, roots := repoStores(ctx, a)
		known, knownRoots = append(stores, repo...), roots
		f.SetupNames = names
		f.SetupUsers = setupUsers(a.opts.Home)
		setupFolder = setupFolders(a.opts.Home)
	}
	var (
		scanned   []opssetup.KnownStore
		scanNotes []string
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		opssetup.ReportDiscoverStep(ctx, opssetup.DiscoverStep{ID: opssetup.StepGit, Label: "Reading git identities"})
		g := p.GlobalIdentity(ctx)
		incs := p.IncludeIdentities(ctx)
		opssetup.ReportDiscoverStep(ctx, opssetup.DiscoverStep{ID: opssetup.StepGit, Label: "Reading git identities", Finished: true})
		// The folders the gitconfig and the setups tie to an account are
		// where a teammate's repo gets cloned: their committed
		// .lore/stores.yaml suggest store urls before lore runs there.
		var inc []string
		for _, in := range incs {
			inc = append(inc, in.Folder)
		}
		if a != nil {
			h := userHome(SetupQuery{})
			scanNotes = namedFolderNotes(named, h)
			opssetup.ReportDiscoverStep(ctx, opssetup.DiscoverStep{ID: opssetup.StepFolders, Label: "Scanning folders for lore repos"})
			scanned = scannedStores(ctx, h, append(append(slices.Clone(named), setupFolder...), inc...), knownRoots)
			opssetup.ReportDiscoverStep(ctx, opssetup.DiscoverStep{ID: opssetup.StepFolders, Label: "Scanning folders for lore repos", Finished: true})
		}
		mu.Lock()
		defer mu.Unlock()
		f.GitGlobal = opssetup.GitIdentityFact{Name: g.Name, Email: g.Email}
		for _, in := range incs {
			f.GitIncludes = append(f.GitIncludes, opssetup.GitIncludeFact{Condition: in.Condition, Folder: in.Folder, Path: in.Path, Name: in.Name, Email: in.Email})
		}
	}()
	wg.Wait()
	if a != nil {
		f.KnownStores = append(known, scanned...)
	}
	f.Notes = append(f.Notes, scanNotes...)
	return f, nil
}

// namedFolderNotes are the Notes for --folder values the scan cannot use:
// a folder that is not there, and the ones past importscan.MaxStoreFolders
// (the scan walks the named folders first, then the includeIf and setup
// folders, up to that many in all).
func namedFolderNotes(named []string, home string) []string {
	var out []string
	for i, f := range named {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if i >= importscan.MaxStoreFolders {
			out = append(out, "folder "+f+" was not scanned: the store scan walks at most "+strconv.Itoa(importscan.MaxStoreFolders)+" folders")
			continue
		}
		if st, err := os.Stat(onboard.ExpandHome(strings.TrimSuffix(f, "**"), home)); err != nil || !st.IsDir() {
			out = append(out, "folder "+f+" was not found: nothing was scanned there")
		}
	}
	return out
}

// setupFolders is every folder the existing setups name (config.yaml
// setups[].folders), as written.
func setupFolders(home string) []string {
	cfg, err := ReadSetupsConfig(home)
	if err != nil {
		return nil
	}
	var out []string
	for _, s := range cfg.Setups {
		out = append(out, s.Folders...)
	}
	return out
}

// scannedStores walks folders (importscan.FolderStores: depth 4, at most
// 8 folders and 200 repos, read-only) for repos that are not in
// known_repos and lists the remote stores their .lore/stores.yaml
// attaches (issue 14). A gitdir folder pattern's trailing "**" is
// dropped; local (file:// or absolute) stores are skipped as in
// repoStores.
func scannedStores(ctx context.Context, home string, folders, known []string) []opssetup.KnownStore {
	var clean []string
	for _, f := range folders {
		f = strings.TrimSuffix(strings.TrimSpace(f), "**")
		if f != "" && !slices.Contains(clean, f) {
			clean = append(clean, f)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	res := importscan.FolderStores(ctx, importscan.StoreScan{Folders: clean, Home: home, Known: known})
	var out []opssetup.KnownStore
	for _, r := range res.Repos {
		for _, st := range r.Stores {
			if localStoreURL(st.URL) {
				continue
			}
			out = append(out, opssetup.KnownStore{URL: st.URL, Kind: opssetup.SrcRepoScan, Detail: tildeHome(r.Root, home) + " (" + st.Name + ")"})
		}
	}
	return out
}

// localStoreURL reports a file:// or absolute-path store (never a
// remote suggestion).
func localStoreURL(u string) bool {
	return u == "" || strings.HasPrefix(u, "file://") || strings.HasPrefix(u, "/")
}

// discoverAccount reads one gh login through the API with its own token.
func discoverAccount(ctx context.Context, p *probe.Prober, api ghapi.Client, acct probe.GHAccount) opssetup.GHAccountFacts {
	out := opssetup.GHAccountFacts{Login: acct.User, Active: acct.Active, EmailScope: opssetup.EmailScopeUnknown}
	tok, o := p.GHToken(ctx, acct.User)
	if o.Class != probe.ClassOK {
		out.Status, out.Detail = string(o.Class), o.Detail
		return out
	}
	cctx, cancel := context.WithTimeout(ctx, discoverTimeout)
	defer cancel()
	u, err := api.User(cctx, tok)
	if err != nil {
		out.Status, out.Detail = apiFailure(err)
		return out
	}
	out.Status = string(probe.ClassOK)
	out.ID, out.Name, out.Public = u.User.ID, u.User.Name, u.User.Email
	var wg sync.WaitGroup
	if u.CanReadEmails() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			es, eerr := api.Emails(cctx, tok)
			switch {
			case eerr == nil:
				out.EmailScope = opssetup.EmailScopeOK
				for _, e := range es {
					out.Emails = append(out.Emails, opssetup.GHEmail{Email: e.Email, Primary: e.Primary, Verified: e.Verified})
				}
				slices.SortStableFunc(out.Emails, func(x, y opssetup.GHEmail) int { return boolRank(y.Primary) - boolRank(x.Primary) })
			case ghapi.StatusOf(eerr) == 403 || ghapi.StatusOf(eerr) == 404:
				out.EmailScope = opssetup.EmailScopeMissing
			}
		}()
	} else {
		out.EmailScope = opssetup.EmailScopeMissing
	}
	var repos []opssetup.GHRepo
	wg.Add(1)
	go func() {
		defer wg.Done()
		rs, rerr := api.Repos(cctx, tok)
		if rerr != nil {
			return
		}
		repos = storeRepos(rs)
	}()
	var avatar string
	if u.User.AvatarURL != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			avatar, _ = api.Avatar(cctx, u.User.AvatarURL)
		}()
	}
	wg.Wait()
	out.Repos, out.Avatar = repos, avatar
	return out
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// apiFailure maps a ghapi error to a class and fixed detail.
func apiFailure(err error) (string, string) {
	switch s := ghapi.StatusOf(err); {
	case s == 401 || s == 403:
		return string(probe.ClassAuthDenied), detailAPIDenied
	case s == 0 && errors.Is(err, ghapi.ErrUnreachable):
		return string(probe.ClassUnreachable), detailAPIUnreachable
	}
	return string(probe.ClassFailed), detailAPIFailed
}

// setupUsers maps each GitHub login an existing setup works as (lower
// case) to that setup's name; a config that does not read maps nothing.
func setupUsers(home string) map[string]string {
	out := map[string]string{}
	cfg, err := ReadSetupsConfig(home)
	if err != nil {
		return out
	}
	for _, s := range cfg.Setups {
		if u := strings.ToLower(strings.TrimSpace(s.GitHub.User)); u != "" {
			if _, seen := out[u]; !seen {
				out[u] = s.Name
			}
		}
	}
	// A solo setup with no GitHub account yet (an import: identity and
	// store only) stands for the login its identity handle names, so Home
	// does not also suggest "create personal-2" for it (polish item 3); a
	// setup that names its account wins.
	for _, s := range cfg.Setups {
		h := strings.ToLower(strings.TrimSpace(s.Identity.Handle))
		if s.Kind != onboard.KindSolo || strings.TrimSpace(s.GitHub.User) != "" || h == "" {
			continue
		}
		if _, seen := out[h]; !seen {
			out[h] = s.Name
		}
	}
	return out
}

// knownStores lists the store urls config.yaml already names (setups'
// personal stores, the personal store, org stores) and the setup names.
// A config that does not read is no stores (setup.list reports it).
func knownStores(home string) ([]opssetup.KnownStore, []string) {
	var (
		out   []opssetup.KnownStore
		names []string
	)
	add := func(url string, kind opssetup.SourceKind, detail string) {
		url = strings.TrimSpace(url)
		if url == "" || url == onboard.PersonalStoreInherit || url == onboard.PersonalStoreNone {
			return
		}
		out = append(out, opssetup.KnownStore{URL: url, Kind: kind, Detail: detail})
	}
	if cfg, err := ReadSetupsConfig(home); err == nil {
		for _, s := range cfg.Setups {
			names = append(names, s.Name)
			add(s.PersonalStore, opssetup.SrcLoreSetup, s.Name)
		}
	}
	if oc, _, err := orgcfg.LoadConfig(home); err == nil && oc != nil {
		add(oc.Personal.Store, opssetup.SrcLoreSetup, "personal store")
		for _, o := range oc.Orgs {
			for _, st := range o.Stores {
				add(st.URL, opssetup.SrcLoreOrg, o.Name)
			}
		}
	}
	return out, names
}

// storeRepos keeps an account's store-like repos (opssetup.StoreLikeRepo),
// best named first (lore, notes, store, *-lore before a name that only
// contains a store word), then GitHub's most-recently-updated order, at
// most maxStoreRepos.
func storeRepos(rs []ghapi.Repo) []opssetup.GHRepo {
	type scored struct {
		r     ghapi.Repo
		score int
	}
	var keep []scored
	for _, r := range rs {
		if sc, ok := opssetup.StoreLikeRepo(r.FullName); ok && r.SSHURL != "" {
			keep = append(keep, scored{r, sc})
		}
	}
	slices.SortStableFunc(keep, func(x, y scored) int { return y.score - x.score })
	out := make([]opssetup.GHRepo, 0, min(len(keep), maxStoreRepos))
	for _, k := range keep[:min(len(keep), maxStoreRepos)] {
		out = append(out, opssetup.GHRepo{FullName: k.r.FullName, SSHURL: k.r.SSHURL, Private: k.r.Private})
	}
	return out
}

// repoStores lists the stores the repos lore knows on this machine
// attach (known_repos rows → <root>/.lore/stores.yaml), read-only. A repo
// whose file is gone or does not parse adds nothing; a machine store that
// does not open is no repos (setup.list and doctor report it). Local
// (file://) stores are skipped: a store url suggestion is a remote.
func repoStores(ctx context.Context, a *App) ([]opssetup.KnownStore, []string) {
	repos, err := a.KnownRepos(ctx)
	if err != nil {
		return nil, nil
	}
	userHome, _ := os.UserHomeDir() // "" shows absolute roots
	var (
		out   []opssetup.KnownStore
		roots []string
	)
	for _, r := range repos {
		roots = append(roots, r.Root)
		cfg, err := readStoresFile(r.Root)
		if err != nil {
			continue
		}
		for _, st := range cfg.Stores {
			if localStoreURL(st.URL) {
				continue
			}
			out = append(out, opssetup.KnownStore{URL: st.URL, Kind: opssetup.SrcLoreRepo, Detail: tildeHome(r.Root, userHome) + " (" + string(st.Name) + ")"})
		}
	}
	return out, roots
}

// tildeHome shows p under the user's home as ~/… (the page shows paths
// the way the person types them; home is os.UserHomeDir, not lore's ~/.lore).
func tildeHome(p, home string) string {
	if home != "" {
		if rest, ok := strings.CutPrefix(p, strings.TrimRight(home, "/")+"/"); ok {
			return "~/" + rest
		}
	}
	return p
}
