package onboard

import (
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The `setups:` list of ~/.lore/config.yaml (ONB 3.1, 3.2): machine-local,
// never in a store. Member and admin setups name an orgs[] entry and read
// their identity from it (Q2, CFG9); only a solo setup carries identity.

// PersonalStore values besides a store url (ONB 3.2, 3.4).
const (
	// PersonalStoreInherit uses the top-level personal.store (the default
	// when the key is empty).
	PersonalStoreInherit = "inherit"
	// PersonalStoreNone makes `--to me` fail with E_SETUP_NO_PERSONAL_STORE.
	PersonalStoreNone = "none"
)

// SetupNameMaxLen caps a setup name (it shows in doctor lines and flags).
const SetupNameMaxLen = 64

// PubKeySuffix is the public half's suffix beside a key file. A setup may
// record either file; lore never reads, copies or stores key content, only
// the path and the fingerprint ssh-keygen -lf prints.
const PubKeySuffix = ".pub"

// GitRoute is how git reaches GitHub for a setup: a closed set (RouteSSH,
// RouteHTTPS; "" in a saved config means ssh, see ValidRoute). The
// OpenAPI document lists it (EnumValues).
type GitRoute string

// Git transports a setup's github block may record (SetupGitHub.Route).
const (
	// RouteSSH (also the empty value, every config saved before it
	// existed): git reaches GitHub over ssh, through HostAlias or ssh's
	// own config for github.com.
	RouteSSH GitRoute = "ssh"
	// RouteHTTPS: no ssh route logs in as the account, so git reaches
	// GitHub over https with that account's gh login, through a per-call
	// credential helper (gh auth token --user); the token lives only in
	// memory, never in git config or a file.
	RouteHTTPS GitRoute = "https"
)

// GitRoutes lists every transport, ssh first (the default).
func GitRoutes() []GitRoute { return []GitRoute{RouteSSH, RouteHTTPS} }

// EnumValues lists every transport (op.Enumerated); "" is not listed, the
// field is left out of the JSON then (omitempty).
func (GitRoute) EnumValues() []string { return enumStrings(GitRoutes()) }

// setupNameRe is snake_case or kebab (3.2).
var setupNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// ValidSetupName reports whether s is a valid setup name.
func ValidSetupName(s string) bool { return len(s) <= SetupNameMaxLen && setupNameRe.MatchString(s) }

// SetupEntry is one `setups[]` entry (ONB 3.2). Unknown keys inside an
// entry are kept byte for byte by the wire writer (it edits the node
// tree), so this struct is only what lore reads.
type SetupEntry struct {
	Name    string   `yaml:"name" json:"name"`
	Kind    Kind     `yaml:"kind" json:"kind"`
	Org     string   `yaml:"org,omitempty" json:"org,omitempty"`
	Default bool     `yaml:"default,omitempty" json:"default,omitempty"`
	Folders []string `yaml:"folders,omitempty" json:"folders,omitempty"`
	// Identity is solo only (member/admin read orgs[<org>].identity).
	Identity SetupIdentity `yaml:"identity,omitempty" json:"identity,omitzero"`
	GitHub   SetupGitHub   `yaml:"github,omitempty" json:"github,omitzero"`
	// PersonalStore is PersonalStoreInherit (also when empty),
	// PersonalStoreNone, or a store url.
	PersonalStore string       `yaml:"personal_store,omitempty" json:"personal_store,omitempty"`
	Tools         []string     `yaml:"tools,omitempty" json:"tools,omitempty"`
	Secrets       SetupSecrets `yaml:"secrets,omitempty" json:"secrets,omitzero"`
	Live          SetupLive    `yaml:"live,omitempty" json:"live,omitzero"`
	// Note is the person's own line on why this setup exists (shown on
	// Home and by lore setup show; at most NoteMaxLen characters).
	Note string `yaml:"note,omitempty" json:"note,omitempty"`
	// Disabled keeps the entry but takes it out of every match (--setup,
	// LORE_SETUP, repo pins, folders, org, default) and out of
	// doctor --all, like a commented-out line (lore setup disable).
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	// DisabledAt is when it was disabled, RFC 3339 UTC ("" when enabled).
	DisabledAt string `yaml:"disabled_at,omitempty" json:"disabled_at,omitempty"`
}

// SetupIdentity is a solo setup's identity (3.2). Emails are extra
// addresses, all flagged identity contacts.
type SetupIdentity struct {
	Key    string   `yaml:"key,omitempty" json:"key,omitempty"`
	Name   string   `yaml:"name,omitempty" json:"name,omitempty"`
	Handle string   `yaml:"handle,omitempty" json:"handle,omitempty"`
	Emails []string `yaml:"emails,omitempty" json:"emails,omitempty"`
	// Aliases are the person's earlier keys, oldest first: a change of Key
	// appends the old one (SOURCE_OF_TRUTH, an email change keeps one
	// person). The actor id is derived from Aliases[0] when set, so the
	// person keeps one actor; lookups by an old email still find it.
	Aliases []string `yaml:"aliases,omitempty" json:"aliases,omitempty"`
}

// IsZero reports an absent block.
func (i SetupIdentity) IsZero() bool {
	return i.Key == "" && i.Name == "" && i.Handle == "" && len(i.Emails) == 0 && len(i.Aliases) == 0
}

// WithKey is i with its key set to key: a different, non-empty old key
// joins Aliases (once, oldest first), so the actor survives an email
// change. The new key is never listed as its own alias unless it was the
// first key (Aliases[0] fixes the actor id).
func (i SetupIdentity) WithKey(key string) SetupIdentity {
	out := i
	out.Aliases = slices.Clone(i.Aliases)
	if old := i.Key; old != "" && old != key && !slices.Contains(out.Aliases, old) {
		out.Aliases = append(out.Aliases, old)
	}
	out.Key = key
	return out
}

// SetupGitHub is the account and key a setup uses (ONB 4.3, Q5).
type SetupGitHub struct {
	// User is the account `ssh -T` must name.
	User string `yaml:"user,omitempty" json:"user,omitempty"`
	// HostAlias is the ssh Host lore uses for github.com urls of this
	// setup (per-call url.<alias>.insteadOf, Q5).
	HostAlias string `yaml:"host_alias,omitempty" json:"host_alias,omitempty"`
	// SSHKey is the key file the route uses, as written (~/.ssh/id_x or
	// ~/.ssh/id_x.pub; configs saved before 2026-11-07 hold a .pub path).
	// Optional: empty means the alias's (or ssh's) own key decides. lore
	// never reads the file; KeyFingerprint says which key it was.
	SSHKey string `yaml:"ssh_key,omitempty" json:"ssh_key,omitempty"`
	// KeyFingerprint is `ssh-keygen -lf <key>`'s SHA256 fingerprint of the
	// route's key at save time (github.key_matches compares it with the
	// key the alias uses now); empty when unknown.
	KeyFingerprint string `yaml:"key_fingerprint,omitempty" json:"key_fingerprint,omitempty"`
	// Route is how git reaches GitHub: RouteSSH ("" too) or
	// RouteHTTPS (gh login of User, no ssh route logs in as it).
	Route GitRoute `yaml:"route,omitempty" json:"route,omitempty"`
	// GHAccount is the gh login for API checks (optional).
	GHAccount string `yaml:"gh_account,omitempty" json:"gh_account,omitempty"`
}

// IsZero reports an absent block.
func (g SetupGitHub) IsZero() bool {
	return g.User == "" && g.HostAlias == "" && g.SSHKey == "" && g.GHAccount == "" && g.KeyFingerprint == "" && g.Route == ""
}

// IsHTTPS reports the https transport (gh login, no ssh route).
func (g SetupGitHub) IsHTTPS() bool { return g.Route == RouteHTTPS }

// ValidRoute reports a known transport ("" is ssh).
func ValidRoute(t GitRoute) bool {
	return t == "" || t == RouteSSH || t == RouteHTTPS
}

// SetupSecrets is the default backend for new secret references (4.8).
type SetupSecrets struct {
	Backend string `yaml:"backend,omitempty" json:"backend,omitempty"`
}

// IsZero reports an absent block.
func (s SetupSecrets) IsZero() bool { return s.Backend == "" }

// OptBool is an optional bool kept as text so an unset value is told
// apart from false without a pointer (op outputs carry no pointers):
// "" = unset, "true", "false". YAML `autopush: true` decodes into it.
type OptBool string

// OptBool values.
const (
	OptUnset OptBool = ""
	OptTrue  OptBool = "true"
	OptFalse OptBool = "false"
)

// Valid reports whether o is one of the three values.
func (o OptBool) Valid() bool { return o == OptUnset || o == OptTrue || o == OptFalse }

// SetupLive is the optional per-setup override of the top-level live:
// block (3.2). Only autopush for now.
type SetupLive struct {
	Autopush OptBool `yaml:"autopush,omitempty" json:"autopush,omitempty"`
}

// IsZero reports an absent block.
func (l SetupLive) IsZero() bool { return l.Autopush == OptUnset }

// Settings are the 7.3 keys with their defaults applied.
type Settings struct {
	// IdleMinutes is setup.idle_minutes: the page server stops after this
	// long without a request.
	IdleMinutes int `json:"idle_minutes"`
	// OpenBrowser is setup.open_browser: false = print the URL only.
	OpenBrowser bool `json:"open_browser"`
	// ScanDepth is doctor.scan_depth: the fallback folder scan depth of
	// `lore doctor --all` when known_repos is empty (Q11).
	ScanDepth int `json:"scan_depth"`
	// ProbeTimeoutS is doctor.probe_timeout_s: per ssh/git probe.
	ProbeTimeoutS int `json:"probe_timeout_s"`
}

// Defaults and bounds of the 7.3 keys. The bounds keep a typo from
// hanging every probe (probe_timeout_s: 2000) or walking a whole disk.
const (
	DefaultIdleMinutes   = 30
	DefaultOpenBrowser   = true
	DefaultScanDepth     = 3
	DefaultProbeTimeoutS = 20
	MaxIdleMinutes       = 24 * 60
	MaxScanDepth         = 10
	MaxProbeTimeoutS     = 300
)

// DefaultSettings is Settings with every default.
func DefaultSettings() Settings {
	return Settings{IdleMinutes: DefaultIdleMinutes, OpenBrowser: DefaultOpenBrowser,
		ScanDepth: DefaultScanDepth, ProbeTimeoutS: DefaultProbeTimeoutS}
}

// Config is everything of config.yaml onboarding reads: the setups, the
// per-repo pins (`lore setup use --here`) and the 7.3 settings.
type Config struct {
	Setups []SetupEntry
	// RepoSetups is repo_setups: repo id → setup name (machine-only, like
	// repo_orgs).
	RepoSetups map[string]string
	Settings   Settings
	// SharedStore is the top-level personal.store ("" when unset): what a
	// setup's personal_store inherit reaches. Without one, inherit has
	// nothing to inherit, so the page never preselects it.
	SharedStore string
	// TopIdentity is the top-level identity: block (zero when unset);
	// Leftovers reports it when no solo setup uses it.
	TopIdentity SetupIdentity
	// Trash is trash.setups: the setups lore setup remove moved aside,
	// newest last. They match nothing and are never validated as setups;
	// lore setup trash restore brings one back.
	Trash []TrashedSetup
	// Purged is trash.purged, the purge tombstones; Leftovers reads their
	// fingerprints to tell what a purge left from what you set yourself.
	Purged []PurgedSetup
	// OrgIdentities are orgs[].identity by org name: who a member or
	// admin setup is (its identity lives there, CFG9), read-only here.
	OrgIdentities map[string]SetupIdentity
	// OrgStores are orgs[].stores[0].url by org name: the team store an
	// admin setup's team step shows when it is opened for edit, read-only.
	OrgStores map[string]string
	// MigratedIdentityKey is the key of ~/.lore/identity.yaml.migrated
	// (the v0.1.17 identity a migration moved), "" when there is none.
	MigratedIdentityKey string
}

// IdentityOf is the identity setup e uses: its own for a solo setup, its
// org entry's (orgs[<org>].identity) for a member or admin one, whose
// setups[] entry holds none. Readers that show or check a setup's email
// (setup plan, the page) use it, so a team setup shows the email it
// writes with, also after an email change.
func (c Config) IdentityOf(e SetupEntry) SetupIdentity {
	if e.Kind.HasOrg() && e.Identity.IsZero() {
		if id, ok := c.OrgIdentities[e.Org]; ok {
			return id
		}
	}
	return e.Identity
}

// Setup returns the entry named name.
func (c Config) Setup(name string) (SetupEntry, bool) {
	i := slices.IndexFunc(c.Setups, func(e SetupEntry) bool { return e.Name == name })
	if i < 0 {
		return SetupEntry{}, false
	}
	return c.Setups[i], true
}

// ExpandHome replaces a leading ~ (alone or ~/…) with home.
func ExpandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return p
}

// Known is what a setups validation checks names against: the orgs[]
// entry names and the tool profile names (built-in + context.profiles).
type Known struct {
	Orgs  []string
	Tools []string
	// Home expands ~ in folder globs.
	Home string
}

// Validate checks every 3.2 rule and returns one problem per broken rule,
// each naming the entry ("setups[personal]: …", "setups[2]: …" when the
// name is missing). Empty means valid. repoSetups values must name a setup.
func Validate(setups []SetupEntry, repoSetups map[string]string, k Known) []string {
	var out []string
	seen := map[string]bool{}
	defaults := []string{}
	for i, e := range setups {
		where := "setups[" + e.Name + "]"
		if e.Name == "" {
			where = "setups[" + strconv.Itoa(i) + "]"
			out = append(out, where+": name is required")
		} else if !ValidSetupName(e.Name) {
			out = append(out, where+": name "+strconv.Quote(e.Name)+" must be snake_case or kebab-case (a-z, 0-9, _ or -, starting with a letter), at most "+strconv.Itoa(SetupNameMaxLen)+" characters")
		}
		if e.Name != "" {
			if seen[e.Name] {
				out = append(out, where+": name is used by two setups; names must be unique")
			}
			seen[e.Name] = true
		}
		if !e.Kind.Valid() {
			out = append(out, where+": kind "+strconv.Quote(string(e.Kind))+" must be solo, member or admin")
		}
		if e.Default {
			defaults = append(defaults, e.Name)
		}
		if e.Kind.HasOrg() {
			switch {
			case e.Org == "":
				out = append(out, where+": org is required for a "+string(e.Kind)+" setup (an orgs[] entry name)")
			case !slices.Contains(k.Orgs, e.Org):
				out = append(out, where+": org "+strconv.Quote(e.Org)+" is not an orgs[] entry; lore org list shows them")
			}
			if !e.Identity.IsZero() {
				out = append(out, where+": identity is for solo setups only; a "+string(e.Kind)+" setup reads orgs["+e.Org+"].identity")
			}
		}
		for _, g := range e.Folders {
			if !filepath.IsAbs(ExpandHome(g, k.Home)) {
				out = append(out, where+": folder "+strconv.Quote(g)+" must be absolute (start with / or ~/)")
			}
		}
		if e.GitHub.SSHKey != "" {
			if why, ok := ValidateValue(R(RuleKeyPath), e.GitHub.SSHKey); !ok {
				out = append(out, where+": github.ssh_key "+strconv.Quote(e.GitHub.SSHKey)+" "+why)
			}
		}
		if !ValidRoute(e.GitHub.Route) {
			out = append(out, where+": github.route "+strconv.Quote(string(e.GitHub.Route))+" must be ssh or https")
		}
		if e.GitHub.IsHTTPS() && e.GitHub.User == "" {
			out = append(out, where+": github.route https needs github.user (the gh login git uses)")
		}
		for _, t := range e.Tools {
			if !slices.Contains(k.Tools, t) {
				out = append(out, where+": tool "+strconv.Quote(t)+" is not a known tool profile ("+strings.Join(k.Tools, ", ")+")")
			}
		}
		if n := len([]rune(e.Note)); n > NoteMaxLen {
			out = append(out, where+": note is "+strconv.Itoa(n)+" characters; at most "+strconv.Itoa(NoteMaxLen))
		}
		if !e.Live.Autopush.Valid() {
			out = append(out, where+": live.autopush "+strconv.Quote(string(e.Live.Autopush))+" must be true or false")
		}
	}
	if len(defaults) > 1 {
		out = append(out, "setups: default: true is set on "+strings.Join(defaults, " and ")+"; at most one setup may be the default")
	}
	repos := make([]string, 0, len(repoSetups))
	for r := range repoSetups {
		repos = append(repos, r)
	}
	slices.Sort(repos)
	for _, r := range repos {
		if !seen[repoSetups[r]] {
			out = append(out, "repo_setups["+r+"]: no setup named "+strconv.Quote(repoSetups[r]))
		}
	}
	return out
}

// ValidateSettings checks the 7.3 keys' bounds; one problem per key.
func ValidateSettings(s Settings) []string {
	var out []string
	if s.IdleMinutes < 1 || s.IdleMinutes > MaxIdleMinutes {
		out = append(out, "setup.idle_minutes "+strconv.Itoa(s.IdleMinutes)+" must be between 1 and "+strconv.Itoa(MaxIdleMinutes))
	}
	if s.ScanDepth < 1 || s.ScanDepth > MaxScanDepth {
		out = append(out, "doctor.scan_depth "+strconv.Itoa(s.ScanDepth)+" must be between 1 and "+strconv.Itoa(MaxScanDepth))
	}
	if s.ProbeTimeoutS < 1 || s.ProbeTimeoutS > MaxProbeTimeoutS {
		out = append(out, "doctor.probe_timeout_s "+strconv.Itoa(s.ProbeTimeoutS)+" must be between 1 and "+strconv.Itoa(MaxProbeTimeoutS))
	}
	return out
}
