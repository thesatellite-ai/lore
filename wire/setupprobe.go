package wire

// setupprobe.go — setup.Prober on *App (ONBOARDING_DESIGN 2.1 L7 row, 2.3
// "run checks", 4.x check tables, 6.1): maps each onboard check to an
// adapters/probe call made with the setup's Route (Q5: the host alias on
// every remote call), and fills ProbeResult.Vars for the fix templates.
//
//	[]ProbeRequest ─▶ one goroutine each (probe.Prober's limiter bounds them)
//	               ─▶ probeOne: switch on Check.ID ─▶ ProbeResult (in request order)
//
// A probe that cannot run is StatusError with the reason in Detail, never a
// Go error (contract). A failed warn-level check is StatusWarn. A check
// whose input is not known yet (no url answered, no alias) is StatusSkip
// with Detail saying why. Every Detail goes through probe.Sanitize.

import (
	"context"
	"maps"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thesatellite-ai/lore/adapters/machine"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsrepoinit "github.com/thesatellite-ai/lore/ops/repoinit"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/skill"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// gitSuffix ends a store url's repo name.
const gitSuffix = ".git"

// secretsBackendEnv is the secretmgr scheme with no program: always present.
const secretsBackendEnv = "env"

// skipDetail* are the StatusSkip reasons (2.2: skip carries why).
const (
	skipNoURL     = "no store url yet"
	skipNoAlias   = "github.com is used directly: there is no Host alias block to check"
	skipNotGitHub = "not a github.com url"
	// skipFolderStore: the store is a local folder (file:// without git);
	// privacy and org checks are about a shared git repo.
	skipFolderStore = "a local folder store (no git): nothing shared to check"
	skipNoUser      = "no GitHub user for this setup yet"
	skipNoEmail     = "no email answered yet"
	skipNoKey       = "no key file recorded; the route's own key is used"
	skipNoGreeting  = "ssh -T did not log in; github.ssh_auth says why"
	skipNoBackend   = "no secrets backend chosen"
	skipNoFolders   = "no folders for this setup yet"
	skipNoGH        = "gh is not installed; it is optional"
	// skipNoActiveGH: gh is installed but has no active github.com login.
	skipNoActiveGH = "gh has no active github.com account; github.gh_account says how to log in"
	// skipNotHere* are the checks this prober (doctor, the setup page) does not run yet (they read
	// org store rows); each names where the user can look instead. User
	// words only: they show in doctor --all and its --json.
	skipNotHere       = "not checked here yet"
	skipNotHereRoster = skipNotHere + "; lore actor list in a repo of the org shows the roster"
	skipNotHereInvite = skipNotHere + "; lore team invite list --status pending shows the pending invites"
	skipNoOrg         = "no org for this setup"
	skipNoName        = "no name answered yet"
	skipNoInviteGH    = "the invite names no GitHub user"
	skipNotInRepo     = "not inside a repo"
)

// setupProber is the App's setup.Prober.
type setupProber struct {
	a *App
	p *probe.Prober
}

var _ opssetup.Prober = setupProber{}

// Probe implements opssetup.Prober: one result per request, in order.
func (sp setupProber) Probe(ctx context.Context, reqs []opssetup.ProbeRequest) []opssetup.ProbeResult {
	out := make([]opssetup.ProbeResult, len(reqs))
	// One remote answer per url and route for this batch: store.read,
	// store.marker and org.free share one fetch, store.write one dry-run
	// push (adapters/probe/snapshot.go). The memo ends with this call.
	ctx = probe.WithRemoteMemo(ctx)
	reqs = sp.withCreateTarget(ctx, reqs)
	// At most opsdoctor.MaxParallel checks at once (min(4, CPUs)): one
	// goroutine per request ran every check of every setup together.
	opsdoctor.Parallel(len(reqs), func(i int) {
		r := reqs[i]
		start := time.Now()
		res := sp.probeOne(probe.WithLabel(ctx, string(r.Check.ID)), r) // debug trace: calls carry the check id
		res.Check, res.Target = r.Check.ID, r.Target
		if res.Status == onboard.StatusOK && res.Detail == "" {
			res.Detail = okDetail(r.Check.ID, res.Vars)
		}
		res.Detail = probe.SanitizeWithin(res.Detail, probe.MaxCheckDetail)
		if res.Status == onboard.StatusFail && r.Check.Level == onboard.LevelWarn {
			res.Status = onboard.StatusWarn
		}
		if res.Vars == nil {
			res.Vars = map[string]string{}
		}
		res.TookMS = time.Since(start).Milliseconds()
		out[i] = res
	})
	return out
}

// result is a ProbeResult of status, detail and vars.
func result(s onboard.Status, detail string, vars map[string]string) opssetup.ProbeResult {
	return opssetup.ProbeResult{Status: s, Detail: detail, Vars: vars}
}

// skip is a StatusSkip result.
func skip(why string) opssetup.ProbeResult { return result(onboard.StatusSkip, why, nil) }

// outcome maps a probe class to a status (probe.Class.Status: ok / fail /
// error) with its sanitised detail.
func outcome(o probe.Outcome, vars map[string]string) opssetup.ProbeResult {
	return result(o.Class.Status(), o.Detail, vars)
}

// pick is the answer when given, else the setup's value.
func pick(a onboard.Answers, id, fallback string) string {
	if s := answerText(a, id); s != "" {
		return s
	}
	return fallback
}

// route is the setup's Route (Q5): https as the account's gh login when
// the route is https; else the host alias (github.com itself is no alias)
// and the key path with ~ expanded.
func (sp setupProber) route(r opssetup.ProbeRequest) probe.Route {
	if sp.isHTTPS(r) {
		return probe.Route{HTTPSUser: pick(r.Answers, onboard.QIDGitHubAccount, r.Setup.GitHub.User)}
	}
	alias := pick(r.Answers, onboard.QIDHostAlias, r.Setup.GitHub.HostAlias)
	if alias == probe.GitHubHost {
		alias = ""
	}
	key := pick(r.Answers, onboard.QIDSSHKey, r.Setup.GitHub.SSHKey)
	if key != "" {
		key = onboard.ExpandHome(key, sp.p.Home())
	}
	return probe.Route{HostAlias: alias, SSHKey: key}
}

// targetStorePrefix starts a named store target ("store me", "store
// agency"; ops/doctor.TargetStore builds it, A9).
const targetStorePrefix = opssetup.TargetStorePrefix

// storeURL is the url a store check probes: a url Target as is; a named
// Target ("store me", "store <org>", "store <attached name>") resolved
// through config.yaml and this repo's stores; else the answered url, else
// the setup's own personal_store url. "" when nothing names one.
// orgStoreURL refuses, for the team step (its answers name the org), a
// store url the org's config.yaml entry cannot hold: orgconf validates
// orgs[].stores[].url with repourl (a hosted repo, never file:// or a
// local path), so before this the step's store.url check passed a folder
// url that Save then refused with E_CONFIG_INVALID (a ✓ next to the error
// about the same url). A personal store may still be a local folder.
func orgStoreURL(r opssetup.ProbeRequest, u string) error {
	if answerText(r.Answers, onboard.QIDOrg) == "" {
		return nil
	}
	if _, err := repourl.Normalize(u); err != nil {
		return errs.New(errs.InvalidInput, "a team store must be a hosted git repo such as git@github.com:agency/lore-team.git: "+err.Error())
	}
	if err := repourl.CheckFetchable(u); err != nil {
		return errs.New(errs.InvalidInput, "a team store must be a git url lore can fetch: "+err.Error())
	}
	return nil
}

func (sp setupProber) storeURL(ctx context.Context, r opssetup.ProbeRequest) string {
	if r.Target != "" && looksLikeURL(r.Target) {
		return r.Target
	}
	if name, ok := strings.CutPrefix(r.Target, targetStorePrefix); ok {
		return sp.namedStoreURL(ctx, r.Setup, strings.TrimSpace(name))
	}
	if u := answerText(r.Answers, onboard.QIDStoreURL); u != "" {
		return u
	}
	if ps := r.Setup.PersonalStore; ps != "" && ps != onboard.PersonalStoreInherit && ps != onboard.PersonalStoreNone {
		return ps
	}
	return ""
}

// namedStoreURL resolves a store name: me (the setup's personal_store url,
// else personal.store; none = no store, 3.4), an orgs[] entry (its store
// holding the org folder, else its first), else a store of this repo.
func (sp setupProber) namedStoreURL(ctx context.Context, setup onboard.SetupEntry, name string) string {
	home := sp.a.opts.Home
	if name == opsstores.PersonalName {
		switch ps := setup.PersonalStore; ps {
		case onboard.PersonalStoreNone:
			return ""
		case "", onboard.PersonalStoreInherit:
			mc, err := readMachineConfig(home)
			if err != nil {
				return ""
			}
			return mc.personalURL()
		default:
			return ps
		}
	}
	if cfg, _, err := orgcfg.LoadConfig(home); err == nil {
		for _, o := range cfg.Orgs {
			if o.Name != name || len(o.Stores) == 0 {
				continue
			}
			for _, st := range o.Stores {
				if st.OrgFolder != "" {
					return st.URL
				}
			}
			return o.Stores[0].URL
		}
	}
	// A store of this repo: only in an initialised repo (ListStores opens
	// the repo store, which would write in a repo lore is not set up in).
	if top := sp.hereTop(ctx); top == "" || !loreReady(top) {
		return ""
	}
	if entries, err := sp.a.ListStores(ctx); err == nil {
		for _, e := range entries {
			if e.Name == name {
				return e.URL
			}
		}
	}
	return ""
}

// looksLikeURL tells a store url from a store name in Target.
func looksLikeURL(s string) bool { return strings.Contains(s, "://") || strings.Contains(s, "@") }

// githubOwnerRepo is owner/repo of a github.com store url (git@<alias>:
// counts when alias is the setup's alias of github.com); false for other
// hosts and file:// (gh only knows GitHub).
func githubOwnerRepo(u, alias string) (string, string, bool) {
	g, _, ok := onboard.ParseGitURL(u)
	if !ok || g.Owner == "" || g.Repo == "" || (g.Host != probe.GitHubHost && (alias == "" || g.Host != alias)) {
		return "", "", false
	}
	return g.Owner, g.Repo, true
}

// probeOne runs one check.
func (sp setupProber) probeOne(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	switch r.Check.ID {
	case onboard.CheckSetupConfig:
		if _, err := ValidateSetupsConfig(sp.a.opts.Home); err != nil {
			return result(onboard.StatusFail, err.Error(), nil)
		}
		return result(onboard.StatusOK, "", nil)
	case onboard.CheckSetupNameFree:
		return sp.nameFree(r)
	case onboard.CheckSetupOrgEntry, onboard.CheckOrgNameFree:
		return sp.orgEntry(r)
	case onboard.CheckIdentityEmailFormat:
		// The answer, else the first address the setup already holds (its
		// identity key, extra emails, or its org entry's identity); a key
		// with no address (human:sam) is not an email to check.
		email := pick(r.Answers, onboard.QIDEmail, "")
		if email == "" {
			held := sp.identityEmails(r)
			if i := slices.IndexFunc(held, func(e string) bool { return strings.Contains(e, "@") }); i >= 0 {
				email = held[i]
			}
		}
		if email == "" {
			return skip(skipNoEmail)
		}
		if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
			return result(onboard.StatusFail, email+" is not an email address", map[string]string{onboard.VarEmail: email})
		}
		return result(onboard.StatusOK, "", map[string]string{onboard.VarEmail: email})
	case onboard.CheckIdentityGitEmail:
		return sp.gitEmail(ctx, r)
	case onboard.CheckStoreAddress, onboard.CheckOrgSource:
		return sp.sourceOfTruthStores(ctx, r) // setupprobe_sotstores.go
	case onboard.CheckIdentityActorCopies:
		return sp.actorCopies(ctx) // doctor_actorcopies.go
	case onboard.CheckIdentityTopLevel, onboard.CheckIdentityTopLevelKeep:
		return sp.topLevelIdentity(r) // doctor_identitysource.go
	case onboard.CheckGitHubSSHKey:
		return sp.sshKey(ctx, r)
	case onboard.CheckGitHubSSHAuth, onboard.CheckGitHubSSHUser:
		return sp.sshAuth(ctx, r)
	case onboard.CheckGitHubAliasConfig:
		return sp.aliasConfig(ctx, r)
	case onboard.CheckGitHubKeyMatches:
		return sp.keyMatches(ctx, r)
	case onboard.CheckGitHubIdentityEmail:
		return sp.identityEmail(ctx, r)
	case onboard.CheckGitHubGHAccount, onboard.CheckGitHubGHMatches:
		return sp.ghAccount(ctx, r)
	case onboard.CheckStoreURL:
		u := sp.storeURL(ctx, r)
		if u == "" {
			return skip(skipNoURL)
		}
		if _, err := cleanFileURL(u); err != nil {
			return result(onboard.StatusFail, err.Error(), map[string]string{onboard.VarURL: probe.Sanitize(u)})
		}
		if err := orgStoreURL(r, u); err != nil {
			return result(onboard.StatusFail, err.Error(), map[string]string{onboard.VarURL: probe.Sanitize(u)})
		}
		return result(onboard.StatusOK, "", map[string]string{onboard.VarURL: u})
	case onboard.CheckRepoInit:
		return sp.repoInit(ctx, r)
	case onboard.CheckRepoOrg:
		return sp.repoOrg(ctx, r)
	case onboard.CheckRepoStores:
		if r.Target == "" {
			// this_folder (4.10): store.read per .lore/stores.yaml store.
			return sp.repoStores(ctx, r)
		}
		return sp.storeRead(ctx, r)
	case onboard.CheckStoreRead:
		return sp.storeRead(ctx, r)
	case onboard.CheckToolsSkillInstalled, onboard.CheckToolsSkillVersion, onboard.CheckToolsSkillModified:
		return sp.toolSkill(ctx, r)
	case onboard.CheckToolsPointerFile:
		return sp.pointerFile(ctx, r)
	case onboard.CheckToolsHooks:
		return sp.hooks(ctx, r)
	case onboard.CheckFoldersOverlap, onboard.CheckFoldersShadow:
		return sp.foldersTie(r)
	case onboard.CheckFoldersReachable:
		return sp.foldersReach(r)
	case onboard.CheckStoreWrite:
		return sp.storeWrite(ctx, r)
	case onboard.CheckStoreMarker:
		u := sp.storeURL(ctx, r)
		if u == "" {
			return skip(skipNoURL)
		}
		var m probe.MarkerResult
		if dir, ok := sp.p.FolderStorePath(u); ok {
			m = sp.p.FolderMarker(dir) // a folder store: the file itself
		} else {
			m = sp.p.StoreMarker(ctx, u, sp.route(r))
		}
		vars := sp.urlVars(u, r)
		vars[onboard.VarStoreName] = markerStoreName(r)
		res := outcome(m.Outcome, vars)
		if res.Status == onboard.StatusOK && !m.Present {
			res.Status, res.Detail = onboard.StatusFail, "no lore-store.yaml in the store yet"
		}
		return res
	case onboard.CheckOrgFree:
		u := sp.storeURL(ctx, r)
		if u == "" {
			return skip(skipNoURL)
		}
		if _, ok := sp.p.FolderStorePath(u); ok {
			return skip(skipFolderStore) // an org store is a git repo the team shares
		}
		f := sp.p.RootFiles(ctx, u, sp.route(r), orgcfg.OrgFile)
		res := outcome(f.Outcome, sp.urlVars(u, r))
		if res.Status == onboard.StatusOK && len(f.Found) > 0 {
			res.Status, res.Detail = onboard.StatusFail, "the store already holds "+orgcfg.OrgFile
		}
		return res
	case onboard.CheckStorePrivate:
		return sp.storePrivate(ctx, r)
	case onboard.CheckInviteGitHubAccess:
		return sp.collaborator(ctx, r)
	case onboard.CheckSecretsBackendPresent:
		return sp.secretsBackend(ctx, r)
	case onboard.CheckFoldersExist:
		return sp.foldersExist(r)
	}
	// Roster, invite, period and live checks read org store rows: the
	// ops that own those reads answer them (A7/A9), not this prober.
	return skip(notHereDetail(r.Check.ID))
}

// creating reports whether r checks the personal store step's answers with
// the choice create (the store does not have to exist yet).
func creating(r opssetup.ProbeRequest) bool {
	return answerText(r.Answers, onboard.QIDPersonalStore) == onboard.PersonalStoreCreate
}

// storeRead is store.read: git ls-remote through the setup's route.
func (sp setupProber) storeRead(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	u := sp.storeURL(ctx, r)
	if u == "" {
		return skip(skipNoURL)
	}
	if dir, ok := sp.p.FolderStorePath(u); ok {
		// A folder store (no git): read the folder, not ls-remote. A store
		// the person asks lore to create may not exist yet (the save makes
		// it); `existing` and every later check still need the folder.
		if creating(r) {
			return outcome(sp.p.FolderCreatable(dir), sp.urlVars(u, r))
		}
		return outcome(sp.p.FolderRead(dir), sp.urlVars(u, r))
	}
	res := sp.p.ReadRemote(ctx, u, sp.route(r))
	out := sp.reachedBefore(readVars(outcome(res.Outcome, sp.urlVars(u, r)), res.Class), u, r)
	if out.Status == onboard.StatusOK && out.Detail == "" {
		out.Detail = detailReadOK
		if res.Empty {
			out.Detail = detailReadEmpty
		}
	}
	return out
}

// hereTop is the git top of the folder lore runs in, "" outside a repo.
// Read with git, not App.RepoTop: that opens the repo store, which writes
// .lore/ and git wiring in a repo lore is not set up in (A13 bug 3).
func (sp setupProber) hereTop(ctx context.Context) string {
	return gitTopLevel(ctx, sp.a.projectDir())
}

// repoTarget is the repo root a Target names: an absolute path, or the
// root of a "<tool> <root>" target; "" for every other target.
func repoTarget(t string) string { return opssetup.TargetRepo(t) }

// notHereDetail is the skip detail of a check this prober does not run:
// where the user can look instead, when there is a command for it.
func notHereDetail(id onboard.CheckID) string {
	switch id {
	case onboard.CheckIdentityRoster, onboard.CheckIdentityPeriod:
		return skipNotHereRoster
	case onboard.CheckInvitePending:
		return skipNotHereInvite
	}
	return skipNotHere
}

// markerStoreName is the lore name of the store store.marker checks, for
// its fix (lore store init {store_name} {url}): the store a doctor target
// names ("store agency"), else me, the store of the personal store step,
// the only setup step that checks the marker.
func markerStoreName(r opssetup.ProbeRequest) string {
	if name, ok := strings.CutPrefix(r.Target, targetStorePrefix); ok {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	return opsstores.PersonalName
}

// urlVars are the fix vars of a store url: {url}, and {owner}/{repo} for
// any host url (the fixes name them); a url gh cannot act on (not
// github.com) selects the FixNotGitHub variant (A6 fix.go).
func (sp setupProber) urlVars(u string, r opssetup.ProbeRequest) map[string]string {
	v := map[string]string{onboard.VarURL: probe.Sanitize(u)}
	if owner, repo, ok := githubOwnerRepo(u, sp.route(r).HostAlias); ok {
		v[onboard.VarOwner], v[onboard.VarRepo] = owner, repo
		return v
	}
	if owner, repo, ok := onboard.OwnerRepo(u); ok {
		v[onboard.VarOwner], v[onboard.VarRepo] = owner, repo
	}
	v[onboard.VarFixVariant] = string(onboard.FixNotGitHub)
	return v
}

// readVars adds the FixUnreachable variant to a store.read result whose
// host did not answer (4.4: "check your network", not "create it").
func readVars(res opssetup.ProbeResult, c probe.Class) opssetup.ProbeResult {
	if c == probe.ClassUnreachable || c == probe.ClassTimeout {
		res.Vars[onboard.VarFixVariant] = string(onboard.FixUnreachable)
	}
	return res
}

// writeVars picks the store.write fix variant from the push test's class.
// Only a refused push (ClassNoWrite) proves "you can read it but not write
// it" (the default text); a host that did not answer is FixUnreachable, a
// refused read (no access, auth, host key, gh login) is FixNoRead, any
// other failure FixWriteUnknown. A variant set already (FixNotGitHub from
// urlVars) is kept: its text names no read right.
func writeVars(res opssetup.ProbeResult, c probe.Class) opssetup.ProbeResult {
	if res.Status != onboard.StatusFail || res.Vars[onboard.VarFixVariant] != "" {
		return res
	}
	switch c {
	case probe.ClassNoWrite:
	case probe.ClassUnreachable, probe.ClassTimeout:
		res.Vars[onboard.VarFixVariant] = string(onboard.FixUnreachable)
	case probe.ClassNoAccess, probe.ClassAuthDenied, probe.ClassHostKey, probe.ClassNotLoggedIn:
		res.Vars[onboard.VarFixVariant] = string(onboard.FixNoRead)
	default:
		res.Vars[onboard.VarFixVariant] = string(onboard.FixWriteUnknown)
	}
	return res
}

// nameFree is setup.name_free (4.1): fails when another setups[] entry
// already has the answered name. Re-checking the setup being repaired (its
// own name, req.Setup.Name) is not a clash.
func (sp setupProber) nameFree(r opssetup.ProbeRequest) opssetup.ProbeResult {
	name := pick(r.Answers, onboard.QIDName, "")
	if name == "" {
		return skip(skipNoName)
	}
	cfg, err := ReadSetupsConfig(sp.a.opts.Home)
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	v := map[string]string{onboard.VarSetup: name}
	_, taken := cfg.Setup(name)
	switch {
	case taken && name != r.Setup.Name:
		return result(onboard.StatusFail, "setup "+name+" already exists", v)
	case taken:
		// The setup's own name, opened for edit: it passes, and says so
		// rather than "name is free", which read as the new-setup flow
		// (owner report 2026-11-08; machines TestRestLifecycle).
		return result(onboard.StatusOK, detailOwnName, v)
	}
	return result(onboard.StatusOK, "", v)
}

// orgEntry is setup.org_entry (the org must be an orgs[] entry) and
// org.name_free (4.6: it must not be one yet).
func (sp setupProber) orgEntry(r opssetup.ProbeRequest) opssetup.ProbeResult {
	org := pick(r.Answers, onboard.QIDOrg, r.Setup.Org)
	if org == "" {
		return skip(skipNoOrg)
	}
	mc, err := readMachineConfig(sp.a.opts.Home)
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	exists := false
	for _, o := range mc.Orgs {
		exists = exists || o.Name == org
	}
	v := map[string]string{onboard.VarOrg: org}
	if (r.Check.ID == onboard.CheckOrgNameFree) == exists {
		if exists {
			if cfg, cerr := ReadSetupsConfig(sp.a.opts.Home); cerr == nil {
				for _, t := range cfg.Trash {
					if t.Setup.Org == org {
						// A setup in the trash still owns the team: say how to get it back or free it.
						return result(onboard.StatusFail, "team "+org+" belongs to setup "+t.Setup.Name+", which is in the trash: restore it (lore setup trash restore "+t.Setup.Name+"), or purge it (lore setup trash purge "+t.Setup.Name+") to reuse the name", v)
					}
				}
			}
			return result(onboard.StatusFail, "an orgs[] entry named "+org+" exists", v)
		}
		return result(onboard.StatusFail, "no orgs[] entry named "+org, v)
	}
	return result(onboard.StatusOK, "", v)
}

// setupEmails are the identity addresses of the request (answer, else the
// solo identity key's address, plus the extra emails).
func setupEmails(r opssetup.ProbeRequest) []string {
	var out []string
	if e := answerText(r.Answers, onboard.QIDEmail); e != "" {
		out = append(out, e)
	} else if _, addr, ok := strings.Cut(r.Setup.Identity.Key, ":"); ok && addr != "" {
		out = append(out, addr)
	}
	return append(out, r.Setup.Identity.Emails...)
}

// identityEmails are setupEmails, else, for a member or admin setup, the
// address of its org entry's identity (ONB 3.1 CFG9: such a setup names
// orgs[<org>] and the identity lives there, never copied into setups[]).
// Without this fallback doctor --all skipped identity.email_format and
// identity.git_email of every finished member/admin setup with "no email
// answered yet" (BUGFIX-21). An unreadable config.yaml gives no fallback:
// setup.config reports it.
func (sp setupProber) identityEmails(r opssetup.ProbeRequest) []string {
	if out := setupEmails(r); len(out) > 0 {
		return out
	}
	org := pick(r.Answers, onboard.QIDOrg, r.Setup.Org)
	if org == "" {
		return nil
	}
	mc, err := readMachineConfig(sp.a.opts.Home)
	if err != nil {
		return nil
	}
	for _, o := range mc.Orgs {
		if o.Name != org {
			continue
		}
		if _, addr, ok := strings.Cut(o.Identity.Key, ":"); ok && strings.Contains(addr, "@") {
			return []string{addr}
		}
	}
	return nil
}

// gitEmail is identity.git_email (4.2, 3.4): git's email (the repo's
// when Target is a folder, else the global one) is one of the setup's
// (identityEmails: a member/admin setup's are its org entry's).
func (sp setupProber) gitEmail(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	emails := sp.identityEmails(r)
	if len(emails) == 0 {
		return skip(skipNoEmail)
	}
	var id probe.GitIdentity
	if root := repoTarget(r.Target); root != "" {
		id = sp.p.RepoIdentity(ctx, root)
	} else {
		id = sp.p.GlobalIdentity(ctx)
	}
	v := map[string]string{onboard.VarGitEmail: id.Email, onboard.VarEmail: emails[0]}
	if !id.Class.Ran() {
		return outcome(id.Outcome, v)
	}
	if !slices.ContainsFunc(emails, func(e string) bool { return strings.EqualFold(e, id.Email) }) {
		got := id.Email
		if got == "" {
			got = "no email"
		}
		return result(onboard.StatusFail, "git config user.email is "+got, v)
	}
	return result(onboard.StatusOK, "", v)
}

// isHTTPS: the request's route is https (answers first, else the setup).
func (sp setupProber) isHTTPS(r opssetup.ProbeRequest) bool {
	return onboard.GitRoute(pick(r.Answers, onboard.QIDRoute, string(r.Setup.GitHub.Route))) == onboard.RouteHTTPS
}

// skipHTTPS is the ssh checks' skip on the https route.
const skipHTTPS = "route is https with the account's gh login; no ssh"

// sshKey is github.ssh_key (4.3): the recorded key file (private key or
// .pub; a .pub is never required) exists and ssh-keygen -lf reads its
// fingerprint (lore never reads the file itself).
func (sp setupProber) sshKey(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	if sp.isHTTPS(r) {
		return skip(skipHTTPS)
	}
	path := pick(r.Answers, onboard.QIDSSHKey, r.Setup.GitHub.SSHKey)
	if path == "" {
		return skip(skipNoKey)
	}
	v := map[string]string{onboard.VarPath: path}
	k := sp.p.KeyFileFingerprint(ctx, path)
	v[onboard.VarFingerprint] = k.Fingerprint
	return outcome(k.Outcome, v)
}

// sshAuth is github.ssh_auth (ssh -T answered with a greeting) and
// github.ssh_user (the greeting names the setup's user).
func (sp setupProber) sshAuth(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	user := pick(r.Answers, onboard.QIDGitHubAccount, r.Setup.GitHub.User)
	if r.Check.ID == onboard.CheckGitHubSSHUser && user == "" {
		return skip(skipNoUser)
	}
	if sp.isHTTPS(r) {
		return skip(skipHTTPS)
	}
	rt := sp.route(r)
	a := sp.p.SSHAuth(ctx, rt)
	v := map[string]string{onboard.VarAlias: a.Host, onboard.VarUser: user, onboard.VarGot: a.User, onboard.VarPath: rt.SSHKey}
	res := outcome(a.Outcome, v)
	if r.Check.ID == onboard.CheckGitHubSSHUser && res.Status != onboard.StatusOK {
		// No greeting, so no account to compare: github.ssh_auth reports
		// why (two rows for one cause read as a contradiction, and this
		// fix's "logs in as {got}" had no {got}; realmac findings 7, 8).
		return skip(skipNoGreeting)
	}
	if r.Check.ID == onboard.CheckGitHubSSHUser && res.Status == onboard.StatusOK && !strings.EqualFold(a.User, user) {
		if rt.HostAlias == "" && rt.SSHKey != "" {
			// ssh -i probed the setup's recorded key, not the alias: name
			// the key, and fix it by dropping the key (realmac NEW-2: the
			// alias already logged in as the account, and the old text
			// sent the person to edit a working ~/.ssh/config).
			key := pick(r.Answers, onboard.QIDSSHKey, r.Setup.GitHub.SSHKey)
			v[onboard.VarSSHKey], v[onboard.VarFixVariant] = key, string(onboard.FixRecordedKey)
			return result(onboard.StatusFail, "the recorded key "+key+" logs in as "+a.User, v)
		}
		return result(onboard.StatusFail, a.Host+" logs in as "+a.User, v)
	}
	return res
}

// aliasConfig is github.alias_config (4.3): the alias block has
// IdentitiesOnly yes.
func (sp setupProber) aliasConfig(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	if sp.isHTTPS(r) {
		return skip(skipHTTPS)
	}
	alias := sp.route(r).HostAlias
	if alias == "" {
		return skip(skipNoAlias)
	}
	v := map[string]string{onboard.VarAlias: alias}
	cfg, err := sp.p.ReadSSHConfig(ctx)
	if err != nil {
		return result(onboard.StatusError, err.Error(), v)
	}
	if !cfg.Effective(alias).IdentitiesOnly {
		return result(onboard.StatusFail, "Host "+alias+" has no 'IdentitiesOnly yes'", v)
	}
	return result(onboard.StatusOK, "", v)
}

// ghAccount is github.gh_account (gh has a login for the user) and
// github.gh_matches (which account is gh's active one). gh is optional:
// missing gh fails gh_account (a warn) and skips gh_matches. gh_matches is
// informational (onboard.LevelInfo) and always ok when it runs: lore runs
// gh for a setup as the setup's own login, so gh's active account only
// matters for gh used by hand; a mismatch is said in the detail.
func (sp setupProber) ghAccount(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	user := r.Setup.GitHub.GHAccount
	if user == "" {
		user = pick(r.Answers, onboard.QIDGitHubAccount, r.Setup.GitHub.User)
	}
	if user == "" {
		return skip(skipNoUser)
	}
	s := sp.p.GHAuthStatus(ctx)
	v := map[string]string{onboard.VarUser: user, onboard.VarActive: s.Active}
	if s.Class == probe.ClassToolMissing {
		if r.Check.ID == onboard.CheckGitHubGHMatches {
			return skip(skipNoGH)
		}
		return result(onboard.StatusFail, "gh is not installed", v)
	}
	if s.Class != probe.ClassOK && s.Class != probe.ClassNotLoggedIn {
		return outcome(s.Outcome, v)
	}
	if r.Check.ID == onboard.CheckGitHubGHMatches {
		if s.Active == "" {
			// Nothing to compare (was "gh's active account is ." with an
			// empty name); github.gh_account reports the missing login.
			return skip(skipNoActiveGH)
		}
		if !strings.EqualFold(s.Active, user) {
			// Informational only: lore runs gh for this setup as its own
			// login (GH_TOKEN of user), so the active account matters only
			// for gh run by hand. Never a warning, never a fix to run.
			return result(onboard.StatusOK, "gh's active account is "+s.Active+"; lore runs gh as "+user+" for this setup, so the active account only matters for gh you run by hand", v)
		}
		return result(onboard.StatusOK, "", v)
	}
	if !slices.ContainsFunc(s.Accounts, func(a probe.GHAccount) bool { return strings.EqualFold(a.User, user) }) {
		return result(onboard.StatusFail, "gh has no login for "+user, v)
	}
	return result(onboard.StatusOK, "", v)
}

// storeWrite is store.write: the dry-run push of 4.4, tagged with this
// machine's id.
func (sp setupProber) storeWrite(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	u := sp.storeURL(ctx, r)
	if u == "" {
		return skip(skipNoURL)
	}
	v := sp.urlVars(u, r)
	if dir, ok := sp.p.FolderStorePath(u); ok {
		// A folder store (no git): a temp file in the folder, not a push
		// (for create: in its nearest existing parent when it is missing).
		if creating(r) {
			return outcome(sp.p.FolderCreatable(dir), v)
		}
		return outcome(sp.p.FolderWrite(dir), v)
	}
	id, _, err := machine.Load(sp.a.opts.Home, os.Getenv)
	if err != nil {
		return result(onboard.StatusError, err.Error(), v)
	}
	w := sp.p.WriteRemote(ctx, u, sp.route(r), probeRefToken(id.String()))
	res := writeVars(outcome(w.Outcome, v), w.Class)
	if res.Status == onboard.StatusOK && res.Detail == "" {
		res.Detail = detailWriteOK
	}
	return res
}

// probeRefTokenMax is the longest machine token the probe ref may carry
// (adapters/probe reMachineID allows 1-64 of [A-Za-z0-9_-]).
const probeRefTokenMax = 40

// probeRefToken is the short, ref-safe form of a machine id for the write
// test's refs/lore/probe/<token> (ONB 4.4). The full id is mch_ + 64 hex
// (68 characters), longer than a probe ref allows (A13 bug 2); the dry
// run never creates the ref, so a prefix that tells machines apart is
// enough. Characters outside [A-Za-z0-9_-] are dropped.
func probeRefToken(id string) string {
	var b strings.Builder
	for _, c := range id {
		if b.Len() >= probeRefTokenMax {
			break
		}
		if c == '_' || c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			b.WriteRune(c)
		}
	}
	if b.Len() == 0 {
		return probeRefFallback
	}
	return b.String()
}

// probeRefFallback names the probe ref when the machine id has no usable
// character (never for a real id).
const probeRefFallback = "machine"

// storePrivate is store.private (gh api repos/{owner}/{repo} .private).
func (sp setupProber) storePrivate(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	u := sp.storeURL(ctx, r)
	if u == "" {
		return skip(skipNoURL)
	}
	if _, ok := sp.p.FolderStorePath(u); ok {
		return skip(skipFolderStore)
	}
	v := sp.urlVars(u, r)
	if v[onboard.VarFixVariant] == string(onboard.FixNotGitHub) {
		return skip(skipNotGitHub)
	}
	g := sp.p.GHRepoAs(ctx, v[onboard.VarOwner], v[onboard.VarRepo], opssetup.GHAccount(r.Setup))
	res := outcome(g.Outcome, v)
	if res.Status != onboard.StatusOK {
		// gh could not answer (not logged in, no access, offline): the
		// visibility is unknown. An error (the check could not run), never
		// a fail: a fail offered "This repo is public … make it private"
		// as a runnable fix for a repo that may well be private.
		why := g.Detail
		if why == "" {
			why = string(g.Class)
		}
		return result(onboard.StatusError, "could not tell whether "+v[onboard.VarOwner]+"/"+v[onboard.VarRepo]+" is private: "+why, v)
	}
	switch g.Private {
	case probe.TriYes:
		return res
	case probe.TriNo:
		return result(onboard.StatusFail, v[onboard.VarOwner]+"/"+v[onboard.VarRepo]+" is public", v)
	}
	return result(onboard.StatusError, "gh did not say whether the repo is private", v)
}

// collaborator is invite.github_access (4.6): the invite's GitHub user is
// a collaborator of the org store. The user comes from the github answer
// (team invite) or the setup's github user.
func (sp setupProber) collaborator(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	u := sp.storeURL(ctx, r)
	user := pick(r.Answers, onboard.QIDGitHubAccount, "")
	if user == "" {
		return skip(skipNoInviteGH)
	}
	if u == "" {
		return skip(skipNoURL)
	}
	v := sp.urlVars(u, r)
	v[onboard.VarUser] = user
	if v[onboard.VarFixVariant] == string(onboard.FixNotGitHub) {
		return skip(skipNotGitHub)
	}
	c := sp.p.GHCollaboratorAs(ctx, v[onboard.VarOwner], v[onboard.VarRepo], user, opssetup.GHAccount(r.Setup))
	res := outcome(c.Outcome, v)
	if res.Status == onboard.StatusOK && !c.Collaborator {
		return result(onboard.StatusFail, user+" has no access to "+v[onboard.VarOwner]+"/"+v[onboard.VarRepo]+" yet", v)
	}
	return res
}

// secretsBackend is secrets.backend_present (4.8).
func (sp setupProber) secretsBackend(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	b := pick(r.Answers, onboard.QIDBackend, r.Setup.Secrets.Backend)
	if b == "" || b == onboard.SecretsNone {
		return skip(skipNoBackend)
	}
	v := map[string]string{onboard.VarBackend: b}
	if b == secretsBackendEnv {
		return result(onboard.StatusOK, "", v)
	}
	for _, d := range sp.p.DetectSecrets(ctx) {
		if d.Manager != b {
			continue
		}
		if d.Path == "" {
			return result(onboard.StatusFail, b+" is not on PATH", v)
		}
		return result(onboard.StatusOK, "", v)
	}
	return result(onboard.StatusFail, b+" is not a secrets backend lore knows", v)
}

// foldersExist is folders.exist (4.9): each glob's literal base is a folder.
func (sp setupProber) foldersExist(r opssetup.ProbeRequest) opssetup.ProbeResult {
	globs := r.Setup.Folders
	if a, ok := r.Answers[onboard.QIDFolders]; ok {
		globs = a.List
	}
	if len(globs) == 0 {
		return skip(skipNoFolders)
	}
	var missing []string
	for _, g := range globs {
		base := GlobBase(onboard.ExpandHome(g, sp.p.Home()))
		if fi, err := os.Stat(filepath.Clean(base)); err != nil || !fi.IsDir() {
			missing = append(missing, base)
		}
	}
	v := map[string]string{onboard.VarFolder: strings.Join(missing, ", ")}
	if len(missing) > 0 {
		return result(onboard.StatusFail, "not a folder: "+strings.Join(missing, ", "), v)
	}
	return result(onboard.StatusOK, "", v)
}

// Prefill implements opssetup.Prober: the value or choices a question
// starts from (4.2–4.9). Every read is local or a harmless status call.
func (sp setupProber) Prefill(ctx context.Context, id onboard.PrefillID, setup onboard.SetupEntry) (opssetup.PrefillOut, error) {
	var cache *prefillCache // nil-safe: a bare setupProber probes every time
	if sp.a != nil {
		cache = sp.a.prefillCache
	}
	key, keep := prefillCacheKey(id, setup)
	ctx = probe.WithLabel(ctx, "prefill "+string(id)) // debug trace label
	if keep {
		if out, at, ok := cache.get(key); ok {
			probe.RecordCached(ctx, string(id), at) // Details: "cached, from <time>", not "no calls"
			return out, nil
		}
	}
	out, err := sp.prefill(ctx, id, setup)
	if err == nil && keep {
		cache.put(key, out)
	}
	return out, err
}

// prefill is Prefill without the cache: one detection, probed now.
func (sp setupProber) prefill(ctx context.Context, id onboard.PrefillID, setup onboard.SetupEntry) (opssetup.PrefillOut, error) {
	text := func(s string) opssetup.PrefillOut { return opssetup.PrefillOut{Value: onboard.Answer{Text: s}} }
	switch id {
	case onboard.PrefillNone, onboard.PrefillInviteEmail:
		// The invite's email is the join op's (it parses the invite).
		return opssetup.PrefillOut{}, nil
	case onboard.PrefillGitUserName:
		return text(sp.p.GlobalIdentity(ctx).Name), nil
	case onboard.PrefillGitUserEmail:
		return text(sp.p.GlobalIdentity(ctx).Email), nil
	case onboard.PrefillGHAccounts:
		s := sp.p.GHLogins() // hosts.yml only; the gh check asks GitHub
		out := text(s.Active)
		for _, a := range s.Accounts {
			c := onboard.Choice{Value: a.User, Label: a.User}
			if a.Active {
				c.Detail = "active in gh"
			}
			out.Choices = append(out.Choices, c)
		}
		return out, nil
	case onboard.PrefillSSHGitHubKeys:
		return sp.prefillAliases(ctx, setup)
	case onboard.PrefillGitHubUsers:
		return sp.prefillUsers(ctx, setup)
	case onboard.PrefillSSHPubKeys:
		// Choices only: the value is the saved key, never a guess (the
		// route decides the key; an unrelated lone .pub once filled it).
		out := opssetup.PrefillOut{Value: onboard.Answer{Text: setup.GitHub.SSHKey}}
		for _, k := range sp.p.PublicKeys(ctx) {
			path := sp.tildePath(k.Path)
			out.Choices = append(out.Choices, onboard.Choice{Value: path, Label: path, Detail: strings.TrimSpace(k.Type + " " + k.Fingerprint)})
		}
		return out, nil
	case onboard.PrefillToolsDetected:
		out := opssetup.PrefillOut{Value: onboard.Answer{IsList: true, List: []string{}}}
		for _, d := range sp.p.DetectTools(ctx) {
			c := onboard.Choice{Value: d.Tool, Label: toolprofile.DisplayName(d.Tool)}
			if d.Detected {
				c.Detail = "found"
				out.Value.List = append(out.Value.List, d.Tool)
			}
			out.Choices = append(out.Choices, c)
		}
		return out, nil
	case onboard.PrefillSecretsFound:
		out := opssetup.PrefillOut{}
		// PATH lookup only: the version calls (gcloud, az, aws, gopass:
		// seconds each) belong to the secrets step's check, not to plan.
		for _, d := range sp.p.FindSecrets() {
			if d.Path == "" {
				continue
			}
			if out.Value.Text == "" {
				out.Value.Text = d.Manager
			}
			out.Choices = append(out.Choices, onboard.Choice{Value: d.Manager, Label: d.Program, Detail: sp.tildePath(d.Path)})
		}
		out.Choices = append(out.Choices, onboard.Choice{Value: onboard.SecretsNone, Label: onboard.SecretsNone})
		return out, nil
	case onboard.PrefillFoldersHere:
		// The start folder (or its git repo), never its parent (realmac
		// M1); nothing at the home folder (opssetup.PrefillFolder), and
		// nothing when the suggestions' own rule refuses that folder for
		// this setup (opssetup.StartFolderAllowed: another identity's
		// includeIf folder, another setup's folder, or outside this
		// identity's own includeIf folders, which the step suggests).
		f, err := sp.FolderFacts(ctx, opssetup.FoldersIn{Setup: setup.Name})
		if err != nil {
			return opssetup.PrefillOut{}, err
		}
		f.Emails = opssetup.SetupEmails(setup, f.Emails)
		g := opssetup.PrefillFolder(f.Home, f.Start, f.GitRoot)
		dir := f.Start
		if f.GitRoot != "" {
			dir = f.GitRoot
		}
		if g == "" || !opssetup.StartFolderAllowed(f, dir) {
			return opssetup.PrefillOut{Value: onboard.Answer{IsList: true, List: []string{}}}, nil
		}
		if sp.foldersTieWith(setup, g) {
			// A glob that ties with another setup's would be refused by
			// this step's own folders.overlap check: never prefill what
			// the step then rejects (realmac finding 14). The field stays
			// empty for the person to choose.
			return opssetup.PrefillOut{Value: onboard.Answer{IsList: true, List: []string{}}}, nil
		}
		return opssetup.PrefillOut{Value: onboard.Answer{IsList: true, List: []string{g}}}, nil
	case onboard.PrefillStoreURLCreate:
		alias, user := setup.GitHub.HostAlias, setup.GitHub.User
		if alias == "" {
			alias = probe.GitHubHost
		}
		if user == "" {
			return opssetup.PrefillOut{}, nil
		}
		return text("git@" + alias + ":" + user + "/lore-me" + gitSuffix), nil
	}
	return opssetup.PrefillOut{}, errs.New(errs.InvalidInput, "unknown prefill "+string(id)).WithHint("the catalog names a prefill this lore does not have")
}

// prefillAliases is the 4.3 table: one choice per ~/.ssh/config alias of
// github.com (plus github.com itself), each with its key and the user ssh
// -T greets, probed in parallel under the limiter.
func (sp setupProber) prefillAliases(ctx context.Context, setup onboard.SetupEntry) (opssetup.PrefillOut, error) {
	cfg, err := sp.p.ReadSSHConfig(ctx)
	if err != nil {
		return opssetup.PrefillOut{}, err
	}
	aliases := cfg.GitHubAliases()
	if !slices.ContainsFunc(aliases, func(a probe.SSHAlias) bool { return a.Alias == probe.GitHubHost }) {
		aliases = append(aliases, cfg.Effective(probe.GitHubHost))
		aliases[len(aliases)-1].Alias = probe.GitHubHost
	}
	// Key path and fingerprint only (local files and ssh-keygen -l): the
	// "logs in as" greeting is ssh -T over the network, which the github
	// step's ssh.auth check shows; plan never waits on it.
	choices := make([]onboard.Choice, len(aliases))
	var wg sync.WaitGroup
	for i, a := range aliases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			parts := []string{}
			if len(a.IdentityFiles) > 0 {
				key := a.IdentityFiles[0]
				parts = append(parts, key)
				if k := sp.p.KeyFileFingerprint(ctx, key); k.Fingerprint != "" {
					parts = append(parts, k.Fingerprint)
				}
			}
			choices[i] = onboard.Choice{Value: a.Alias, Label: a.Alias, Detail: probe.Sanitize(strings.Join(parts, " · "))}
		}()
	}
	wg.Wait()
	// The value is the saved alias only: with none, the route is decided
	// from the account when the step is checked (ops/setup deriveRoute).
	out := opssetup.PrefillOut{Choices: choices, Value: onboard.Answer{Text: setup.GitHub.HostAlias}}
	return out, nil
}

// tildePath writes a path under the home folder as ~/… (the form 3.2
// keeps in config.yaml and the page shows).
func (sp setupProber) tildePath(p string) string {
	if h := sp.p.Home(); h != "" {
		if rel, ok := strings.CutPrefix(p, h+string(filepath.Separator)); ok {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return p
}

// prefillUsers is the github_account choices (4.3, onboard.PrefillGitHubUsers):
// the users ssh -T greets through github.com and each alias, then gh's
// logins. The value is the setup's user, else gh's active account, else
// the only user found.
func (sp setupProber) prefillUsers(ctx context.Context, setup onboard.SetupEntry) (opssetup.PrefillOut, error) {
	out := opssetup.PrefillOut{Value: onboard.Answer{Text: setup.GitHub.User}}
	add := func(user, detail string) {
		if user == "" {
			return
		}
		for i, c := range out.Choices {
			if strings.EqualFold(c.Value, user) {
				if detail != "" && !strings.Contains(c.Detail, detail) {
					out.Choices[i].Detail = strings.TrimSpace(c.Detail + " · " + detail)
				}
				return
			}
		}
		out.Choices = append(out.Choices, onboard.Choice{Value: user, Label: user, Detail: detail})
	}
	// Local only: the setup's own user and gh's logins from hosts.yml. The
	// user each ssh alias greets as needs ssh -T (network), which the
	// github step's checks show; plan never waits on it.
	gh := sp.p.GHLogins()
	for _, a := range gh.Accounts {
		d := "gh login"
		if a.Active {
			d = "gh active"
		}
		add(a.User, d)
	}
	if out.Value.Text == "" {
		switch {
		case gh.Active != "":
			out.Value.Text = gh.Active
		case len(out.Choices) == 1:
			out.Value.Text = out.Choices[0].Value
		}
	}
	return out, nil
}

// Details of passing checks (A13 bug 9: a ✓ row says what was seen, as
// the 5.4 step screen shows "Hi mira" or the key path).
const (
	detailReadOK      = "read ok"
	detailReadEmpty   = "read ok (empty repo)"
	detailWriteOK     = "dry-run push ok"
	detailMarkerOK    = "lore-store.yaml found"
	detailPrivate     = "private"
	detailConfigOK    = "setups[] is valid"
	detailNameFree    = "name is free"
	detailOwnName     = "this setup's own name"
	detailIDOnly      = "IdentitiesOnly yes"
	detailNoTie       = "no other setup ties on these folders"
	detailNoShadow    = "no other setup takes part of these folders"
	detailNoStores    = "no stores attached in .lore/stores.yaml"
	detailNoOrgRepo   = "this repo names no org"
	detailSetupNoOrg  = "a solo setup works in any repo"
	detailOrgFree     = "no lore-org.yaml in the store yet"
	detailLoreMissing = "lore is not set up in this repo yet (repo.init)"
	detailHooksOther  = "hooks are checked in the repo lore runs in"
	detailNoTools     = "no tools chosen for this setup"
)

// repoRoot is the repo a repo check looks at: the Target's folder, else
// this process's repo; "" outside one.
func (sp setupProber) repoRoot(ctx context.Context, r opssetup.ProbeRequest) string {
	if root := repoTarget(r.Target); root != "" {
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			return root
		}
		return ""
	}
	return sp.hereTop(ctx)
}

// loreMetaRel is the file `lore init` leaves: a repo without it has no
// lore data (a half-made .lore/ with only a cache does not count).
var loreMetaRel = filepath.Join(".lore", "data", "_meta.json")

// loreReady reports whether root holds an initialised .lore/.
func loreReady(root string) bool {
	_, err := os.Stat(filepath.Join(root, loreMetaRel))
	return err == nil
}

// repoInit is repo.init (4.10, warn): the repo has a .lore/. Its fix
// `lore init` runs in the repo (setup.fix runs a repo check's fix there).
func (sp setupProber) repoInit(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	root := sp.repoRoot(ctx, r)
	if root == "" {
		return skip(skipNotInRepo)
	}
	v := map[string]string{onboard.VarRepoRoot: root}
	if !loreReady(root) {
		if t := initTargets(ctx, sp, r); t != "" {
			v[onboard.VarFixVariant], v[onboard.VarTargets] = string(onboard.FixInitTargets), t
		}
		return result(onboard.StatusFail, "no .lore/ in "+root, v)
	}
	return result(onboard.StatusOK, ".lore/ set up in "+root, v)
}

// initTargets is the --target value for repo.init's fix: the setup's
// chosen tools that have a pointer file, comma separated, in the setup's
// order ("" when none, or the profile registry cannot be read: init then
// does its default, the tool files found in the repo, else AGENTS.md).
func initTargets(ctx context.Context, sp setupProber, r opssetup.ProbeRequest) string {
	profiles, _ := pointerProfiles(ctx, sp.a, setupTools(r))
	return strings.Join(profiles, ",")
}

// pointerProfiles keeps the tools that name a tool profile with a pointer
// file, once each, in order, with those files; none when the registry
// cannot be read (callers then keep init's default).
func pointerProfiles(ctx context.Context, a *App, tools []string) (profiles, files []string) {
	if len(tools) == 0 {
		return nil, nil
	}
	reg, err := skill.Registry(ctx, a)
	if err != nil {
		return nil, nil
	}
	for _, t := range tools {
		if p, ok := reg.Lookup(t); ok && p.HasPointerFile() && !slices.Contains(profiles, t) {
			profiles, files = append(profiles, t), append(files, p.PointerFile)
		}
	}
	return profiles, files
}

// SetupInitTools is what `lore init` with no --target wires (owner
// decision 2026-11-07): the tools of the setup that applies in this
// folder, when it chose any with a pointer file. No setup, a refused
// resolution or no such tool: zero, and init keeps its default.
func (a *App) SetupInitTools(ctx context.Context) opsrepoinit.SetupTools {
	res, err := a.SetupResolution(ctx, "")
	if err != nil || res.Active.Name == "" || res.Active.Why == opssetup.WhyNone {
		return opsrepoinit.SetupTools{}
	}
	profiles, files := pointerProfiles(ctx, a, res.Entry.Tools)
	if len(profiles) == 0 {
		return opsrepoinit.SetupTools{}
	}
	return opsrepoinit.SetupTools{Setup: res.Active.Name, Profiles: profiles, Files: files}
}

// repoOrg is repo.org (4.10, error): the org the repo resolves to (3.3
// org resolution without a setup hint, the way setup.Store.Active reads
// it) is the setup's org; otherwise the E_SETUP_ORG_MISMATCH case.
func (sp setupProber) repoOrg(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	root := sp.repoRoot(ctx, r)
	if root == "" {
		return skip(skipNotInRepo)
	}
	org := pick(r.Answers, onboard.QIDOrg, r.Setup.Org)
	if !r.Setup.Kind.HasOrg() || org == "" {
		return result(onboard.StatusOK, detailSetupNoOrg, nil)
	}
	s, err := orgcfg.Gather(ctx, orgcfg.In{Home: sp.a.opts.Home, Dir: root, EnvOrg: strings.TrimSpace(os.Getenv(orgcfg.EnvOrg))})
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	if s.ResolveErr != nil {
		return result(onboard.StatusError, s.ResolveErr.Error(), nil)
	}
	got := s.Result.OrgName()
	v := map[string]string{onboard.VarOrg: org, onboard.VarRepoOrg: got, onboard.VarSource: s.Result.MatchedBy, onboard.VarRepoRoot: root}
	switch got {
	case "":
		return result(onboard.StatusOK, detailNoOrgRepo, v)
	case org:
		return result(onboard.StatusOK, "org "+got+" ("+s.Result.MatchedBy+")", v)
	}
	return result(onboard.StatusFail, "this repo is org "+got+" ("+s.Result.MatchedBy+"), the setup is org "+org, v)
}

// repoStores is repo.stores in the this_folder step (4.10): store.read
// through the setup's route for every store of the repo's
// .lore/stores.yaml, read from the file (opening the repo store would
// write in it). The first unreadable store names the fix.
func (sp setupProber) repoStores(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	root := sp.repoRoot(ctx, r)
	if root == "" {
		return skip(skipNotInRepo)
	}
	if !loreReady(root) {
		return skip(detailLoreMissing)
	}
	cfg, err := readStoresFile(root)
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	if len(cfg.Stores) == 0 {
		return result(onboard.StatusOK, detailNoStores, nil)
	}
	type one struct {
		name string
		res  probe.ReadResult
	}
	got := make([]one, len(cfg.Stores))
	var wg sync.WaitGroup
	for i, st := range cfg.Stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = one{name: string(st.Name), res: sp.p.ReadRemote(ctx, st.URL, sp.route(r))}
		}()
	}
	wg.Wait()
	var ok, bad []string
	var v map[string]string
	status := onboard.StatusOK
	for i, g := range got {
		if g.res.Class.Status() == onboard.StatusOK {
			ok = append(ok, g.name)
			continue
		}
		bad = append(bad, g.name+": "+g.res.Detail)
		if v == nil {
			v = sp.urlVars(cfg.Stores[i].URL, r)
			v[onboard.VarStore] = probe.Sanitize(cfg.Stores[i].URL)
			status = g.res.Class.Status()
		}
	}
	if len(bad) > 0 {
		return result(status, strings.Join(bad, "; "), v)
	}
	return result(onboard.StatusOK, "read ok: "+strings.Join(ok, ", "), nil)
}

// setupTools are the tools a tool check covers: the Target's tool (a
// profile name, or "<tool> <root>"), else the answered tools, else the
// setup's.
func setupTools(r opssetup.ProbeRequest) []string {
	if t := strings.TrimSpace(r.Target); t != "" && !filepath.IsAbs(t) && !strings.HasPrefix(t, targetStorePrefix) {
		tool, _, _ := strings.Cut(t, " ")
		return []string{tool}
	}
	if a, ok := r.Answers[onboard.QIDTools]; ok && a.IsList {
		return a.List
	}
	return r.Setup.Tools
}

// toolOne is one tool's verdict of a tool check.
type toolOne struct {
	tool   string
	status onboard.Status
	detail string
	vars   map[string]string
}

// mergeTools folds per-tool verdicts into one result (a step check covers
// every tool of the setup): the worst status wins, the detail lists each
// tool, the fix vars are the first tool with that status (its fix names
// one tool, as `lore skill install --tool {tool}` does).
func mergeTools(all []toolOne) opssetup.ProbeResult {
	if len(all) == 0 {
		return skip(detailNoTools)
	}
	if len(all) == 1 {
		v := all[0].vars
		if v == nil {
			v = map[string]string{}
		}
		v[onboard.VarTool] = all[0].tool
		return result(all[0].status, all[0].detail, v)
	}
	rank := map[onboard.Status]int{onboard.StatusSkip: 0, onboard.StatusOK: 1, onboard.StatusError: 2, onboard.StatusWarn: 3, onboard.StatusFail: 4}
	worst := all[0]
	parts := make([]string, 0, len(all))
	for _, t := range all {
		if rank[t.status] > rank[worst.status] {
			worst = t
		}
		d := string(t.status)
		if t.detail != "" {
			d = t.detail
		}
		parts = append(parts, t.tool+": "+d)
	}
	v := worst.vars
	if v == nil {
		v = map[string]string{}
	}
	v[onboard.VarTool] = worst.tool
	return result(worst.status, strings.Join(parts, "; "), v)
}

// toolSkill is tools.skill_installed / skill_version / skill_modified
// (4.7, 4.7.1) from skill.status (the Installer doctor uses too).
func (sp setupProber) toolSkill(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	tools := setupTools(r)
	if len(tools) == 0 {
		return skip(detailNoTools)
	}
	reg, err := skill.Registry(ctx, sp.a)
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	home, err := sp.a.SkillHome(ctx)
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	ins := skill.Installer{Home: home, Version: sp.a.BinaryVersion(), Registry: reg}
	st, err := ins.Status(ctx, skill.StatusIn{Tools: tools})
	if err != nil {
		return result(onboard.StatusError, "skill status: "+err.Error(), nil)
	}
	all := make([]toolOne, 0, len(st.Tools))
	older := skill.OlderPathLoreOnce(ctx, sp.a, st.BinaryVersion)
	for _, ts := range st.Tools {
		p, _ := reg.Lookup(ts.Tool) // Status refused unknown names
		if r.Check.ID == onboard.CheckToolsSkillInstalled && !p.HasSkillDir() {
			// A pointer-file tool (cursor, gemini_cli, copilot) never needs
			// a skill folder: its "installed" is the repo's pointer file.
			all = append(all, sp.pointerInstalled(ctx, r, p))
			continue
		}
		row := skill.CheckRow(r.Check.ID, ts, st.BinaryVersion, older)
		all = append(all, toolOne{tool: ts.Tool, status: row.Status, detail: row.Detail, vars: row.Vars})
	}
	return mergeTools(all)
}

// pointerInstalled is tools.skill_installed for a tool that reads lore
// through a pointer file and has no skill folder: always a skip that says
// so. Whether that pointer file is there and imports LORE.md is
// tools.pointer_file's row only (one place, one fix; it showed twice
// before, under both checks).
func (sp setupProber) pointerInstalled(_ context.Context, _ opssetup.ProbeRequest, p toolprofile.Profile) toolOne {
	via := p.PointerFile
	if !p.HasPointerFile() {
		via = toolprofile.CanonicalOutput
	}
	return toolOne{tool: p.Name, status: onboard.StatusSkip,
		detail: toolprofile.DisplayName(p.Name) + " needs no skill; it reads lore through " + via + " (tools.pointer_file checks it)"}
}

// loreMDName is the file a tool's pointer file must import (4.7
// tools.pointer_file: "does not import .lore/LORE.md"); the import may be
// relative to the pointer file's folder, so the name is what is matched.
const loreMDName = "LORE.md"

// pointerFile is tools.pointer_file (4.7, repo × tool, warn): each tool's
// pointer file in the repo mentions LORE.md. Fix: lore render in the repo.
func (sp setupProber) pointerFile(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	root := sp.repoRoot(ctx, r)
	if root == "" {
		return skip(skipNotInRepo)
	}
	if !loreReady(root) {
		return skip(detailLoreMissing)
	}
	reg, err := skill.Registry(ctx, sp.a)
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	var all []toolOne
	for _, t := range setupTools(r) {
		v := map[string]string{onboard.VarRepoRoot: root}
		p, ok := reg.Lookup(t)
		if !ok {
			all = append(all, toolOne{tool: t, status: onboard.StatusSkip, detail: "no tool profile " + t, vars: v})
			continue
		}
		if !p.HasPointerFile() {
			all = append(all, toolOne{tool: t, status: onboard.StatusSkip, detail: t + " reads no pointer file", vars: v})
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(p.PointerFile))
		// A pointer file that is a link out of the repo (a shared team
		// file, a dotfiles checkout that is gone) is one lore refuses to
		// write through: name it as that, not "no CLAUDE.md" or "does not
		// import", whose render fix would only be refused again.
		if _, lerr := WriteThroughLink(root, path); errs.Has(lerr, errs.Refused) {
			end, _, _ := LinkEnd(path)
			lv := maps.Clone(v)
			lv[onboard.VarFixVariant] = string(onboard.FixLinked)
			all = append(all, toolOne{tool: t, status: onboard.StatusFail, vars: lv,
				detail: p.PointerFile + " is a symbolic link to " + end + ", outside the repository; lore does not write through it"})
			continue
		}
		b, err := os.ReadFile(filepath.Clean(path))
		switch {
		case err != nil:
			// A plain render writes it only while the tool is one of the
			// repo's context.targets; the fix (render --add-target) adds it.
			all = append(all, toolOne{tool: t, status: onboard.StatusFail, vars: v,
				detail: "no " + p.PointerFile + " in this repo"})
		case !strings.Contains(string(b), loreMDName):
			all = append(all, toolOne{tool: t, status: onboard.StatusFail, detail: p.PointerFile + " does not import .lore/" + loreMDName, vars: v})
		default:
			all = append(all, toolOne{tool: t, status: onboard.StatusOK, detail: p.PointerFile + " imports .lore/" + loreMDName, vars: v})
		}
	}
	return mergeTools(all)
}

// hooks is tools.hooks (4.7, repo × tool, warn): the --agent-hooks plan
// of the repo has no change for the tool. setup.Host plans the repo this
// process runs in, so another repo's target is skipped.
func (sp setupProber) hooks(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	root := sp.repoRoot(ctx, r)
	if root == "" {
		return skip(skipNotInRepo)
	}
	if top := sp.hereTop(ctx); top == "" || resolvedPath(top) != resolvedPath(root) {
		return skip(detailHooksOther)
	}
	if !loreReady(root) {
		return skip(detailLoreMissing)
	}
	h, err := sp.a.SetupHost(ctx)
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	plans, err := h.PlanAgentHooks(ctx, false, false)
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	var all []toolOne
	for _, t := range setupTools(r) {
		v := map[string]string{onboard.VarRepoRoot: root}
		one := toolOne{tool: t, status: onboard.StatusSkip, detail: t + " has no confirmed session hook", vars: v}
		for _, p := range plans {
			if p.Tool != t {
				continue
			}
			switch {
			case p.Change:
				one.status, one.detail = onboard.StatusFail, "not installed: "+p.File
			case p.Hook.ConfigFile != "":
				one.status, one.detail = onboard.StatusOK, strings.TrimSpace(p.File+" "+p.Note)
			default:
				// No hook to install for this tool here (no confirmed hook,
				// or the tool's folder is not in this repo).
				one.detail = strings.TrimSpace(p.File + " " + p.Note)
			}
			if one.status == onboard.StatusFail {
				break
			}
		}
		all = append(all, one)
	}
	return mergeTools(all)
}

// foldersTieWith reports whether glob, as setup's only folder, would tie
// with another setup's glob (the folders.overlap rule).
func (sp setupProber) foldersTieWith(setup onboard.SetupEntry, glob string) bool {
	cfg, err := ReadSetupsConfig(sp.a.opts.Home)
	if err != nil {
		return false
	}
	me := setup
	me.Folders = []string{glob}
	setups := []onboard.SetupEntry{me}
	for _, s := range cfg.Setups {
		if s.Name != me.Name {
			setups = append(setups, s)
		}
	}
	for _, o := range onboard.Overlaps(setups, sp.p.Home(), filepath.EvalSymlinks) {
		if o.A.Setup == me.Name || o.B.Setup == me.Name {
			return true
		}
	}
	return false
}

// foldersTie is folders.overlap (4.9, error: another setup's glob ties on
// a folder) and folders.shadow (warn: another setup's more specific glob
// takes part of this one), over setups[] with this setup's folders as
// answered.
func (sp setupProber) foldersTie(r opssetup.ProbeRequest) opssetup.ProbeResult {
	cfg, err := ReadSetupsConfig(sp.a.opts.Home)
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	me := r.Setup
	if a, ok := r.Answers[onboard.QIDFolders]; ok && a.IsList {
		me.Folders = a.List
	}
	if len(me.Folders) == 0 {
		return skip(skipNoFolders)
	}
	setups := []onboard.SetupEntry{me}
	for _, s := range cfg.Setups {
		if s.Name != me.Name {
			setups = append(setups, s)
		}
	}
	home := sp.p.Home()
	if r.Check.ID == onboard.CheckFoldersShadow {
		sh := onboard.Shadows(setups, me.Name, home, filepath.EvalSymlinks)
		if len(sh) == 0 {
			return result(onboard.StatusOK, detailNoShadow, nil)
		}
		parts := make([]string, 0, len(sh))
		for _, x := range sh {
			parts = append(parts, x.By.Glob+" stays with "+x.By.Setup)
		}
		return result(onboard.StatusFail, strings.Join(parts, "; "),
			map[string]string{onboard.VarOtherFolder: sh[0].By.Glob, onboard.VarOther: sh[0].By.Setup})
	}
	var parts []string
	var v map[string]string
	for _, o := range onboard.Overlaps(setups, home, filepath.EvalSymlinks) {
		mine, other := o.A, o.B
		if other.Setup == me.Name {
			mine, other = other, mine
		}
		if mine.Setup != me.Name {
			continue // a tie between two other setups is doctor's machine check
		}
		parts = append(parts, mine.Glob+" ties with "+other.Setup+" "+other.Glob)
		if v == nil {
			v = map[string]string{onboard.VarFolder: mine.Glob, onboard.VarSetup: me.Name, onboard.VarOther: other.Setup}
		}
	}
	if len(parts) > 0 {
		return result(onboard.StatusFail, strings.Join(parts, "; "), v)
	}
	return result(onboard.StatusOK, detailNoTie, nil)
}

// okDetail is what a passing check saw, from its vars (A13 bug 9: "Hi
// mira" for ssh, the fingerprint for the key); "" when there is nothing
// worth a line.
func okDetail(id onboard.CheckID, v map[string]string) string {
	switch id {
	case onboard.CheckSetupConfig:
		return detailConfigOK
	case onboard.CheckSetupNameFree:
		return detailNameFree
	case onboard.CheckIdentityEmailFormat:
		return v[onboard.VarEmail]
	case onboard.CheckIdentityGitEmail:
		return "git uses " + v[onboard.VarGitEmail]
	case onboard.CheckGitHubSSHKey:
		return strings.TrimSpace(v[onboard.VarFingerprint] + " " + v[onboard.VarPath])
	case onboard.CheckGitHubSSHAuth, onboard.CheckGitHubSSHUser:
		if g := v[onboard.VarGot]; g != "" {
			return "Hi " + g
		}
	case onboard.CheckGitHubAliasConfig:
		return detailIDOnly
	case onboard.CheckGitHubGHAccount:
		return "gh has a login for " + v[onboard.VarUser]
	case onboard.CheckGitHubGHMatches:
		return "gh's active account is " + v[onboard.VarActive]
	case onboard.CheckStoreURL:
		return v[onboard.VarURL]
	case onboard.CheckStoreMarker:
		return detailMarkerOK
	case onboard.CheckStorePrivate:
		return detailPrivate
	case onboard.CheckOrgFree:
		return detailOrgFree
	case onboard.CheckSecretsBackendPresent:
		return v[onboard.VarBackend] + " found"
	case onboard.CheckFoldersExist:
		return "every folder exists"
	case onboard.CheckSetupOrgEntry, onboard.CheckOrgNameFree:
		return v[onboard.VarOrg]
	}
	return ""
}
