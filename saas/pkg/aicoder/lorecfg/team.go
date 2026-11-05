package lorecfg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// Team config file (~/.lore/config.yaml, LORE_TEAM_SPEC §5.2). Born YAML:
// there was never a TOML version, but Locate still runs so a hand-written
// config.toml would be migrated like every other lore config.
const (
	// TeamFileBase is the base name of ~/.lore/config.yaml.
	TeamFileBase = "config"
	// TeamFilePerm keeps the file private: it names the user's identities in
	// every company they work for.
	TeamFilePerm = 0o600
	// teamDirPerm is used when ~/.lore does not exist yet.
	teamDirPerm = 0o755
)

// Closed sets used by the team config. Every reader compares against these
// names, never a literal.
const (
	// DefaultStoreBranch is a store's branch when an entry names none (§5.2),
	// and the branch an entry must use to be matched automatically (TEAM-022).
	DefaultStoreBranch = "main"
	// OrgFolderRoot as `org_folder` means "the store root's project" (a
	// dedicated company store, F13).
	OrgFolderRoot = "."
	// ReservedFolderPrefix starts folder names lore keeps for itself in a
	// live ref's tree (`_root`, `_live`; fixed rule 3). No project folder,
	// org name or org_folder may start with it.
	ReservedFolderPrefix = "_"
	// PersonalStoreLocal keeps the personal store in ~/.lore/personal.
	PersonalStoreLocal = "local"
	// PersonalEverywhere is the personal-store folder for notes that load in
	// every repo (F17). An org may not be named this, or its personal folder
	// would be the everywhere folder.
	PersonalEverywhere = "everywhere"
	// TargetRepo is the `--to` / defaults.client_repos value meaning "this
	// repo" (F14). An org may not be named this.
	TargetRepo = "repo"
	// TargetMe is the `--to` value for the personal store (F14, F17). An org
	// may not be named this.
	TargetMe = "me"
)

// AgentDetails says where agent work notes for a client task go (D8, §7.6).
type AgentDetails string

const (
	// AgentDetailsInternal keeps agent notes in the linked internal project
	// (the default: a client never sees how the work was done).
	AgentDetailsInternal AgentDetails = "internal"
	// AgentDetailsClient keeps them in the client repo, for clients who asked.
	AgentDetailsClient AgentDetails = "client"
)

// MergeCheckAdapter names how lore sees that a done task merged (F9).
type MergeCheckAdapter string

const (
	// MergeCheckNone relies on commits and trailers only.
	MergeCheckNone MergeCheckAdapter = "none"
	// MergeCheckGitHub also asks `gh` about merged pull requests.
	MergeCheckGitHub MergeCheckAdapter = "github"
)

// Built-in defaults for the top-level blocks (§1 "Config keys"). Each is
// named so `lore config check`, docs and the merge below agree.
// ds:def id=team-config-defaults-vcrgzmhf owner=@khanakia stability=stable desc="built-in defaults of ~/.lore/config.yaml"
const (
	DefaultLiveMaxAge          = 7 * Day          // TEAM-026
	DefaultLiveFetchTTL        = 60 * time.Second // TEAM-025
	DefaultLiveAutopush        = true
	DefaultLiveCompactAfter    = 1000
	DefaultLiveRemote          = "origin"
	DefaultLeaseTTL            = 20 * time.Minute
	DefaultLeaseRenewEvery     = 5 * time.Minute
	DefaultLeaseGrace          = 2 * time.Minute
	DefaultLeasePushRetries    = 5
	DefaultMaxClaimsPerAgent   = 1
	DefaultMaxAttempts         = 3
	DefaultLinkIndexMaxAge     = time.Hour
	DefaultMergeCheck          = MergeCheckNone
	DefaultAgentDetailsSetting = AgentDetailsInternal
)

// DefaultLiveKinds lists the kinds that are live unless config says
// otherwise (fixed rule 2, §3.2). Keys are table names, the same names the
// sync registry uses.
// ds:def id=team-live-kinds-62s7pcxa owner=@khanakia stability=stable desc="kinds that are live by default"
func DefaultLiveKinds() map[string]bool {
	return map[string]bool{
		"tasks": true, "missions": true, "handoffs": true, "comments": true,
		"rules": false, "decisions": false, "memories": false,
	}
}

// Config is ~/.lore/config.yaml. Every field is optional; an absent file is
// a valid, empty config (no orgs: every repo resolves to "no org").
//
// Optional blocks are pointers or pointer-valued fields so an unset value can
// be told apart from an explicit zero; Effective* merges built-in defaults →
// top-level block → org entry override.
type Config struct {
	// Identity is the fallback identity for repos where orgs are configured
	// but none matched (F10 rule 6, coordinator Q7): it keeps a personal or
	// unrelated repo from being stamped with a company identity that the
	// legacy chain (git email) would otherwise pick up.
	Identity       OrgIdentity        `yaml:"identity,omitempty"`
	Personal       Personal           `yaml:"personal,omitempty"`
	Orgs           []Org              `yaml:"orgs,omitempty"`
	RepoOrgs       map[string]RepoOrg `yaml:"repo_orgs,omitempty"`
	DefaultBranch  string             `yaml:"default_branch,omitempty"`
	Live           Live               `yaml:"live,omitempty"`
	Lease          Lease              `yaml:"lease,omitempty"`
	Agents         Agents             `yaml:"agents,omitempty"`
	MergeCheck     MergeCheck         `yaml:"merge_check,omitempty"`
	LinkIndex      LinkIndex          `yaml:"link_index,omitempty"`
	UntrustedHosts []string           `yaml:"untrusted_hosts,omitempty"`
	Context        Context            `yaml:"context,omitempty"`
	// UpgradeNotice false turns off the once-a-day new-release notice (FORMAT_EVOLUTION 8.6); nil = on.
	UpgradeNotice *bool `yaml:"upgrade_notice,omitempty"`
	// AutoUpgrade true turns the daily notice into an upgrade (FORMAT_EVOLUTION 8.7); default off.
	AutoUpgrade *bool `yaml:"auto_upgrade,omitempty"`
}

// Context is the `context:` block: how `lore render` wires the generated
// .lore/LORE.md into AI tools.
type Context struct {
	// Profiles are the user's own tool profiles, added after the built-ins.
	// TOOL NEUTRAL: the built-ins (claude_code, codex, cursor, gemini_cli,
	// copilot, generic) are plain entries of the same registry, so a tool
	// lore does not ship is one entry here, never a code change. Which
	// profiles a repo uses is read from the repo's committed pointer files
	// (toolprofile.Resolve), not from this machine-local file.
	Profiles []toolprofile.Profile `yaml:"profiles,omitempty"`
}

// Personal is the personal store setting (§5.2).
type Personal struct {
	// Store is PersonalStoreLocal (the default when empty) or a git URL.
	Store string `yaml:"store,omitempty"`
}

// Org is one entry under `orgs:` (§5.2, F13).
type Org struct {
	Name string `yaml:"name"`
	// ID is the org's permanent id: the prj_ id of its org folder's
	// _meta.json (P2-ORG-1: no org_ prefix, CONFIG_GUIDE Q3). Written by
	// `lore org rename`; Name is only this machine's label for it
	// (PROVENANCE 10.7). Empty until recorded.
	ID       string      `yaml:"id,omitempty"`
	Identity OrgIdentity `yaml:"identity,omitempty"`
	Stores   []Store     `yaml:"stores,omitempty"`
	// Own are patterns of the org's own code repos (no client guards).
	Own []string `yaml:"own,omitempty"`
	// Repos are patterns of client repos the org works in.
	Repos        []string     `yaml:"repos,omitempty"`
	Defaults     OrgDefaults  `yaml:"defaults,omitempty"`
	AgentDetails AgentDetails `yaml:"agent_details,omitempty"`
	Live         *Live        `yaml:"live,omitempty"`
	Lease        *Lease       `yaml:"lease,omitempty"`
	Agents       *Agents      `yaml:"agents,omitempty"`
	MergeCheck   *MergeCheck  `yaml:"merge_check,omitempty"`
}

// OrgKeys returns every org entry's identity key (trimmed, non-empty), so
// the no-org fallback can refuse to borrow one of them (F10 rule 6).
func (c *Config) OrgKeys() []string {
	var keys []string
	for i := range c.Orgs {
		if k := strings.TrimSpace(c.Orgs[i].Identity.Key); k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

// OrgIdentity is who you are inside one org. Required whenever the entry
// resolves (F18); the key is `human:<your email in that org>` (TEAM-029).
type OrgIdentity struct {
	Key    string `yaml:"key,omitempty"`
	Name   string `yaml:"name,omitempty"`
	Handle string `yaml:"handle,omitempty"`
}

// Store is one item of an entry's `stores:` (D10, F13).
type Store struct {
	URL string `yaml:"url"`
	// Branch is the store branch; empty means DefaultStoreBranch. May not
	// contain "/" (fixed rule 3: refs/lore/live/<branch> must never clash).
	Branch string `yaml:"branch,omitempty"`
	// OrgFolder marks the one store holding the roster, teams, the global
	// project and lore-org.yaml; "." is the store root's project.
	OrgFolder string `yaml:"org_folder,omitempty"`
}

// EffectiveBranch returns Branch or DefaultStoreBranch.
func (s Store) EffectiveBranch() string {
	if s.Branch == "" {
		return DefaultStoreBranch
	}
	return s.Branch
}

// OrgDefaults are per-entry write defaults (F14).
type OrgDefaults struct {
	// ClientRepos maps a row kind (task, memory, rule, decision, …) to
	// TargetRepo, the entry's name, or "<entry>/<folder>".
	ClientRepos map[string]string `yaml:"client_repos,omitempty"`
}

// Live is the `live:` block, top level or per entry (§3.2).
type Live struct {
	Defaults     map[string]bool `yaml:"defaults,omitempty"`
	MaxAge       *Duration       `yaml:"max_age,omitempty"`
	FetchTTL     *Duration       `yaml:"fetch_ttl,omitempty"`
	Autopush     *bool           `yaml:"autopush,omitempty"`
	CompactAfter *int            `yaml:"compact_after,omitempty"`
	Remote       string          `yaml:"remote,omitempty"`
}

// Lease is the `lease:` block (claim timing, F7).
type Lease struct {
	TTL         *Duration `yaml:"ttl,omitempty"`
	RenewEvery  *Duration `yaml:"renew_every,omitempty"`
	Grace       *Duration `yaml:"grace,omitempty"`
	PushRetries *int      `yaml:"push_retries,omitempty"`
}

// Agents is the `agents:` block (§7.4).
type Agents struct {
	MaxClaimsPerAgent *int `yaml:"max_claims_per_agent,omitempty"`
	MaxAttempts       *int `yaml:"max_attempts,omitempty"`
}

// MergeCheck is the `merge_check:` block (F9).
type MergeCheck struct {
	Adapter MergeCheckAdapter `yaml:"adapter,omitempty"`
}

// LinkIndex is the `link_index:` block (F11).
type LinkIndex struct {
	MaxAge *Duration `yaml:"max_age,omitempty"`
}

// RepoOrg is one `repo_orgs:` value, written by `lore org use` (F10 rule 2).
//
// Two shapes are accepted, because the spec shows the short one and the
// fork check needs the long one:
//
//	repo_orgs:
//	  prj_…: northwind                     # short: no remote check
//	  prj_…: {org: northwind, remotes: [github.com/acme/app]}
//
// `lore org use` always writes the long shape with the repo's normalised
// remotes, so a fork carrying the upstream's project id is noticed (F10).
type RepoOrg struct {
	Org     string   `yaml:"org"`
	Remotes []string `yaml:"remotes,omitempty"`
}

// UnmarshalYAML accepts the short scalar shape as well as the mapping.
func (r *RepoOrg) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		r.Org = n.Value
		r.Remotes = nil
		return nil
	}
	type plain RepoOrg
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*r = RepoOrg(p)
	return nil
}

// FindOrg returns the entry named name, or nil.
func (c *Config) FindOrg(name string) *Org {
	for i := range c.Orgs {
		if c.Orgs[i].Name == name {
			return &c.Orgs[i]
		}
	}
	return nil
}

// OrgNames returns every entry name, sorted, for messages and prompts.
func (c *Config) OrgNames() []string {
	out := make([]string, 0, len(c.Orgs))
	for _, o := range c.Orgs {
		out = append(out, o.Name)
	}
	sort.Strings(out)
	return out
}

// OrgFolderStore returns the store that sets org_folder, or nil when the
// entry has none (then there is no roster or global project, F13).
func (o *Org) OrgFolderStore() *Store {
	for i := range o.Stores {
		if o.Stores[i].OrgFolder != "" {
			return &o.Stores[i]
		}
	}
	return nil
}

// EffectiveLive is the merged `live:` setting for one org entry (or none).
type EffectiveLive struct {
	Defaults     map[string]bool
	MaxAge       time.Duration
	FetchTTL     time.Duration
	Autopush     bool
	CompactAfter int
	Remote       string
}

// LiveFor merges built-in defaults → top-level `live:` → the entry's
// `live:` (org may be nil). Each layer overrides only what it sets; kind
// defaults merge key by key.
func (c *Config) LiveFor(org *Org) EffectiveLive {
	e := EffectiveLive{
		Defaults:     DefaultLiveKinds(),
		MaxAge:       DefaultLiveMaxAge,
		FetchTTL:     DefaultLiveFetchTTL,
		Autopush:     DefaultLiveAutopush,
		CompactAfter: DefaultLiveCompactAfter,
		Remote:       DefaultLiveRemote,
	}
	layers := []*Live{&c.Live}
	if org != nil && org.Live != nil {
		layers = append(layers, org.Live)
	}
	for _, l := range layers {
		for k, v := range l.Defaults {
			e.Defaults[k] = v
		}
		if l.MaxAge != nil {
			e.MaxAge = l.MaxAge.D()
		}
		if l.FetchTTL != nil {
			e.FetchTTL = l.FetchTTL.D()
		}
		if l.Autopush != nil {
			e.Autopush = *l.Autopush
		}
		if l.CompactAfter != nil {
			e.CompactAfter = *l.CompactAfter
		}
		if l.Remote != "" {
			e.Remote = l.Remote
		}
	}
	return e
}

// EffectiveLease is the merged `lease:` setting.
type EffectiveLease struct {
	TTL, RenewEvery, Grace time.Duration
	PushRetries            int
}

// LeaseFor merges defaults → top level → entry, like LiveFor.
func (c *Config) LeaseFor(org *Org) EffectiveLease {
	e := EffectiveLease{TTL: DefaultLeaseTTL, RenewEvery: DefaultLeaseRenewEvery,
		Grace: DefaultLeaseGrace, PushRetries: DefaultLeasePushRetries}
	layers := []*Lease{&c.Lease}
	if org != nil && org.Lease != nil {
		layers = append(layers, org.Lease)
	}
	for _, l := range layers {
		if l.TTL != nil {
			e.TTL = l.TTL.D()
		}
		if l.RenewEvery != nil {
			e.RenewEvery = l.RenewEvery.D()
		}
		if l.Grace != nil {
			e.Grace = l.Grace.D()
		}
		if l.PushRetries != nil {
			e.PushRetries = *l.PushRetries
		}
	}
	return e
}

// EffectiveAgents is the merged `agents:` setting.
type EffectiveAgents struct {
	MaxClaimsPerAgent, MaxAttempts int
}

// AgentsFor merges defaults → top level → entry.
func (c *Config) AgentsFor(org *Org) EffectiveAgents {
	e := EffectiveAgents{MaxClaimsPerAgent: DefaultMaxClaimsPerAgent, MaxAttempts: DefaultMaxAttempts}
	layers := []*Agents{&c.Agents}
	if org != nil && org.Agents != nil {
		layers = append(layers, org.Agents)
	}
	for _, l := range layers {
		if l.MaxClaimsPerAgent != nil {
			e.MaxClaimsPerAgent = *l.MaxClaimsPerAgent
		}
		if l.MaxAttempts != nil {
			e.MaxAttempts = *l.MaxAttempts
		}
	}
	return e
}

// MergeCheckFor merges defaults → top level → entry.
func (c *Config) MergeCheckFor(org *Org) MergeCheckAdapter {
	a := DefaultMergeCheck
	if c.MergeCheck.Adapter != "" {
		a = c.MergeCheck.Adapter
	}
	if org != nil && org.MergeCheck != nil && org.MergeCheck.Adapter != "" {
		a = org.MergeCheck.Adapter
	}
	return a
}

// LinkIndexMaxAge returns link_index.max_age or its default.
func (c *Config) LinkIndexMaxAge() time.Duration {
	if c.LinkIndex.MaxAge != nil {
		return c.LinkIndex.MaxAge.D()
	}
	return DefaultLinkIndexMaxAge
}

// EffectiveAgentDetails returns the entry's agent_details or the default.
func (o *Org) EffectiveAgentDetails() AgentDetails {
	if o.AgentDetails == "" {
		return DefaultAgentDetailsSetting
	}
	return o.AgentDetails
}

// LoadTeam reads ~/.lore/config.yaml from dir. An absent file is an empty,
// valid config (loc.Exists is false). A present file that fails to parse or
// validate is an error naming every problem.
func LoadTeam(ctx context.Context, dir string, o Options) (*Config, Located, error) {
	loc, err := Locate(ctx, dir, TeamFileBase, o)
	if err != nil {
		return nil, loc, err
	}
	if !loc.Exists {
		return &Config{}, loc, nil
	}
	cfg, err := Load[Config](ctx, loc, o)
	if err != nil {
		return nil, loc, err
	}
	return cfg, loc, nil
}

// ensureDir creates dir with teamDirPerm when missing.
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, teamDirPerm); err != nil {
		return fmt.Errorf("lorecfg: create %s: %w", dir, err)
	}
	return nil
}

// teamPath is dir/config.yaml.
func teamPath(dir string) string { return filepath.Join(dir, TeamFileBase+ExtYAML) }
