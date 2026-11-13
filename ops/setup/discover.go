package setup

// discover.go — setup.discover (owner request 2026-11-06: "a system to
// figure out all automatically so the user can choose immediately, and
// also fetch the email if possible"). One read-only call the setup page
// starts as soon as it opens; it answers account cards (one per gh login:
// name, emails, avatar, the alias and key that log in as it, the folders
// its gitconfig includeIf names) and, per field kind, every candidate
// value with where it came from and a score, so each input can be a
// combobox with the best value preselected.
//
//	page ─▶ setup.discover ─┬▶ Prober (AccountDiscoverer): gh hosts.yml, gh auth token (memory) ─▶ GitHub API
//	                        │                              git global + includeIf, known lore stores
//	                        └▶ Prober (GitHubLister, setup.aliases): ssh aliases + every key alone (ssh -T)
//	                        ─▶ MergeDiscovery (pure, discover_merge.go) ─▶ DiscoverOut
//
// Nothing is written. The gh token never reaches this layer: the port
// answers facts only (wire/setupdiscover.go reads the token and calls the
// API). Both halves run in parallel; either may fail alone and the other
// still answers (Notes say what is missing).

import (
	"context"
	"sync"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// OpDiscover is the op name (page allowlist).
const OpDiscover = "setup.discover"

// DiscoverIn is setup.discover's input.
type DiscoverIn struct {
	Fresh bool `json:"fresh,omitempty" flag:"fresh" desc:"probe ssh again instead of the alias table kept for a minute"`
	// Folders are walked first for repos lore has not initialised (their
	// .lore/stores.yaml suggest store urls), before the includeIf and
	// setup folders; the scan's limits hold (at most 8 folders in all,
	// depth 4, 200 repos) and nothing is written.
	Folders []string `json:"folders,omitempty" flag:"folder" desc:"also walk these folders for repos whose .lore/stores.yaml suggests store urls (repeat)"`
	// Async answers at once: the kept answer while it is fresh, else the
	// probes start in a background job and the answer carries its
	// progress (Status); the page polls until Status.State is done
	// (discover_job.go). Folders are ignored with it.
	Async bool `json:"async,omitempty" flag:"async" desc:"answer at once with the kept answer or the progress of a background discovery (poll until status.state is done)"`
}

// DiscoverFactsIn is what the person asked the facts half to read beyond
// its own sources (DiscoverIn.Folders).
type DiscoverFactsIn struct {
	Folders []string
}

// SourceKind is where a discovered value came from (closed set; the page
// maps each to a short label and a popover).
type SourceKind string

// Source kinds.
const (
	SrcGHLogin       SourceKind = "gh_login"       // a login in gh's hosts.yml
	SrcGHActive      SourceKind = "gh_active"      // gh's active login
	SrcGitHubProfile SourceKind = "github_profile" // GitHub API /user (name)
	SrcGitHubEmail   SourceKind = "github_email"   // GitHub API /user/emails
	SrcGitHubPublic  SourceKind = "github_public"  // the profile's public email
	SrcGitHubNoreply SourceKind = "github_noreply" // <id>+<login>@users.noreply.github.com
	SrcGitGlobal     SourceKind = "git_global"     // git config --global user.*
	SrcGitInclude    SourceKind = "git_include"    // an includeIf file's user.*
	SrcSSHAlias      SourceKind = "ssh_alias"      // a ~/.ssh/config Host and the user it logs in as
	SrcSSHKey        SourceKind = "ssh_key"        // a key file tested alone
	SrcLoreSetup     SourceKind = "lore_setup"     // a setup in ~/.lore/config.yaml
	SrcLoreOrg       SourceKind = "lore_org"       // an org store in ~/.lore/config.yaml
	SrcLoreRepo      SourceKind = "lore_repo"      // a store a known repo's .lore/stores.yaml attaches
	SrcRepoScan      SourceKind = "repo_scan"      // a store the .lore/stores.yaml of a repo lore has not initialised attaches (found under an includeIf or setup folder)
	SrcGitHubRepo    SourceKind = "github_repo"    // a repo the account can see (store-like name)
)

// SourceKinds lists every source kind.
func SourceKinds() []SourceKind {
	return []SourceKind{SrcGHLogin, SrcGHActive, SrcGitHubProfile, SrcGitHubEmail, SrcGitHubPublic, SrcGitHubNoreply,
		SrcGitGlobal, SrcGitInclude, SrcSSHAlias, SrcSSHKey, SrcLoreSetup, SrcLoreOrg, SrcLoreRepo, SrcRepoScan, SrcGitHubRepo}
}

// EnumValues lists every source kind (op.Enumerated). internal/layers pins
// it to the constants.
func (SourceKind) EnumValues() []string {
	out := make([]string, 0, len(SourceKinds()))
	for _, k := range SourceKinds() {
		out = append(out, string(k))
	}
	return out
}

// Source is one place a value was seen. Detail is the specific place
// (the includeIf folder, the alias, the account), shown in the popover.
type Source struct {
	Kind   SourceKind `json:"kind"`
	Label  string     `json:"label"`
	Detail string     `json:"detail,omitempty"`
}

// Candidate is one value for a field with every source and a score
// (0–100; the page preselects the highest). Accounts are the gh logins
// the value belongs to, so a picked account card can pick its own value.
type Candidate struct {
	Value    string   `json:"value"`
	Sources  []Source `json:"sources"`
	Score    int      `json:"score"`
	Accounts []string `json:"accounts,omitempty"`
}

// DiscoverFields is every field kind's candidates, best first.
type DiscoverFields struct {
	Name     []Candidate `json:"name"`
	Email    []Candidate `json:"email"`
	Username []Candidate `json:"username"`
	Handle   []Candidate `json:"handle"`
	Alias    []Candidate `json:"alias"`
	Key      []Candidate `json:"key"`
	StoreURL []Candidate `json:"store_url"`
	Folder   []Candidate `json:"folder"`
}

// Email scope states of an account.
const (
	EmailScopeOK      = "ok"      // /user/emails answered
	EmailScopeMissing = "missing" // the token lacks user:email
	EmailScopeUnknown = "unknown" // the API was not reached
)

// AccountRoute is one alias or key that logs in as the account.
type AccountRoute struct {
	Alias      string `json:"alias,omitempty"`
	Key        string `json:"key,omitempty"`
	KeyMissing bool   `json:"key_missing,omitempty"`
	OK         bool   `json:"ok"`
}

// FolderHint is a folder the gitconfig ties to the account.
type FolderHint struct {
	Folder string `json:"folder"`
	// From is the includeIf condition and file ("gitdir:~/work/acme/ → ~/.gitconfig-acme").
	From string `json:"from"`
}

// Fact is one card value and where it came from (the card's popover).
type Fact struct {
	Field  string `json:"field"`
	Value  string `json:"value"`
	Source Source `json:"source"`
}

// DiscoveredAccount is one account card.
type DiscoveredAccount struct {
	Login  string `json:"login"`
	Active bool   `json:"active"`
	Name   string `json:"name,omitempty"`
	// Avatar is a data: URI (the page's CSP allows no remote images).
	Avatar string `json:"avatar,omitempty"`
	// Email is the best email; Emails every other one, best first.
	Email        string   `json:"email,omitempty"`
	Emails       []string `json:"emails,omitempty"`
	NoreplyEmail string   `json:"noreply_email,omitempty"`
	EmailScope   string   `json:"email_scope"`
	// RefreshCommand adds the user:email scope (interactive: gh opens a
	// browser); set only when EmailScope is missing.
	RefreshCommand string `json:"refresh_command,omitempty"`
	// Alias and Key are the best route that logs in as Login; Routes all.
	Alias   string         `json:"alias,omitempty"`
	Key     string         `json:"key,omitempty"`
	Routes  []AccountRoute `json:"routes,omitempty"`
	Folders []FolderHint   `json:"folders,omitempty"`
	// SetupName is the suggested setup name (unique among the cards and
	// the existing setups).
	SetupName string `json:"setup_name"`
	// UsedBy is the existing setup that already works as Login ("" when
	// none): Home suggests a new setup only for an unused account.
	UsedBy string `json:"used_by,omitempty"`
	// PurgedBy is the setup deleted for good that worked as Login, set
	// while no "Suggest again" (setup.suggest_again) brought the account
	// back and no live or trashed setup covers it; Covered is the line
	// naming it and the way back. Home suggests no setup for such a card
	// (issue 6).
	PurgedBy string `json:"purged_by,omitempty"`
	Covered  string `json:"covered,omitempty"`
	Facts    []Fact `json:"facts"`
	// Status is the API probe class (ok, not_logged_in, unreachable,
	// auth_denied, tool_missing, failed); Detail one fixed-text line.
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// GitIdentityFact is git's global user.name / user.email.
type GitIdentityFact struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// GitIncludeFact is one includeIf file that sets an identity.
type GitIncludeFact struct {
	Condition string `json:"condition"`
	Folder    string `json:"folder,omitempty"`
	Path      string `json:"path"`
	Name      string `json:"name,omitempty"`
	Email     string `json:"email,omitempty"`
}

// DiscoverOut is setup.discover's output.
type DiscoverOut struct {
	Accounts []DiscoveredAccount `json:"accounts"`
	Fields   DiscoverFields      `json:"fields"`
	// GitGlobal and GitIncludes are git's identities, for the page's
	// "where did this come from" popovers.
	GitGlobal   GitIdentityFact  `json:"git_global"`
	GitIncludes []GitIncludeFact `json:"git_includes,omitempty"`
	// Notes are the parts that could not be read (fixed text).
	Notes []string `json:"notes,omitempty"`
	// Status is set on an async call: done or running (with the named
	// steps), and when the answer was probed.
	Status DiscoverStatus `json:"status,omitzero"`
}

// GHAccountFacts is one gh login and what the GitHub API said with its
// own token. No token field exists: the port never hands one up.
type GHAccountFacts struct {
	Login      string    `json:"login"`
	Active     bool      `json:"active"`
	ID         int64     `json:"id,omitempty"`
	Name       string    `json:"name,omitempty"`
	Avatar     string    `json:"avatar,omitempty"`
	Public     string    `json:"public_email,omitempty"`
	Emails     []GHEmail `json:"emails,omitempty"`
	EmailScope string    `json:"email_scope"`
	Repos      []GHRepo  `json:"repos,omitempty"`
	Status     string    `json:"status"`
	Detail     string    `json:"detail,omitempty"`
}

// GHEmail is one /user/emails row.
type GHEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// GHRepo is a store-like repo the account can see.
type GHRepo struct {
	FullName string `json:"full_name"`
	SSHURL   string `json:"ssh_url"`
	Private  bool   `json:"private"`
}

// KnownStore is a store url lore already uses on this machine.
type KnownStore struct {
	URL    string     `json:"url"`
	Kind   SourceKind `json:"kind"` // SrcLoreSetup, SrcLoreOrg, SrcLoreRepo or SrcRepoScan
	Detail string     `json:"detail"`
}

// DiscoverFacts is the port's answer: the local and API facts.
type DiscoverFacts struct {
	GH          []GHAccountFacts
	GitGlobal   GitIdentityFact
	GitIncludes []GitIncludeFact
	KnownStores []KnownStore
	// SetupNames are the existing setups (suggested names avoid them).
	SetupNames []string
	// SetupUsers maps a GitHub login (lower case) to the first existing
	// setup whose github.user it is (DiscoveredAccount.UsedBy).
	SetupUsers map[string]string
	Notes      []string
}

// AccountDiscoverer is the optional Prober upgrade setup.discover needs
// (wire's Prober has it).
type AccountDiscoverer interface {
	DiscoverFacts(ctx context.Context, in DiscoverFactsIn) (DiscoverFacts, error)
}

// Notes of a partial answer.
const (
	noteNoAliases = "ssh aliases and keys could not be tested; the GitHub step's checks still run them"
	noteNoFacts   = "GitHub accounts and git identities could not be read"
)

// Discover is setup.discover: async, the kept answer or a background
// job's progress (discover_job.go); else the facts and the alias/key table
// probed now in parallel, merged, and kept for the page.
func (e engine) Discover(ctx context.Context, in DiscoverIn) (DiscoverOut, error) {
	if in.Async && e.jobs != nil {
		if err := e.canDiscover(ctx); err != nil {
			return DiscoverOut{}, err
		}
		return e.discoverAsync(ctx, in)
	}
	fp := e.fingerprint(ctx)
	out, err := e.discoverNow(ctx, in)
	if err != nil {
		return DiscoverOut{}, err
	}
	if len(in.Folders) == 0 {
		e.keep(ctx, out, fp)
	}
	out = cloneDiscover(out)
	if c, ok := e.env.(AccountCoverer); ok {
		markPurged(ctx, c, out.Accounts)
	}
	return out, nil
}

// canDiscover refuses before a job starts when the env cannot discover.
func (e engine) canDiscover(ctx context.Context) error {
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return err
	}
	if _, ok := pr.(AccountDiscoverer); !ok {
		return errs.New(errs.Unsupported, "this lore cannot discover accounts").
			WithHint("fill the identity and GitHub steps by hand; lore setup aliases lists the ssh aliases")
	}
	return nil
}

// discoverNow probes and merges, without the purged marks (those are
// applied per answer, so a kept answer never carries stale ones).
func (e engine) discoverNow(ctx context.Context, in DiscoverIn) (DiscoverOut, error) {
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return DiscoverOut{}, err
	}
	d, ok := pr.(AccountDiscoverer)
	if !ok {
		return DiscoverOut{}, errs.New(errs.Unsupported, "this lore cannot discover accounts").
			WithHint("fill the identity and GitHub steps by hand; lore setup aliases lists the ssh aliases")
	}
	var (
		wg      sync.WaitGroup
		facts   DiscoverFacts
		factErr error
		table   AliasesOut
		tblErr  error = errs.New(errs.Unsupported, "no alias lister")
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		facts, factErr = d.DiscoverFacts(ctx, DiscoverFactsIn{Folders: in.Folders})
	}()
	if l, lok := pr.(GitHubLister); lok {
		wg.Add(1)
		go func() {
			defer wg.Done()
			table, tblErr = l.GitHubAliases(ctx, AliasesIn{Fresh: in.Fresh, Keys: true})
		}()
	}
	wg.Wait()
	if factErr != nil {
		facts = DiscoverFacts{Notes: []string{noteNoFacts}}
	}
	if tblErr != nil {
		facts.Notes = append(facts.Notes, noteNoAliases)
	}
	return MergeDiscovery(facts, table), nil
}

// DiscoverOps returns setup.discover bound to env; wire registers it next
// to GuidedOps.
func DiscoverOps(env Onboard) []op.Entry {
	e := newEngine(env, onboardCatalog{})
	e.jobs = &discoverJobs{}
	return []op.Entry{
		op.Define(op.Spec{Name: OpDiscover, Local: true, RESTNoun: restNoun, Permission: op.PermRead, Access: op.AccessRead,
			Summary:  "Find the GitHub accounts, emails, ssh aliases, keys, folders and stores on this machine, for the setup page to offer",
			Errors:   errsPort,
			Examples: []op.Example{{Title: "what can lore fill in", CLI: "lore setup discover --json", Want: ""}}},
			e.Discover),
		op.Define(op.Spec{Name: OpSuggestAgain, Local: true, RESTNoun: restNoun, Permission: op.PermWrite, Access: op.AccessRead,
			Summary:  "Suggest a setup again for a GitHub account Home stopped suggesting because its setup was purged (remembered in ~/.lore/state)",
			Errors:   with(errs.NotFound),
			Examples: []op.Example{{Title: "suggest a setup for alice again", CLI: "lore setup suggest-again --login alice", Want: ""}}},
			e.SuggestAgain),
	}
}
