package teamharness

import (
	"gopkg.in/yaml.v3"
)

// Config is ~/.lore/config.yaml as LORE_TEAM_SPEC.md §1 "Config keys" and
// §5.2 define it. Tests build it in Go so a typo in a key is a compile error
// here rather than a silently ignored YAML key; only the keys the spec names
// exist. Zero values are omitted, so a test writes exactly what it sets.
type Config struct {
	// Identity is the top-level fallback identity used when orgs exist but
	// none matched the repo (F10 rule 6, coordinator Q7 13:35).
	Identity       *Identity         `yaml:"identity,omitempty"`
	Personal       *Personal         `yaml:"personal,omitempty"`
	Orgs           []Org             `yaml:"orgs,omitempty"`
	RepoOrgs       map[string]string `yaml:"repo_orgs,omitempty"`
	Live           *Live             `yaml:"live,omitempty"`
	Lease          *Lease            `yaml:"lease,omitempty"`
	Agents         *Agents           `yaml:"agents,omitempty"`
	MergeCheck     *MergeCheck       `yaml:"merge_check,omitempty"`
	DefaultBranch  string            `yaml:"default_branch,omitempty"`
	LinkIndex      *LinkIndex        `yaml:"link_index,omitempty"`
	UntrustedHosts []string          `yaml:"untrusted_hosts,omitempty"`
}

// Personal is the personal store: "local" or a git URL.
type Personal struct {
	Store string `yaml:"store"`
}

// PersonalLocal is the personal.store value for ~/.lore/personal.
const PersonalLocal = "local"

// Org is one org entry (§5.2).
type Org struct {
	Name         string       `yaml:"name"`
	Identity     Identity     `yaml:"identity"`
	Stores       []Store      `yaml:"stores,omitempty"`
	Own          []string     `yaml:"own,omitempty"`
	Repos        []string     `yaml:"repos,omitempty"`
	Defaults     *OrgDefaults `yaml:"defaults,omitempty"`
	AgentDetails string       `yaml:"agent_details,omitempty"`
	Live         *Live        `yaml:"live,omitempty"`
	Lease        *Lease       `yaml:"lease,omitempty"`
	Agents       *Agents      `yaml:"agents,omitempty"`
}

// Identity is a person's key, display name and handle inside one org.
type Identity struct {
	Key    string `yaml:"key"`
	Name   string `yaml:"name"`
	Handle string `yaml:"handle,omitempty"`
}

// Store is one item of an org entry's stores: list.
type Store struct {
	URL       string `yaml:"url"`
	Branch    string `yaml:"branch,omitempty"`
	OrgFolder string `yaml:"org_folder,omitempty"`
}

// OrgDefaults holds defaults.client_repos (kind → repo | <org> | <org>/<folder>).
type OrgDefaults struct {
	ClientRepos map[string]string `yaml:"client_repos,omitempty"`
}

// Live is the live: block.
type Live struct {
	Defaults     map[string]bool `yaml:"defaults,omitempty"`
	MaxAge       string          `yaml:"max_age,omitempty"`
	FetchTTL     string          `yaml:"fetch_ttl,omitempty"`
	Autopush     *bool           `yaml:"autopush,omitempty"`
	CompactAfter int             `yaml:"compact_after,omitempty"`
	Remote       string          `yaml:"remote,omitempty"`
}

// Lease is the lease: block (claim timing, F7).
type Lease struct {
	TTL         string `yaml:"ttl,omitempty"`
	RenewEvery  string `yaml:"renew_every,omitempty"`
	Grace       string `yaml:"grace,omitempty"`
	PushRetries int    `yaml:"push_retries,omitempty"`
}

// Agents is the agents: block.
type Agents struct {
	MaxClaimsPerAgent int `yaml:"max_claims_per_agent,omitempty"`
	MaxAttempts       int `yaml:"max_attempts,omitempty"`
}

// MergeCheck is the merge_check: block (F9).
type MergeCheck struct {
	Adapter string `yaml:"adapter,omitempty"`
}

// LinkIndex is the link_index: block (F11).
type LinkIndex struct {
	MaxAge string `yaml:"max_age,omitempty"`
}

// FreshFetch is the live: block every fixture uses unless a test is about
// the fetch cache itself: fetch_ttl 0s makes every command see teammates'
// writes at once, so a test's outcome never depends on how many seconds
// passed between two commands (F3 caches fetches for 60 s by default).
func FreshFetch() *Live { return &Live{FetchTTL: "0s"} }

// Bool returns a pointer for the optional boolean keys.
func Bool(v bool) *bool { return &v }

// marshalConfig renders cfg as YAML.
func marshalConfig(cfg Config) ([]byte, error) { return yaml.Marshal(cfg) }
