// Package orgconf is the org part of ~/.lore/config.yaml and the committed
// .lore/org.yaml as plain Go values (layer L1: parse and validate bytes,
// no file access).
//
// Why a core copy: org resolution (LORE_TEAM_SPEC F10, F13, F14, F18) used
// to live only in the old module (saas/pkg/aicoder/lorecfg), so the
// standalone binary (cmd/lore) refused every setup with an org
// (FA-OLDCODE-1). The core may not import the old modules (CODE ORG), so
// the config shapes org resolution reads move here, byte-compatible with
// v0.1.17's YAML: the same file is read by both binaries.
//
// Scope: org resolution reads identity, personal, orgs, repo_orgs,
// live.fetch_ttl and link_index.max_age. Validate also checks the
// machine's other settings (default_branch, live, lease, agents,
// merge_check, untrusted_hosts, upgrade_notice, auto_upgrade,
// context.profiles; machine.go), because every core command loads this
// Config first and an invalid config.yaml must stop it (fail closed,
// CONFIG_GUIDE: nothing in lore reads an invalid config; P3-CFG-5
// narrows P3-ORG-1). `policies:` and `store:` have their own readers in
// wire (machine_policy.go, locktimeout.go), which fail closed themselves.
//
//	config.yaml bytes ──▶ Parse ──▶ Config ──▶ Validate (every problem at once)
//	org.yaml bytes    ──▶ ParseOrgFile ──▶ OrgFile (validated)
package orgconf

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
)

// Names fixed by v0.1.17's config format.
const (
	// TeamFileBase is the base name of ~/.lore/config.yaml.
	TeamFileBase = "config"
	// OrgFileBase is the base name of the committed .lore/org.yaml.
	OrgFileBase = "org"
	// ExtYAML is the extension of both files.
	ExtYAML = ".yaml"
	// DefaultStoreBranch is a store's branch when an entry names none.
	DefaultStoreBranch = "main"
	// OrgFolderRoot is org_folder "." : the store root is the org folder.
	OrgFolderRoot = "."
	// ReservedFolderPrefix starts lore's own folder names (_root, _live).
	ReservedFolderPrefix = "_"
	// PersonalStoreLocal is personal.store's v0.1.17 "no remote" value.
	PersonalStoreLocal = "local"
	// PersonalEverywhere is the personal-store folder of notes that load
	// everywhere.
	PersonalEverywhere = "everywhere"
	// TargetRepo is the --to target (and client_repos value) for this repo.
	TargetRepo = "repo"
	// TargetMe is the --to target for the personal store.
	TargetMe = "me"
	// projectIDPrefix starts every repo_orgs key (a project id).
	projectIDPrefix = "prj_"
)

// AgentDetails is an entry's agent_details setting.
type AgentDetails string

// agent_details values.
const (
	// AgentDetailsInternal keeps agent ids in the org's own stores.
	AgentDetailsInternal AgentDetails = "internal"
	// AgentDetailsClient also shows agent ids in client repos.
	AgentDetailsClient AgentDetails = "client"
)

// Config is the org part of ~/.lore/config.yaml. An absent file is a valid,
// empty config (no orgs: every repo resolves to "no org").
type Config struct {
	// Identity is the fallback identity where orgs exist but none matched
	// (F10 rule 6).
	Identity OrgIdentity        `yaml:"identity,omitempty"`
	Personal Personal           `yaml:"personal,omitempty"`
	Orgs     []Org              `yaml:"orgs,omitempty"`
	RepoOrgs map[string]RepoOrg `yaml:"repo_orgs,omitempty"`
	// Live is read for live.fetch_ttl only (store clone freshness); its
	// other keys are validated.
	Live      Live      `yaml:"live,omitempty"`
	LinkIndex LinkIndex `yaml:"link_index,omitempty"`
	// Settings are the machine's other keys, validated only (machine.go).
	Settings `yaml:",inline"`
}

// Personal is the personal store setting.
type Personal struct {
	// Store is PersonalStoreLocal (the default when empty) or a store url.
	Store string `yaml:"store,omitempty"`
}

// Org is one entry under `orgs:` (F13).
type Org struct {
	Name string `yaml:"name"`
	// ID is the org's permanent id (P2-ORG-1); Name is this machine's label.
	ID       string      `yaml:"id,omitempty"`
	Identity OrgIdentity `yaml:"identity,omitempty"`
	Stores   []Store     `yaml:"stores,omitempty"`
	// Own are patterns of the org's own code repos (no client guards).
	Own []string `yaml:"own,omitempty"`
	// Repos are patterns of client repos the org works in.
	Repos        []string     `yaml:"repos,omitempty"`
	Defaults     OrgDefaults  `yaml:"defaults,omitempty"`
	AgentDetails AgentDetails `yaml:"agent_details,omitempty"`
	// Live overrides the top level's live.fetch_ttl for this entry.
	Live *Live `yaml:"live,omitempty"`
	// OrgSettings are the entry's lease/agents/merge_check overrides,
	// validated only (machine.go).
	OrgSettings `yaml:",inline"`
}

// OrgIdentity is who you are inside one org (F18); the key is
// `human:<your email in that org>` (TEAM-029).
type OrgIdentity struct {
	Key    string `yaml:"key,omitempty"`
	Name   string `yaml:"name,omitempty"`
	Handle string `yaml:"handle,omitempty"`
}

// Store is one item of an entry's `stores:` (D10, F13).
type Store struct {
	URL string `yaml:"url"`
	// Branch is the store branch; empty means DefaultStoreBranch.
	Branch string `yaml:"branch,omitempty"`
	// OrgFolder marks the store holding the roster and company rules; "."
	// is the store root's project.
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
	// ClientRepos maps a row kind (task, memory, …) to TargetRepo, the
	// entry's name, or "<entry>/<folder>".
	ClientRepos map[string]string `yaml:"client_repos,omitempty"`
}

// RepoOrg is one `repo_orgs:` value (F10 rule 2). Two shapes: the short
// scalar `prj_…: northwind` and `{org: northwind, remotes: [...]}`.
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
	if c == nil {
		return nil
	}
	for i := range c.Orgs {
		if c.Orgs[i].Name == name {
			return &c.Orgs[i]
		}
	}
	return nil
}

// OrgNames returns every entry name, sorted, for messages.
func (c *Config) OrgNames() []string {
	out := make([]string, 0, len(c.Orgs))
	for _, o := range c.Orgs {
		out = append(out, o.Name)
	}
	sort.Strings(out)
	return out
}

// OrgKeys returns every entry's identity key (trimmed, non-empty), so the
// no-org fallback never borrows one (F10 rule 6).
func (c *Config) OrgKeys() []string {
	var keys []string
	for i := range c.Orgs {
		if k := strings.TrimSpace(c.Orgs[i].Identity.Key); k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

// OrgFolderStore returns the store that sets org_folder, or nil.
func (o *Org) OrgFolderStore() *Store {
	for i := range o.Stores {
		if o.Stores[i].OrgFolder != "" {
			return &o.Stores[i]
		}
	}
	return nil
}

// EffectiveAgentDetails returns the entry's agent_details or internal.
func (o *Org) EffectiveAgentDetails() AgentDetails {
	if o.AgentDetails == "" {
		return AgentDetailsInternal
	}
	return o.AgentDetails
}

// StoreFor returns the entry's store whose url normalises to norm, or nil.
func (o *Org) StoreFor(norm string) *Store {
	for i := range o.Stores {
		if n, err := repourl.Normalize(o.Stores[i].URL); err == nil && n == norm {
			return &o.Stores[i]
		}
	}
	return nil
}

// yamlSyntaxPrefix starts yaml.v3's syntax errors (a value error from an
// UnmarshalYAML method has no such prefix).
const yamlSyntaxPrefix = "yaml: "

// Parse decodes config.yaml bytes and validates the org part. Empty bytes
// are an empty config.
func Parse(b []byte) (*Config, error) {
	c := &Config{}
	if err := yamlerr.Decode(b, c, yamlerr.Lenient); err != nil {
		// Only a syntax error (yaml's own "yaml: line 1: …") is "not valid
		// YAML"; a value of the wrong kind (a *yaml.TypeError) is named by
		// its line and key path in the user's words (yamlerr), and a
		// Duration's "line 2: invalid duration …" is kept as it is.
		var te *yaml.TypeError
		switch {
		case errors.As(err, &te):
			return nil, fmt.Errorf("orgconf: %s%s: %w", TeamFileBase, ExtYAML, err)
		case strings.HasPrefix(errors.Unwrap(err).Error(), yamlSyntaxPrefix):
			return nil, fmt.Errorf("orgconf: %s%s is not valid YAML: %w", TeamFileBase, ExtYAML, err)
		default:
			return nil, fmt.Errorf("orgconf: %s%s: %w", TeamFileBase, ExtYAML, err)
		}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// OrgFile is the committed `.lore/org.yaml`: names only, never a url, an
// identity or a pattern (TEAM-023).
type OrgFile struct {
	Org     string `yaml:"org"`
	Project string `yaml:"project,omitempty"`
}

// Validate checks the org name and the optional project folder.
func (f *OrgFile) Validate() error {
	var all []error
	if err := validateOrgName(f.Org); err != nil {
		all = append(all, fmt.Errorf("org %q: %w", f.Org, err))
	}
	if f.Project != "" {
		if err := ValidateFolderName(f.Project); err != nil {
			all = append(all, fmt.Errorf("project %q: %w", f.Project, err))
		}
	}
	return errors.Join(all...)
}

// ParseOrgFile decodes and validates org.yaml bytes.
func ParseOrgFile(b []byte) (*OrgFile, error) {
	f := &OrgFile{}
	if err := yamlerr.Decode(b, f, yamlerr.Lenient); err != nil { // line + key, never a Go type
		return nil, fmt.Errorf("orgconf: %s%s is not valid YAML: %w", OrgFileBase, ExtYAML, err)
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return f, nil
}

// Validation shapes, as v0.1.17 (lorecfg/team_validate.go).
var (
	orgNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
	folderRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	handleRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	kindRe    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Limits with their reasons.
const (
	// maxNameLen keeps names usable as folder names and in one-line output.
	maxNameLen = 64
	// humanKeyPrefix is the only identity key form for a person (TEAM-029).
	humanKeyPrefix = "human:"
	// RefSep separates a folder from its org in "<org>/<folder>" targets.
	RefSep = "/"
)

// reservedOrgNames already mean something as a --to target or folder.
var reservedOrgNames = map[string]string{
	TargetRepo:         "it is the --to target for this repo",
	TargetMe:           "it is the --to target for your personal store",
	PersonalEverywhere: "it is the personal-store folder for notes that load everywhere",
}

// windowsDeviceNames are the file names Windows reserves for devices, in
// any case and with any extension ("con", "NUL.txt", "com1"): a folder of
// that name in a store cannot be checked out on Windows, so one teammate's
// folder would break the store for every Windows clone (B-XOS-1).
var windowsDeviceNames = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true}

// windowsUnsafeName says why name cannot be a folder on Windows ("" when
// it can): a device name, or a trailing '.', which Windows strips, so
// "web." and "web" would be one folder there.
func windowsUnsafeName(name string) string {
	stem, _, _ := strings.Cut(strings.ToLower(name), ".")
	switch {
	case windowsDeviceNames[stem]:
		return fmt.Sprintf("%q is a reserved device name on Windows, where the folder could not be checked out", name)
	case strings.HasSuffix(name, "."):
		return "a name ending in '.' cannot be a folder on Windows"
	}
	return ""
}

// Validate reports every problem of the org part at once, each prefixed
// with where it is (orgs[1] "northwind": stores[0].branch …).
func (c *Config) Validate() error {
	var all []error
	add := func(e error) { all = append(all, e) }
	if s := c.Personal.Store; s != "" && s != PersonalStoreLocal && !isFolderStoreURL(s) {
		if _, err := repourl.Normalize(s); err != nil {
			add(fmt.Errorf("personal.store %q: not %q and not a git URL: %w", s, PersonalStoreLocal, err))
		}
	}
	if k := c.Identity.Key; k != "" {
		if err := ValidateHumanKey(k); err != nil {
			add(fmt.Errorf("identity.key %q: %w", k, err))
		}
	}
	if h := c.Identity.Handle; h != "" && !handleRe.MatchString(h) {
		add(fmt.Errorf("identity.handle %q: use lowercase letters, digits, '_' or '-' (no '@')", h))
	}
	all = append(all, c.validateSettings()...)
	seen := map[string]int{}
	for i := range c.Orgs {
		o := &c.Orgs[i]
		where := fmt.Sprintf("orgs[%d] %q", i, o.Name)
		if j, dup := seen[o.Name]; dup && o.Name != "" {
			add(fmt.Errorf("%s: name already used by orgs[%d]", where, j))
		}
		seen[o.Name] = i
		all = append(all, prefixAll(where, o.validate())...)
	}
	pids := make([]string, 0, len(c.RepoOrgs))
	for pid := range c.RepoOrgs {
		pids = append(pids, pid)
	}
	sort.Strings(pids) // stable message order
	for _, pid := range pids {
		ro := c.RepoOrgs[pid]
		where := fmt.Sprintf("repo_orgs[%s]", pid)
		if !strings.HasPrefix(pid, projectIDPrefix) {
			add(fmt.Errorf("%s: key is not a project id", where))
		}
		if ro.Org == "" {
			add(fmt.Errorf("%s: org is empty", where))
		} else if c.FindOrg(ro.Org) == nil {
			add(fmt.Errorf("%s: org %q is not an entry under orgs: (have: %s)", where, ro.Org, strings.Join(c.OrgNames(), ", ")))
		}
		for k, r := range ro.Remotes {
			if _, err := repourl.Normalize(r); err != nil {
				add(fmt.Errorf("%s.remotes[%d] %q: %w", where, k, r, err))
			}
		}
	}
	return errors.Join(all...)
}

// validate checks one org entry; messages are relative to the entry.
func (o *Org) validate() []error {
	var all []error
	add := func(e error) { all = append(all, e) }
	if err := validateOrgName(o.Name); err != nil {
		add(fmt.Errorf("name: %w", err))
	}
	if k := o.Identity.Key; k != "" {
		if err := ValidateHumanKey(k); err != nil {
			add(fmt.Errorf("identity.key %q: %w", k, err))
		}
	}
	if h := o.Identity.Handle; h != "" && !handleRe.MatchString(h) {
		add(fmt.Errorf("identity.handle %q: use lowercase letters, digits, '_' or '-' (no '@')", h))
	}
	orgFolders := 0
	stores := map[string]int{}
	for i, s := range o.Stores {
		w := fmt.Sprintf("stores[%d]", i)
		n, err := repourl.Normalize(s.URL)
		if err != nil {
			add(fmt.Errorf("%s.url %q: %w", w, s.URL, err))
		}
		if s.Branch != "" {
			if err := validateBranchName(s.Branch); err != nil {
				add(fmt.Errorf("%s.branch %q: %w", w, s.Branch, err))
			}
		}
		key := n + "@" + s.EffectiveBranch()
		if j, dup := stores[key]; dup && n != "" {
			add(fmt.Errorf("%s: same store and branch as stores[%d]", w, j))
		}
		stores[key] = i
		if s.OrgFolder != "" {
			orgFolders++
			if s.OrgFolder != OrgFolderRoot {
				if err := ValidateFolderName(s.OrgFolder); err != nil {
					add(fmt.Errorf("%s.org_folder %q: %w", w, s.OrgFolder, err))
				}
			}
		}
	}
	if orgFolders > 1 {
		add(fmt.Errorf("stores: org_folder is set on %d stores; at most one store per entry holds the roster and company rules", orgFolders))
	}
	for i, p := range o.Own {
		if _, err := repourl.Compile(p); err != nil {
			add(fmt.Errorf("own[%d] %q: %w", i, p, err))
		}
	}
	for i, p := range o.Repos {
		if _, err := repourl.Compile(p); err != nil {
			add(fmt.Errorf("repos[%d] %q: %w", i, p, err))
		}
	}
	kinds := make([]string, 0, len(o.Defaults.ClientRepos))
	for k := range o.Defaults.ClientRepos {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		target := o.Defaults.ClientRepos[kind]
		w := "defaults.client_repos." + kind
		if !kindRe.MatchString(kind) {
			add(fmt.Errorf("%s: kind must be a lowercase name such as task or memory", w))
		}
		if err := validateClientTarget(o.Name, target); err != nil {
			add(fmt.Errorf("%s %q: %w", w, target, err))
		}
	}
	switch o.AgentDetails {
	case "", AgentDetailsInternal, AgentDetailsClient:
	default:
		add(fmt.Errorf("agent_details %q: must be %q or %q", o.AgentDetails, AgentDetailsInternal, AgentDetailsClient))
	}
	if o.Live != nil {
		all = append(all, prefixAll("live", o.Live.validate())...)
	}
	all = append(all, o.validateSettings()...)
	return all
}

// validateOrgName checks an org entry name (also a personal-store folder
// and a --to target).
func validateOrgName(name string) error {
	switch {
	case name == "":
		return errors.New("required")
	case len(name) > maxNameLen:
		return fmt.Errorf("longer than %d characters", maxNameLen)
	case strings.HasPrefix(name, ReservedFolderPrefix):
		return fmt.Errorf("names starting with %q are reserved for lore", ReservedFolderPrefix)
	case !orgNameRe.MatchString(name):
		return errors.New("use lowercase letters, digits, '.' or '-', starting with a letter or digit")
	}
	if why, bad := reservedOrgNames[name]; bad {
		return fmt.Errorf("%q is reserved: %s", name, why)
	}
	if why := windowsUnsafeName(name); why != "" {
		return errors.New(why)
	}
	return nil
}

// ValidateFolderName checks one store project folder name.
func ValidateFolderName(name string) error {
	switch {
	case name == "":
		return errors.New("empty folder name")
	case len(name) > maxNameLen:
		return fmt.Errorf("longer than %d characters", maxNameLen)
	case strings.HasPrefix(name, ReservedFolderPrefix):
		return fmt.Errorf("folder names starting with %q are reserved for lore (_root, _live)", ReservedFolderPrefix)
	case name == "." || name == "..":
		return errors.New("must be a folder name, not a relative path")
	case !folderRe.MatchString(name):
		return errors.New("one folder name: letters, digits, '.', '_' or '-' (no '/')")
	}
	if why := windowsUnsafeName(name); why != "" {
		return errors.New(why)
	}
	return nil
}

// ValidateHumanKey checks a person's identity key: human:<email> (TEAM-029).
func ValidateHumanKey(key string) error {
	if !strings.HasPrefix(key, humanKeyPrefix) {
		return fmt.Errorf("must be %s<your email in that org> (TEAM-029)", humanKeyPrefix)
	}
	email := strings.TrimPrefix(key, humanKeyPrefix)
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 || strings.ContainsAny(email, " \t<>") {
		return fmt.Errorf("%q is not an email address", email)
	}
	return nil
}

// validateBranchName applies git's ref-name rules that matter here, plus
// the store rule that a branch may not contain "/" (fixed rule 3).
func validateBranchName(b string) error {
	switch {
	case strings.Contains(b, "/"):
		return errors.New(`a store branch may not contain "/" (refs/lore/live/<branch> must never clash with another branch)`)
	case strings.HasPrefix(b, "-"), strings.HasPrefix(b, "."), strings.HasSuffix(b, "."),
		strings.HasSuffix(b, ".lock"), strings.Contains(b, ".."), strings.Contains(b, "@{"),
		strings.ContainsAny(b, " \t~^:?*[\\"):
		return errors.New("not a valid git branch name")
	}
	return nil
}

// validateClientTarget checks one defaults.client_repos value: TargetRepo,
// the entry's own name, or "<entry>/<folder>" (F14).
func validateClientTarget(orgName, target string) error {
	if target == TargetRepo {
		return nil
	}
	org, folder, hasFolder := strings.Cut(target, RefSep)
	if org != orgName {
		return fmt.Errorf("must be %q, %q or %q/<folder>; another org cannot be a default", TargetRepo, orgName, orgName)
	}
	if hasFolder {
		return ValidateFolderName(folder)
	}
	return nil
}

// prefixAll prefixes each error with where.
func prefixAll(where string, list []error) []error {
	out := make([]error, 0, len(list))
	for _, e := range list {
		out = append(out, fmt.Errorf("%s: %w", where, e))
	}
	return out
}

// isFolderStoreURL accepts a 0.1.18 folder-store url (file://<abs path>).
func isFolderStoreURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "file" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if u.Host != "" && u.Host != "localhost" {
		return false
	}
	return path.IsAbs(u.Path) && path.Clean(u.Path) == u.Path
}
