package setup

// ports.go — the contracts of guided setup (ONBOARDING_DESIGN 2.1, 7.1):
// the op names of every new op, the ports the ops call (Prober, Store,
// Applier, FixRunner; wire implements them with adapters/probe,
// config.yaml and machine.db), and each op's input and output. The op
// bodies live in their own files (plan.go, check.go, …); this file holds
// no behaviour.
//
//	page / CLI / agent ─▶ setup.plan ─▶ Store (setups, progress) + catalog
//	                   ─▶ setup.check ─▶ onboard rules ─▶ Prober.Probe (parallel, timeouts)
//	                   ─▶ setup.apply ─▶ Applier.Apply (the one write) ─▶ Store.PutProgress
//	                   ─▶ setup.fix   ─▶ FixRunner.RunFix (re-validated argv, after a confirm)
//
// Bare `lore setup` (Q1): setup.run with neither --schema-only nor
// --agent-hooks runs the quiet schema update and returns RunOut.Web;
// cmd/lore sees Web set and starts the page (surface/setupweb). The op
// never opens a browser itself (it is L5 and Local; over REST or --json
// Web is only reported).

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
)

// Operation names of guided setup (ONB 7.1). setup.run (OpRun) exists.
const (
	OpPlan   = "setup.plan"
	OpCheck  = "setup.check"
	OpApply  = "setup.apply"
	OpFix    = "setup.fix"
	OpList   = "setup.list"
	OpShow   = "setup.show"
	OpUse    = "setup.use"
	OpEdit   = "setup.edit"
	OpRemove = "setup.remove"
	OpJoin   = "setup.join"
	OpRepair = "setup.repair"

	// Team invites (ops/team; ONB 4.6). Defined here so the page
	// allowlist and the docs name every onboarding op from one place.
	OpTeamInvite       = "team.invite"
	OpTeamInviteList   = "team.invite.list"
	OpTeamInviteRevoke = "team.invite.revoke"
	OpTeamInviteResend = "team.invite.resend"

	// Skill install and status (ops/skill; ONB 4.7.1).
	OpSkillInstall = "skill.install"
	OpSkillStatus  = "skill.status"

	// OpRepoInit is `lore init` as a registry op (ops/repoinit; ONB 10.2),
	// so the page's "this folder" step can run it.
	OpRepoInit = "repo.init"
)

// PageOps lists the ops the setup page may call (ONB 5.3 allowlist, 7.1
// "Page allowlist" column), besides doctor.run, whoami.show and
// identity.show which exist. setup.run is not on it.
func PageOps() []string {
	return []string{OpPlan, OpCheck, OpApply, OpFix, OpList, OpShow, OpUse, OpEdit, OpRemove, OpJoin, OpRepair,
		OpTeamInvite, OpTeamInviteList, OpTeamInviteRevoke, OpTeamInviteResend,
		OpSkillInstall, OpSkillStatus, OpRepoInit, OpAliases, OpDiscover, OpSuggestAgain,
		OpImportScan, OpImport, // setup.import_scan / setup.import (import.go)
		OpDisable, OpEnable, OpTrashList, OpTrashRestore, OpTrashPurge, OpImportDismiss, // lifecycle.go
		OpFolders, OpBrowse, // folders.go
		OpLeftoversClear} // leftovers.go
}

// Environment and header that pick a setup (ONB 3.3, 7.1).
const (
	EnvSetup    = "LORE_SETUP"
	HeaderSetup = "X-Lore-Setup"
)

// ---------------------------------------------------------------------
// Ports

// ProbeRequest is one check to run. Answers are the step's validated
// answers; Target names what is checked when a check runs per item ("store
// acme-hub", a tool name, a repo root), "" for the setup as a whole.
type ProbeRequest struct {
	Check   onboard.Check
	Setup   onboard.SetupEntry
	Answers onboard.Answers
	Target  string
}

// ProbeResult is one probe's raw outcome. Vars fill the check's fix
// template ({got}, {fingerprint}, {active}, …) and never hold a secret.
// The op turns it into an onboard.CheckResult (level, filled fix, time).
type ProbeResult struct {
	Check  onboard.CheckID
	Target string
	Status onboard.Status
	Detail string
	Vars   map[string]string
	TookMS int64
}

// Prober runs checks and prefills (adapters/probe behind wire).
//
// Invariants: never writes anything (the write test is a dry-run push,
// ONB 4.4); every probe has a timeout (doctor.probe_timeout_s for ssh and
// git, 10 s for gh) and at most 4 run at once machine-wide (6.1); a probe
// that cannot run is onboard.StatusError, never a Go error, so one broken
// tool never hides the other results. The returned slice has one result
// per request, in request order.
type Prober interface {
	Probe(ctx context.Context, reqs []ProbeRequest) []ProbeResult
	// Prefill answers one detection (ONB 2.2 PrefillID) for setup (zero
	// entry while creating): the default value and/or the choices.
	Prefill(ctx context.Context, id onboard.PrefillID, setup onboard.SetupEntry) (PrefillOut, error)
}

// PrefillOut is a prefill's answer.
type PrefillOut struct {
	Value   onboard.Answer   `json:"value"`
	Choices []onboard.Choice `json:"choices,omitempty"`
}

// ActiveSetup is which setup is in effect here and why (ONB 3.3): Why is
// one of the Why* values; Name is "" with WhyNone.
type ActiveSetup struct {
	Name string `json:"name"`
	Why  string `json:"why"`
	// Detail is the matching glob, env value or repo id, for --explain.
	Detail string `json:"detail,omitempty"`
}

// The 3.3 resolution steps, first answer wins.
const (
	WhyFlag    = "flag"    // --setup <name>
	WhyEnv     = "env"     // LORE_SETUP
	WhyRepoPin = "repo"    // repo_setups (lore setup use --here)
	WhyFolder  = "folder"  // folder glob match, most specific wins
	WhyOrg     = "org"     // the repo's org → the one setup of that org
	WhyDefault = "default" // default: true
	WhyNone    = "none"    // no setup: lore behaves as before
)

// SetupPatcher is the optional Store upgrade setup.edit writes through:
// only the keys after changes from before are written, against the file
// as it is when written, so an edit made from an earlier read never puts
// back a key another process changed meanwhile (a lost update). Without
// it the edit replaces the whole entry (PutSetup).
type SetupPatcher interface {
	PatchSetup(ctx context.Context, before, after onboard.SetupEntry) error
}

// Store is setups[] in config.yaml plus setup_progress and known_repos in
// machine.db (wire). Every write of setups[] validates the whole
// candidate list first (E_CONFIG_INVALID naming the entry, nothing
// written) and keeps unknown keys byte for byte.
type Store interface {
	// Config is setups[], repo_setups and the 7.3 settings, validated.
	Config(ctx context.Context) (onboard.Config, error)
	// PutSetup adds or replaces (by name) one entry; created reports new.
	// A default entry stops every other one being the default, in the same
	// write (at most one default against the file as it is then).
	PutSetup(ctx context.Context, e onboard.SetupEntry) (created bool, err error)
	// RemoveSetup deletes the entry, its repo_setups pins and its
	// setup_progress rows; never stores or orgs[]. found=false: none.
	RemoveSetup(ctx context.Context, name string) (found bool, err error)
	// PinRepo writes repo_setups[<this repo's id>] = name ("" clears);
	// it returns the repo id.
	PinRepo(ctx context.Context, name string) (repoID string, err error)
	// Active resolves the setup in effect for the current folder (3.3);
	// flag is the --setup value ("" = none given).
	Active(ctx context.Context, flag string) (ActiveSetup, error)

	// Progress lists setup's setup_progress rows (catalog order not
	// guaranteed); none for a finished or unknown setup.
	Progress(ctx context.Context, setup string) ([]onboard.Progress, error)
	// PutProgress writes one row (natural id (setup, step): a re-save
	// rewrites the same row).
	PutProgress(ctx context.Context, p onboard.Progress) error
	// DeleteProgress removes every row of setup (summary done, or remove).
	DeleteProgress(ctx context.Context, setup string) error

	// KnownRepos lists known_repos (Q11).
	KnownRepos(ctx context.Context) ([]onboard.KnownRepo, error)
	// TouchRepo records that lore ran in r.Root now (natural id root).
	TouchRepo(ctx context.Context, r onboard.KnownRepo) error
}

// ApplyRequest is one step's write.
type ApplyRequest struct {
	Setup   onboard.SetupEntry
	Step    onboard.StepID
	Apply   onboard.ApplyID
	Answers onboard.Answers
	// DryRun computes what would change and writes nothing.
	DryRun bool
}

// ApplyResult is what Apply wrote. Changed=false is the idempotent
// re-run ("unchanged", ONB 2.3).
type ApplyResult struct {
	Changed bool `json:"changed"`
	// Wrote names each thing written: a config path, "setups[personal]",
	// a store id, a row id.
	Wrote []string `json:"wrote"`
	// Setup is the entry after the write (the op saves progress with it).
	Setup onboard.SetupEntry `json:"setup"`
}

// Applier makes a step's one write through the existing ops where they
// exist (identity.set, store.init, org.create, org.import, …; ONB 2.3).
// Invariant: compares first and reports Changed=false without writing
// when everything is already as asked.
type Applier interface {
	Apply(ctx context.Context, req ApplyRequest) (ApplyResult, error)
}

// FixRun is one fix command's outcome.
type FixRun struct {
	Argv []string `json:"ran"`
	Exit int      `json:"exit"`
	// OutputTail is the last lines of output, secrets masked.
	OutputTail string `json:"output_tail"`
	// Recheck is the check's status when setup.fix ran it again right
	// after a fix that exited 0 ("" when it did not: the fix failed, or
	// the runner was called outside setup.fix).
	Recheck onboard.Status `json:"recheck,omitempty"`
	// StillFailing is set when the fix exited 0 but the check still fails
	// or warns: "ran OK but the problem is still there: <detail>", so no
	// caller reads a bare "finished" as fixed.
	StillFailing string `json:"still_failing,omitempty"`
}

// FixRunner runs a Runnable fix's argv after the op re-validated it
// against the check's template (E_FIX_REFUSED otherwise) and the person
// confirmed (E_NEEDS_CONFIRM; Outward always asks).
type FixRunner interface {
	RunFix(ctx context.Context, argv []string) (FixRun, error)
	// RunFixIn is RunFix in folder dir ("" = this process's folder):
	// `lore render` and `lore init` act on the repo they run in and lore
	// has no -C flag, so a repo check's fix runs in its target repo.
	RunFixIn(ctx context.Context, dir string, argv []string) (FixRun, error)
}

// TargetStorePrefix starts a store check's Target ("store me", "store
// agency").
const TargetStorePrefix = "store "

// TargetRepo is the repo root a check Target names: an absolute path, or
// the root of a "<tool> <root>" target; "" for every other target (a
// store, "machine", none). setup.fix and repair run a fix there; the wire
// prober probes there.
func TargetRepo(t string) string {
	if filepath.IsAbs(t) {
		return t
	}
	if _, root, ok := strings.Cut(t, " "); ok && filepath.IsAbs(root) && !strings.HasPrefix(t, TargetStorePrefix) {
		return root
	}
	return ""
}

// Onboard is everything the guided-setup ops need; *wire.App implements
// it. Each getter answers E_UNSUPPORTED on a binary without that part.
type Onboard interface {
	SetupStore(ctx context.Context) (Store, error)
	SetupProber(ctx context.Context) (Prober, error)
	SetupApplier(ctx context.Context) (Applier, error)
	SetupFixRunner(ctx context.Context) (FixRunner, error)
}

// ---------------------------------------------------------------------
// Inputs and outputs (ONB 7.1). json names are the REST/--json fields;
// flag/arg tags build the CLI.

// WebStart asks the binary to open the setup page (bare `lore setup`,
// Q1). Returned by setup.run and setup.join; cmd/lore acts on it.
type WebStart struct {
	// Open is true when the run asks for the page (bare `lore setup`,
	// --web; Q1). A bare run's other fields are often all zero, so Open is
	// what tells it apart, also over --json and REST.
	Open   bool   `json:"open,omitempty"`
	Setup  string `json:"setup,omitempty"`
	NoOpen bool   `json:"no_open,omitempty"`
	Port   int    `json:"port,omitempty"`
	// Invite is the invite text for the join flow (setup.join).
	Invite string `json:"invite,omitempty"`
}

// PlanIn is setup.plan's input.
type PlanIn struct {
	Kind string `json:"kind,omitempty" flag:"kind" desc:"solo, member or admin (default: the setup's kind, else solo)"`
	// Setup is the setup being made or repaired: REST body only; the CLI
	// passes the global --setup (op.SetupOf), never a per-op flag.
	Setup string `json:"setup,omitempty" desc:"the setup being made or repaired"`
}

// PlanOut is setup.plan's output. Steps carry their questions with the
// prefills resolved (PlanStep, plan.go).
type PlanOut struct {
	Steps    []PlanStep         `json:"steps"`
	Progress []onboard.Progress `json:"progress"`
	// Exists is set when the setup's setups[] entry is in config.yaml:
	// the page edits it ("Edit setup: <name>") instead of making a new one.
	Exists bool `json:"exists"`
}

// CheckIn is setup.check's input.
type CheckIn struct {
	Setup   string          `json:"setup" arg:"0" desc:"the setup name"`
	Step    string          `json:"step" flag:"step" desc:"the step id (lore setup plan lists them)"`
	Answers onboard.Answers `json:"answers,omitempty" flag:"answers" desc:"the step's answers as a JSON object"`
}

// CheckOut is setup.check's output.
type CheckOut struct {
	Step        onboard.StepID        `json:"step"`
	Status      onboard.StepStatus    `json:"status"`
	FieldErrors []onboard.FieldError  `json:"field_errors"`
	Checks      []onboard.CheckResult `json:"checks"`
}

// ApplyIn is setup.apply's input.
type ApplyIn struct {
	Setup   string          `json:"setup" arg:"0" desc:"the setup name"`
	Step    string          `json:"step" flag:"step" desc:"the step id"`
	Answers onboard.Answers `json:"answers,omitempty" flag:"answers" desc:"the step's answers as a JSON object (default: the saved ones)"`
}

// ApplyOut is setup.apply's output.
type ApplyOut struct {
	Step    onboard.StepID     `json:"step"`
	Status  onboard.StepStatus `json:"status"`
	Changed bool               `json:"changed"`
	Wrote   []string           `json:"wrote"`
	// Checks are the step's results after the save (reused fresh ones keep
	// their own time), so the page and Summary show what ran: a finished
	// setup keeps no progress rows to read them from (realmac NEW-5).
	Checks []onboard.CheckResult `json:"checks"`
	// Warnings are one-line notices of the save, such as a folder given
	// more than once and saved once; left out when there is none.
	Warnings []string `json:"warnings,omitempty"`
}

// FixIn is setup.fix's input.
type FixIn struct {
	Setup  string `json:"setup" arg:"0" desc:"the setup name"`
	Check  string `json:"check" flag:"check" desc:"the check id whose fix to run"`
	Target string `json:"target,omitempty" flag:"target" desc:"the check's target when it runs per item (a store, a tool, a repo)"`
	Choice string `json:"choice,omitempty" flag:"choice" desc:"for a fix that offers choices, the id of the one to run (a linked skill folder: replace or update)"`
}

// FixOut is setup.fix's output (FixRun's fields).
type FixOut = FixRun

// ListIn is setup.list's input.
type ListIn struct{}

// SetupSummary is one row of setup.list.
type SetupSummary struct {
	Name    string       `json:"name"`
	Kind    onboard.Kind `json:"kind"`
	Org     string       `json:"org,omitempty"`
	Default bool         `json:"default"`
	Folders []string     `json:"folders"`
	// Health is the 5.4 Home cell ("✓ 14", "⚠ 1", "✗ 1", "? 2"; list.go
	// HealthCell) from cached doctor results or the setup's unfinished
	// steps; "" when never checked.
	Health string `json:"health,omitempty"`
	// Disabled setups stay listed (greyed on Home) but match nothing.
	Disabled   bool   `json:"disabled,omitempty"`
	DisabledAt string `json:"disabled_at,omitempty"`
	// Note is the setup's own "why this setup exists" line.
	Note string `json:"note,omitempty"`
	// Unfinished are the steps whose required value the entry lacks
	// (list.go unfinishedSteps; an imported setup has no github block),
	// so Home can say "finish the GitHub step"; empty when complete.
	Unfinished []onboard.StepID `json:"unfinished,omitempty"`
}

// ListOut is setup.list's output.
type ListOut struct {
	Setups []SetupSummary `json:"setups"`
	Active ActiveSetup    `json:"active"`
	// Here is the current folder's context (list.go, onboard.ResolveHere);
	// zero (left out of the JSON) when the env has no HereSource. omitempty
	// beside omitzero marks it optional in the API document (op/fields.go
	// reads omitempty; json leaves a zero struct out through omitzero).
	Here onboard.Here `json:"here,omitempty,omitzero"`
	// Trashed counts trash.setups (Home's "Trash (N)"; lore setup trash list).
	Trashed int `json:"trashed"`
	// ConfigProblems are config.yaml's problems, each with its file, line
	// and fix (list.go ConfigProblem); empty when the file is valid. While
	// there are any, Home lists the setups read-only (Setups is empty when
	// the file does not even decode).
	ConfigProblems []ConfigProblem `json:"config_problems,omitempty"`
	// Leftovers are config.yaml's top-level identity and personal.store
	// when no setup uses them (onboard.Leftovers): Home's "Leftovers on
	// this machine" box, cleared by setup.leftovers.clear. Empty when none.
	Leftovers []onboard.Leftover `json:"leftovers"`
	// Unsaved are setups the page started whose entry is not in
	// config.yaml yet (list.go unsavedSetups): a team setup left before
	// its join or team step keeps its answers only as setup_progress rows,
	// so without this the person coming back the next day saw no trace of
	// it. Empty when none.
	Unsaved []UnsavedSetup `json:"unsaved,omitempty"`
	// ConfigStyle names config.yaml's keys written on one line (doctor's
	// config_style warning, configstyle.go) with the fix lore config fmt:
	// Home's box with Run it. Zero (left out) when the file reads in block
	// style.
	ConfigStyle ConfigStyle `json:"config_style,omitempty,omitzero"`
}

// UnsavedSetup is one setup being made that config.yaml does not hold
// yet: its name and kind (the kind step's answer), the steps saved so far
// (catalog order) and the first step still to do.
type UnsavedSetup struct {
	Name string           `json:"name"`
	Kind onboard.Kind     `json:"kind"`
	Done []onboard.StepID `json:"done"`
	Next onboard.StepID   `json:"next,omitempty"`
}

// ShowIn is setup.show's input.
type ShowIn struct {
	Name string `json:"name" arg:"0" desc:"the setup name"`
}

// ShowOut is setup.show's output: the entry, keys only, no secrets.
type ShowOut struct {
	Setup onboard.SetupEntry `json:"setup"`
}

// UseIn is setup.use's input.
type UseIn struct {
	Name  string `json:"name,omitempty" arg:"0" desc:"the setup name"`
	Here  bool   `json:"here,omitempty" flag:"here" desc:"pin this repo to the setup (repo_setups, this machine only)"`
	Clear bool   `json:"clear,omitempty" flag:"clear" desc:"with --here: remove this repo's pin"`
}

// Use scopes.
const (
	UseScopeRepo  = "repo"  // repo_setups pin written
	UseScopeShell = "shell" // print `export LORE_SETUP=<name>`
)

// UseOut is setup.use's output.
type UseOut struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
	// Line is the shell line to run for the shell scope.
	Line string `json:"line,omitempty"`
}

// EditIn is setup.edit's input: the 3.2 fields as flags; an empty flag
// leaves the field as it is.
type EditIn struct {
	Name       string   `json:"name" arg:"0" desc:"the setup name"`
	Folders    []string `json:"folders,omitempty" flag:"folder" desc:"folder globs this setup covers (replaces the list; repeat for several)"`
	Default    bool     `json:"default,omitempty" flag:"default" desc:"make this the default setup (clears it on the other one)"`
	NoDefault  bool     `json:"no_default,omitempty" flag:"no-default" desc:"stop being the default setup"`
	GitHubUser string   `json:"github_user,omitempty" flag:"github-user" desc:"the GitHub account ssh -T must name"`
	HostAlias  string   `json:"host_alias,omitempty" flag:"host-alias" desc:"the ssh Host alias for github.com urls of this setup"`
	SSHKey     string   `json:"ssh_key,omitempty" flag:"ssh-key" desc:"the key file the route uses (private key or its .pub)"`
	// Route is ssh or https (the gh login of the GitHub user).
	Route onboard.GitRoute `json:"route,omitempty" flag:"route" desc:"how git reaches GitHub: ssh, or https with the account's gh login"`
	// NoSSHKey clears the recorded key: the alias's own key is used.
	NoSSHKey      bool     `json:"no_ssh_key,omitempty" flag:"no-ssh-key" desc:"record no key; the alias's own key is used"`
	GHAccount     string   `json:"gh_account,omitempty" flag:"gh-account" desc:"the gh login for API checks"`
	PersonalStore string   `json:"personal_store,omitempty" flag:"personal-store" desc:"inherit, none, or a store url"`
	Tools         []string `json:"tools,omitempty" flag:"tool" desc:"tool profiles (replaces the list)"`
	Secrets       string   `json:"secrets,omitempty" flag:"secrets" desc:"default secret backend (gopass, op, …)"`
	// Note replaces the setup's note; NoNote clears it.
	Note   string `json:"note,omitempty" flag:"note" desc:"why this setup exists (one or two lines, shown on Home and by setup show)"`
	NoNote bool   `json:"no_note,omitempty" flag:"no-note" desc:"clear the setup's note"`
}

// EditOut is setup.edit's output.
type EditOut struct {
	Name    string `json:"name"`
	Changed bool   `json:"changed"`
	// Warnings are one-line notices of the edit, such as a --folder given
	// more than once and saved once; left out when there is none.
	Warnings []string `json:"warnings,omitempty"`
}

// RemoveIn is setup.remove's input.
type RemoveIn struct {
	Name string `json:"name" arg:"0" desc:"the setup name"`
}

// RemoveOut is setup.remove's output.
type RemoveOut struct {
	Name string `json:"name"`
	// RemovedAt is when it moved to the trash (RFC 3339 UTC).
	RemovedAt string `json:"removed_at"`
}

// JoinIn is setup.join's input.
type JoinIn struct {
	Invite string `json:"invite" arg:"0" desc:"your org's name or store url (a member an admin added with lore member add), or an invite line starting with lore-invite:v1:"`
	NoOpen bool   `json:"no_open,omitempty" flag:"no-open" desc:"print the page address instead of opening a browser"`
}

// Invite is the parsed invite text (ONB 4.5): no secret, grants nothing.
// It is logic/onboard's type (the encoder and decoder live there, and
// logic cannot import ops).
type Invite = onboard.Invite

// InvitePrefix starts every invite line.
const InvitePrefix = onboard.InvitePrefix

// JoinOut is setup.join's output: the page request, plus the parsed
// invite and the member plan for --json.
type JoinOut struct {
	Web    WebStart `json:"web"`
	Invite Invite   `json:"invite"`
	Plan   PlanOut  `json:"plan"`
}

// RepairIn is setup.repair's input.
type RepairIn struct {
	Name   string `json:"name,omitempty" arg:"0" desc:"the setup to repair"`
	All    bool   `json:"all,omitempty" flag:"all" desc:"repair every setup with a failure"`
	Yes    bool   `json:"yes,omitempty" desc:"run the runnable, non-outward fixes (the CLI passes the global --yes)"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"only list the failing steps and their fixes"`
}

// RepairStep is one step to re-ask.
type RepairStep struct {
	Step   onboard.StepID        `json:"step"`
	Checks []onboard.CheckResult `json:"checks"`
}

// RepairSetup is one setup's repair plan.
type RepairSetup struct {
	Name  string       `json:"name"`
	Steps []RepairStep `json:"steps"`
}

// RepairOut is setup.repair's output.
type RepairOut struct {
	Setups []RepairSetup `json:"setups"`
	// Web is set when the page should open on the first failing step.
	Web WebStart `json:"web,omitzero"`
}

// TeamInviteIn is team.invite's input (ONB 4.6).
type TeamInviteIn struct {
	Email    string   `json:"email" arg:"0" desc:"the person's email"`
	GitHub   string   `json:"github,omitempty" flag:"github" desc:"the GitHub user expected to accept"`
	Roles    []string `json:"roles,omitempty" flag:"role" desc:"role names (repeat)"`
	Projects []string `json:"projects,omitempty" flag:"project" desc:"projects to join (repeat)"`
	Teams    []string `json:"teams,omitempty" flag:"team" desc:"a team to join on accept: @handle (repeat)"`
	Type     string   `json:"type,omitempty" flag:"type" desc:"employee, contractor, intern or client_contact (default employee)"`
	Expires  string   `json:"expires,omitempty" flag:"expires" desc:"lifetime, e.g. 14d (default 14d)"`
	Grant    bool     `json:"grant,omitempty" flag:"grant" desc:"also grant push access on GitHub with gh (asks first)"`
}

// TeamInviteOut is team.invite's output.
type TeamInviteOut struct {
	ID string `json:"id"`
	// Line is the invite line to send (json "text", the 7.1 contract).
	Line      string `json:"text"`
	ExpiresOn string `json:"expires_on"`
	// Email and Org are who is invited and to which org (for Text).
	Email string `json:"email"`
	Org   string `json:"org"`
	// Teams are the team handles the person joins on accept.
	Teams []string `json:"teams,omitempty"`
	// GitHub is the expected GitHub user, Repo the store's owner/repo on
	// GitHub ("" when the store is not on github.com).
	GitHub string `json:"github,omitempty"`
	Repo   string `json:"repo,omitempty"`
	// GitHubAccess: granted, pending (command printed), or "" without --github.
	GitHubAccess string `json:"github_access,omitempty"`
	// GitHubFix is the gh command that grants access, set while pending.
	GitHubFix string `json:"github_fix,omitempty"`
	// GitHubDetail is set while pending when the access could not be
	// checked at all (gh missing, not logged in, an API error): "pending"
	// then means unknown, not "has no access" (ONB 4.6, A11 bug 7).
	GitHubDetail string `json:"github_detail,omitempty"`
}

// githubPending is TeamInviteOut.GitHubAccess while the person has no
// access yet (ops/team.GitHubPending, same value).
const githubPending = "pending"

// Text implements op.Texter: the ONB 1.6 block.
func (o TeamInviteOut) Text() string {
	var b strings.Builder
	where := o.Org
	if where == "" {
		where = "the org store"
	} else {
		where = "store " + where
	}
	fmt.Fprintf(&b, "✓ invite %s written to %s (expires %s)\n", o.ID, where, o.ExpiresOn)
	if o.GitHub != "" {
		target := o.Repo
		if target == "" {
			target = "the org store"
		}
		if o.GitHubAccess == githubPending && o.GitHubDetail != "" {
			fmt.Fprintf(&b, "⚠ could not check whether %s can push to %s: %s\n", o.GitHub, target, o.GitHubDetail)
			if o.GitHubFix != "" {
				fmt.Fprintf(&b, "  to grant it: %s\n", o.GitHubFix)
			}
		} else if o.GitHubAccess == githubPending {
			fmt.Fprintf(&b, "⚠ %s has no access to %s yet\n", o.GitHub, target)
			if o.GitHubFix != "" {
				fmt.Fprintf(&b, "  fix: %s\n       (or add %s to a GitHub team that has write access)\n", o.GitHubFix, o.GitHub)
			}
		} else if o.GitHubAccess != "" {
			fmt.Fprintf(&b, "✓ %s can push to %s\n", o.GitHub, target)
		}
	}
	fmt.Fprintf(&b, "Send this to %s (it holds no secret):\n  lore setup join %s\n", o.Email, o.Line)
	return b.String()
}

// TeamInviteListIn is team.invite.list's input.
type TeamInviteListIn struct {
	Status string `json:"status,omitempty" flag:"status" desc:"pending, accepted, revoked or expired"`
	// --created-by / --updated-by: the shared writer filters of every
	// list (INTEG-AUDIT-1; engine.List applies them to the table that
	// declares this list's noun, schema.Noun).
	metalist.Audit
}

// InviteRow is one invite in a listing.
type InviteRow struct {
	ID         string   `json:"id"`
	Email      string   `json:"email"`
	GitHub     string   `json:"github,omitempty"`
	Roles      []string `json:"roles,omitempty"`
	Projects   []string `json:"projects,omitempty"`
	Teams      []string `json:"teams,omitempty"`
	PeriodType string   `json:"period_type"`
	Status     string   `json:"status"`
	ExpiresOn  string   `json:"expires_on"`
	AcceptedBy string   `json:"accepted_by,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// TeamInviteListOut is team.invite.list's output.
type TeamInviteListOut struct {
	Invites []InviteRow `json:"invites"`
}

// TeamInviteRefIn is the input of team.invite.revoke and .resend.
type TeamInviteRefIn struct {
	Ref string `json:"ref" arg:"0" desc:"an inv_ id or the invited email"`
}

// TeamInviteRevokeOut is team.invite.revoke's output.
type TeamInviteRevokeOut struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// TeamInviteResendOut is team.invite.resend's output.
type TeamInviteResendOut struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// skill.install / skill.status inputs and outputs live in ops/skill
// (InstallIn, InstallOut, StatusIn, StatusOut); repo.init's in ops/repoinit
// (InitIn, InitOut). Only their names are repeated above, for PageOps;
// TestOpNamesMatch pins them equal.

// OrgStoreLister is an optional Store upgrade: the store urls of the
// orgs[org] entry in config.yaml (found=false: no such entry). The team
// step's create-only checks use it to tell an org entry this setup's own
// interrupted save left behind (same name, same store, no setup on it:
// saving again resumes it) from a real clash (onboard.OwnOrg). A Store
// without it keeps the plain checks.
type OrgStoreLister interface {
	OrgStoreURLs(ctx context.Context, org string) (urls []string, found bool, err error)
}
