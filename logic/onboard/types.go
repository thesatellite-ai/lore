// Package onboard is the pure core of guided setup (layer L1,
// ONBOARDING_DESIGN 2.1): the vocabulary every front end shares (kinds,
// steps, questions, checks, fixes, check results), the `setups:` entry of
// ~/.lore/config.yaml (3.2) and its validation. No I/O: probes live in
// adapters/probe, writes in wire, the ops in ops/setup.
//
//	front end ─▶ setup.plan (Steps) ─▶ setup.check (answers ─▶ Rules ─▶ Checks ─▶ CheckResult)
//	          ─▶ setup.apply (ApplyID) ─▶ step status done
//
// The step catalog (the Go values) is catalog.go; this file holds only the
// types and the closed sets the catalog, the probes, the page and
// `lore doctor --all` key on. Ids are stable: scripts and the page key on
// them (6.1), so never rename one.
package onboard

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Kind is a setup kind (ONB 1.3).
type Kind string

// The three kinds.
const (
	KindSolo   Kind = "solo"
	KindMember Kind = "member"
	KindAdmin  Kind = "admin"
)

// Kinds lists the kinds in display order.
func Kinds() []Kind { return []Kind{KindSolo, KindMember, KindAdmin} }

// Valid reports whether k is one of Kinds.
func (k Kind) Valid() bool { return k == KindSolo || k == KindMember || k == KindAdmin }

// HasOrg reports whether the kind names an orgs[] entry (member, admin;
// ONB 3.2: org required, identity read from orgs[].identity, Q2).
func (k Kind) HasOrg() bool { return k == KindMember || k == KindAdmin }

// StepID names one step of the catalog (ONB 2.2, section 4).
type StepID string

// Every step, in catalog order (ONB 4.1-4.11).
const (
	StepKind          StepID = "kind"
	StepIdentity      StepID = "identity"
	StepGitHub        StepID = "github"
	StepPersonalStore StepID = "personal_store"
	StepJoin          StepID = "join"
	StepTeam          StepID = "team"
	StepTools         StepID = "tools"
	StepSecrets       StepID = "secrets"
	StepFolders       StepID = "folders"
	StepThisFolder    StepID = "this_folder"
	StepSummary       StepID = "summary"
)

// StepIDs lists every step in catalog order.
func StepIDs() []StepID {
	return []StepID{StepKind, StepIdentity, StepGitHub, StepPersonalStore, StepJoin, StepTeam,
		StepTools, StepSecrets, StepFolders, StepThisFolder, StepSummary}
}

// CheckID names one check (ONB 6.1 and section 4). Stable: the page,
// `lore doctor --all --json` and scripts key on it.
type CheckID string

// Every check id (ONB 6.1 plus the step-only checks of 4.x).
const (
	CheckSetupConfig   CheckID = "setup.config"
	CheckSetupOrgEntry CheckID = "setup.org_entry"
	CheckSetupNameFree CheckID = "setup.name_free"

	CheckIdentityEmailFormat CheckID = "identity.email_format"
	CheckIdentityInviteEmail CheckID = "identity.invite_email"
	CheckIdentityGitEmail    CheckID = "identity.git_email"
	CheckIdentityRoster      CheckID = "identity.roster"
	CheckIdentityPeriod      CheckID = "identity.period"
	// SOURCE_OF_TRUTH row 1: the old top-level identity: is retired into
	// the setup; with two values the person picks (one Run it per value).
	CheckIdentityTopLevel     CheckID = "identity.top_level"
	CheckIdentityTopLevelKeep CheckID = "identity.top_level_keep"
	// SOURCE_OF_TRUTH rows 2-3: every copy of an actor matches its source
	// (the roster row in a team, the identity in solo); Run it refreshes them.
	CheckIdentityActorCopies CheckID = "identity.actor_copies"

	CheckGitHubSSHKey      CheckID = "github.ssh_key"
	CheckGitHubSSHAuth     CheckID = "github.ssh_auth"
	CheckGitHubSSHUser     CheckID = "github.ssh_user"
	CheckGitHubAliasConfig CheckID = "github.alias_config"
	CheckGitHubGHAccount   CheckID = "github.gh_account"
	CheckGitHubGHMatches   CheckID = "github.gh_matches"
	// CheckGitHubKeyMatches: the recorded key is the alias's own key and
	// logs in as the setup's account (a key/alias/account mix-up saves
	// otherwise: a key of one account recorded with the alias of another).
	CheckGitHubKeyMatches CheckID = "github.key_matches"
	// CheckGitHubIdentityEmail: the identity email is one of the account's
	// GitHub emails (warn, never refuses; hints.go).
	CheckGitHubIdentityEmail CheckID = "github.identity_email"

	CheckStoreURL     CheckID = "store.url"
	CheckStoreRead    CheckID = "store.read"
	CheckStoreWrite   CheckID = "store.write"
	CheckStoreMarker  CheckID = "store.marker"
	CheckStorePrivate CheckID = "store.private"

	CheckInviteParse        CheckID = "invite.parse"
	CheckInviteFound        CheckID = "invite.found"
	CheckInviteStatus       CheckID = "invite.status"
	CheckInviteEmail        CheckID = "invite.email"
	CheckInvitePending      CheckID = "invite.pending"
	CheckInviteGitHubAccess CheckID = "invite.github_access"

	CheckOrgFree     CheckID = "org.free"
	CheckOrgNameFree CheckID = "org.name_free"

	CheckLiveMode CheckID = "live.mode"

	CheckToolsSkillInstalled CheckID = "tools.skill_installed"
	CheckToolsSkillVersion   CheckID = "tools.skill_version"
	CheckToolsSkillModified  CheckID = "tools.skill_modified"
	CheckToolsPointerFile    CheckID = "tools.pointer_file"
	CheckToolsHooks          CheckID = "tools.hooks"

	CheckSecretsBackendPresent CheckID = "secrets.backend_present"

	CheckFoldersExist   CheckID = "folders.exist"
	CheckFoldersOverlap CheckID = "folders.overlap"
	CheckFoldersShadow  CheckID = "folders.shadow"
	// CheckFoldersReachable: lore can pick the setup by itself (a folder,
	// the default or its org; hints.go).
	CheckFoldersReachable CheckID = "folders.reachable"

	CheckRepoInit   CheckID = "repo.init"
	CheckRepoOrg    CheckID = "repo.org"
	CheckRepoStores CheckID = "repo.stores"

	// SOURCE_OF_TRUTH row 7: one store id at two addresses on this
	// machine (writes go to one; Run it moves every place to it).
	CheckStoreAddress CheckID = "store.address"
	// SOURCE_OF_TRUTH row 9: the org entry is older than (or differs
	// from) the store's lore-org.yaml it copies.
	CheckOrgSource CheckID = "org.source"
)

// CheckIDs lists every check id, grouped as above.
func CheckIDs() []CheckID {
	return []CheckID{
		CheckSetupConfig, CheckSetupOrgEntry, CheckSetupNameFree,
		CheckIdentityEmailFormat, CheckIdentityInviteEmail, CheckIdentityGitEmail, CheckIdentityRoster, CheckIdentityPeriod,
		CheckIdentityTopLevel, CheckIdentityTopLevelKeep, CheckIdentityActorCopies,
		CheckGitHubSSHKey, CheckGitHubSSHAuth, CheckGitHubSSHUser, CheckGitHubAliasConfig, CheckGitHubGHAccount, CheckGitHubGHMatches, CheckGitHubKeyMatches, CheckGitHubIdentityEmail,
		CheckStoreURL, CheckStoreRead, CheckStoreWrite, CheckStoreMarker, CheckStorePrivate,
		CheckInviteParse, CheckInviteFound, CheckInviteStatus, CheckInviteEmail, CheckInvitePending, CheckInviteGitHubAccess,
		CheckOrgFree, CheckOrgNameFree,
		CheckLiveMode,
		CheckToolsSkillInstalled, CheckToolsSkillVersion, CheckToolsSkillModified, CheckToolsPointerFile, CheckToolsHooks,
		CheckSecretsBackendPresent,
		CheckFoldersExist, CheckFoldersOverlap, CheckFoldersShadow, CheckFoldersReachable,
		CheckRepoInit, CheckRepoOrg, CheckRepoStores,
		CheckStoreAddress, CheckOrgSource,
	}
}

// QType is a question's answer shape; the front end picks its widget from
// it (ONB 2.2, 2.4).
type QType string

// Question types.
const (
	QText      QType = "text"
	QEmail     QType = "email"
	QEmailList QType = "email_list"
	QChoice    QType = "choice"
	QMulti     QType = "multi"
	QBool      QType = "bool"
	QPath      QType = "path"
	QGlobList  QType = "glob_list"
	QInvite    QType = "invite"
)

// QTypes lists every question type (a front end must render each one).
func QTypes() []QType {
	return []QType{QText, QEmail, QEmailList, QChoice, QMulti, QBool, QPath, QGlobList, QInvite}
}

// IsList reports whether answers of this type are a list of strings
// (Answer.List); QBool is Answer.Bool; every other type is Answer.Text.
func (q QType) IsList() bool { return q == QEmailList || q == QMulti || q == QGlobList }

// PrefillID names a read-only detection that fills a question's default or
// its choices (ONB 2.2, 4.2-4.9). The Prober answers it.
type PrefillID string

// Prefills.
const (
	PrefillNone           PrefillID = ""
	PrefillGitUserName    PrefillID = "git.user_name"     // git config --global user.name
	PrefillGitUserEmail   PrefillID = "git.user_email"    // git config --global user.email
	PrefillGHAccounts     PrefillID = "gh.accounts"       // gh auth status: logins, which is active
	PrefillSSHGitHubKeys  PrefillID = "ssh.github_keys"   // ~/.ssh/config aliases → key → ssh -T user (4.3 table)
	PrefillGitHubUsers    PrefillID = "github.users"      // GitHub users ssh -T greets per alias + gh logins (4.3 github_account)
	PrefillSSHPubKeys     PrefillID = "ssh.pub_keys"      // ~/.ssh/*.pub paths
	PrefillToolsDetected  PrefillID = "tools.detected"    // tool binaries / home folders found (4.7)
	PrefillSecretsFound   PrefillID = "secrets.detected"  // secret managers on PATH (4.8)
	PrefillFoldersHere    PrefillID = "folders.here"      // the cwd's parent as a glob (4.9)
	PrefillInviteEmail    PrefillID = "invite.email"      // the parsed invite's email (4.2, member)
	PrefillStoreURLCreate PrefillID = "store.url_suggest" // git@{alias}:{user}/lore-me.git (4.4)
)

// ProbeID names the probe family a check runs (ONB 2.1 adapters/probe).
// ProbePure checks are decided from the answers and config alone (no I/O
// beyond what the Store already read).
type ProbeID string

// Probe families.
const (
	ProbePure        ProbeID = "pure"         // answers + config only (email format, name free, overlap)
	ProbeGitConfig   ProbeID = "git.config"   // git config reads (user.email)
	ProbeSSHKey      ProbeID = "ssh.key"      // public key file + ssh-keygen -lf
	ProbeSSHAuth     ProbeID = "ssh.auth"     // ssh -T via the alias: auth + user
	ProbeSSHConfig   ProbeID = "ssh.config"   // ~/.ssh/config parse (IdentitiesOnly)
	ProbeGHAuth      ProbeID = "gh.auth"      // gh auth status (never --show-token)
	ProbeGHRepo      ProbeID = "gh.repo"      // gh api repos/…: private, collaborator
	ProbeGitRead     ProbeID = "git.read"     // git ls-remote --heads
	ProbeGitWrite    ProbeID = "git.write"    // dry-run push of a probe ref (4.4)
	ProbeStoreMarker ProbeID = "store.marker" // lore-store.yaml / lore-org.yaml in the remote
	ProbeRoster      ProbeID = "roster"       // org store rows: contacts, periods, invites
	ProbeTools       ProbeID = "tools"        // skill folder, version line, pointer file, hooks
	ProbeSecrets     ProbeID = "secrets"      // secret manager on PATH (harmless status call)
	ProbeFS          ProbeID = "fs"           // folders exist
	ProbeRepo        ProbeID = "repo"         // .lore/ present, org resolution, attached stores
)

// ApplyID names the one write a step makes when every error-level check
// passes (ONB 2.3). ApplyNone: the step writes nothing (this_folder,
// summary).
type ApplyID string

// Applies.
const (
	ApplyNone          ApplyID = ""
	ApplySetupEntry    ApplyID = "setup.entry"    // kind/name/default: create the setups[] entry
	ApplyIdentity      ApplyID = "identity"       // solo: setups[].identity; member/admin: identity.set --org
	ApplyGitHub        ApplyID = "github"         // setups[].github
	ApplyPersonalStore ApplyID = "personal_store" // store.init me, or setups[].personal_store
	ApplyJoin          ApplyID = "join"           // org.import + roster rows + invite accepted (4.5)
	ApplyTeam          ApplyID = "team"           // store.init + org.create + admin rows (4.6)
	ApplyTools         ApplyID = "tools"          // setups[].tools
	ApplySecrets       ApplyID = "secrets"        // setups[].secrets.backend
	ApplyFolders       ApplyID = "folders"        // setups[].folders + default
)

// Level is how bad a failing check is.
type Level string

// Levels.
const (
	LevelError Level = "error"
	LevelWarn  Level = "warn"
	// LevelInfo is a warn result worth reading with nothing to do now
	// (tools.skill_installed while the lore on PATH is older than this
	// binary: install after upgrading). A prober asks for it with
	// Vars[VarLevel] = "info"; it never blocks and is not counted as a
	// warning.
	LevelInfo Level = "info"
)

// Status is one check result's outcome (ONB 2.2). StatusError means the
// probe itself could not run (timeout, tool missing): degraded, never
// broken (6.4).
type Status string

// Statuses; the page shows ✓ ⚠ ✗ – ? for them.
const (
	StatusOK    Status = "ok"
	StatusWarn  Status = "warn"
	StatusFail  Status = "fail"
	StatusSkip  Status = "skip"
	StatusError Status = "error"
)

// Statuses lists every status.
func Statuses() []Status { return []Status{StatusOK, StatusWarn, StatusFail, StatusSkip, StatusError} }

// StepStatus is a step's state in setup_progress (ONB 2.3, 3.4).
type StepStatus string

// Step statuses.
const (
	StepTodo    StepStatus = "todo"
	StepReady   StepStatus = "ready"
	StepFailed  StepStatus = "failed"
	StepDone    StepStatus = "done"
	StepSkipped StepStatus = "skipped"
)

// StepStatuses lists every step status (the setup_progress.status values).
func StepStatuses() []string {
	return []string{string(StepTodo), string(StepReady), string(StepFailed), string(StepDone), string(StepSkipped)}
}

// Step is one step of the catalog (ONB 2.2).
type Step struct {
	ID        StepID     `json:"id"`
	Title     string     `json:"title"`
	Help      string     `json:"help,omitempty"`
	Kinds     []Kind     `json:"kinds"`
	Needs     []StepID   `json:"needs,omitempty"`
	Questions []Question `json:"questions,omitempty"`
	Checks    []CheckID  `json:"checks,omitempty"`
	Apply     ApplyID    `json:"apply,omitempty"`
	Optional  bool       `json:"optional,omitempty"`
}

// Question is one field of a step (ONB 2.2).
type Question struct {
	ID       string       `json:"id"`
	Label    string       `json:"label"`
	Help     string       `json:"help,omitempty"`
	Type     QType        `json:"type"`
	Required bool         `json:"required,omitempty"`
	Prefill  PrefillID    `json:"prefill,omitempty"`
	Choices  ChoiceSource `json:"choices,omitzero"`
	Rules    []Rule       `json:"rules,omitempty"`
	// Kinds limits the question to some kinds (empty = every kind of the
	// step), e.g. `default` is solo only (4.1).
	Kinds []Kind `json:"kinds,omitempty"`
}

// ChoiceSource is where a choice/multi question's options come from: a
// static list, or a prefill that returns them (or both: static first).
type ChoiceSource struct {
	Static  []Choice  `json:"static,omitempty"`
	Prefill PrefillID `json:"prefill,omitempty"`
}

// Choice is one option.
type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
	// Detail is a short second line (a key fingerprint, "active").
	Detail string `json:"detail,omitempty"`
}

// RuleKind is a validation rule's name (ONB 2.2 Question.Rules).
type RuleKind string

// Rule kinds. RuleMaxLen and RuleNotIn carry an argument.
const (
	RuleEmail     RuleKind = "email"      // net/mail, the rule identity.set uses
	RuleSnakeCase RuleKind = "snake_case" // ^[a-z][a-z0-9_]*$
	RuleSetupName RuleKind = "setup_name" // snake_case or kebab (3.2 name)
	RuleOrgName   RuleKind = "org_name"   // a team name: an orgs[] entry (orgconf) and a store name (row.ValidName)
	RuleGitURL    RuleKind = "git_url"    // git@host:o/r, ssh://, https://, file://; no userinfo token
	RuleMaxLen    RuleKind = "max_len"    // max_len:64
	RuleNotIn     RuleKind = "not_in"     // not_in:repo,me,team,company
	RuleKeyPath   RuleKind = "key_path"   // an ssh key file path (private or .pub), absolute or ~/
	RuleAbsGlob   RuleKind = "abs_glob"   // absolute after ~ expansion
)

// Rule is one validation rule in its text form, "kind" or "kind:arg"
// ("email", "max_len:64", "not_in:repo,me"); a string so it is the same in
// Go, JSON and the op schema.
type Rule string

// R is the argument-free rule of kind k.
func R(k RuleKind) Rule { return Rule(k) }

// Kind is the rule's kind.
func (r Rule) Kind() RuleKind {
	k, _, _ := strings.Cut(string(r), ":")
	return RuleKind(k)
}

// Arg is the rule's argument, "" when none.
func (r Rule) Arg() string {
	_, a, _ := strings.Cut(string(r), ":")
	return a
}

// MaxLen is the RuleMaxLen rule for n.
func MaxLen(n int) Rule { return Rule(string(RuleMaxLen) + ":" + strconv.Itoa(n)) }

// NotIn is the RuleNotIn rule for the given words.
func NotIn(words ...string) Rule { return Rule(string(RuleNotIn) + ":" + strings.Join(words, ",")) }

// Check is one check of the catalog (ONB 2.2).
type Check struct {
	ID    CheckID `json:"id"`
	Title string  `json:"title"`
	Level Level   `json:"level"`
	Probe ProbeID `json:"probe"`
	// Fix is the template; {name} placeholders are filled from the answers
	// and the probe's Vars.
	Fix Fix `json:"fix"`
}

// Fix is what to do about a failing check (ONB 2.2). Command is an argv
// template; a Runnable fix runs only after an explicit confirm, and an
// Outward one (touches a remote service) always asks.
//
// Text is one short line. Why, when set, is the longer explanation the
// page keeps behind Details. Choices, when set, are the commands the
// person picks between (each runs on its own Run it, after a confirm, via
// setup.fix --choice <id>); such a fix has no Command of its own.
type Fix struct {
	Text     string      `json:"text"`
	Command  []string    `json:"command"`
	Runnable bool        `json:"runnable"`
	Outward  bool        `json:"outward"`
	Why      string      `json:"why,omitempty"`
	Choices  []FixChoice `json:"choices,omitempty"`
}

// FixChoice is one command of a fix the person picks between (a linked
// skill folder: replace the link, or write into its target). Command is an
// argv template filled and re-validated like Fix.Command.
type FixChoice struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Command []string `json:"command"`
}

// Choice is the choice with id, if the fix has it.
func (f Fix) Choice(id string) (FixChoice, bool) {
	for _, c := range f.Choices {
		if c.ID == id {
			return c, true
		}
	}
	return FixChoice{}, false
}

// CheckResult is one check's outcome, the same JSON everywhere (page,
// doctor, --json; ONB 2.2):
//
//	{"check":"store.write","status":"fail","level":"error","target":"store acme-hub",
//	 "detail":"…","fix":{"text":"…","command":[…],"runnable":true,"outward":false},
//	 "checked_at":"2026-11-06T10:12:03Z","took_ms":812}
//
// Fix is filled (templates resolved) only when Status is warn/fail; a
// producer sets Command to an empty slice, never nil, so it prints [].
type CheckResult struct {
	Check     CheckID   `json:"check"`
	Status    Status    `json:"status"`
	Level     Level     `json:"level"`
	Target    string    `json:"target,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Fix       Fix       `json:"fix,omitzero"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	TookMS    int64     `json:"took_ms,omitempty"`
}

// Answer is one question's answer: Text for single-value types, List for
// QType.IsList types, Bool for QBool. Its JSON form is the natural one
// (a string, a list of strings, or a bool).
type Answer struct {
	Text string
	List []string
	Bool bool
	// IsBool / IsList record which form was given (a JSON false or [] is
	// still an answer).
	IsBool bool
	IsList bool
}

// MarshalJSON writes the natural form.
func (a Answer) MarshalJSON() ([]byte, error) {
	switch {
	case a.IsBool:
		return json.Marshal(a.Bool)
	case a.IsList:
		if a.List == nil {
			return []byte("[]"), nil
		}
		return json.Marshal(a.List)
	}
	return json.Marshal(a.Text)
}

// UnmarshalJSON reads a string, a list of strings or a bool.
func (a *Answer) UnmarshalJSON(b []byte) error {
	*a = Answer{}
	switch t := strings.TrimSpace(string(b)); {
	case t == "true" || t == "false":
		a.IsBool, a.Bool = true, t == "true"
		return nil
	case strings.HasPrefix(t, "["):
		a.IsList = true
		return json.Unmarshal(b, &a.List)
	}
	return json.Unmarshal(b, &a.Text)
}

// Answers are one step's answers by Question.ID. Its text form (a CLI
// flag such as `--answers '{"email":"mira@agency.example"}'`) is the same
// JSON object, so op inputs and outputs can carry it.
type Answers map[string]Answer

// MarshalText implements encoding.TextMarshaler (the JSON object).
func (a Answers) MarshalText() ([]byte, error) {
	if a == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]Answer(a))
}

// UnmarshalText implements encoding.TextUnmarshaler (the JSON object).
func (a *Answers) UnmarshalText(b []byte) error {
	m := map[string]Answer{}
	if len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
	}
	*a = m
	return nil
}

// MarshalJSON keeps the JSON form an object (not a quoted string).
func (a Answers) MarshalJSON() ([]byte, error) { return a.MarshalText() }

// UnmarshalJSON reads the object.
func (a *Answers) UnmarshalJSON(b []byte) error { return a.UnmarshalText(b) }

// FieldError is one answer that failed validation (ONB 2.3: nothing is
// probed while any exists).
type FieldError struct {
	Question string `json:"question"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

// Progress is one setup_progress row (ONB 3.4): a step of an unfinished
// setup. UpdatedAt is the row's envelope updated_at.
type Progress struct {
	Setup     string        `json:"setup"`
	Step      StepID        `json:"step"`
	Answers   Answers       `json:"answers"`
	Checks    []CheckResult `json:"checks"`
	Status    StepStatus    `json:"status"`
	UpdatedAt time.Time     `json:"updated_at,omitzero"`
}

// KnownRepo is one known_repos row (Q11): a repo lore has run in on this
// machine, scanned by `lore doctor --all`.
type KnownRepo struct {
	RepoID   string    `json:"repo_id"`
	Root     string    `json:"root"`
	LastSeen time.Time `json:"last_seen,omitzero"`
}

// enumStrings is vals as plain strings, for the EnumValues methods below.
func enumStrings[T ~string](vals []T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}

// EnumValues lists every kind (op.Enumerated): the OpenAPI document's enum
// for a field of this type. internal/layers pins it to the constants.
func (Kind) EnumValues() []string { return enumStrings(Kinds()) }

// EnumValues lists every check status (op.Enumerated).
func (Status) EnumValues() []string { return enumStrings(Statuses()) }

// EnumValues lists every step status (op.Enumerated).
func (StepStatus) EnumValues() []string { return StepStatuses() }

// EnumValues lists every question type (op.Enumerated).
func (QType) EnumValues() []string { return enumStrings(QTypes()) }

// Levels lists every level, worst first.
func Levels() []Level { return []Level{LevelError, LevelWarn, LevelInfo} }

// EnumValues lists every level (op.Enumerated).
func (Level) EnumValues() []string { return enumStrings(Levels()) }

// Note reports whether r is an info-level warning or fail: shown to read,
// never counted as something to fix (the page's isInfo, the doctor's
// counts and Home's Health cell all use it, so they agree).
func (r CheckResult) Note() bool {
	return r.Level == LevelInfo && (r.Status == StatusWarn || r.Status == StatusFail)
}
