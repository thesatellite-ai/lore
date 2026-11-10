package wire

// setupactive.go — which setup is active here, and what that changes
// (ONBOARDING_DESIGN 3.3 and 3.4).
//
// 3.3, the first answer wins:
//
//	1 --setup <name>      op.SetupOf(ctx) (CLI global flag, REST X-Lore-Setup)
//	2 LORE_SETUP=<name>   this shell
//	3 repo_setups pin     `lore setup use --here` (keyed by the repo id)
//	4 folder match        git toplevel, else the directory; most specific glob
//	5 org match           the org the repo resolves to → its one setup
//	6 default: true
//	7 none                lore behaves exactly as before
//
// (The design lists the repo pin under "this repo"; it sits after the
// shell and before folders because it is an explicit choice for one repo.)
//
// 3.4 effects wired from here:
//
//	identity  a solo setup's identity: between the org entry and the
//	          top-level identity (adapters/identity, resolveIdentity)
//	org       a member/admin setup's org is a hint below every F10 rule
//	          (OrgResolution.Situation → orgresolve.SetupHint, Q4); another
//	          org from a stronger source refuses writes with
//	          E_SETUP_ORG_MISMATCH (setupWriteGate, called by writeState)
//	me        personal_store: a url replaces personal.store; none makes
//	          `--to me` E_SETUP_NO_PERSONAL_STORE (setupPersonal)
//	git       lore's own git calls to a store a setup owns get that
//	          setup's ssh host alias (Q5, storeRoute in storeroute.go)

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opswhoami "github.com/thesatellite-ai/lore/ops/whoami"
)

// Outcomes of one 3.3 step in `lore whoami --explain` (the same words as
// the org trace, orgresolve.Outcome*, in snake_case per 10.11).
const (
	SetupStepHit        = "hit"
	SetupStepMiss       = "miss"
	SetupStepNotReached = "not_reached"
	SetupStepRefused    = "refused"
)

// SetupExplainStep is one line of the setup trace (whoami --explain).
type SetupExplainStep struct {
	// Step is the 3.3 step, an ops/setup Why* value.
	Step string `json:"step"`
	// Outcome is hit, miss, not_reached or refused.
	Outcome string `json:"outcome"`
	// Detail says why ("LORE_SETUP is not set", the matching glob, …).
	Detail string `json:"detail,omitempty"`
}

// SetupResolution is the 3.3 decision.
type SetupResolution struct {
	// Active is the setup in effect (WhyNone with no name when none).
	Active opssetup.ActiveSetup `json:"active"`
	// Entry is that setup's config entry (zero when none).
	Entry onboard.SetupEntry `json:"-"`
	// Folder is the folder compared with the globs (git toplevel or dir).
	Folder string `json:"folder,omitempty"`
	// Steps is every step in order with its outcome.
	Steps []SetupExplainStep `json:"steps"`
}

// SetupQuery is everything ResolveSetup reads; every field is a seam.
type SetupQuery struct {
	// Home is the lore home (~/.lore).
	Home string
	// Dir is the folder the command runs in.
	Dir string
	// Flag is --setup / X-Lore-Setup ("" = not given).
	Flag string
	// Getenv reads LORE_SETUP (os.Getenv when nil).
	Getenv func(string) string
	// UserHome expands ~ in folder globs (os.UserHomeDir when "").
	UserHome string
	// TopLevel is the git toplevel of dir ("" = not a repo); nil runs git.
	TopLevel func(ctx context.Context, dir string) string
	// RepoID is the repo id of the toplevel ("" = none); nil reads
	// .lore/data/_meta.json.
	RepoID func(top string) string
	// Resolve resolves symlinks (filepath.EvalSymlinks when nil).
	Resolve onboard.Resolver
	// OrgOf is the org the repo resolves to WITHOUT a setup hint, for step
	// 5; nil skips the step (traced as a miss). Called only when needed.
	OrgOf func(ctx context.Context) (string, error)
}

// ResolveSetup runs 3.3. On a refusal (a name that is no setup, a folder
// or org tie) it returns the partial resolution (its Steps are still what
// --explain prints) together with E_SETUP_NOT_FOUND or E_SETUP_AMBIGUOUS.
// With no setups configured it answers none without running git.
func ResolveSetup(ctx context.Context, q SetupQuery) (SetupResolution, error) {
	cfg, err := ReadSetupsConfig(q.Home)
	if err != nil {
		return none(), err
	}
	res := SetupResolution{}
	done := false
	trace := func(step, outcome, detail string) {
		res.Steps = append(res.Steps, SetupExplainStep{Step: step, Outcome: outcome, Detail: detail})
	}
	choose := func(e onboard.SetupEntry, why, detail string) {
		res.Active, res.Entry, done = opssetup.ActiveSetup{Name: e.Name, Why: why, Detail: detail}, e, true
		trace(why, SetupStepHit, detail)
	}
	refuse := func(step, detail string, err error) (SetupResolution, error) {
		trace(step, SetupStepRefused, detail)
		res.Active = opssetup.ActiveSetup{Why: opssetup.WhyNone}
		return res, err
	}
	byName := func(name string) (onboard.SetupEntry, error) {
		if e, ok := cfg.Setup(name); ok {
			if e.Disabled {
				return onboard.SetupEntry{}, setupDisabled(name)
			}
			return e, nil
		}
		return onboard.SetupEntry{}, setupNotFound(name, cfg.Setups)
	}
	// A disabled setup matches nothing (owner decision 2026-11-06): the
	// folder, org and default steps see only the enabled ones.
	enabled := cfg.Enabled()

	// 1. --setup
	if name := strings.TrimSpace(q.Flag); name != "" {
		e, nerr := byName(name)
		if nerr != nil {
			return refuse(opssetup.WhyFlag, "--setup "+name+" names no setup", nerr)
		}
		choose(e, opssetup.WhyFlag, "--setup "+name)
	} else {
		trace(opssetup.WhyFlag, SetupStepMiss, "--setup not given")
	}

	// 2. LORE_SETUP
	getenv := q.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	switch env := strings.TrimSpace(getenv(opssetup.EnvSetup)); {
	case done:
		trace(opssetup.WhyEnv, SetupStepNotReached, "")
	case env == "":
		trace(opssetup.WhyEnv, SetupStepMiss, opssetup.EnvSetup+" is not set")
	default:
		e, nerr := byName(env)
		if nerr != nil {
			return refuse(opssetup.WhyEnv, opssetup.EnvSetup+"="+env+" names no setup", nerr)
		}
		choose(e, opssetup.WhyEnv, opssetup.EnvSetup+"="+env)
	}

	if !done && len(enabled) == 0 {
		for _, s := range []string{opssetup.WhyRepoPin, opssetup.WhyFolder, opssetup.WhyOrg, opssetup.WhyDefault} {
			trace(s, SetupStepMiss, "no setups on this machine")
		}
		trace(opssetup.WhyNone, SetupStepHit, "lore behaves as without setups")
		res.Active = opssetup.ActiveSetup{Why: opssetup.WhyNone}
		return res, nil
	}

	// The folder: the git toplevel, else the directory itself.
	if !done {
		res.Folder = setupFolder(ctx, q)
	}

	// 3. repo_setups pin
	switch {
	case done:
		trace(opssetup.WhyRepoPin, SetupStepNotReached, "")
	default:
		repoID := ""
		if res.Folder != "" {
			repoID = repoIDOf(q, res.Folder)
		}
		pin := cfg.RepoSetups[repoID]
		switch {
		case repoID == "":
			trace(opssetup.WhyRepoPin, SetupStepMiss, "this folder has no repo id")
		case pin == "":
			trace(opssetup.WhyRepoPin, SetupStepMiss, "no repo_setups pin for "+repoID)
		case !slices.ContainsFunc(enabled, func(e onboard.SetupEntry) bool { return e.Name == pin }) && slices.ContainsFunc(cfg.Setups, func(e onboard.SetupEntry) bool { return e.Name == pin }):
			trace(opssetup.WhyRepoPin, SetupStepMiss, "repo_setups["+repoID+"] names "+pin+", which is disabled")
		default:
			e, nerr := byName(pin)
			if nerr != nil {
				return refuse(opssetup.WhyRepoPin, "repo_setups["+repoID+"] names "+pin+", which is no setup", nerr)
			}
			choose(e, opssetup.WhyRepoPin, repoID)
		}
	}

	// 4. folder match
	if done {
		trace(opssetup.WhyFolder, SetupStepNotReached, "")
	} else {
		fr := onboard.MatchFolder(enabled, res.Folder, userHome(q), setupResolver(q))
		switch {
		case fr.Ambiguous():
			names := make([]string, len(fr.Tie))
			for i, m := range fr.Tie {
				names[i] = m.Setup + " (" + m.Glob + ")"
			}
			return refuse(opssetup.WhyFolder, strings.Join(names, ", "), setupAmbiguousFolder(res.Folder, fr.Tie))
		case fr.Found:
			e, _ := cfg.Setup(fr.Match.Setup)
			choose(e, opssetup.WhyFolder, fr.Match.Glob)
		default:
			trace(opssetup.WhyFolder, SetupStepMiss, "no setup's folders match "+res.Folder)
		}
	}

	// 5. org match
	switch {
	case done:
		trace(opssetup.WhyOrg, SetupStepNotReached, "")
	case q.OrgOf == nil:
		trace(opssetup.WhyOrg, SetupStepMiss, "the org was not checked")
	default:
		org, oerr := q.OrgOf(ctx)
		var of []onboard.SetupEntry
		for _, s := range enabled {
			if org != "" && s.Org == org {
				of = append(of, s)
			}
		}
		switch {
		case oerr != nil:
			trace(opssetup.WhyOrg, SetupStepMiss, "the org could not be resolved: "+oerr.Error())
		case org == "":
			trace(opssetup.WhyOrg, SetupStepMiss, "this repo resolves to no org")
		case len(of) == 0:
			trace(opssetup.WhyOrg, SetupStepMiss, "no setup is for org "+org)
		case len(of) > 1:
			return refuse(opssetup.WhyOrg, "org "+org+": "+of[0].Name+", "+of[1].Name, setupAmbiguousOrg(org, of))
		default:
			choose(of[0], opssetup.WhyOrg, org)
		}
	}

	// 6. default
	if done {
		trace(opssetup.WhyDefault, SetupStepNotReached, "")
	} else {
		for _, s := range enabled {
			if s.Default {
				choose(s, opssetup.WhyDefault, "true")
				break
			}
		}
		if !done {
			trace(opssetup.WhyDefault, SetupStepMiss, "no setup has default: true")
		}
	}

	// 7. none
	if done {
		trace(opssetup.WhyNone, SetupStepNotReached, "")
	} else {
		trace(opssetup.WhyNone, SetupStepHit, "lore behaves as without setups")
		res.Active = opssetup.ActiveSetup{Why: opssetup.WhyNone}
	}
	return res, nil
}

// setupDisabled refuses a --setup / LORE_SETUP naming a disabled setup.
func setupDisabled(name string) error {
	return errs.New(errs.SetupNotFound, "setup "+strconv.Quote(name)+" is disabled; it matches nothing until it is enabled").
		WithHint("lore setup enable " + name)
}

// none is the empty resolution.
func none() SetupResolution {
	return SetupResolution{Active: opssetup.ActiveSetup{Why: opssetup.WhyNone}}
}

// setupAmbiguousFolder is 7.2's folder tie text.
func setupAmbiguousFolder(folder string, tie []onboard.FolderMatch) error {
	b := tie[len(tie)-1].Setup
	a := make([]string, 0, len(tie)-1)
	for _, m := range tie[:len(tie)-1] {
		a = append(a, m.Setup)
	}
	return errs.New(errs.SetupAmbiguous, folder+" matches setups "+strings.Join(a, ", ")+" and "+b+" equally; make one folder glob more specific (lore setup edit) or run with --setup").
		WithHint("lore setup edit " + tie[0].Setup + " --folder <a more specific glob>, or --setup " + tie[0].Setup)
}

// setupAmbiguousOrg is 7.2's "two setups for one org" text.
func setupAmbiguousOrg(org string, of []onboard.SetupEntry) error {
	names := make([]string, len(of))
	for i, s := range of {
		names[i] = s.Name
	}
	return errs.New(errs.SetupAmbiguous, "this repo is org "+org+", which setups "+strings.Join(names[:len(names)-1], ", ")+" and "+names[len(names)-1]+" both use; give one of them a folder glob that matches here (lore setup edit) or run with --setup").
		WithHint("lore setup use " + of[0].Name + " --here pins this repo")
}

// setupFolder is the git toplevel of q.Dir, else q.Dir (absolute).
func setupFolder(ctx context.Context, q SetupQuery) string {
	dir := q.Dir
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	top := ""
	if q.TopLevel != nil {
		top = q.TopLevel(ctx, dir)
	} else {
		top = gitTopLevel(ctx, dir)
	}
	if top != "" {
		return top
	}
	return dir
}

// gitTopLevel runs `git rev-parse --show-toplevel` in dir ("" outside a
// repo or without git), once per folder in a read run (doctor_nocreate.go).
func gitTopLevel(ctx context.Context, dir string) string {
	if run := noCreateOf(ctx); run != nil {
		return run.topLevel(dir, func() string { return gitTopLevelNow(ctx, dir) })
	}
	return gitTopLevelNow(ctx, dir)
}

// gitTopLevelNow is gitTopLevel without the read run's memory.
func gitTopLevelNow(ctx context.Context, dir string) string {
	out, err := gitRun{dir: dir, args: []string{"rev-parse", "--show-toplevel"}}.output(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// repoIDOf is the repo id of top (.lore/data/_meta.json), "" when none.
func repoIDOf(q SetupQuery, top string) string {
	if q.RepoID != nil {
		return q.RepoID(top)
	}
	m, ok, err := rowfs.ReadRepoMeta(top)
	if err != nil || !ok {
		return ""
	}
	return m.RepoID
}

// userHome is the folder ~ names in globs.
func userHome(q SetupQuery) string {
	if q.UserHome != "" {
		return q.UserHome
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// setupResolver is q.Resolve, else filepath.EvalSymlinks.
func setupResolver(q SetupQuery) onboard.Resolver {
	if q.Resolve != nil {
		return q.Resolve
	}
	return filepath.EvalSymlinks
}

// setupQueryFor is the production query for a folder: the context's
// --setup, the process environment, git, the real file system.
func setupQueryFor(ctx context.Context, home, dir string, orgOf func(context.Context) (string, error)) SetupQuery {
	return SetupQuery{Home: home, Dir: dir, Flag: op.SetupOf(ctx), OrgOf: orgOf}
}

// ActiveSetup is setup.Store.Active for wire/setupstore.go: 3.3 for this
// app's folder, flag overriding the context's --setup when non-empty. The
// org step resolves the org without a setup hint (orgcfg.Gather with no
// store links: LORE_ORG, lore org use, .lore/org.yaml, own:/repos:
// patterns), so a link-only org is not seen by step 5.
func (a *App) ActiveSetup(ctx context.Context, flag string) (opssetup.ActiveSetup, error) {
	res, err := a.SetupResolution(ctx, flag)
	return res.Active, err
}

// SetupResolution is ActiveSetup with the entry and the trace (whoami
// --explain, identity.show).
func (a *App) SetupResolution(ctx context.Context, flag string) (SetupResolution, error) {
	home, dir := a.opts.Home, a.projectDir()
	q := setupQueryFor(ctx, home, dir, plainOrgOf(home, dir))
	if flag != "" {
		q.Flag = flag
	}
	return ResolveSetup(ctx, q)
}

// plainOrgOf resolves the org of dir with no setup hint and no store
// links (cheap: no fetch); a refusal answers no org.
func plainOrgOf(home, dir string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		s, err := orgcfg.Gather(ctx, orgcfg.In{Home: home, Dir: dir, EnvOrg: strings.TrimSpace(os.Getenv(orgcfg.EnvOrg))})
		if err != nil {
			return "", err
		}
		if s.ResolveErr != nil {
			return "", s.ResolveErr
		}
		return s.Result.OrgName(), nil
	}
}

// setupHint is the org hint of a resolution (3.3, Q4): a member or admin
// setup's org; zero for solo, none, or a setup chosen BY its org (step 5:
// the org already resolved, nothing to add).
func setupHint(res SetupResolution) orgresolve.SetupHint {
	e := res.Entry
	if e.Org == "" || !e.Kind.HasOrg() || res.Active.Why == opssetup.WhyOrg {
		return orgresolve.SetupHint{}
	}
	why := res.Active.Why
	if res.Active.Detail != "" {
		why += " " + res.Active.Detail
	}
	return orgresolve.SetupHint{Name: e.Name, Org: e.Org, Why: why}
}

// setupIdentity is the solo setup's identity for the chain (3.4); nil for
// any other setup, for none, and when resolution fails (the write gate
// then refuses writes, so nothing is stamped with a guessed identity).
func setupIdentity(ctx context.Context, home, dir string) *identity.SetupIdentity {
	res, err := ResolveSetup(ctx, setupQueryFor(ctx, home, dir, nil))
	if err != nil || res.Entry.Kind != onboard.KindSolo || strings.TrimSpace(res.Entry.Identity.Key) == "" {
		return nil
	}
	id := res.Entry.Identity
	return &identity.SetupIdentity{Setup: res.Entry.Name, Entry: identity.Entry{Key: id.Key, Name: id.Name, Handle: id.Handle}, Aliases: id.Aliases}
}

// setupWriteGate is the 3.3 write refusal, asked by writeState before
// every write: a setup that cannot be resolved (E_SETUP_NOT_FOUND,
// E_SETUP_AMBIGUOUS) and an explicitly chosen setup (flag, env, pin,
// folder) whose org another, stronger source contradicts
// (E_SETUP_ORG_MISMATCH) refuse the write; reads never ask. A default
// setup is a fallback, not a claim about this folder, so it never refuses.
func (a *App) setupWriteGate(ctx context.Context) error {
	home, dir := a.opts.Home, a.projectDir()
	res, err := ResolveSetup(ctx, setupQueryFor(ctx, home, dir, nil))
	if err != nil {
		return err
	}
	hint := setupHint(res)
	if hint.Org == "" || res.Active.Why == opssetup.WhyDefault || a.opts.Org == nil {
		return nil
	}
	org, err := a.opts.Org(ctx)
	if err != nil || org == nil || org.Name == hint.Org {
		return nil // an org refusal is the org hooks' to report; no org: the hint applied
	}
	return setupMismatchErr(ctx, home, dir, hint, org.Name)
}

// setupExplicitGate refuses every repo operation, reads included, when the
// caller names a setup explicitly (--setup / X-Lore-Setup, LORE_SETUP) and
// that name is no enabled setup (E_SETUP_NOT_FOUND, ONB error table: "--setup,
// LORE_SETUP or an op names no setup"). The person asked for a setup that
// does not exist, so answering with another setup's view would be a silent
// guess (BUGFIX-18). Only the flag and env steps refuse here: a folder or org
// tie and a stale repo pin still let reads go on (setupWriteGate refuses
// their writes). Local ops (setup.*, whoami) never come here, so the setup
// page can still name a setup it is about to create.
func (a *App) setupExplicitGate(ctx context.Context) error {
	if strings.TrimSpace(op.SetupOf(ctx)) == "" && strings.TrimSpace(os.Getenv(opssetup.EnvSetup)) == "" {
		return nil // nothing named: no config read
	}
	res, err := ResolveSetup(ctx, setupQueryFor(ctx, a.opts.Home, a.projectDir(), nil))
	if err == nil || !errs.Has(err, errs.SetupNotFound) || len(res.Steps) == 0 {
		return nil
	}
	if last := res.Steps[len(res.Steps)-1]; last.Step != opssetup.WhyFlag && last.Step != opssetup.WhyEnv {
		return nil
	}
	return err
}

// setupMismatchErr builds E_SETUP_ORG_MISMATCH naming the source that
// resolved the other org (re-run without store links; a link-decided org
// is named "store link").
func setupMismatchErr(ctx context.Context, home, dir string, hint orgresolve.SetupHint, repoOrg string) error {
	s, gerr := orgcfg.Gather(ctx, orgcfg.In{Home: home, Dir: dir, EnvOrg: strings.TrimSpace(os.Getenv(orgcfg.EnvOrg)), Setup: hint})
	if gerr != nil {
		return gerr // the config itself is broken: that is the refusal to fix first
	}
	if s.ResolveErr == nil {
		if cerr := s.Result.CheckSetupWrite(); cerr != nil {
			return orgcfg.ResolveError(cerr)
		}
	}
	return orgcfg.ResolveError(orgresolve.MismatchError(hint, repoOrg, orgresolve.StepLink))
}

// setupPersonal is the 3.4 `me` override of the active setup: url replaces
// personal.store; none reports personal_store: none (name is the setup).
// inherit, no setup, or a resolution error leave personal.store as is.
func setupPersonal(ctx context.Context, home, dir string) (url string, isNone bool, name string) {
	res, err := ResolveSetup(ctx, setupQueryFor(ctx, home, dir, nil))
	if err != nil || res.Active.Name == "" {
		return "", false, ""
	}
	switch p := strings.TrimSpace(res.Entry.PersonalStore); p {
	case "", onboard.PersonalStoreInherit:
		return "", false, res.Entry.Name
	case onboard.PersonalStoreNone:
		return "", true, res.Entry.Name
	default:
		return p, false, res.Entry.Name
	}
}

// effectivePersonalURL is personal.store as the active setup sees it
// (3.4): the setup's url, "" for personal_store: none, else mc's.
func effectivePersonalURL(ctx context.Context, home, dir string, mc machineConfig) string {
	u, isNone, _ := setupPersonal(ctx, home, dir)
	switch {
	case isNone:
		return ""
	case u != "":
		return u
	}
	return mc.personalURL()
}

// checkPersonalTarget refuses `--to me` (and `me/<folder>`) under a setup
// with personal_store: none (E_SETUP_NO_PERSONAL_STORE, 7.2).
func checkPersonalTarget(ctx context.Context, home, dir, to string) error {
	base, _, _ := strings.Cut(to, targetSep)
	if base != attach.ReservedMe {
		return nil
	}
	if _, isNone, name := setupPersonal(ctx, home, dir); isNone {
		return errs.New(errs.SetupNoPersonalStore, "setup "+name+" has no personal store (personal_store: none); write to the repo or the org store, or lore setup edit "+name+" --personal-store <url>").
			WithHint("--to repo, --to <org store>, or lore setup edit " + name + " --personal-store <url>")
	}
	return nil
}

// optsProjectDir is App.projectDir for code that holds only Options.
func optsProjectDir(o Options) string {
	if o.WorkDir != "" {
		return o.WorkDir
	}
	return o.RepoDir
}

// checkMeTarget is prepareTarget's 3.4 refusal of `--to me` under a setup
// with personal_store: none, before anything is attached.
func (a *App) checkMeTarget(ctx context.Context, e op.Entry, d op.Decoder) error {
	in, err := e.Decode(d)
	if err != nil {
		return nil // the engine's Run reports the decode error itself
	}
	for _, tv := range in.Texts() {
		if tv.Field == engine.InputTarget && tv.Text != "" {
			return checkPersonalTarget(ctx, a.opts.Home, a.projectDir(), tv.Text)
		}
	}
	return nil
}

// gatherWithSetup is OrgResolution.Situation's gather with the active
// setup's org hint (3.3, Q4). With no setup, or a solo one, it is the plain
// gather of before. Steps 1–4 of 3.3 need no org; only when they miss is
// the plain situation gathered first (for step 5), and gathered again with
// the hint only when the default setup brings an org. A setup that cannot
// be resolved adds no hint: reads go on, and writeState refuses writes.
func (r *OrgResolution) gatherWithSetup(ctx context.Context) (*orgcfg.Situation, error) {
	in := orgcfg.In{Home: r.home, Dir: r.dir, EnvOrg: os.Getenv(orgcfg.EnvOrg), Links: r.links}
	var plain *orgcfg.Situation
	var plainErr error
	gathered := false
	orgOf := func(ctx context.Context) (string, error) {
		if !gathered {
			plain, plainErr, gathered = nil, nil, true
			plain, plainErr = orgcfg.Gather(ctx, in)
		}
		if plainErr != nil {
			return "", plainErr
		}
		if plain.ResolveErr != nil {
			return "", plain.ResolveErr
		}
		return plain.Result.OrgName(), nil
	}
	res, err := ResolveSetup(ctx, setupQueryFor(ctx, r.home, r.dir, orgOf))
	hint := orgresolve.SetupHint{}
	if err == nil {
		hint = setupHint(res)
	}
	if gathered && (hint.Org == "" || plainErr != nil || (plain.ResolveErr == nil && plain.Result.Org != nil)) {
		// the plain answer stands: no hint, or an org already resolved (the
		// default's org never overrides it, and a default never refuses)
		return plain, plainErr
	}
	if hint.Org == "" {
		return orgcfg.Gather(ctx, in)
	}
	in.Setup = hint
	return orgcfg.Gather(ctx, in)
}

// envGitSSHCommand is git's ssh command variable (Q5 fallback).
const envGitSSHCommand = "GIT_SSH_COMMAND"

// routeFor builds the git args and env of one setup's github block (3.4,
// Q5): with github.route https, the github.com urls go over https with a
// per-call credential helper asking gh for github.user's token
// (probe.Route.HTTPSUser; nothing stored); with github.host_alias,
// `-c url.git@<alias>:.insteadOf=git@github.com:` per call; without one but
// with github.ssh_key, GIT_SSH_COMMAND="ssh -i <key> -o IdentitiesOnly=yes".
// Nothing is written to git config; an invalid route adds nothing.
func routeFor(g onboard.SetupGitHub, userHomeDir string) (args, env []string) {
	if g.IsHTTPS() {
		r := probe.Route{HTTPSUser: strings.TrimSpace(g.User)}
		if r.HTTPSUser == "" || r.Validate() != nil {
			return nil, nil
		}
		return r.GitArgs(), nil
	}
	alias := strings.TrimSpace(g.HostAlias)
	if alias == probe.GitHubHost {
		alias = "" // the plain host: ssh's own config decides
	}
	r := probe.Route{HostAlias: alias, SSHKey: strings.TrimSpace(g.SSHKey)}
	if r.Validate() != nil {
		return nil, nil
	}
	if a := r.GitArgs(); len(a) > 0 {
		return a, nil
	}
	if r.SSHKey != "" {
		return nil, []string{envGitSSHCommand + "=" + r.GitSSHCommand(userHomeDir)}
	}
	return nil, nil
}

// whoamiSetup is the whoami setup block and lines (3.3, 10.11): one
// "setup" row after the org row, and with --explain every step with hit or
// miss after the org trace. Nothing when no setup exists on this machine
// (whoami prints exactly what it printed before onboarding). The error is
// the resolution's refusal (--setup or LORE_SETUP naming no setup:
// E_SETUP_NOT_FOUND, 7.2): whoami prints every line, then fails with it,
// as it does for an org refusal.
func whoamiSetup(ctx context.Context, opts Options, explain bool, lines []string) (opswhoami.Setup, []string, error) {
	cfg, cerr := ReadSetupsConfig(opts.Home)
	if cerr == nil && len(cfg.Setups) == 0 && op.SetupOf(ctx) == "" && os.Getenv(opssetup.EnvSetup) == "" {
		return opswhoami.Setup{}, lines, nil
	}
	home, dir := opts.Home, optsProjectDir(opts)
	res, err := ResolveSetup(ctx, setupQueryFor(ctx, home, dir, plainOrgOf(home, dir)))
	out := opswhoami.Setup{Name: res.Active.Name, Why: res.Active.Why, Detail: res.Active.Detail, Kind: string(res.Entry.Kind), Org: res.Entry.Org}
	value := "none  (no folder, flag or default chose one)"
	switch {
	case err != nil:
		out.Error = errs.EnvelopeOf(err).Error.Message
		value = "refused  (" + out.Error + ")"
	case res.Active.Name != "":
		value = res.Active.Name + "  (" + res.Active.Why
		if res.Active.Detail != "" {
			value += ": " + res.Active.Detail
		}
		value += ")"
	}
	row := fmt.Sprintf("%-*s %s", whoamiLabelWidth, "setup", value)
	at := min(1, len(lines)) // after the org row
	lines = append(lines[:at], append([]string{row}, lines[at:]...)...)
	if explain {
		lines = append(lines, "", "setup resolution (first step that answers wins):")
		for i, st := range res.Steps {
			out.Steps = append(out.Steps, opswhoami.SetupStep{Step: st.Step, Outcome: st.Outcome, Detail: st.Detail})
			mark := "·"
			switch st.Outcome {
			case SetupStepHit:
				mark = "✓"
			case SetupStepMiss, SetupStepRefused:
				mark = "✗"
			}
			lines = append(lines, fmt.Sprintf("  %s %d. %-8s %s", mark, i+1, st.Step, st.Detail))
		}
	}
	return out, lines, err
}
