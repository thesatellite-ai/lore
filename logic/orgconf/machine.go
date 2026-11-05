// machine.go — the config.yaml keys org resolution does not read but every
// command must refuse when they are broken (P3-CFG-5).
//
// Why here: v0.1.17 loaded the whole file through lorecfg before every
// command, so `live.max_age: 1d12h` stopped reads and writes with
// E_CONFIG_INVALID (CONFIG_GUIDE: nothing in lore reads an invalid
// config). The core loads only this package's Config (orgcfg.LoadConfig on
// the first org hook of every command, both binaries and `lore api`), so
// these keys are declared and checked here with v0.1.17's rules
// (lorecfg/team_validate.go), never read for their values: the readers of
// each setting keep their own defaults.
//
//	Config.Validate ──▶ validateSettings (default_branch, live, lease, agents,
//	                    merge_check, link_index, untrusted_hosts,
//	                    context.profiles; upgrade_notice/auto_upgrade by type)
//	Org.validate    ──▶ live, validateSettings (lease, agents, merge_check)

package orgconf

import (
	"errors"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
)

// MergeCheckAdapter is merge_check.adapter (v0.1.17 lorecfg).
type MergeCheckAdapter string

// merge_check.adapter values.
const (
	// MergeCheckNone relies on commits and trailers only.
	MergeCheckNone MergeCheckAdapter = "none"
	// MergeCheckGitHub also asks `gh` about merged pull requests.
	MergeCheckGitHub MergeCheckAdapter = "github"
)

// liveRemoteForbidden are characters a git remote name never holds.
const liveRemoteForbidden = " \t/:"

// Settings are the top-level config.yaml keys validated, not read, here.
type Settings struct {
	DefaultBranch  string     `yaml:"default_branch,omitempty"`
	Lease          Lease      `yaml:"lease,omitempty"`
	Agents         Agents     `yaml:"agents,omitempty"`
	MergeCheck     MergeCheck `yaml:"merge_check,omitempty"`
	UntrustedHosts []string   `yaml:"untrusted_hosts,omitempty"`
	Context        Context    `yaml:"context,omitempty"`
	// UpgradeNotice and AutoUpgrade are checked by type only (a bool).
	UpgradeNotice *bool `yaml:"upgrade_notice,omitempty"`
	AutoUpgrade   *bool `yaml:"auto_upgrade,omitempty"`
}

// OrgSettings are an org entry's overrides validated, not read, here.
type OrgSettings struct {
	Lease      *Lease      `yaml:"lease,omitempty"`
	Agents     *Agents     `yaml:"agents,omitempty"`
	MergeCheck *MergeCheck `yaml:"merge_check,omitempty"`
}

// Lease is the `lease:` block (claim timing, F7).
type Lease struct {
	TTL         *Duration `yaml:"ttl,omitempty"`
	RenewEvery  *Duration `yaml:"renew_every,omitempty"`
	Grace       *Duration `yaml:"grace,omitempty"`
	PushRetries *int      `yaml:"push_retries,omitempty"`
}

// Agents is the `agents:` block (claim limits).
type Agents struct {
	MaxClaimsPerAgent *int `yaml:"max_claims_per_agent,omitempty"`
	MaxAttempts       *int `yaml:"max_attempts,omitempty"`
}

// MergeCheck is the `merge_check:` block.
type MergeCheck struct {
	Adapter MergeCheckAdapter `yaml:"adapter,omitempty"`
}

// Context is the `context:` block; only profiles is validated here.
type Context struct {
	Profiles []toolprofile.Profile `yaml:"profiles,omitempty"`
}

// validateSettings checks the top-level settings; messages name the key.
func (c *Config) validateSettings() []error {
	var all []error
	add := func(e error) { all = append(all, e) }
	if b := c.DefaultBranch; b != "" {
		if err := validateGitBranch(b); err != nil {
			add(fmt.Errorf("default_branch %q: %w", b, err))
		}
	}
	all = append(all, prefixAll("live", c.Live.validate())...)
	all = append(all, prefixAll("lease", c.Lease.validate())...)
	all = append(all, prefixAll("agents", c.Agents.validate())...)
	all = append(all, prefixAll("merge_check", c.MergeCheck.validate())...)
	if c.LinkIndex.MaxAge != nil && c.LinkIndex.MaxAge.D() <= 0 {
		add(errors.New("link_index.max_age: must be greater than zero"))
	}
	for i, h := range c.UntrustedHosts {
		if strings.TrimSpace(h) == "" {
			add(fmt.Errorf("untrusted_hosts[%d]: empty pattern", i))
		}
	}
	if len(c.Context.Profiles) > 0 {
		if _, err := toolprofile.New(c.Context.Profiles); err != nil {
			add(fmt.Errorf("context.profiles: %w", err))
		}
	}
	return all
}

// validateSettings checks an org entry's overrides (relative messages).
func (o *Org) validateSettings() []error {
	var all []error
	if o.Lease != nil {
		all = append(all, prefixAll("lease", o.Lease.validate())...)
	}
	if o.Agents != nil {
		all = append(all, prefixAll("agents", o.Agents.validate())...)
	}
	if o.MergeCheck != nil {
		all = append(all, prefixAll("merge_check", o.MergeCheck.validate())...)
	}
	return all
}

// validate checks a live block (v0.1.17 lorecfg.Live.validate).
func (l *Live) validate() []error {
	var all []error
	for k := range l.Defaults {
		if !kindRe.MatchString(k) {
			all = append(all, fmt.Errorf("defaults.%s: kind must be a lowercase table name such as tasks", k))
		}
	}
	if l.MaxAge != nil && l.MaxAge.D() <= 0 {
		all = append(all, errors.New("max_age: must be greater than zero (a live row needs a way out, fixed rule 6)"))
	}
	if l.CompactAfter != nil && *l.CompactAfter < 1 {
		all = append(all, errors.New("compact_after: must be at least 1"))
	}
	if l.Remote != "" && strings.ContainsAny(l.Remote, liveRemoteForbidden) {
		all = append(all, fmt.Errorf("remote %q: must be a git remote name such as origin", l.Remote))
	}
	return all
}

// validate checks a lease block (renew_every must be shorter than ttl).
func (l *Lease) validate() []error {
	var all []error
	if l.TTL != nil && l.TTL.D() <= 0 {
		all = append(all, errors.New("ttl: must be greater than zero"))
	}
	if l.RenewEvery != nil && l.RenewEvery.D() <= 0 {
		all = append(all, errors.New("renew_every: must be greater than zero"))
	}
	if l.TTL != nil && l.RenewEvery != nil && l.RenewEvery.D() >= l.TTL.D() {
		all = append(all, errors.New("renew_every: must be shorter than ttl, or every claim expires before it is renewed"))
	}
	if l.PushRetries != nil && *l.PushRetries < 0 {
		all = append(all, errors.New("push_retries: may not be negative"))
	}
	return all
}

// validate checks an agents block.
func (a *Agents) validate() []error {
	var all []error
	if a.MaxClaimsPerAgent != nil && *a.MaxClaimsPerAgent < 1 {
		all = append(all, errors.New("max_claims_per_agent: must be at least 1"))
	}
	if a.MaxAttempts != nil && *a.MaxAttempts < 1 {
		all = append(all, errors.New("max_attempts: must be at least 1"))
	}
	return all
}

// validate checks a merge_check block.
func (m *MergeCheck) validate() []error {
	switch m.Adapter {
	case "", MergeCheckNone, MergeCheckGitHub:
		return nil
	default:
		return []error{fmt.Errorf("adapter %q: must be %q or %q", m.Adapter, MergeCheckNone, MergeCheckGitHub)}
	}
}

// validateGitBranch applies git's ref-name rules to a code-repo branch
// (default_branch may contain "/", unlike a store branch).
func validateGitBranch(b string) error {
	switch {
	case strings.HasPrefix(b, "-"), strings.HasPrefix(b, "."), strings.HasSuffix(b, "."),
		strings.HasSuffix(b, ".lock"), strings.Contains(b, ".."), strings.Contains(b, "@{"),
		strings.ContainsAny(b, " \t~^:?*[\\"), strings.HasPrefix(b, "/"), strings.HasSuffix(b, "/"):
		return errors.New("not a valid git branch name")
	}
	return nil
}
