// Package orgresolve decides which single org entry of ~/.lore/config.yaml
// applies to the repo a command runs in, and records why (LORE_TEAM_SPEC
// F10, TEAM-009, TEAM-022, TEAM-023).
//
// The decision is a pure function, Resolve(Inputs) → Result: no git, no
// files, no network. The caller gathers the inputs (LORE_ORG, the project
// id, normalised remotes, the committed `.lore/org.yaml`, the link answer)
// and prints the Result. That keeps every rule testable as a table and lets
// `lore explain org` print the same trace the decision used.
//
// Order (the first rule that answers wins):
//
//  1. LORE_ORG=<entry>
//  2. repo_orgs[<project id>] (`lore org use`), checked against the remote
//  3. committed `.lore/org.yaml`, only in an own repo not linked as a client
//  4. a store link to this project id AND remote (staging entries skipped)
//  5. the remote matches an entry's own: or repos: patterns (staging skipped)
//  6. none: no org
//
// Invariant: one repo resolves to at most one entry; two entries at the
// same rule is an error, never a guess.
package orgresolve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// Step is the F10 rule that decided.
type Step int

// The rules, numbered as in the spec so messages can say "rule 3".
const (
	StepEnv       Step = 1
	StepRepoOrgs  Step = 2
	StepCommitted Step = 3
	StepLink      Step = 4
	StepPattern   Step = 5
	StepNone      Step = 6
	// StepSetup is the active setup's org used when rules 1–5 name none
	// (ONBOARDING_DESIGN 3.3, Q4: like LORE_ORG, but below every other
	// source). Numbered after the F10 rules so their numbers stay; it is
	// traced before rule 6 and only when a setup names an org.
	StepSetup Step = 7
	// StepMovedLink is a CLIENT link naming this project id at a remote
	// this repo no longer has, used when rules 1–5 name no org: the client
	// repo moved (renamed, transferred) and its clone followed with `git
	// remote set-url`. Such a link never routes a write (it is not a link
	// of this remote, F11), but the repo keeps its leak guards (fail
	// closed) until the person re-links it here or, for a copy that is a
	// different project, gives it a new id (`lore project reid`).
	StepMovedLink Step = 8
)

// String is the short name printed after "matched by:".
func (s Step) String() string {
	switch s {
	case StepEnv:
		return "LORE_ORG"
	case StepRepoOrgs:
		return "lore org use"
	case StepCommitted:
		return ".lore/org.yaml"
	case StepLink:
		return "store link"
	case StepPattern:
		return "repo pattern"
	case StepSetup:
		return "setup"
	case StepMovedLink:
		return "store link at an old remote"
	default:
		return "nothing matched"
	}
}

// Role is what the repo is to the resolved org (F10 `repo_role`).
type Role string

const (
	// RoleClient is a client's repo; leak guards apply.
	RoleClient Role = "client"
	// RoleOwn is the org's own repo; no client guards.
	RoleOwn Role = "own"
	// RoleNone is no org resolved.
	RoleNone Role = "none"
)

// Outcome of one rule in the trace.
type Outcome string

// The trace outcomes, shown verbatim by `lore explain org`.
const (
	// OutcomeHit: the rule decided the org.
	OutcomeHit Outcome = "hit"
	// OutcomeMiss: the rule ran and did not apply.
	OutcomeMiss Outcome = "miss"
	// OutcomeNotReached: an earlier rule already decided.
	OutcomeNotReached Outcome = "not reached"
)

// RuleTrace is one line of `lore explain org`.
type RuleTrace struct {
	Step    Step
	Outcome Outcome
	Reason  string
}

// Inputs are everything the decision reads. Gather them with the CLI's
// executor; build them by hand in tests.
type Inputs struct {
	// Config is the loaded ~/.lore/config.yaml (never nil; empty is fine).
	Config *orgconf.Config
	// EnvOrg is LORE_ORG ("" = unset).
	EnvOrg string
	// ProjectID is this project's id from .lore/data/_meta.json ("" when
	// the project has none yet; then rule 2 cannot match).
	ProjectID string
	// Remotes are the repo's remotes, already normalised (repourl.Normalize),
	// deduplicated. Empty for a repo with no remote.
	Remotes []string
	// Committed is the parsed `.lore/org.yaml`, nil when absent.
	Committed *orgconf.OrgFile
	// Links is the link source's answer for (ProjectID, Remotes).
	Links LinkResult
	// StoreDefaultBranch returns a store's default branch by normalised URL;
	// nil means orgconf.DefaultStoreBranch for every store. An entry whose
	// store branch differs from it is a staging entry (TEAM-022).
	StoreDefaultBranch func(storeURL string) string
	// Setup is the active setup's org hint (ONBOARDING_DESIGN 3.3/3.4);
	// zero when no setup is active or it names no org.
	Setup SetupHint
}

// SetupHint is the org of the active setup (member/admin) and why that
// setup is active, for the trace and the E_SETUP_ORG_MISMATCH text.
type SetupHint struct {
	// Name is the setup's name.
	Name string
	// Org is its orgs[] entry ("" = no hint).
	Org string
	// Why says how the setup was chosen ("folder ~/www/projects/agency/**").
	Why string
}

// SetupMismatch is set when rules 1–5 resolve another org than the active
// setup's (3.3 "Disagreement"): writes are refused, reads still work.
type SetupMismatch struct {
	// Setup is the active setup and its org.
	Setup SetupHint
	// RepoOrg is the org the repo resolved to.
	RepoOrg string
	// Source is the rule that resolved it (Step.String()).
	Source string
	// Message is the E_SETUP_ORG_MISMATCH text (7.2).
	Message string
}

// Disagreement is set when an explicit choice (rule 1 or 2) names another
// org than the store links do. Org-scoped writes are refused with
// E_ORG_DISAGREES unless the write names its target with --to <org> (F10).
type Disagreement struct {
	Chosen  string
	Linked  []string
	ByRule  Step
	Message string
}

// Result is the decision.
type Result struct {
	// Org is the resolved entry; nil means no org (rule 6).
	Org *orgconf.Org
	// Step is the rule that decided.
	Step Step
	// MatchedBy is the one-line reason printed by whoami.
	MatchedBy string
	// Role is client, own or none.
	Role Role
	// LinkStatus is the link source's status, carried for display.
	LinkStatus LinkStatus
	// LinkDetail is the link source's one-line detail.
	LinkDetail string
	// LinkUnknown: an org resolved but whether the repo is linked as a
	// client could not be checked. Writes without --to fail closed.
	LinkUnknown bool
	// Links are the links for the resolved entry (several = a group repo).
	Links []Link
	// MovedLink is the client link at an old remote that decided the org
	// (StepMovedLink): its folder is read (rows, leak index), never a
	// write target, until the repo is linked at its new remote.
	MovedLink *Link
	// Disagreement is non-nil when an explicit choice contradicts a link.
	Disagreement *Disagreement
	// SetupMismatch is non-nil when the active setup names another org.
	SetupMismatch *SetupMismatch
	// Trace lists every rule in order with its outcome.
	Trace []RuleTrace
	// Warnings are printed once by whoami / explain (e.g. a fork).
	Warnings []string
}

// OrgName returns the resolved entry's name, or "".
func (r Result) OrgName() string {
	if r.Org == nil {
		return ""
	}
	return r.Org.Name
}

// Resolve runs F10. On a refusal (ambiguous match, unknown LORE_ORG entry)
// it returns the partial Result — its Trace is still what `lore explain org`
// prints — together with an *Error.
func Resolve(in Inputs) (Result, error) {
	cfg := in.Config
	if cfg == nil {
		cfg = &orgconf.Config{}
	}
	r := Result{Role: RoleNone, LinkStatus: in.Links.Status, LinkDetail: in.Links.Detail}
	staging := stagingSet(cfg, in.StoreDefaultBranch)

	// Links from staging entries are never used for MATCHING (TEAM-022):
	// rule 4 sees only `links`. They are still attached to the result when
	// the staging entry is chosen on purpose (rule 1 or 2), so `--to
	// <staging entry>` finds the folder linked on the staging branch
	// (cookbook 13). A LinkSource may therefore return staging links.
	var links, known []Link
	for _, l := range in.Links.Links {
		if cfg.FindOrg(l.Entry) == nil {
			continue
		}
		known = append(known, l)
		if !staging[l.Entry] {
			links = append(links, l)
		}
	}
	linkedEntries := entriesOf(links)

	decided := false
	trace := func(s Step, o Outcome, reason string) {
		r.Trace = append(r.Trace, RuleTrace{Step: s, Outcome: o, Reason: reason})
	}
	notReached := func(s Step) { trace(s, OutcomeNotReached, "an earlier rule decided") }
	choose := func(o *orgconf.Org, s Step, why string, role Role) {
		r.Org, r.Step, r.MatchedBy, r.Role = o, s, why, role
		r.Links = linksFor(known, o.Name)
		decided = true
	}

	// ── Rule 1: LORE_ORG ───────────────────────────────────────────────
	if in.EnvOrg != "" {
		o := cfg.FindOrg(in.EnvOrg)
		if o == nil {
			trace(StepEnv, OutcomeMiss, fmt.Sprintf("LORE_ORG=%s names no entry", in.EnvOrg))
			return r, &Error{Kind: KindUnknownEntry,
				Message:    fmt.Sprintf("LORE_ORG=%s names no entry under orgs: in ~/.lore/config.yaml", in.EnvOrg),
				Hint:       "unset LORE_ORG or use one of the entries listed",
				Candidates: cfg.OrgNames()}
		}
		trace(StepEnv, OutcomeHit, fmt.Sprintf("LORE_ORG=%s", in.EnvOrg))
		choose(o, StepEnv, "LORE_ORG="+in.EnvOrg, explicitRole(o, links, in.Remotes))
	} else {
		trace(StepEnv, OutcomeMiss, "LORE_ORG is not set")
	}

	// ── Rule 2: repo_orgs (lore org use) ───────────────────────────────
	switch {
	case decided:
		notReached(StepRepoOrgs)
	case in.ProjectID == "":
		trace(StepRepoOrgs, OutcomeMiss, "this project has no project id yet (.lore/data/_meta.json)")
	default:
		ro, ok := cfg.RepoOrgs[in.ProjectID]
		switch {
		case !ok:
			trace(StepRepoOrgs, OutcomeMiss, fmt.Sprintf("no `lore org use` choice for %s", in.ProjectID))
		case len(ro.Remotes) > 0 && !anyIn(in.Remotes, ro.Remotes):
			msg := fmt.Sprintf("`lore org use --entry %s` was recorded for %s, but this clone's remotes are %s (a fork carrying the upstream's project id?); run `lore org use` here to confirm",
				ro.Org, strings.Join(ro.Remotes, ", "), listOrNone(in.Remotes))
			trace(StepRepoOrgs, OutcomeMiss, msg)
			r.Warnings = append(r.Warnings, msg)
		default:
			o := cfg.FindOrg(ro.Org) // Validate guarantees it exists
			if o == nil {
				trace(StepRepoOrgs, OutcomeMiss, fmt.Sprintf("repo_orgs names %q, which is not an entry", ro.Org))
				break
			}
			trace(StepRepoOrgs, OutcomeHit, fmt.Sprintf("repo_orgs[%s] = %s", in.ProjectID, ro.Org))
			choose(o, StepRepoOrgs, "lore org use (this machine)", explicitRole(o, links, in.Remotes))
		}
	}

	// ── Rule 3: committed .lore/org.yaml (own repos only) ──────────────
	switch {
	case decided:
		notReached(StepCommitted)
	case in.Committed == nil:
		trace(StepCommitted, OutcomeMiss, "no .lore/org.yaml")
	default:
		name := in.Committed.Org
		o := cfg.FindOrg(name)
		switch {
		case o == nil:
			trace(StepCommitted, OutcomeMiss, fmt.Sprintf(".lore/org.yaml names %q, which is not an entry in your config", name))
		case len(in.Remotes) == 0:
			trace(StepCommitted, OutcomeMiss, fmt.Sprintf(".lore/org.yaml names %s, but this repo has no remote, so it cannot be shown to be one of %s's own repos", name, name))
		case !matchesAny(o.Own, in.Remotes):
			trace(StepCommitted, OutcomeMiss, fmt.Sprintf(".lore/org.yaml names %s, but no remote (%s) matches its own: patterns; ignored (only an org's own repos may name it)", name, strings.Join(in.Remotes, ", ")))
		case hasClientLink(links):
			msg := fmt.Sprintf(".lore/org.yaml names %s, but a store links this repo as a CLIENT repo; refused (a client repo cannot steer the guards)", name)
			trace(StepCommitted, OutcomeMiss, msg)
			r.Warnings = append(r.Warnings, msg)
		default:
			trace(StepCommitted, OutcomeHit, fmt.Sprintf(".lore/org.yaml names %s and the remote matches its own: patterns", name))
			choose(o, StepCommitted, ".lore/org.yaml (committed, own repo)", RoleOwn)
		}
	}

	// ── Rule 4: store links ────────────────────────────────────────────
	switch {
	case decided:
		notReached(StepLink)
	case in.Links.Status == LinkStatusNotChecked:
		trace(StepLink, OutcomeMiss, nonEmpty(in.Links.Detail, notCheckedDetail))
	case len(linkedEntries) == 0:
		trace(StepLink, OutcomeMiss, fmt.Sprintf("no store links this project id at these remotes (links %s)", in.Links.Status))
	case len(linkedEntries) > 1:
		trace(StepLink, OutcomeMiss, fmt.Sprintf("linked by %d entries: %s", len(linkedEntries), strings.Join(linkedEntries, ", ")))
		return r, &Error{Kind: KindAmbiguous,
			Message:    fmt.Sprintf("this repo is linked by %d org entries at once (%s)", len(linkedEntries), strings.Join(linkedEntries, ", ")),
			Hint:       "choose one with `lore org use --entry <entry>` (or LORE_ORG=<entry> for one command, e.g. `lore init` before the project has an id)",
			Candidates: linkedEntries}
	default:
		o := cfg.FindOrg(linkedEntries[0])
		ls := linksFor(links, o.Name)
		role := RoleClient
		if allOwn(ls) {
			role = RoleOwn
		}
		trace(StepLink, OutcomeHit, fmt.Sprintf("linked in %s (%s)", o.Name, describeLinks(ls)))
		choose(o, StepLink, fmt.Sprintf("link in %s, project id + remote", describeLinks(ls)), role)
	}

	// ── Rule 5: own: / repos: patterns ─────────────────────────────────
	if decided {
		notReached(StepPattern)
	} else {
		hits := map[string]Role{}
		hitPattern := map[string]string{} // org → the pattern that matched, for "matched by"
		for i := range cfg.Orgs {
			o := &cfg.Orgs[i]
			if staging[o.Name] {
				continue
			}
			if p := firstMatch(o.Repos, in.Remotes); p != "" {
				hits[o.Name], hitPattern[o.Name] = RoleClient, p // fail closed: client wins over own
			} else if p := firstMatch(o.Own, in.Remotes); p != "" {
				hits[o.Name], hitPattern[o.Name] = RoleOwn, p
			}
		}
		names := sortedKeys(hits)
		switch {
		case len(in.Remotes) == 0:
			trace(StepPattern, OutcomeMiss, "this repo has no remote")
		case len(names) == 0:
			trace(StepPattern, OutcomeMiss, fmt.Sprintf("no entry's own: or repos: pattern matches %s", strings.Join(in.Remotes, ", ")))
		case len(names) > 1:
			trace(StepPattern, OutcomeMiss, fmt.Sprintf("patterns of %d entries match: %s", len(names), strings.Join(names, ", ")))
			return r, &Error{Kind: KindAmbiguous,
				Message:    fmt.Sprintf("the patterns of %d org entries match this repo (%s)", len(names), strings.Join(names, ", ")),
				Hint:       "choose one with `lore org use --entry <entry>` (or LORE_ORG=<entry> for one command, e.g. `lore init` before the project has an id), or narrow the own:/repos: patterns",
				Candidates: names}
		default:
			o := cfg.FindOrg(names[0])
			kind := "repos"
			if hits[o.Name] == RoleOwn {
				kind = "own"
			}
			trace(StepPattern, OutcomeHit, fmt.Sprintf("%s's %s: pattern %s matches %s", o.Name, kind, hitPattern[o.Name], strings.Join(in.Remotes, ", ")))
			// Spec §F10 whoami example: "matched by: own pattern github.com/betalabs/*".
			choose(o, StepPattern, fmt.Sprintf("%s pattern %s of %s", kind, hitPattern[o.Name], o.Name), hits[o.Name])
		}
	}

	// ── Moved client link (fail closed) ────────────────────────────────
	// No rule above named an org, yet a store links this very project id
	// as a CLIENT repo at another remote: the repo moved, or this is a copy
	// of a client repo. Either way it holds the client's code, so the
	// guards stay on; the old link routes nothing (F11).
	if !decided {
		if o, l, ok := movedClientLink(cfg, staging, in.Links.Moved, in.ProjectID); ok {
			msg := fmt.Sprintf("this project id is linked as a CLIENT repo of %s at %s, not at this repo's remote (%s); leak guards stay on. If the repo moved, run `lore org link --org %s --project %s` here; if it is a different project, run `lore project reid`",
				o.Name, l.Remote, listOrNone(in.Remotes), o.Name, l.Folder)
			trace(StepMovedLink, OutcomeHit, msg)
			r.Warnings = append(r.Warnings, msg)
			choose(o, StepMovedLink, fmt.Sprintf("link in %s at old remote %s (same project id)", describeLinks([]Link{l}), l.Remote), RoleClient)
			r.MovedLink = &l
		}
	}

	// ── Setup hint (ONBOARDING 3.3, Q4) ────────────────────────────────
	// Below every F10 rule: a store link or a pattern naming another org
	// stays the truth (fail closed: a client repo inside a setup's folder
	// keeps its leak guards), and the clash is recorded as a mismatch.
	if hint := in.Setup; hint.Org != "" {
		o := cfg.FindOrg(hint.Org)
		switch {
		case decided:
			notReached(StepSetup)
			if r.Org.Name != hint.Org && !sameOrg(r.Org, o) {
				r.SetupMismatch = setupMismatch(hint, r.Org.Name, r.Step)
			}
		case o == nil:
			trace(StepSetup, OutcomeMiss, fmt.Sprintf("setup %s names org %q, which is not an entry", hint.Name, hint.Org))
		default:
			trace(StepSetup, OutcomeHit, fmt.Sprintf("setup %s (%s) is org %s", hint.Name, hint.Why, hint.Org))
			choose(o, StepSetup, "setup "+hint.Name+" ("+hint.Why+")", explicitRole(o, links, in.Remotes))
		}
	}

	// ── Rule 6: none ───────────────────────────────────────────────────
	if decided {
		notReached(StepNone)
	} else {
		trace(StepNone, OutcomeHit, "no rule matched: fallback identity, personal everywhere/ notes only")
		r.Step, r.MatchedBy, r.Role = StepNone, "nothing matched", RoleNone
		return r, nil
	}

	// Fail-closed flag: an org resolved, but a store that might link this
	// repo could not be read (F10 conflicts, last row).
	if in.Links.Status == LinkStatusUnknown && r.Step != StepLink {
		r.LinkUnknown = true
	}

	// Explicit choice vs. links (rules 1 and 2 only).
	if (r.Step == StepEnv || r.Step == StepRepoOrgs) && len(linkedEntries) > 0 {
		var others []string
		for _, e := range linkedEntries {
			lo := cfg.FindOrg(e)
			if e != r.Org.Name && !sameOrg(r.Org, lo) {
				others = append(others, e)
			}
		}
		if len(others) > 0 {
			r.Disagreement = &Disagreement{Chosen: r.Org.Name, Linked: others, ByRule: r.Step,
				Message: fmt.Sprintf("%s chose %s, but this repo is linked in %s", r.Step, r.Org.Name, strings.Join(others, ", "))}
			r.Warnings = append(r.Warnings, r.Disagreement.Message+"; org-scoped writes need --to <org>")
		}
	}
	return r, nil
}

// movedClientLink is the first client link (non-staging, known entry) of
// moved: links naming projectID at a remote this repo does not have.
func movedClientLink(cfg *orgconf.Config, staging map[string]bool, moved []Link, projectID string) (*orgconf.Org, Link, bool) {
	if projectID == "" {
		return nil, Link{}, false
	}
	for _, l := range moved {
		o := cfg.FindOrg(l.Entry)
		if o == nil || staging[l.Entry] || l.Own {
			continue
		}
		return o, l, true
	}
	return nil, Link{}, false
}

// DisagreesHint is E_ORG_DISAGREES' hint: what the write commands accept
// now (--force-org is gone from them; the target is named with --to).
const DisagreesHint = "name the org the write goes to with --to <org>, or fix the choice with `lore org use` (or unset LORE_ORG)"

// CheckWrite applies the F10 write-time refusals to an org-scoped write:
// a disagreement needs the target named (--to <org>; forceOrg is the
// override `lore task note` still passes), and an unknown link status needs an
// explicit --to (F14 `link status unknown → E_LINK_UNKNOWN`).
func (r Result) CheckWrite(forceOrg, explicitTo bool) error {
	if err := r.CheckSetupWrite(); err != nil {
		return err
	}
	if r.Disagreement != nil && !forceOrg {
		return &Error{Kind: KindDisagrees, Message: r.Disagreement.Message,
			Hint: DisagreesHint}
	}
	if r.LinkUnknown && !explicitTo {
		return &Error{Kind: KindLinkUnknown,
			Message: "lore cannot tell whether this repo is a client repo: " + nonEmpty(r.LinkDetail, "a store could not be read"),
			Hint:    "retry when the store is reachable, or pass --to repo|<org>|me to choose the target yourself"}
	}
	return nil
}

// CheckSetupWrite is the 3.3 disagreement refusal on its own: every write
// path asks it (wire's writeState), not only routed creates.
func (r Result) CheckSetupWrite() error {
	if m := r.SetupMismatch; m != nil {
		return &Error{Kind: KindSetupMismatch, Message: m.Message,
			Hint: fmt.Sprintf("run with --setup <a setup of %s>, or fix %s", m.RepoOrg, m.Source)}
	}
	return nil
}

// MismatchError is the E_SETUP_ORG_MISMATCH refusal for a setup hint and
// the org a rule resolved (for callers that know the clash but not the
// whole Result).
func MismatchError(hint SetupHint, repoOrg string, step Step) error {
	return Result{SetupMismatch: setupMismatch(hint, repoOrg, step)}.CheckSetupWrite()
}

// setupMismatch builds the 7.2 E_SETUP_ORG_MISMATCH text.
func setupMismatch(hint SetupHint, repoOrg string, step Step) *SetupMismatch {
	return &SetupMismatch{Setup: hint, RepoOrg: repoOrg, Source: step.String(),
		Message: fmt.Sprintf("this repo is org %s (from %s) but setup %s (%s) is org %s; run with --setup <a setup of %s> or fix %s",
			repoOrg, step, hint.Name, hint.Why, hint.Org, repoOrg, step)}
}

// explicitRole is the role for an entry chosen on purpose (rules 1 and 2):
// a link decides; else the patterns; else client, because a repo nobody
// has declared as the org's own is treated as a client's (fail closed).
func explicitRole(o *orgconf.Org, links []Link, remotes []string) Role {
	if ls := linksFor(links, o.Name); len(ls) > 0 {
		if allOwn(ls) {
			return RoleOwn
		}
		return RoleClient
	}
	if matchesAny(o.Repos, remotes) {
		return RoleClient
	}
	if matchesAny(o.Own, remotes) {
		return RoleOwn
	}
	return RoleClient
}

// IsStaging reports whether an entry is a staging entry: one of its stores
// uses a branch other than that store's default branch (TEAM-022).
func IsStaging(o *orgconf.Org, defaultBranch func(storeURL string) string) bool {
	for _, s := range o.Stores {
		def := orgconf.DefaultStoreBranch
		if defaultBranch != nil {
			if n, err := repourl.NormalizeStore(s.URL); err == nil {
				if b := defaultBranch(n); b != "" {
					def = b
				}
			}
		}
		if s.EffectiveBranch() != def {
			return true
		}
	}
	return false
}

func stagingSet(cfg *orgconf.Config, def func(string) string) map[string]bool {
	out := map[string]bool{}
	for i := range cfg.Orgs {
		if IsStaging(&cfg.Orgs[i], def) {
			out[cfg.Orgs[i].Name] = true
		}
	}
	return out
}

// sameOrg: two entries are one org at different store branches when they
// share a store URL (northwind and northwind-stag). Choosing the staging
// entry for a repo linked in the main entry is the intended use of
// LORE_ORG / `lore org use`, not a disagreement.
func sameOrg(a, b *orgconf.Org) bool {
	if a == nil || b == nil {
		return false
	}
	urls := map[string]bool{}
	for _, s := range a.Stores {
		if n, err := repourl.NormalizeStore(s.URL); err == nil {
			urls[n] = true
		}
	}
	for _, s := range b.Stores {
		if n, err := repourl.NormalizeStore(s.URL); err == nil && urls[n] {
			return true
		}
	}
	return false
}

// matchesAny reports whether any pattern matches any remote. Patterns were
// validated when the config loaded; one that fails to compile here matches
// nothing.
func matchesAny(patterns, remotes []string) bool {
	return firstMatch(patterns, remotes) != ""
}

// firstMatch returns the first pattern (in config order) that matches any of
// remotes, or "" when none does. Invalid patterns never match; Validate has
// already reported them.
func firstMatch(patterns, remotes []string) string {
	for _, p := range patterns {
		cp, err := repourl.Compile(p)
		if err != nil {
			continue
		}
		for _, r := range remotes {
			if cp.Match(r) {
				return p
			}
		}
	}
	return ""
}

func entriesOf(links []Link) []string {
	set := map[string]bool{}
	for _, l := range links {
		set[l.Entry] = true
	}
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

func linksFor(links []Link, entry string) []Link {
	var out []Link
	for _, l := range links {
		if l.Entry == entry {
			out = append(out, l)
		}
	}
	return out
}

func hasClientLink(links []Link) bool {
	for _, l := range links {
		if !l.Own {
			return true
		}
	}
	return false
}

func allOwn(links []Link) bool {
	for _, l := range links {
		if !l.Own {
			return false
		}
	}
	return len(links) > 0
}

func describeLinks(ls []Link) string {
	parts := make([]string, 0, len(ls))
	for _, l := range ls {
		p := l.StoreURL + " " + l.Folder + "/"
		if l.Default {
			p += " (default)"
		}
		if l.Own {
			p += " (own)"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

func anyIn(have, want []string) bool {
	set := map[string]bool{}
	for _, w := range want {
		if n, err := repourl.Normalize(w); err == nil {
			set[n] = true
		}
	}
	for _, h := range have {
		if set[h] {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]Role) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func listOrNone(xs []string) string {
	if len(xs) == 0 {
		return "(none)"
	}
	return strings.Join(xs, ", ")
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
