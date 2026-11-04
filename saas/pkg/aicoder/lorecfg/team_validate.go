package lorecfg

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"saas/pkg/aicoder/ids"
	"saas/pkg/aicoder/repourl"
)

// Validation shapes. Named so the error messages and the checks agree.
var (
	// orgNameRe: lowercase letters, digits, '.', '-'; must start with a letter
	// or digit. An org name is a folder name in the personal store and the
	// `--to` target, so it must be a safe single path segment.
	orgNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
	// folderRe: one store folder segment (letters, digits, '.', '_', '-');
	// the reserved '_' prefix is checked separately for a clearer message.
	folderRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	// handleRe: a roster handle as typed after '@'.
	handleRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	// kindRe: a row kind or table name used as a map key in config.
	kindRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Limits with their reasons.
const (
	// maxNameLen keeps names usable as folder names and in one-line output.
	maxNameLen = 64
	// humanKeyPrefix is the only identity key form for a person (TEAM-029):
	// human:<email in that org>.
	humanKeyPrefix = "human:"
	// refSep separates a folder from its org in "<org>/<folder>" targets.
	refSep = "/"
)

// reservedOrgNames may not be used as org names: each already means
// something as a `--to` target or a personal-store folder.
var reservedOrgNames = map[string]string{
	TargetRepo:         "it is the --to target for this repo",
	TargetMe:           "it is the --to target for your personal store",
	PersonalEverywhere: "it is the personal-store folder for notes that load everywhere",
}

// Validate implements cfgkit.Validator. It reports every problem at once,
// each prefixed with where it is (orgs[1] "northwind": stores[0].branch …).
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if s := c.Personal.Store; s != "" && s != PersonalStoreLocal && !isFolderStoreURL(s) {
		if _, err := repourl.Normalize(s); err != nil {
			add("personal.store %q: not %q and not a git URL: %v", s, PersonalStoreLocal, err)
		}
	}
	if k := c.Identity.Key; k != "" {
		if err := ValidateHumanKey(k); err != nil {
			add("identity.key %q: %v", k, err)
		}
	}
	if h := c.Identity.Handle; h != "" && !handleRe.MatchString(h) {
		add("identity.handle %q: use lowercase letters, digits, '_' or '-' (no '@')", h)
	}
	if c.DefaultBranch != "" {
		if err := validateBranchName(c.DefaultBranch, false); err != nil {
			add("default_branch %q: %v", c.DefaultBranch, err)
		}
	}
	errs = append(errs, prefixAll("live", c.Live.validate())...)
	errs = append(errs, prefixAll("lease", c.Lease.validate())...)
	errs = append(errs, prefixAll("agents", c.Agents.validate())...)
	errs = append(errs, prefixAll("merge_check", c.MergeCheck.validate())...)
	if c.LinkIndex.MaxAge != nil && c.LinkIndex.MaxAge.D() <= 0 {
		add("link_index.max_age: must be greater than zero")
	}
	for i, h := range c.UntrustedHosts {
		if strings.TrimSpace(h) == "" {
			add("untrusted_hosts[%d]: empty pattern", i)
		}
	}

	seen := map[string]int{}
	for i := range c.Orgs {
		o := &c.Orgs[i]
		where := fmt.Sprintf("orgs[%d] %q", i, o.Name)
		if j, dup := seen[o.Name]; dup && o.Name != "" {
			add("%s: name already used by orgs[%d]", where, j)
		}
		seen[o.Name] = i
		errs = append(errs, prefixAll(where, o.validate())...)
	}

	for pid, ro := range c.RepoOrgs {
		where := fmt.Sprintf("repo_orgs[%s]", pid)
		if err := ids.Validate(pid, ids.PrefixProject); err != nil {
			add("%s: key is not a project id: %v", where, err)
		}
		if ro.Org == "" {
			add("%s: org is empty", where)
		} else if c.FindOrg(ro.Org) == nil {
			add("%s: org %q is not an entry under orgs: (have: %s)", where, ro.Org, strings.Join(c.OrgNames(), ", "))
		}
		for k, r := range ro.Remotes {
			if _, err := repourl.Normalize(r); err != nil {
				add("%s.remotes[%d] %q: %v", where, k, r, err)
			}
		}
	}
	return errors.Join(errs...)
}

// validate checks one org entry; messages are relative to the entry.
func (o *Org) validate() []error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if err := validateOrgName(o.Name); err != nil {
		add("name: %v", err)
	}
	if k := o.Identity.Key; k != "" {
		if err := ValidateHumanKey(k); err != nil {
			add("identity.key %q: %v", k, err)
		}
	}
	if h := o.Identity.Handle; h != "" && !handleRe.MatchString(h) {
		add("identity.handle %q: use lowercase letters, digits, '_' or '-' (no '@')", h)
	}

	orgFolders := 0
	stores := map[string]int{}
	for i, s := range o.Stores {
		w := fmt.Sprintf("stores[%d]", i)
		n, err := repourl.Normalize(s.URL)
		if err != nil {
			add("%s.url %q: %v", w, s.URL, err)
		}
		if s.Branch != "" {
			if err := validateBranchName(s.Branch, true); err != nil {
				add("%s.branch %q: %v", w, s.Branch, err)
			}
		}
		key := n + "@" + s.EffectiveBranch()
		if j, dup := stores[key]; dup && n != "" {
			add("%s: same store and branch as stores[%d]", w, j)
		}
		stores[key] = i
		if s.OrgFolder != "" {
			orgFolders++
			if s.OrgFolder != OrgFolderRoot {
				if err := ValidateFolderName(s.OrgFolder); err != nil {
					add("%s.org_folder %q: %v", w, s.OrgFolder, err)
				}
			}
		}
	}
	if orgFolders > 1 {
		add("stores: org_folder is set on %d stores; at most one store per entry holds the roster and company rules", orgFolders)
	}

	for i, p := range o.Own {
		if _, err := repourl.Compile(p); err != nil {
			add("own[%d] %q: %v", i, p, err)
		}
	}
	for i, p := range o.Repos {
		if _, err := repourl.Compile(p); err != nil {
			add("repos[%d] %q: %v", i, p, err)
		}
	}

	for kind, target := range o.Defaults.ClientRepos {
		w := fmt.Sprintf("defaults.client_repos.%s", kind)
		if !kindRe.MatchString(kind) {
			add("%s: kind must be a lowercase name such as task or memory", w)
		}
		if err := validateClientTarget(o.Name, target); err != nil {
			add("%s %q: %v", w, target, err)
		}
	}

	switch o.AgentDetails {
	case "", AgentDetailsInternal, AgentDetailsClient:
	default:
		add("agent_details %q: must be %q or %q", o.AgentDetails, AgentDetailsInternal, AgentDetailsClient)
	}
	if o.Live != nil {
		errs = append(errs, prefixAll("live", o.Live.validate())...)
	}
	if o.Lease != nil {
		errs = append(errs, prefixAll("lease", o.Lease.validate())...)
	}
	if o.Agents != nil {
		errs = append(errs, prefixAll("agents", o.Agents.validate())...)
	}
	if o.MergeCheck != nil {
		errs = append(errs, prefixAll("merge_check", o.MergeCheck.validate())...)
	}
	return errs
}

func (l *Live) validate() []error {
	var errs []error
	for k := range l.Defaults {
		if !kindRe.MatchString(k) {
			errs = append(errs, fmt.Errorf("defaults.%s: kind must be a lowercase table name such as tasks", k))
		}
	}
	if l.MaxAge != nil && l.MaxAge.D() <= 0 {
		errs = append(errs, errors.New("max_age: must be greater than zero (a live row needs a way out, fixed rule 6)"))
	}
	if l.CompactAfter != nil && *l.CompactAfter < 1 {
		errs = append(errs, errors.New("compact_after: must be at least 1"))
	}
	if l.Remote != "" && strings.ContainsAny(l.Remote, " \t/:") {
		errs = append(errs, fmt.Errorf("remote %q: must be a git remote name such as origin", l.Remote))
	}
	return errs
}

func (l *Lease) validate() []error {
	var errs []error
	if l.TTL != nil && l.TTL.D() <= 0 {
		errs = append(errs, errors.New("ttl: must be greater than zero"))
	}
	if l.RenewEvery != nil && l.RenewEvery.D() <= 0 {
		errs = append(errs, errors.New("renew_every: must be greater than zero"))
	}
	if l.TTL != nil && l.RenewEvery != nil && l.RenewEvery.D() >= l.TTL.D() {
		errs = append(errs, errors.New("renew_every: must be shorter than ttl, or every claim expires before it is renewed"))
	}
	if l.PushRetries != nil && *l.PushRetries < 0 {
		errs = append(errs, errors.New("push_retries: may not be negative"))
	}
	return errs
}

func (a *Agents) validate() []error {
	var errs []error
	if a.MaxClaimsPerAgent != nil && *a.MaxClaimsPerAgent < 1 {
		errs = append(errs, errors.New("max_claims_per_agent: must be at least 1"))
	}
	if a.MaxAttempts != nil && *a.MaxAttempts < 1 {
		errs = append(errs, errors.New("max_attempts: must be at least 1"))
	}
	return errs
}

func (m *MergeCheck) validate() []error {
	switch m.Adapter {
	case "", MergeCheckNone, MergeCheckGitHub:
		return nil
	default:
		return []error{fmt.Errorf("adapter %q: must be %q or %q", m.Adapter, MergeCheckNone, MergeCheckGitHub)}
	}
}

// validateOrgName checks an org entry name (also a personal-store folder and
// a --to target).
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
	return nil
}

// ValidateFolderName checks one store project folder name (fixed rule 3:
// `_`-prefixed names are reserved for lore's own folders in a live ref).
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

// validateBranchName applies git's ref-name rules that matter here, plus the
// store rule that a branch may not contain "/" (fixed rule 3).
func validateBranchName(b string, store bool) error {
	switch {
	case store && strings.Contains(b, "/"):
		return errors.New(`a store branch may not contain "/" (refs/lore/live/<branch> must never clash with another branch)`)
	case strings.HasPrefix(b, "-"), strings.HasPrefix(b, "."), strings.HasSuffix(b, "."),
		strings.HasSuffix(b, ".lock"), strings.Contains(b, ".."), strings.Contains(b, "@{"),
		strings.ContainsAny(b, " \t~^:?*[\\"), strings.HasPrefix(b, "/"), strings.HasSuffix(b, "/"):
		return errors.New("not a valid git branch name")
	}
	return nil
}

// validateClientTarget checks one defaults.client_repos value: TargetRepo,
// the entry's own name, or "<entry>/<folder>". Another org's name is
// refused: a default may only point inside the org that resolved (F14).
func validateClientTarget(orgName, target string) error {
	if target == TargetRepo {
		return nil
	}
	org, folder, hasFolder := strings.Cut(target, refSep)
	if org != orgName {
		return fmt.Errorf("must be %q, %q or %q/<folder>; another org cannot be a default", TargetRepo, orgName, orgName)
	}
	if hasFolder {
		return ValidateFolderName(folder)
	}
	return nil
}

// prefixAll prefixes each error with where (no-op for an empty list).
func prefixAll(where string, errs []error) []error {
	out := make([]error, 0, len(errs))
	for _, e := range errs {
		out = append(out, fmt.Errorf("%s: %w", where, e))
	}
	return out
}

// isFolderStoreURL accepts a 0.1.18 folder-store url as personal.store
// (`lore store init me` writes file://<home>/.lore/stores/me): scheme file,
// no host (or localhost), an absolute clean path, and no query, fragment
// or userinfo. Anything else keeps the git-url check.
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
