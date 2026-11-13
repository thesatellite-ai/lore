package onboard

import "slices"

// The step catalog as Go values (ONB 2.1, 2.2, section 4) and the check
// table (ONB 6.1 plus the step-only checks of 4.x). Pure data: the page,
// the CLI, `lore doctor --all` and `lore setup repair` all read it, the
// JSON the page shows is marshalled from it at run time (no codegen).
//
// 4.12 (messaging preferences) is deferred (⏸): it will be one more Step
// with Optional: true appended after StepFolders; nothing here assumes the
// list is closed beyond StepIDs().

// Question ids (ONB section 4). Ids are unique within one kind's plan of a
// step; the identity step has two `email` questions told apart by Kinds
// (member prefills from the invite, 4.2).
const (
	QIDKind          = "kind"
	QIDName          = "name"
	QIDDefault       = "default"
	QIDEmail         = "email"
	QIDEmails        = "emails"
	QIDHandle        = "handle"
	QIDGitHubAccount = "github_account"
	QIDHostAlias     = "host_alias"
	QIDSSHKey        = "ssh_key"
	QIDRoute         = "route"
	QIDPersonalStore = "personal_store"
	QIDStoreURL      = "store_url"
	QIDInvite        = "invite"
	QIDOrg           = "org"
	QIDOwn           = "own"
	QIDRepos         = "repos"
	QIDOrgFolder     = "org_folder"
	QIDTools         = "tools"
	QIDBackend       = "backend"
	QIDFolders       = "folders"
	QIDPush          = "push"
)

// The personal_store choice values (ONB 4.4). PersonalStoreInherit ("same
// as my other setups", personal.store) and PersonalStoreNone are A0's
// constants in setups.go, reused so the answer is the config value.
const (
	PersonalStoreCreate   = "create"
	PersonalStoreExisting = "existing"
	PersonalStoreLocal    = "local"
	// LocalStoreFolder is the folder, under ~/.lore/stores, of the local
	// personal store (a plain folder). It starts with "_": GitHub and
	// GitLab owner names cannot, so it never clashes with the owner folder
	// of a git clone (stores/<owner>/<repo>), and "_" marks lore's own, as
	// _store/ does in a store (owner item 17, 2026-11-12).
	LocalStoreFolder = "_me"
	// LegacyLocalStoreFolder is LocalStoreFolder before 2026-11-12; lore
	// moves a personal folder store found there to LocalStoreFolder.
	LegacyLocalStoreFolder = "me"
	// LocalStoreURL is the url the "local folder" choice stands for.
	LocalStoreURL = "file://~/.lore/stores/" + LocalStoreFolder
)

// SecretsNone is the "no secret manager" choice of the secrets step (4.8).
const SecretsNone = "none"

// Limits on free-text answers. The person's name and handle show in doctor
// lines and roster rows; a GitHub login is at most 39 characters.
const (
	maxPersonName  = 200
	maxHandle      = 64
	maxGitHubLogin = 39
	maxURL         = 512
	maxPattern     = 256
)

// reservedSetupNames are store names lore already gives a meaning to
// (`--to repo|me|team|company`); a setup or org named like one would make
// `--to` ambiguous (ONB 2.2 Rules example).
var reservedSetupNames = []string{"repo", "me", "team", "company"}

// allKinds is every kind; a Step or check with it applies everywhere.
func allKinds() []Kind { return Kinds() }

// orgKinds are the kinds that name an orgs[] entry.
func orgKinds() []Kind { return []Kind{KindMember, KindAdmin} }

// Catalog returns every step of section 4 (4.1-4.11) in StepIDs order. A
// fresh copy each call, so callers may filter or edit it.
func Catalog() []Step {
	nameRules := []Rule{R(RuleSetupName), MaxLen(SetupNameMaxLen), NotIn(reservedSetupNames...)}
	return []Step{
		{ // ONB 4.1
			ID:    StepKind,
			Title: "What kind of setup is this?",
			Help:  "A setup is one way you work on this machine: just you, a team you joined, or a team you run. You can have several.",
			Kinds: allKinds(),
			Questions: []Question{
				{ID: QIDKind, Label: "Kind", Type: QChoice, Required: true,
					Help: "lore setup join picks \"Join a team\" for you.",
					Choices: ChoiceSource{Static: []Choice{
						{Value: string(KindSolo), Label: "Just me"},
						{Value: string(KindMember), Label: "Join a team"},
						{Value: string(KindAdmin), Label: "Start a team"},
					}}},
				{ID: QIDName, Label: "Setup name", Type: QText, Required: true,
					Help:  "Used by --setup and in lore doctor lines, e.g. personal or the team's name.",
					Rules: nameRules},
				{ID: QIDDefault, Label: "Use this setup when no folder matches", Type: QBool,
					Kinds: []Kind{KindSolo}},
			},
			Checks: []CheckID{CheckSetupNameFree},
			Apply:  ApplySetupEntry,
		},
		{ // ONB 4.2
			ID:    StepIdentity,
			Title: "Who are you?",
			Help:  "Your name and the email you use for this work. Notes and tasks you write carry it.",
			Kinds: allKinds(),
			Needs: []StepID{StepKind},
			Questions: []Question{
				{ID: QIDName, Label: "Your name", Type: QText, Required: true,
					Prefill: PrefillGitUserName, Rules: []Rule{MaxLen(maxPersonName)}},
				{ID: QIDEmail, Label: "Email", Type: QEmail, Required: true,
					Prefill: PrefillGitUserEmail, Rules: []Rule{R(RuleEmail)},
					Kinds: []Kind{KindSolo, KindAdmin}},
				{ID: QIDEmail, Label: "Email", Type: QEmail, Required: true,
					Help:    "The invite names the address the team expects.",
					Prefill: PrefillInviteEmail, Rules: []Rule{R(RuleEmail)},
					Kinds: []Kind{KindMember}},
				{ID: QIDEmails, Label: "Other addresses you use", Type: QEmailList,
					Help: "Optional. Each one is recorded as yours, so commits and mail from it match you.", Rules: []Rule{R(RuleEmail)}},
				{ID: QIDHandle, Label: "Handle", Type: QText,
					Help: "Optional short name, e.g. your GitHub user.", Rules: []Rule{MaxLen(maxHandle)}},
			},
			Checks: []CheckID{CheckIdentityEmailFormat, CheckIdentityInviteEmail, CheckIdentityGitEmail, CheckIdentityRoster},
			Apply:  ApplyIdentity,
		},
		{ // ONB 4.3
			ID:    StepGitHub,
			Title: "Which GitHub account?",
			Help:  "Pick the account this setup works as. lore then finds how git reaches GitHub as it: an ssh route (an alias of ~/.ssh/config, or github.com itself) that logs in as the account, else https with its gh login. lore never edits ~/.ssh/config. Skip it (save it with no answers: lore setup apply --setup <setup> --step github --answers '{}') when your stores are not on GitHub (another git host, a local path) or this machine is offline: git then reaches each store as it is configured.",
			Kinds: allKinds(),
			Needs: []StepID{StepIdentity},
			// B1-24: a store on another git host or a local path, or a
			// machine with no gh login or GitHub key, could never finish
			// the guided setup; skipped, git reaches stores as configured.
			Optional: true,
			Questions: []Question{
				{ID: QIDGitHubAccount, Label: "GitHub account", Type: QChoice, Required: true,
					Choices: ChoiceSource{Prefill: PrefillGitHubUsers},
					Rules:   []Rule{MaxLen(maxGitHubLogin)}},
				// host_alias, ssh_key and route are derived when left
				// empty (ops/setup deriveRoute: the route that logs in as
				// the account); answering them forces a route.
				{ID: QIDHostAlias, Label: "ssh host alias", Type: QChoice,
					Help:    "Optional: lore picks the route that logs in as the account. github.com, or a Host block in ~/.ssh/config whose HostName is github.com.",
					Choices: ChoiceSource{Prefill: PrefillSSHGitHubKeys},
					Rules:   []Rule{MaxLen(maxHandle)}},
				{ID: QIDSSHKey, Label: "ssh key file", Type: QChoice,
					Help:    "Optional: the key the route uses (a private key path or its .pub). Empty: the alias's own key, recorded with its fingerprint.",
					Choices: ChoiceSource{Prefill: PrefillSSHPubKeys},
					Rules:   []Rule{R(RuleKeyPath), MaxLen(maxURL)}},
				{ID: QIDRoute, Label: "How git connects", Type: QChoice,
					Help: "Optional: ssh, or https with the account's gh login. Empty: ssh when a route logs in as the account, else https.",
					Choices: ChoiceSource{Static: []Choice{
						{Value: string(RouteSSH), Label: "ssh", Detail: "an ssh route that logs in as the account"},
						{Value: string(RouteHTTPS), Label: "https", Detail: "the account's gh login, token only in memory"}}}},
			},
			Checks: []CheckID{CheckGitHubSSHKey, CheckGitHubSSHAuth, CheckGitHubSSHUser, CheckGitHubKeyMatches,
				CheckGitHubAliasConfig, CheckGitHubGHAccount, CheckGitHubGHMatches, CheckGitHubIdentityEmail},
			Apply: ApplyGitHub,
		},
		{ // ONB 4.4
			ID:    StepPersonalStore,
			Title: "Where do your own notes go?",
			Help:  "Your personal store (lore's `me`) is a private git repo only you write to.",
			Kinds: allKinds(),
			Needs: []StepID{StepGitHub},
			Questions: []Question{
				{ID: QIDPersonalStore, Label: "Personal store", Type: QChoice, Required: true,
					Choices: ChoiceSource{Static: []Choice{
						{Value: PersonalStoreCreate, Label: "Create a new private repo"},
						{Value: PersonalStoreExisting, Label: "Use an existing repo"},
						{Value: PersonalStoreLocal, Label: "A local folder", Detail: LocalStoreURL},
						{Value: PersonalStoreInherit, Label: "Same as my other setups", Detail: "personal.store"},
						{Value: PersonalStoreNone, Label: "None", Detail: "--to me fails in this setup"},
					}}},
				{ID: QIDStoreURL, Label: "Repo url", Type: QText,
					Help:    "An ssh url like git@github.com:you/lore-me.git. Needed for create and existing.",
					Prefill: PrefillStoreURLCreate, Rules: []Rule{R(RuleGitURL), MaxLen(maxURL)}},
				{ID: QIDPush, Label: "When notes reach the repo", Type: QChoice, Choices: pushChoices(),
					Help: "Optional. How your notes reach the repo; empty is now. Needs a git repo (create, existing, or same as my other setups pointing at one): a local folder has nothing to push to. Sets personal.push in ~/.lore/config.yaml, which every setup's personal store follows."},
			},
			Checks: []CheckID{CheckStoreURL, CheckStoreRead, CheckStoreWrite, CheckStoreMarker, CheckStorePrivate},
			Apply:  ApplyPersonalStore,
		},
		{ // ONB 4.5
			ID:    StepJoin,
			Title: "Join your team",
			Help:  "If your admin added you (lore member add), give your org's store url (the one lore member add printed for them; the org's name works only once this machine has the org): lore finds you on the roster by your email. Otherwise paste the invite line they sent. Neither holds a secret: GitHub decides who can reach the team's store.",
			Kinds: []Kind{KindMember},
			Needs: []StepID{StepIdentity, StepGitHub},
			Questions: []Question{
				{ID: QIDInvite, Label: "Org or invite", Type: QInvite, Required: true,
					Help: "Your org's store url (or its name, when this machine has it already), or one line starting with lore-invite:v1:", Rules: []Rule{MaxLen(maxURL * 4)}},
			},
			Checks: []CheckID{CheckInviteParse, CheckStoreRead, CheckInviteFound, CheckInviteStatus, CheckInviteEmail, CheckStoreWrite},
			Apply:  ApplyJoin,
		},
		{ // ONB 4.6
			ID:    StepTeam,
			Title: "Start or attach your team store",
			Help:  "lore makes a private org store for the team's shared notes, tasks and roster, and adds you as its first admin. Given your team's existing store (a new machine of an admin), lore attaches it: the org entry is built from the store's lore-org.yaml (every store, own and repos, as a member's join) and you are added to its roster as an admin when you are not on it yet.",
			Kinds: []Kind{KindAdmin},
			Needs: []StepID{StepIdentity, StepGitHub},
			Questions: []Question{
				{ID: QIDOrg, Label: "Team name", Type: QText, Required: true,
					Rules: []Rule{R(RuleOrgName), MaxLen(SetupNameMaxLen), NotIn(reservedSetupNames...)}},
				{ID: QIDStoreURL, Label: "Team store url", Type: QText, Required: true,
					Help:    "A new or empty private repo, e.g. git@github.com:agency/lore-team.git, or your team's existing store to attach it.",
					Prefill: PrefillStoreURLCreate, Rules: []Rule{R(RuleGitURL), MaxLen(maxURL)}},
				{ID: QIDOwn, Label: "The team's own repos", Type: QGlobList,
					Help: "Patterns host/owner/repo, with * or ** as a whole segment and never for the host, like " + RepoPatternExample + ".", Rules: []Rule{R(RuleRepoPattern), MaxLen(maxPattern)}},
				{ID: QIDRepos, Label: "Client repos", Type: QGlobList,
					Help: "Optional patterns of client repos the team works in, in the same shape, like github.com/client/*.", Rules: []Rule{R(RuleRepoPattern), MaxLen(maxPattern)}},
				{ID: QIDOrgFolder, Label: "Folder inside the store", Type: QText,
					Help: "Leave empty for org.", Rules: []Rule{R(RuleSnakeCase), MaxLen(SetupNameMaxLen)}},
				{ID: QIDPush, Label: "When notes reach the team store", Type: QChoice, Choices: pushChoices(),
					Help: "Optional. How every member's writes reach the team store; empty is now. Written into the store's lore-store.yaml, so every clone follows it; only an admin can change it later (lore store set)."},
			},
			Checks: []CheckID{CheckStoreURL, CheckStoreRead, CheckStoreWrite, CheckStorePrivate, CheckOrgFree, CheckOrgNameFree},
			Apply:  ApplyTeam,
		},
		{ // ONB 4.7
			ID:       StepTools,
			Title:    "Which coding tools do you use?",
			Help:     "lore records the tools you use and checks each one has the lore skill, current; a missing or old skill shows with its fix, lore skill install --tool <tool>.",
			Kinds:    allKinds(),
			Needs:    []StepID{StepKind},
			Optional: true,
			Questions: []Question{
				{ID: QIDTools, Label: "Tools", Type: QMulti,
					Prefill: PrefillToolsDetected, Choices: ChoiceSource{Prefill: PrefillToolsDetected}},
			},
			Checks: []CheckID{CheckToolsSkillInstalled, CheckToolsSkillVersion, CheckToolsSkillModified,
				CheckToolsPointerFile, CheckToolsHooks},
			Apply: ApplyTools,
		},
		{ // ONB 4.8
			ID:       StepSecrets,
			Title:    "Where do you keep secrets?",
			Help:     "New secret references default to this manager. lore only asks it for its version, never for a secret.",
			Kinds:    allKinds(),
			Needs:    []StepID{StepKind},
			Optional: true,
			Questions: []Question{
				{ID: QIDBackend, Label: "Secret manager", Type: QChoice,
					Prefill: PrefillSecretsFound,
					Choices: ChoiceSource{Static: []Choice{{Value: SecretsNone, Label: "None"}}, Prefill: PrefillSecretsFound}},
			},
			Checks: []CheckID{CheckSecretsBackendPresent},
			Apply:  ApplySecrets,
		},
		{ // ONB 4.9
			ID:       StepFolders,
			Title:    "Which folders is this setup for?",
			Help:     "lore picks this setup inside these folders; the most specific folder wins.",
			Kinds:    allKinds(),
			Needs:    []StepID{StepKind},
			Optional: true,
			Questions: []Question{
				{ID: QIDFolders, Label: "Folders", Type: QGlobList,
					Help:    "Absolute globs, ~ allowed; ** means any depth, e.g. ~/www/projects/agency/**.",
					Prefill: PrefillFoldersHere, Rules: []Rule{R(RuleAbsGlob), MaxLen(maxPattern)}},
				{ID: QIDDefault, Label: "Use this setup when no folder matches", Type: QBool},
			},
			Checks: []CheckID{CheckFoldersExist, CheckFoldersOverlap, CheckFoldersShadow, CheckFoldersReachable},
			Apply:  ApplyFolders,
		},
		{ // ONB 4.10: only when started inside a git repo (PlanFor inRepo).
			ID:       StepThisFolder,
			Title:    "This folder",
			Help:     "The folder you started lore setup in: which setup lore uses there, whether it is this one, and whether lore is set up in the repo. Nothing to answer; the checks run as the step opens.",
			Kinds:    allKinds(),
			Needs:    []StepID{StepKind},
			Optional: true,
			Checks:   []CheckID{CheckRepoInit, CheckRepoOrg, CheckRepoStores, CheckIdentityGitEmail},
			Apply:    ApplyNone,
		},
		{ // ONB 4.11: writes nothing; deletes progress rows when all done.
			ID:    StepSummary,
			Title: "Summary",
			Help:  "Every step and every warning with its fix. Run lore doctor --all any time to see this again.",
			Kinds: allKinds(),
			Apply: ApplyNone,
		},
	}
}

// StepByID returns the catalog step with id.
func StepByID(id StepID) (Step, bool) {
	for _, s := range Catalog() {
		if s.ID == id {
			return s, true
		}
	}
	return Step{}, false
}

// StoreRole is which store a store.* check is about; it decides the level
// of store.write and store.private (ONB 4.4, 4.6, 6.1).
type StoreRole string

// Store roles.
const (
	// StoreRoleNone: the check is not about a store.
	StoreRoleNone StoreRole = ""
	// StoreRoleMe is the setup's personal store.
	StoreRoleMe StoreRole = "me"
	// StoreRoleOrg is the org roster store (also the store an admin
	// creates in 4.6).
	StoreRoleOrg StoreRole = "org"
	// StoreRoleAttached is a writable `.lore/stores.yaml` store.
	StoreRoleAttached StoreRole = "attached"
	// StoreRoleAttachedReadOnly is a store attached --read-only.
	StoreRoleAttachedReadOnly StoreRole = "attached_read_only"
)

// LevelCtx is the context a check's level may depend on.
type LevelCtx struct {
	Kind  Kind
	Store StoreRole
}

// LevelFor is the level of check id in context c. Two checks vary:
//   - store.private: error for an org store (4.6: "error for an org store,
//     not a warning"), warn otherwise (4.4). 6.1 also says error for `me`;
//     the step table of 4.4 (warn) wins here because a public personal
//     repo is the person's own choice.
//   - store.write: warn for a store attached --read-only (6.1; the prober
//     may report skip for it), error for every other store.
//
// Every other check has its catalog level.
func LevelFor(id CheckID, c LevelCtx) Level {
	switch id {
	case CheckStorePrivate:
		if c.Store == StoreRoleOrg {
			return LevelError
		}
		return LevelWarn
	case CheckStoreWrite:
		if c.Store == StoreRoleAttachedReadOnly {
			return LevelWarn
		}
		return LevelError
	}
	if ck, ok := CheckByID(id); ok {
		return ck.Level
	}
	return LevelError
}

// CheckFor is check id as it applies in step, context c and fix variant v:
// LevelFor's level and FixFor's fix. Producers of a CheckResult use this.
func CheckFor(id CheckID, step StepID, c LevelCtx, v FixVariant) (Check, bool) {
	ck, ok := CheckByID(id)
	if !ok {
		return Check{}, false
	}
	ck.Level = LevelFor(id, c)
	ck.Fix = FixFor(id, step, v)
	return ck, true
}

// checkKinds limits a check to some kinds; absent = every kind.
var checkKinds = map[CheckID][]Kind{
	CheckSetupOrgEntry:       orgKinds(),
	CheckIdentityInviteEmail: {KindMember},
	CheckIdentityRoster:      orgKinds(),
	CheckIdentityPeriod:      orgKinds(),
	// A member or admin setup's identity is orgs[<org>].identity, and the
	// top-level one still serves repos of no org: only solo compares.
	CheckIdentityTopLevel:     {KindSolo},
	CheckIdentityTopLevelKeep: {KindSolo},
	CheckInviteParse:          {KindMember},
	CheckInviteFound:          {KindMember},
	CheckInviteStatus:         {KindMember},
	CheckInviteEmail:          {KindMember},
	CheckInvitePending:        {KindAdmin},
	CheckInviteGitHubAccess:   {KindAdmin},
	CheckOrgFree:              {KindAdmin},
	CheckOrgNameFree:          {KindAdmin},

	CheckOrgSource: orgKinds(), // SOURCE_OF_TRUTH row 9
}

// CheckKinds is the kinds check id applies to.
func CheckKinds(id CheckID) []Kind {
	if k, ok := checkKinds[id]; ok {
		return slices.Clone(k)
	}
	return allKinds()
}

// doctorHome is the home step of each check no step runs during setup
// (only `lore doctor --all` / repair run it, 6.1); repair (6.5) re-opens
// that step. Per kind where it differs.
var doctorHome = map[CheckID]map[Kind]StepID{
	CheckSetupConfig:          {KindSolo: StepKind, KindMember: StepKind, KindAdmin: StepKind},
	CheckSetupOrgEntry:        {KindMember: StepJoin, KindAdmin: StepTeam},
	CheckIdentityPeriod:       {KindMember: StepIdentity, KindAdmin: StepIdentity},
	CheckIdentityTopLevel:     {KindSolo: StepIdentity},
	CheckIdentityTopLevelKeep: {KindSolo: StepIdentity},
	CheckIdentityActorCopies:  {KindSolo: StepIdentity, KindMember: StepIdentity, KindAdmin: StepIdentity}, // SOURCE_OF_TRUTH rows 2-3
	CheckLiveMode:             {KindSolo: StepThisFolder, KindMember: StepThisFolder, KindAdmin: StepThisFolder},
	CheckInvitePending:        {KindAdmin: StepTeam},
	CheckInviteGitHubAccess:   {KindAdmin: StepTeam},

	// SOURCE_OF_TRUTH rows 7 and 9.
	CheckStoreAddress: {KindSolo: StepPersonalStore, KindMember: StepJoin, KindAdmin: StepTeam},
	CheckOrgSource:    {KindMember: StepJoin, KindAdmin: StepTeam},
}

// DoctorOnly reports whether no step runs check id during setup.
func DoctorOnly(id CheckID) bool { _, ok := doctorHome[id]; return ok }

// createOnly are the checks that something does not exist yet (ONB 4.1
// setup.name_free, 4.6 org.free and org.name_free): they guard the save
// of their step, which then makes that thing exist. Once the step is
// saved they fail on the setup's own name, org entry or store, so repair
// (6.5) and doctor never run them for a saved step.
var createOnly = map[CheckID]bool{CheckSetupNameFree: true, CheckOrgNameFree: true, CheckOrgFree: true}

// CreateOnly reports whether check id only guards the creation its step
// makes (see createOnly).
func CreateOnly(id CheckID) bool { return createOnly[id] }

// Checks returns every check in CheckIDs order with its catalog level and
// default fix.
func Checks() []Check {
	out := make([]Check, 0, len(CheckIDs()))
	for _, id := range CheckIDs() {
		if c, ok := CheckByID(id); ok {
			out = append(out, c)
		}
	}
	return out
}

// CheckByID returns check id with its catalog (default) level and default
// fix. Use CheckFor when the context is known.
func CheckByID(id CheckID) (Check, bool) {
	m, ok := checkMeta[id]
	if !ok {
		return Check{}, false
	}
	return Check{ID: id, Title: m.title, Level: m.level, Probe: m.probe, Fix: FixFor(id, "", FixDefault)}, true
}

// checkInfo is a check's catalog row minus the fix (fixes live in fix.go
// because they vary by step and variant).
type checkInfo struct {
	title string
	level Level
	probe ProbeID
}

// checkMeta is the check table of ONB 6.1 and 4.x: title, catalog level,
// probe family.
var checkMeta = map[CheckID]checkInfo{
	CheckSetupConfig:   {"Setup entry is valid", LevelError, ProbePure},
	CheckSetupOrgEntry: {"Org entry exists", LevelError, ProbePure},
	CheckSetupNameFree: {"Setup name is free", LevelError, ProbePure},

	CheckIdentityEmailFormat:  {"Email is a valid address", LevelError, ProbePure},
	CheckIdentityInviteEmail:  {"Email matches the invite", LevelError, ProbePure},
	CheckIdentityGitEmail:     {"git commits carry this email", LevelWarn, ProbeGitConfig},
	CheckIdentityRoster:       {"Email is on the team roster", LevelWarn, ProbeRoster},
	CheckIdentityPeriod:       {"You are an active team member", LevelWarn, ProbeRoster},
	CheckIdentityTopLevel:     {"No old top-level identity beside the setup's", LevelWarn, ProbePure},
	CheckIdentityTopLevelKeep: {"The other choice: keep the top-level identity", LevelWarn, ProbePure},
	CheckIdentityActorCopies:  {"Copies of actors match their source", LevelWarn, ProbePure},

	CheckGitHubSSHKey:      {"The recorded key file exists", LevelError, ProbeSSHKey},
	CheckGitHubSSHAuth:     {"GitHub accepts the key", LevelError, ProbeSSHAuth},
	CheckGitHubSSHUser:     {"The key logs in as the right account", LevelError, ProbeSSHAuth},
	CheckGitHubAliasConfig: {"ssh alias offers only this key", LevelWarn, ProbeSSHConfig},
	CheckGitHubGHAccount:   {"gh has a login for the account", LevelWarn, ProbeGHAuth},
	// Informational: lore runs gh as the setup's own login, so gh's active
	// account matters only for gh used by hand (never a warning).
	CheckGitHubGHMatches:  {"gh's active account (informational)", LevelInfo, ProbeGHAuth},
	CheckGitHubKeyMatches: {"git's route to GitHub logs in as the account", LevelError, ProbeSSHAuth},
	// gh.auth: the account's own gh token reads its emails (GitHub API).
	CheckGitHubIdentityEmail: {"The identity email is one of the account's GitHub emails", LevelWarn, ProbeGHAuth},

	CheckStoreURL:     {"Store url is valid", LevelError, ProbePure},
	CheckStoreRead:    {"You can read the store", LevelError, ProbeGitRead},
	CheckStoreWrite:   {"You can write the store", LevelError, ProbeGitWrite},
	CheckStoreMarker:  {"The repo is a lore store", LevelWarn, ProbeStoreMarker},
	CheckStorePrivate: {"The repo is private", LevelWarn, ProbeGHRepo},

	CheckInviteParse:        {"Invite text is valid", LevelError, ProbePure},
	CheckInviteFound:        {"Invite is in the team's store", LevelError, ProbeRoster},
	CheckInviteStatus:       {"Invite is still open", LevelError, ProbeRoster},
	CheckInviteEmail:        {"Your email is the invite's", LevelError, ProbePure},
	CheckInvitePending:      {"No stale pending invites", LevelWarn, ProbeRoster},
	CheckInviteGitHubAccess: {"Invited people can reach the store", LevelWarn, ProbeGHRepo},

	CheckOrgFree:     {"The store is new, empty or already a lore store", LevelError, ProbeStoreMarker},
	CheckOrgNameFree: {"The team name is free", LevelError, ProbePure},

	CheckStoreAddress: {"Each store has one address on this machine", LevelWarn, ProbePure},
	CheckOrgSource:    {"The org entry matches the store's lore-org.yaml", LevelWarn, ProbePure},

	CheckLiveMode: {"Live settings work in this repo", LevelWarn, ProbeGitWrite},

	CheckToolsSkillInstalled: {"lore skill is installed", LevelWarn, ProbeTools},
	CheckToolsSkillVersion:   {"lore skill matches the binary", LevelWarn, ProbeTools},
	CheckToolsSkillModified:  {"lore skill is unedited", LevelWarn, ProbeTools},
	CheckToolsPointerFile:    {"Tool reads .lore/LORE.md", LevelWarn, ProbeTools},
	CheckToolsHooks:          {"Session hooks are installed", LevelWarn, ProbeTools},

	CheckSecretsBackendPresent: {"Secret manager is on PATH", LevelWarn, ProbeSecrets},

	CheckFoldersExist:     {"Folders exist", LevelWarn, ProbeFS},
	CheckFoldersOverlap:   {"No other setup ties on a folder", LevelError, ProbePure},
	CheckFoldersShadow:    {"No other setup takes part of a folder", LevelWarn, ProbePure},
	CheckFoldersReachable: {"lore can pick this setup by itself", LevelWarn, ProbePure},

	CheckRepoInit:   {"lore is set up in this repo", LevelWarn, ProbeRepo},
	CheckRepoOrg:    {"The repo's org matches the setup", LevelError, ProbeRepo},
	CheckRepoStores: {"Attached stores are readable", LevelError, ProbeRepo},
}
