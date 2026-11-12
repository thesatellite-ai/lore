package wire

// setupapply.go — setup.Applier on *App (ONBOARDING_DESIGN 2.3 "Apply",
// 4.1–4.10 Apply lines): the one write a step makes once its error-level
// checks pass.
//
//	ApplySetupEntry    ─▶ setups[] entry (kind, name, default; default cleared on the others)
//	ApplyIdentity      ─▶ solo: setups[].identity (the one source; never the top-level identity:)
//	                      member/admin: orgs[<org>].identity (identity.set --org) + the roster
//	                      actor's display name (actor.edit, setupidentity_roster.go)
//	ApplyGitHub        ─▶ setups[].github
//	ApplyPersonalStore ─▶ store.init me <url> when no personal store yet, else setups[].personal_store
//	                      (+ push batch|manual ─▶ config.yaml personal.push, setupapply_push.go)
//	ApplyTools / ApplySecrets / ApplyFolders ─▶ setups[].tools / .secrets.backend / .folders + default
//	ApplyNone (this_folder, summary) ─▶ nothing
//	ApplyJoin / ApplyTeam ─▶ ops/team ApplyJoin / ApplyAdmin (org.import / org.create + roster rows), then setups[]
//	                         (team + push batch|manual ─▶ the team store's lore-store.yaml, after setups[])
//
// Every write compares first (2.3 "idempotent re-run"): when what would be
// written equals what is there, nothing is written and Changed is false.

import (
	"context"
	"maps"
	neturl "net/url"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
	opsidentity "github.com/thesatellite-ai/lore/ops/identity"
	"github.com/thesatellite-ai/lore/ops/orgcmd"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/team"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// setupApplier is the App's setup.Applier.
type setupApplier struct{ a *App }

var _ opssetup.Applier = setupApplier{}

// Apply implements opssetup.Applier.
func (ap setupApplier) Apply(ctx context.Context, req opssetup.ApplyRequest) (opssetup.ApplyResult, error) {
	e := req.Setup
	ctx = ap.a.withNamedSetup(ctx, e.Name)
	if name := answerText(req.Answers, onboard.QIDName); req.Apply == onboard.ApplySetupEntry && name != "" {
		e.Name = name
	}
	if e.Name == "" {
		return opssetup.ApplyResult{}, errs.New(errs.InvalidInput, "setup apply: the setup has no name").WithHint("answer the kind step first (lore setup plan)")
	}
	switch req.Apply {
	case onboard.ApplyNone:
		return opssetup.ApplyResult{Wrote: []string{}, Setup: e}, nil
	case onboard.ApplySetupEntry:
		if k := answerText(req.Answers, onboard.QIDKind); k != "" {
			e.Kind = onboard.Kind(k)
		}
		if !e.Kind.HasOrg() {
			e.Org = "" // a team setup turned solo keeps no org (orgs[] itself stays)
		}
		if a, ok := req.Answers[onboard.QIDDefault]; ok && a.IsBool {
			e.Default = a.Bool
		}
		return ap.putEntry(e, req.DryRun)
	case onboard.ApplyIdentity:
		return ap.applyIdentity(ctx, e, req)
	case onboard.ApplyGitHub:
		e.GitHub = ap.githubBlock(ctx, e.GitHub, req.Answers)
		return ap.putEntry(e, req.DryRun)
	case onboard.ApplyPersonalStore:
		return ap.applyPersonalStore(ctx, e, req)
	case onboard.ApplyTools:
		if a, ok := req.Answers[onboard.QIDTools]; ok {
			e.Tools = slices.Clone(a.List)
		}
		return ap.putEntry(e, req.DryRun)
	case onboard.ApplySecrets:
		if a, ok := req.Answers[onboard.QIDBackend]; ok {
			e.Secrets.Backend = strings.TrimSpace(a.Text)
			if e.Secrets.Backend == onboard.SecretsNone {
				e.Secrets.Backend = ""
			}
		}
		return ap.putEntry(e, req.DryRun)
	case onboard.ApplyFolders:
		if a, ok := req.Answers[onboard.QIDFolders]; ok {
			e.Folders = slices.Clone(a.List)
		}
		if a, ok := req.Answers[onboard.QIDDefault]; ok && a.IsBool {
			e.Default = a.Bool
		}
		return ap.putEntry(e, req.DryRun)
	case onboard.ApplyJoin, onboard.ApplyTeam:
		return ap.applyRoster(ctx, e, req)
	}
	return opssetup.ApplyResult{}, errs.New(errs.InvalidInput, "setup apply: unknown apply "+string(req.Apply)).WithHint("lore setup plan lists the steps")
}

// githubBlock is the github block the step's answers record (ONB 4.3; the
// engine has already derived the route, ops/setup deriveRoute). Route,
// alias and key always follow the answers (empty alias: github.com; empty
// key: the alias's own key), so a new route never keeps an old key of
// another account (github.key_matches). On ssh the route key's
// fingerprint is recorded (ssh-keygen -lf on the key file, a .pub never
// required; lore never reads the file); on https nothing about ssh is.
func (ap setupApplier) githubBlock(ctx context.Context, g onboard.SetupGitHub, a onboard.Answers) onboard.SetupGitHub {
	setIf(&g.User, answerText(a, onboard.QIDGitHubAccount))
	g.Route = onboard.GitRoute(answerText(a, onboard.QIDRoute))
	g.HostAlias = answerText(a, onboard.QIDHostAlias)
	g.SSHKey = answerText(a, onboard.QIDSSHKey)
	g.KeyFingerprint = ""
	if g.Route == onboard.RouteHTTPS {
		g.HostAlias, g.SSHKey = "", ""
		return g
	}
	if g.Route == "" && (g.HostAlias != "" || g.SSHKey != "") {
		g.Route = onboard.RouteSSH
	}
	g.KeyFingerprint = ap.routeFingerprint(ctx, g)
	return g
}

// routeFingerprint is the fingerprint of the key the ssh route uses: the
// recorded key file, else the alias's (or github.com's) first
// IdentityFile; "" when there is none or ssh-keygen cannot read it (the
// check then compares nothing).
func (ap setupApplier) routeFingerprint(ctx context.Context, g onboard.SetupGitHub) string {
	p, err := ap.a.onboardProbe(ctx)
	if err != nil {
		return ""
	}
	key := g.SSHKey
	if key == "" {
		alias := g.HostAlias
		if alias == "" {
			alias = probe.GitHubHost
		}
		cfg, err := p.ReadSSHConfig(ctx)
		if err != nil {
			return ""
		}
		files := cfg.Effective(alias).IdentityFiles
		if len(files) == 0 || !p.IsFile(p.ExpandPath(files[0])) {
			return ""
		}
		key = files[0]
	}
	return p.KeyFileFingerprint(ctx, key).Fingerprint
}

// answerText is a single-value answer, trimmed ("" when absent).
func answerText(a onboard.Answers, id string) string { return strings.TrimSpace(a[id].Text) }

// setIf sets *dst to v when v is not empty.
func setIf(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

// putEntry writes e (and, when e is the default, clears default on every
// other setup: 3.2 "at most one", 4.9) in one config edit, unless every
// entry already reads that way.
func (ap setupApplier) putEntry(e onboard.SetupEntry, dryRun bool) (opssetup.ApplyResult, error) {
	home := ap.a.opts.Home
	cfg, err := ReadSetupsConfig(home)
	if err != nil {
		return opssetup.ApplyResult{}, err
	}
	if ok, oerr := ap.orgReady(e); oerr != nil || !ok {
		// A member/admin entry is valid only once its orgs[] entry exists
		// (3.2), which the join / team step writes (4.5, 4.6). Until then
		// nothing is written: the entry travels in ApplyResult.Setup (and the
		// saved answers) and the join / team apply writes it whole.
		return opssetup.ApplyResult{Wrote: []string{}, Setup: e}, oerr
	}
	writes := []onboard.SetupEntry{}
	for _, o := range cfg.Setups {
		if e.Default && o.Default && o.Name != e.Name {
			o.Default = false
			writes = append(writes, o)
		}
	}
	if old, ok := cfg.Setup(e.Name); !ok || !sameEntry(old, e) {
		writes = append(writes, e)
	}
	res := opssetup.ApplyResult{Changed: len(writes) > 0, Wrote: []string{}, Setup: e}
	if !res.Changed {
		return res, nil
	}
	res.Wrote = append(res.Wrote, configPath(home))
	if dryRun {
		return res, nil
	}
	return res, putSetups(home, writes)
}

// orgReady reports whether e can be written now: a solo entry always, a
// member/admin one only when it names an existing orgs[] entry.
func (ap setupApplier) orgReady(e onboard.SetupEntry) (bool, error) {
	if !e.Kind.HasOrg() {
		return true, nil
	}
	if e.Org == "" {
		return false, nil
	}
	mc, err := readMachineConfig(ap.a.opts.Home)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(mc.Orgs, func(o identity.Org) bool { return o.Name == e.Org }), nil
}

// sameEntry compares two entries as config.yaml would hold them (an empty
// list and an absent one are the same).
func sameEntry(x, y onboard.SetupEntry) bool {
	bx, ex := yaml.Marshal(x)
	by, ey := yaml.Marshal(y)
	return ex == nil && ey == nil && string(bx) == string(by)
}

// putSetups is PutSetup for several entries in one validated edit, so the
// default moving from one setup to another never passes through a file
// with two defaults or none written half.
func putSetups(home string, es []onboard.SetupEntry) error {
	fresh := make([]*yaml.Node, len(es))
	for i, e := range es {
		var n yaml.Node
		if err := n.Encode(e); err != nil {
			return errs.Wrap(errs.Internal, "wire: encode setup "+e.Name, err)
		}
		plainBools(&n)
		fresh[i] = &n
	}
	return editSetups(home, func(root *yaml.Node) error {
		seq := setupsSeq(root)
		for i, e := range es {
			if j := setupIndex(seq, e.Name); j >= 0 {
				seq.Content[j] = mergeSetupNode(seq.Content[j], fresh[i])
				continue
			}
			seq.Content = append(seq.Content, fresh[i])
		}
		// At most one default, against the file as it is now under the
		// lock: putEntry chose which defaults to clear from a read made
		// before the lock, so two first setups saved at once (each told
		// "no setup is the default yet") both wrote default: true and the
		// second save failed E_CONFIG_INVALID. The default written last
		// wins, as the page's one-at-a-time flow does.
		for _, e := range es {
			if e.Default {
				clearOtherDefaults(seq, e.Name)
			}
		}
		return nil
	})
}

// configKeySetupDefault is a setups[] entry's default flag.
const configKeySetupDefault = "default"

// clearOtherDefaults drops default from every entry of seq but name's.
func clearOtherDefaults(seq *yaml.Node, name string) {
	for _, ent := range seq.Content {
		if n := mapGet(ent, configKeyName); ent.Kind == yaml.MappingNode && (n == nil || n.Value != name) {
			mapDelete(ent, configKeySetupDefault)
		}
	}
}

// applyIdentity is 4.2's Apply.
func (ap setupApplier) applyIdentity(ctx context.Context, e onboard.SetupEntry, req opssetup.ApplyRequest) (opssetup.ApplyResult, error) {
	email := answerText(req.Answers, onboard.QIDEmail)
	if email == "" {
		return opssetup.ApplyResult{}, errs.New(errs.InvalidInput, "setup apply identity: no email answered").WithHint("answer the identity step's email")
	}
	want := identity.Entry{Key: identity.KindHuman + ":" + email, Name: answerText(req.Answers, onboard.QIDName), Handle: answerText(req.Answers, onboard.QIDHandle)}
	home := ap.a.opts.Home
	mc, err := readMachineConfig(home)
	if err != nil {
		return opssetup.ApplyResult{}, err
	}
	if e.Kind.HasOrg() {
		// Identity keeps its one home (CFG9, 3.1): orgs[<org>].identity.
		// The org entry exists only after join / team (4.2 "after the org
		// entry exists"); before that the answers wait in setup_progress.
		res := opssetup.ApplyResult{Wrote: []string{}, Setup: e}
		i := slices.IndexFunc(mc.Orgs, func(o identity.Org) bool { return o.Name == e.Org })
		if i < 0 || mc.Orgs[i].Identity == want {
			return res, nil
		}
		res.Changed, res.Wrote = true, []string{configPath(home)}
		if req.DryRun {
			return res, nil
		}
		if old := mc.Orgs[i].Identity.Key; old != "" && old != want.Key {
			// A new email for the same person: the roster actor gains it
			// first, so the new key rebinds to that one actor.
			if _, err := team.AddIdentityEmail(ctx, orgRosterStore{a: ap.a, org: e.Org}, email); err != nil {
				return opssetup.ApplyResult{}, err
			}
		}
		if _, err = ap.a.SetOrgIdentity(ctx, e.Org, opsidentity.Entry{Key: want.Key, Name: want.Name, Handle: want.Handle}); err != nil {
			return res, err
		}
		// The roster actor row is the team's source of the name (row 3):
		// a changed name goes there through actor.edit, not only into
		// this machine's config.
		if want.Name != "" && want.Name != mc.Orgs[i].Identity.Name {
			return res, ap.renameRosterActor(ctx, e.Org, want)
		}
		return res, nil
	}
	var extra []string
	if a, ok := req.Answers[onboard.QIDEmails]; ok {
		for _, m := range a.List {
			if m = strings.TrimSpace(m); m != "" && m != email && !slices.Contains(extra, m) {
				extra = append(extra, m)
			}
		}
	}
	// A new email on the same setup is the same person: the old key joins
	// aliases (WithKey), so the actor keeps its id (SOURCE_OF_TRUTH, an
	// email change keeps one person).
	next := e.Identity.WithKey(want.Key)
	next.Name, next.Handle, next.Emails = want.Name, want.Handle, extra
	if !sameIdentity(e.Identity, next) {
		e.Identity = next
	}
	// else: the same person and addresses, spelled the way the entry has
	// them (its emails may repeat the key's address): left byte for byte,
	// so saving the step unchanged writes nothing.
	res, err := ap.putEntry(e, req.DryRun)
	if err != nil {
		return res, err
	}
	// SOURCE_OF_TRUTH row 1: the setup's identity is the one source; the
	// top-level identity: is never filled from here (a default setup
	// answers outside every folder by itself, 3.3 step 6).
	return res, nil
}

// applyPersonalStore is 4.4's Apply. A repo chosen on a setup is that
// setup's own personal_store url, and the store gets its marker (store
// init) on save. The top-level personal.store is never written from a
// setup: "Same as my other setups" (inherit) is the only way a setup uses
// it, so a repo picked for one account never becomes every other setup's
// store (realmac N2: "Use an existing repo" wrote personal.store and saved
// the setup as inherit). With no personal.store, inherit is refused rather
// than saved as a setup whose `--to me` reaches nothing.
func (ap setupApplier) applyPersonalStore(ctx context.Context, e onboard.SetupEntry, req opssetup.ApplyRequest) (opssetup.ApplyResult, error) {
	choice := answerText(req.Answers, onboard.QIDPersonalStore)
	// The push answer is read and checked before anything is written.
	push, err := stepPushSettings(onboard.StepPersonalStore, req.Answers)
	if err != nil {
		return opssetup.ApplyResult{}, err
	}
	value, initURL, err := ap.personalStoreValue(choice, answerText(req.Answers, onboard.QIDStoreURL))
	if err != nil {
		return opssetup.ApplyResult{}, err
	}
	// the store the push mode applies to: the shared one for inherit
	pushURL := initURL
	if choice == onboard.PersonalStoreInherit {
		mc, merr := readMachineConfig(ap.a.opts.Home)
		if merr != nil {
			return opssetup.ApplyResult{}, merr
		}
		pushURL = mc.personalURL()
	}
	if perr := refusePushWithoutRemote(push, choice, pushURL); perr != nil {
		return opssetup.ApplyResult{}, perr
	}
	e.PersonalStore = value
	res, err := ap.putEntry(e, req.DryRun)
	if err != nil || choice == onboard.PersonalStoreNone {
		return res, err
	}
	if !req.DryRun && initURL != "" {
		if res, err = ap.initPersonalStore(ctx, res, initURL); err != nil {
			return res, err
		}
	}
	return ap.withPersonalPush(res, push, req.DryRun)
}

// personalStoreValue is what the personal_store step's choice records in
// setups[].personal_store, and the store url to make a store (marker)
// when it names a repo or folder ("" for none and inherit).
func (ap setupApplier) personalStoreValue(choice, raw string) (value, initURL string, err error) {
	switch choice {
	case onboard.PersonalStoreNone:
		return choice, "", nil
	case onboard.PersonalStoreInherit:
		mc, err := readMachineConfig(ap.a.opts.Home)
		if err != nil {
			return "", "", err
		}
		if mc.personalURL() == "" {
			return "", "", errs.New(errs.InvalidInput, "setup apply personal_store: there is no shared personal store (personal.store) to be the same as").
				WithHint("pick a repo for this setup (create, existing or a local folder), or none")
		}
		return choice, "", nil
	case onboard.PersonalStoreLocal:
		// onboard.LocalStoreURL: the default `me` folder under the lore home.
		raw = fileuri.URL(personalFolderDir(ap.a.opts.Home), "")
	case onboard.PersonalStoreCreate, onboard.PersonalStoreExisting:
		if raw == "" {
			return "", "", errs.New(errs.InvalidInput, "setup apply personal_store: "+choice+" needs a store url").
				WithHint("answer store_url, e.g. git@github.com:you/lore-me.git")
		}
	default:
		if raw == "" {
			return "", "", errs.New(errs.InvalidInput, "setup apply personal_store: unknown choice "+strconv.Quote(choice)).
				WithHint("pick create, existing, local, inherit or none")
		}
	}
	url, err := cleanFileURL(raw)
	if err != nil {
		return "", "", err
	}
	// A file:// url naming a bare git repository is refused before
	// config.yaml is touched (makeStore refuses it too, but only after
	// putEntry would have saved the url).
	if u, perr := neturl.Parse(url); perr == nil && u.Scheme == folder.Scheme {
		if rerr := refuseBareRepoFolder("", fileuri.OSPath(u.Path)); rerr != nil {
			return "", "", rerr
		}
	}
	return url, url, nil
}

// initPersonalStore makes url a store (its marker) once the entry naming
// it is saved, adding the write to res.
func (ap setupApplier) initPersonalStore(ctx context.Context, res opssetup.ApplyResult, url string) (opssetup.ApplyResult, error) {
	already, err := ap.a.InitStoreMarker(ctx, url)
	if err != nil {
		return res, err
	}
	if !already {
		res.Changed = true
		res.Wrote = append(res.Wrote, url)
	}
	return res, nil
}

// withPersonalPush is res plus the personal push write (applyPersonalPush):
// config.yaml joins Wrote when it changes.
func (ap setupApplier) withPersonalPush(res opssetup.ApplyResult, push []storepush.Assignment, dryRun bool) (opssetup.ApplyResult, error) {
	changed, err := ap.applyPersonalPush(push, dryRun)
	if err != nil || !changed {
		return res, err
	}
	res.Changed = true
	if p := configPath(ap.a.opts.Home); !slices.Contains(res.Wrote, p) {
		res.Wrote = append(res.Wrote, p)
	}
	return res, nil
}

// withEarlySteps folds into a new member/admin entry the github (4.3) and
// personal store (4.4) steps saved before its org existed: putEntry could
// not write them then (orgReady), so they live only in the setup's saved
// progress, and the join / team apply is the first write of the entry.
// Each step's own progress row is read (not the merged answers: the team
// step's store_url is the team store, never the personal one). A value
// the entry already holds (an edit of a finished setup) is kept. It
// returns the personal store url to make a store once the entry is saved.
// Before, a joiner's GitHub account and personal store were silently
// dropped (journey joiner/day1/join).
func (ap setupApplier) withEarlySteps(ctx context.Context, e onboard.SetupEntry) (onboard.SetupEntry, string, error) {
	ps, err := setupStore(ap).Progress(ctx, e.Name)
	if err != nil {
		return e, "", err
	}
	initURL := ""
	for _, p := range ps {
		if p.Status != onboard.StepDone {
			continue
		}
		switch p.Step {
		case onboard.StepGitHub:
			if e.GitHub.User == "" && answerText(p.Answers, onboard.QIDGitHubAccount) != "" {
				e.GitHub = ap.githubBlock(ctx, e.GitHub, p.Answers)
			}
		case onboard.StepPersonalStore:
			if e.PersonalStore == "" {
				if choice := answerText(p.Answers, onboard.QIDPersonalStore); choice != "" {
					if e.PersonalStore, initURL, err = ap.personalStoreValue(choice, answerText(p.Answers, onboard.QIDStoreURL)); err != nil {
						return e, "", err
					}
				}
			}
		}
	}
	return e, initURL, nil
}

// applyRoster is 4.5 (join) and 4.6 (team) Apply: ops/team writes the org
// entry and the roster rows (idempotent: a re-run is Already / writes
// nothing), then the setups[] entry is written with its org (step 6), now
// that orgs[] holds it. The identity (4.2) and GitHub (4.3) answers come
// from the setup's saved progress, the step's own answers on top.
func (ap setupApplier) applyRoster(ctx context.Context, e onboard.SetupEntry, req opssetup.ApplyRequest) (opssetup.ApplyResult, error) {
	host, ok := ap.a.OrgHost(ctx)
	if !ok {
		return opssetup.ApplyResult{}, errs.New(errs.Unsupported, "setup apply: this lore build has no org commands").WithHint("use the lore release binary")
	}
	ans, err := ap.setupAnswers(ctx, e, req.Answers)
	if err != nil {
		return opssetup.ApplyResult{}, err
	}
	emails := []string{}
	if m := answerText(ans, onboard.QIDEmail); m != "" {
		emails = append(emails, m)
	}
	for _, m := range ans[onboard.QIDEmails].List {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(emails, m) {
			emails = append(emails, m)
		}
	}
	name, handle := answerText(ans, onboard.QIDName), answerText(ans, onboard.QIDHandle)
	ghUser := pick(ans, onboard.QIDGitHubAccount, e.GitHub.User)
	var res team.RosterResult
	// The team step's push answer (its own answers only, never the personal
	// store step's `push`), checked before anything is written; written
	// after the admin setups[] entry below.
	var teamOrg, teamURL string
	var push []storepush.Assignment
	if req.Apply == onboard.ApplyJoin && !strings.HasPrefix(strings.TrimSpace(answerText(ans, onboard.QIDInvite)), onboard.InvitePrefix) {
		// a member an admin added (`lore member add`): the answer names the
		// org (an entry here, or its store url), no invite line
		org, url, oerr := ap.memberOrg(ctx, host, answerText(ans, onboard.QIDInvite), emails, name, handle, req.DryRun)
		if oerr != nil {
			return opssetup.ApplyResult{}, oerr
		}
		e.Kind, e.Org = onboard.KindMember, org
		e.Identity = onboard.SetupIdentity{}
		if req.DryRun {
			return opssetup.ApplyResult{Changed: true, Wrote: []string{url, configPath(ap.a.opts.Home)}, Setup: e}, nil
		}
		res, err = team.ApplyJoinMember(ctx, team.JoinDeps{Orgs: host, Entries: ap.a, Profiles: orgRosterStore{a: ap.a, org: org}},
			team.MemberJoinRequest{Org: org, Store: url, Emails: emails, Name: name, Handle: handle, GitHubUser: ghUser})
	} else if req.Apply == onboard.ApplyJoin {
		inv, derr := onboard.DecodeInvite(answerText(ans, onboard.QIDInvite))
		if derr != nil {
			return opssetup.ApplyResult{}, derr
		}
		e.Kind, e.Org = onboard.KindMember, inv.Org
		e.Identity = onboard.SetupIdentity{} // a team setup reads orgs[<org>].identity (3.2); a solo one turned member drops its own
		if req.DryRun {
			return opssetup.ApplyResult{Changed: true, Wrote: []string{inv.Store, configPath(ap.a.opts.Home)}, Setup: e}, nil
		}
		res, err = team.ApplyJoin(ctx, team.JoinDeps{Orgs: host, Entries: ap.a, Profiles: orgRosterStore{a: ap.a, org: inv.Org}},
			team.JoinRequest{Invite: inv, Emails: emails, Name: name, Handle: handle, GitHubUser: ghUser})
	} else {
		org := pick(ans, onboard.QIDOrg, e.Org)
		url := answerText(ans, onboard.QIDStoreURL)
		if org == "" || url == "" {
			return opssetup.ApplyResult{}, errs.New(errs.InvalidInput, "setup apply team: the org name and its store url are needed").
				WithHint("answer the team step's org and store_url")
		}
		if push, err = stepPushSettings(onboard.StepTeam, req.Answers); err != nil {
			return opssetup.ApplyResult{}, err
		}
		teamOrg, teamURL = org, url
		e.Kind, e.Org = onboard.KindAdmin, org
		e.Identity = onboard.SetupIdentity{} // as for join: the identity lives in orgs[<org>].identity
		if req.DryRun {
			return opssetup.ApplyResult{Changed: true, Wrote: []string{url, configPath(ap.a.opts.Home)}, Setup: e}, nil
		}
		// An agent saving the team step is not refused up front (owner
		// decision 2026-11-11): the org store it creates has no policy
		// rows, so agents.may_manage_people has its default, true, at the
		// roster write (profileWriter). A store that already holds the
		// team's false still refuses there.
		res, err = team.ApplyAdmin(ctx, team.AdminDeps{Stores: ap.a, Orgs: host, Entries: ap.a, Profiles: orgRosterStore{a: ap.a, org: org}},
			team.AdminRequest{Org: org, StoreURL: url, OrgFolder: answerText(ans, onboard.QIDOrgFolder), Own: slices.Clone(ans[onboard.QIDOwn].List),
				Repos: slices.Clone(ans[onboard.QIDRepos].List), Emails: emails, Name: name, Handle: handle, GitHubUser: ghUser})
	}
	if err != nil {
		return opssetup.ApplyResult{}, err
	}
	e, initURL, err := ap.withEarlySteps(ctx, e)
	if err != nil {
		return opssetup.ApplyResult{}, err
	}
	out, err := ap.putEntry(e, false)
	if err != nil {
		return out, err
	}
	if initURL != "" {
		if out, err = ap.initPersonalStore(ctx, out, initURL); err != nil {
			return out, err
		}
	}
	if len(res.Wrote) > 0 || res.Imported || res.Created || res.Rejoined {
		out.Changed = true
		out.Wrote = append(out.Wrote, res.Wrote...)
	}
	// Now that setups[] holds the admin entry, this machine may set the
	// team store's shared settings (isOrgAdmin).
	changed, err := ap.applyTeamPush(ctx, teamOrg, teamURL, push)
	if err != nil {
		return out, err
	}
	if changed {
		out.Changed = true
		if !slices.Contains(out.Wrote, teamURL) {
			out.Wrote = append(out.Wrote, teamURL)
		}
	}
	return out, nil
}

// sameIdentity reports whether two setup identities name the same key,
// name, handle and set of addresses (the key's address and the extra
// emails, compared case-insensitively, order and repeats ignored). Saving
// the identity step unchanged rewrote an entry whose emails repeated the
// key's address without it, so a no-op save changed config.yaml and the
// setup's kept checks no longer matched it (round-3 finding).
func sameIdentity(a, b onboard.SetupIdentity) bool {
	if a.Key != b.Key || a.Name != b.Name || a.Handle != b.Handle {
		return false
	}
	set := func(id onboard.SetupIdentity) map[string]bool {
		m := map[string]bool{}
		if _, addr, ok := strings.Cut(id.Key, ":"); ok && addr != "" {
			m[strings.ToLower(addr)] = true
		}
		for _, x := range id.Emails {
			if x = strings.TrimSpace(x); x != "" {
				m[strings.ToLower(x)] = true
			}
		}
		return m
	}
	return maps.Equal(set(a), set(b))
}

// setupAnswers merges the saved answers of every step of setup (catalog
// order) with step on top: the join / team write needs the identity and
// GitHub answers given steps earlier. Under them lie the answers the saved
// entry holds (identity and GitHub account): a finished setup has no
// progress rows (ONB 4.11), so saving its join or team step again (the
// page's edit) found no identity email and was refused with "the admin
// needs an identity email" while the step's checks passed.
func (ap setupApplier) setupAnswers(ctx context.Context, e onboard.SetupEntry, step onboard.Answers) (onboard.Answers, error) {
	out := entryAnswers(e)
	if e.Org != "" && out[onboard.QIDEmail].Text == "" {
		// A member or admin setup's identity lives in its orgs[] entry.
		if cfg, _, err := orgcfg.LoadConfig(ap.a.opts.Home); err == nil {
			if o := cfg.FindOrg(e.Org); o != nil {
				id := onboard.SetupIdentity{Key: o.Identity.Key, Name: o.Identity.Name, Handle: o.Identity.Handle}
				maps.Copy(out, entryAnswers(onboard.SetupEntry{Identity: id}))
			}
		}
	}
	ps, err := setupStore(ap).Progress(ctx, e.Name)
	if err != nil {
		return nil, err
	}
	if e.Org != "" && len(ps) == 0 {
		// A finished team setup: its addresses on the roster count too
		// (the invite's, after the person changed their email).
		if mails, rerr := team.IdentityEmails(ctx, orgRosterStore{a: ap.a, org: e.Org}); rerr == nil && len(mails) > 0 {
			out[onboard.QIDEmails] = onboard.Answer{IsList: true, List: mails}
		}
	}
	for _, p := range ps {
		maps.Copy(out, p.Answers)
	}
	maps.Copy(out, step)
	return out, nil
}

// entryAnswers is the identity and GitHub answers a saved entry holds
// (empty for one not written yet; a member or admin entry's identity is
// in orgs[], which setupAnswers reads).
func entryAnswers(e onboard.SetupEntry) onboard.Answers {
	out := onboard.Answers{}
	set := func(id, v string) {
		if v = strings.TrimSpace(v); v != "" {
			out[id] = onboard.Answer{Text: v}
		}
	}
	if _, addr, ok := strings.Cut(e.Identity.Key, ":"); ok {
		set(onboard.QIDEmail, addr)
	}
	if len(e.Identity.Emails) > 0 {
		out[onboard.QIDEmails] = onboard.Answer{IsList: true, List: slices.Clone(e.Identity.Emails)}
	}
	set(onboard.QIDName, e.Identity.Name)
	set(onboard.QIDHandle, e.Identity.Handle)
	set(onboard.QIDGitHubAccount, e.GitHub.User)
	return out
}

// orgRosterStore is profilestore.Store for one named org's roster (the
// org just imported or created), whatever repo the process runs in:
// App.Roster opens the roster of the repo's org, which during `lore setup
// join` is usually none.
type orgRosterStore struct {
	a   *App
	org string
}

var _ profilestore.Store = orgRosterStore{}

// Roster implements profilestore.Store (same checks as App.Roster).
//
//	the App's resolved org is o.org (a repo of that org) ─▶ its attached org folder store
//	otherwise ─▶ the org folder store named by the orgs[] entry, read fresh from
//	             config.yaml (org.import / org.create wrote it moments ago in this
//	             same apply), opened alone (openDetached) and closed by done
//
// The second path is the join from ~ or from a repo of another org (ONB
// 4.5, 4.6): the App's org hooks resolved before the entry existed, so
// they never name this org, and the roster writes must still land in its
// org folder.
func (o orgRosterStore) Roster(ctx context.Context, write bool) (profilestore.Roster, func(), error) {
	noop := func() {}
	src, folder, ok, err := o.a.orgRosterSource(ctx, o.org)
	if err != nil {
		return nil, noop, err
	}
	x, done := o.a, noop
	if !ok {
		one, found, ferr := orgFolderStore(o.a.opts.Home, o.org)
		if ferr != nil {
			return nil, noop, ferr
		}
		if !found {
			return nil, noop, errs.New(errs.NotFound, "org "+o.org+" has no store holding its roster").
				WithHint("set org_folder on one of the org's stores in ~/.lore/config.yaml")
		}
		if x, err = openOrgFolderApp(ctx, o.a.opts, o.org, one); err != nil {
			return nil, noop, err
		}
		done = func() { _ = x.Close() }
		src, folder = one.Name, one.Folder
	}
	r := newRosterAt(src, o.org+" "+folder+"/")
	if ok {
		var rdone func()
		if x, rdone, err = o.a.rosterApp(ctx, r); err != nil {
			return nil, noop, err
		}
		done = rdone
	}
	st, err := x.rosterState(ctx, r.src, write)
	if err == nil && write {
		err = x.profileWriter(ctx, st, r.src)
	}
	if err == nil && write {
		// The join and team writes act on the caller's own actor row. org.import
		// registers the caller only when it adds the entry, so a join retried
		// after a failure past the import (the entry is there, the
		// registration is not, e.g. its handle was taken) would find no row
		// forever: the caller is registered here as `lore actor register`
		// does it (identity-contact match, handle check), only when no row
		// exists.
		err = x.ensureRegistered(ctx, st, r)
	}
	if err != nil {
		done()
		return nil, noop, err
	}
	return &profileRoster{app: x, st: st, at: r, person: x.rosterPerson(ctx, st, r)}, done, nil
}

// orgFolderStore is the "<org>/<org folder>" store of the orgs[] entry org
// as config.yaml holds it now (F13: the first store with an org_folder),
// found false when the entry or its org folder is missing.
func orgFolderStore(home, org string) (attach.Store, bool, error) {
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil {
		return attach.Store{}, false, err
	}
	e := cfg.FindOrg(org)
	if e == nil {
		return attach.Store{}, false, nil
	}
	for i := range e.Stores {
		sc := &e.Stores[i]
		if sc.OrgFolder == "" {
			continue
		}
		return attach.Store{Name: store.Source(org + orgconf.RefSep + sc.OrgFolder), URL: sc.URL, Folder: sc.OrgFolder, Branch: attachBranch(sc)}, true, nil
	}
	return attach.Store{}, false, nil
}

// openOrgFolderApp is an App over one org folder store and the machine
// store, nothing else (openDetached; its repo-only state refuses as
// StoreOnlyRoster's does). It writes as org's identity (orgs[org].identity,
// CFG9): the folder's own org resolution answers none outside a repo of
// that org, which would make the joining person the machine's auto
// identity instead of the human:<email> org.import just registered. The
// caller closes it.
func openOrgFolderApp(ctx context.Context, opts Options, org string, one attach.Store) (*App, error) {
	opts.Org = func(context.Context) (*identity.Org, error) {
		cfg, _, err := orgcfg.LoadConfig(opts.Home)
		if err != nil {
			return nil, err
		}
		e := cfg.FindOrg(org)
		if e == nil {
			return nil, errs.New(errs.NotFound, "no org named "+org+" in ~/.lore/"+ConfigFile).WithHint("lore org import <store url> --as " + org)
		}
		return &identity.Org{Name: e.Name, Identity: identity.Entry{Key: e.Identity.Key, Name: e.Identity.Name, Handle: e.Identity.Handle}}, nil
	}
	x, err := New(detachedOpts(opts))
	if err != nil {
		return nil, err
	}
	st, err := openDetached(ctx, x.opts, &one)
	if err != nil {
		return nil, err
	}
	st.eng.SetRedactHook(x.redactTask) // D21, as App.state does
	x.mu.Lock()
	x.opened, x.st = true, st
	x.cfgOpened = true
	x.cfgErr = errs.New(errs.NotAStore, "wire: only the org roster is open here").WithHint("run it inside a git repository")
	x.mu.Unlock()
	return x, nil
}

// ensureRegistered registers the caller on r's roster when st has no
// actors row for the caller's actor id (see orgRosterStore.Roster). An
// agent's caller is the person it acts for (rosterCaller), so the row
// looked for is the person's, never the agent's.
func (x *App) ensureRegistered(ctx context.Context, st *state, r rosterAt) error {
	who, err := x.rosterCaller(ctx, r)
	if err != nil {
		return err
	}
	id := st.actor.Any()
	if !who.ID.IsZero() {
		id = who.ID.Any()
	}
	_, got, err := st.eng.Get(ctx, engine.Ref{Store: r.src, Table: schema.TableActors, ID: id})
	if err == nil && !got.Deleted {
		return nil
	}
	if err != nil && !errs.Has(err, errs.NotFound) {
		return err
	}
	_, err = x.registerCaller(ctx, r)
	return err
}

var _ opssetup.OrgStoreLister = setupStore{}

// OrgStoreURLs implements opssetup.OrgStoreLister: the store urls of the
// orgs[org] entry as config.yaml holds it now (the team step's own-org
// rule, onboard.OwnOrg). It lives here, beside the team apply that writes
// that entry, rather than in setupstore.go.
func (s setupStore) OrgStoreURLs(_ context.Context, org string) ([]string, bool, error) {
	cfg, _, err := orgcfg.LoadConfig(s.a.opts.Home)
	if err != nil {
		return nil, false, err
	}
	e := cfg.FindOrg(org)
	if e == nil {
		return nil, false, nil
	}
	urls := make([]string, 0, len(e.Stores))
	for _, sc := range e.Stores {
		urls = append(urls, sc.URL)
	}
	return urls, true, nil
}

// memberOrg resolves a member join's answer (`lore setup join <org>`):
// an org entry on this machine, or the org's store url (imported first
// when no entry names it, the entry named as the store suggests). It
// returns the entry name and its store url.
func (ap setupApplier) memberOrg(ctx context.Context, host orgcmd.Host, answer string, emails []string, name, handle string, dryRun bool) (string, string, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", "", errs.New(errs.InvalidInput, "setup join: name the org (or its store url), or paste an invite line").
			WithHint("lore setup join <org>")
	}
	if !strings.ContainsAny(answer, ":/") {
		o, err := ap.a.TeamOrigin(ctx, answer)
		if err != nil {
			return "", "", errs.New(errs.NotFound, "no org named "+answer+" on this machine yet; join with its store url").
				WithHint("lore setup join <the org's store url> (your admin knows it)")
		}
		return answer, o.Store, nil
	}
	find := func() string {
		cfg, _, err := orgcfg.LoadConfig(ap.a.opts.Home)
		if err != nil || cfg == nil {
			return ""
		}
		want, _ := storeKey(answer)
		for _, o := range cfg.Orgs {
			for _, st := range o.Stores {
				if k, ok := storeKey(st.URL); ok && k == want {
					return o.Name
				}
			}
		}
		return ""
	}
	if org := find(); org != "" || dryRun {
		return org, answer, nil
	}
	if len(emails) == 0 {
		return "", "", errs.New(errs.InvalidInput, "setup join: no email to join with").WithHint("answer the identity step first")
	}
	// Admit (owner item 19): the member check reads the store's roster
	// before anything is written there; someone not on it gets the
	// refusal with no actor row, no commit and no push.
	admit := func(ctx context.Context, entry string) error {
		return team.CheckMember(ctx, orgRosterStore{a: ap.a, org: entry}, entry, emails)
	}
	rep, err := host.Import(ctx, orgcmd.ImportIn{URL: answer, Key: "human:" + emails[0], Name: name, Handle: handle, Admit: admit, SkipRegister: true}, true)
	if err != nil {
		return "", "", errs.Prefix("setup join "+answer, err)
	}
	if rep.Ask != "" {
		return "", "", errs.New(errs.Internal, "org.import asked although the join confirmed it: "+rep.Ask)
	}
	org := find()
	if org == "" {
		return "", "", errs.New(errs.Internal, "setup join: org import of "+answer+" made no org entry")
	}
	return org, answer, nil
}
