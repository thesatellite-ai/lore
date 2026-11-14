package onboard

import (
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Fixes (ONB 2.2 Fix, the fix texts of section 4, 6.1) and filling them.
// A fix's Command is an argv template: the page and `setup.fix` run argv
// only, never a shell string (5.3), and FillFix re-validates every value
// put into it, so a crafted answer or probe output cannot turn a fix into
// another command (`; rm -rf ~`, `--upload-pack=…`, a token url).

// FixVariant picks one of a check's fix texts by probe outcome.
type FixVariant string

// Fix variants.
const (
	// FixDefault is the catalog fix.
	FixDefault FixVariant = ""
	// FixUnreachable: store.read could not reach the host (network, not
	// auth; 4.4).
	FixUnreachable FixVariant = "unreachable"
	// FixNotGitHub: store.read refused for a url that is not on GitHub, so
	// `gh repo create` does not apply.
	FixNotGitHub FixVariant = "not_github"
	// FixNoVersionLine: the installed skill has no lore_version line
	// (4.7.1).
	FixNoVersionLine FixVariant = "no_version_line"
	// FixRepo: identity.git_email checked per repo (doctor, 4.10, 6.2).
	FixRepo FixVariant = "repo"
	// FixMemberJoin: invite.found of a join by the org's name or store url
	// (no invite line): the person is not on the roster, so the fix is the
	// admin's lore member add, never an invite resend (B2-13).
	FixMemberJoin FixVariant = "member_join"
	// FixKeyPair: github.key_matches knows an alias that logs in as the
	// account and its key's .pub ({fix_alias}, {fix_key}).
	FixKeyPair FixVariant = "key_pair"
	// FixKeyAliasOnly: the fix records that alias and no key file (the
	// alias's own key decides).
	FixKeyAliasOnly FixVariant = "key_alias_only"
	// FixRecordedKey: github.ssh_user probed the setup's recorded key file
	// (ssh -i), not the alias, and that key logs in as another account.
	// The runnable fix is github.key_matches': it drops the recorded key
	// when the setup's own alias logs in as the account (realmac NEW-2),
	// else it records another alias (and its .pub) that does, so this
	// text names neither outcome (realmac m1: it promised "drops the
	// recorded key" while the Run it beside it recorded another alias).
	FixRecordedKey FixVariant = "recorded_key"
	// FixNoRead: store.write failed before the write right was tested (the
	// host refused the read itself: no access, auth, host key), so "you
	// can read it but not write it" would be false.
	FixNoRead FixVariant = "no_read"
	// FixGHOptional: store.private could not ask GitHub because gh is not
	// installed or not logged in, on the ssh route where gh is optional:
	// a note naming how to let lore check, never "make it private".
	FixGHOptional FixVariant = "gh_optional"
	// FixWriteUnknown: store.write failed for a reason that says nothing
	// about the write right (git failed some other way).
	FixWriteUnknown FixVariant = "write_unknown"
	// FixAfterUpgrade: installing this binary's skill would give agents
	// commands the older lore on PATH lacks; upgrade lore first (not
	// runnable: the upgrade is the user's package manager).
	FixAfterUpgrade FixVariant = "after_upgrade"
	// FixLinked: the tool's skill folder (or a bundle file in it) is a
	// symbolic link whose target does not hold this binary's skill; a plain
	// install refuses (E_SKILL_LINKED), so the fix names both choices and
	// runs neither (the person picks: replace the link, or write into its
	// target).
	FixLinked FixVariant = "linked"
	// FixInitTargets: repo.init for a setup that chose coding tools with a
	// pointer file: `lore init --target {targets}` wires those tools'
	// files (CLAUDE.md for claude_code, …) instead of init's default
	// (the tool files found in the repo, else AGENTS.md; spec 7.3).
	FixInitTargets FixVariant = "init_targets"
	// FixReachedBefore: store.read failed for a store this machine has
	// fetched from before (its local copy holds the store's history), so
	// the repo existed: "create it" would be wrong. The repo was renamed
	// or moved, this setup's account or key lost access, or the network
	// answers with something else (journey solo: offline on a train,
	// after an ssh key switch).
	FixReachedBefore FixVariant = "reached_before"
	// FixIdentityConflict: identity.top_level found the top-level
	// identity: and the setup's naming two different people (or two
	// spellings); its Run it keeps the setup's, identity.top_level_keep's
	// keeps the top-level one. Neither is picked for the person.
	FixIdentityConflict FixVariant = "identity_conflict"
	// FixNoPeriod: identity.period found the person on the roster with no
	// period at all. "You are marked as left" would be wrong (they never
	// had a stint); the fix is to record the first one (teamfix n3).
	FixNoPeriod FixVariant = "no_period"
)

// VarLevel is the ProbeResult.Vars key a prober sets to "info" to lower a
// warn result to LevelInfo.
const VarLevel = "level"

// VarFixVariant is the ProbeResult.Vars key a prober sets to choose a
// FixVariant.
const VarFixVariant = "fix_variant"

// Placeholder names (the `{name}` of fix texts and argv templates). Setup
// context and answers fill most of them; the rest come from probe Vars.
const (
	VarSetup       = "setup"        // the setup name
	VarName        = "name"         // the kind step's name answer (the setup being made)
	VarOrg         = "org"          // the setup's org
	VarEmail       = "email"        // the identity email
	VarInviteEmail = "invite_email" // the invite's email
	VarInviter     = "inviter"      // the invite's by (an email)
	VarBy          = "by"           // the invite's by (an email)
	VarGitEmail    = "git_email"    // git config user.email in effect
	VarUser        = "user"         // setups[].github.user
	VarGot         = "got"          // the user ssh -T named
	VarActive      = "active"       // gh's active account
	VarAlias       = "alias"        // the ssh host alias
	VarFingerprint = "fingerprint"  // ssh-keygen -lf fingerprint
	VarPath        = "path"         // a public key path
	VarSSHKey      = "ssh_key"      // the setup's key file path (private key or .pub)
	VarOwner       = "owner"        // a GitHub owner (from the store url)
	VarRepo        = "repo"         // a GitHub repo name (from the store url)
	VarURL         = "url"          // a store url
	VarStore       = "store"        // a store url (join, attached stores)
	VarStoreName   = "store_name"   // a store's lore name (me, an org's store): store.marker's lore store init
	VarStoreOrg    = "store_org"    // the org a store already holds (org.free)
	VarGitHub      = "github"       // an invite's GitHub user
	VarGitHubUser  = "github_user"  // the joiner's GitHub user
	VarID          = "id"           // an invite id
	VarStatus      = "status"       // an invite status
	VarTool        = "tool"         // a tool profile name
	VarSkillV      = "skill_v"      // the installed skill's lore_version
	VarBinV        = "bin_v"        // the binary's version
	VarBackend     = "backend"      // a secret manager
	VarFolder      = "folder"       // a folder glob of this setup
	VarOther       = "other"        // another setup's name
	VarOtherFolder = "other_folder" // another setup's folder glob
	VarRepoRoot    = "repo_root"    // a repo's root folder
	VarRepoOrg     = "repo_org"     // the org a repo resolves to
	VarSource      = "source"       // where the repo's org came from (display only)
	VarFixAlias    = "fix_alias"    // github.key_matches: an alias that logs in as the account
	VarFixKey      = "fix_key"      // github.key_matches: that alias's .pub
	VarTargets     = "targets"      // repo.init: the setup's tool profiles, comma separated (lore init --target)
	// identity.top_level / identity.top_level_keep: the two identities, as
	// "Name <key>" (display only, never in a command).
	VarTopIdentity   = "top_identity"
	VarSetupIdentity = "setup_identity"
	// store.address: the store's sto_ id (its address is {url}).
	VarStoreID = "store_id"
)

// valueKind is how a placeholder's value is validated. Every kind except
// vkText is safe in an argv element.
type valueKind int

const (
	vkText        valueKind = iota // one line of display text; never in a Command
	vkName                         // a GitHub login, repo, alias, tool, backend, status
	vkSetup                        // a setup or org name
	vkEmail                        // a bare email address
	vkPath                         // an absolute or ~/ path or glob
	vkURL                          // a store url (RuleGitURL)
	vkID                           // a row id like inv_01jfa0c3
	vkVersion                      // a lore version
	vkFingerprint                  // SHA256:… key fingerprint
	vkProfiles                     // tool profile names, comma separated (claude_code,codex)
)

// Value shapes. nameRe is GitHub's login/repo charset plus dots; none of
// the patterns allow a leading -, so a value can never become an option.
var (
	nameRe        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	idRe          = regexp.MustCompile(`^[a-z]{2,4}_[A-Za-z0-9]{1,64}$`)
	versionRe     = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
	fingerprintRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:+/=]{0,127}$`)
	// profilesRe: toolprofile names (^[a-z][a-z0-9_]*$), comma separated,
	// so the value is one argv word init's --target splits on commas.
	profilesRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}(,[a-z][a-z0-9_]{0,63}){0,15}$`)
)

// shellMeta are characters refused in argv path values: not dangerous to
// argv itself, but never part of a real key or folder path, and a sign of
// an injection attempt worth refusing loudly.
const shellMeta = ";|&$`<>\\\"'(){}"

// maxTextValue caps a display-only value.
const maxTextValue = 300

// placeholderKinds maps every placeholder a fix may use to its value kind.
var placeholderKinds = map[string]valueKind{
	VarSetup: vkSetup, VarName: vkText, VarOrg: vkSetup, VarStoreName: vkSetup, VarStoreOrg: vkSetup, VarRepoOrg: vkSetup, VarOther: vkSetup,
	VarEmail: vkEmail, VarInviteEmail: vkEmail, VarInviter: vkEmail, VarBy: vkEmail, VarGitEmail: vkEmail,
	VarUser: vkName, VarGot: vkName, VarActive: vkName, VarAlias: vkName, VarOwner: vkName, VarRepo: vkName,
	VarGitHub: vkName, VarGitHubUser: vkName, VarStatus: vkName, VarTool: vkName, VarBackend: vkName,
	VarFingerprint: vkFingerprint,
	VarPath:        vkPath, VarSSHKey: vkPath, VarFolder: vkPath, VarOtherFolder: vkPath, VarRepoRoot: vkPath,
	VarURL: vkURL, VarStore: vkURL,
	VarID:     vkID,
	VarSkillV: vkVersion, VarBinV: vkVersion,
	VarTargets:     vkProfiles,
	VarTopIdentity: vkText, VarSetupIdentity: vkText,
	VarSource:   vkText,
	VarFixAlias: vkName, VarFixKey: vkPath,
	VarStoreID: vkID,
}

// checkValue validates v as kind k; inArgv applies the stricter argv rule.
func checkValue(k valueKind, v string, inArgv bool) (why string, ok bool) {
	if v == "" {
		return "has no value", false
	}
	if hasControl(v) {
		return "has a control character or line break", false
	}
	switch k {
	case vkText:
		if inArgv {
			return "is display text and may not go into a command", false
		}
		if utf8.RuneCountInString(v) > maxTextValue {
			return "is longer than " + strconv.Itoa(maxTextValue) + " characters", false
		}
		return "", true
	case vkName:
		if !nameRe.MatchString(v) {
			return "must be a plain name (letters, digits, . _ -), not " + strconv.Quote(v), false
		}
	case vkSetup:
		if !ValidSetupName(v) {
			return "must be a setup or org name (a-z, 0-9, _ or -), not " + strconv.Quote(v), false
		}
	case vkEmail:
		return ValidateValue(R(RuleEmail), v)
	case vkPath:
		if strings.HasPrefix(v, "-") || strings.ContainsAny(v, shellMeta) {
			return "must be a plain path, not " + strconv.Quote(v), false
		}
		return ValidateValue(R(RuleAbsGlob), v)
	case vkURL:
		return ValidateValue(R(RuleGitURL), v)
	case vkID:
		if !idRe.MatchString(v) {
			return "must be an id like inv_01jfa0c3, not " + strconv.Quote(v), false
		}
	case vkVersion:
		if !versionRe.MatchString(v) {
			return "must be a version, not " + strconv.Quote(v), false
		}
	case vkFingerprint:
		if !fingerprintRe.MatchString(v) {
			return "must be a key fingerprint, not " + strconv.Quote(v), false
		}
	case vkProfiles:
		if !profilesRe.MatchString(v) {
			return "must be tool profile names separated by commas, not " + strconv.Quote(v), false
		}
	}
	return "", true
}

// placeholderRe finds {name} placeholders.
var placeholderRe = regexp.MustCompile(`\{([a-z_]+)\}`)

// Placeholders lists the placeholder names a fix uses, sorted and unique.
func Placeholders(f Fix) []string {
	set := map[string]bool{}
	for _, s := range append(append([]string{f.Text, f.Why}, f.Command...), choiceStrings(f)...) {
		for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
			set[m[1]] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// choiceStrings is every label and argv element of f's choices.
func choiceStrings(f Fix) []string {
	var out []string
	for _, c := range f.Choices {
		out = append(out, c.Label)
		out = append(out, c.Command...)
	}
	return out
}

// AnswersVars flattens answers into fill vars: text as is, lists joined
// by ", ", bools as true/false.
func AnswersVars(a Answers) map[string]string {
	out := make(map[string]string, len(a))
	for k, v := range a {
		switch {
		case v.IsBool:
			out[k] = strconv.FormatBool(v.Bool)
		case v.IsList:
			out[k] = strings.Join(v.List, ", ")
		default:
			out[k] = v.Text
		}
	}
	return out
}

// slashPaths is vars with every path value in forward slashes: a Windows
// path (C:\Users\a\hub) would fail the shell-character rule on its
// backslashes, and git, ssh and lore read C:/Users/a/hub the same. A no-op
// elsewhere (a backslash is a plain name character there).
func slashPaths(vars map[string]string) map[string]string {
	out := make(map[string]string, len(vars))
	for k, v := range vars {
		if placeholderKinds[k] == vkPath {
			v = filepath.ToSlash(v)
		}
		out[k] = v
	}
	return out
}

// FillFix fills fix's placeholders from vars and re-validates every value:
// with the placeholder's own value rule (stricter inside Command) and with
// the Rules of step's question of the same id, when one applies. Any
// missing or bad value refuses the whole fix with E_FIX_REFUSED ("fix for
// {check} refused: {field} {why}"); nothing is half filled. The result's
// Command is never nil.
func FillFix(check CheckID, fix Fix, vars map[string]string, step Step) (Fix, error) {
	inArgv := map[string]bool{}
	argvs := slices.Clone(fix.Command)
	for _, c := range fix.Choices {
		argvs = append(argvs, c.Command...)
	}
	for _, a := range argvs {
		for _, m := range placeholderRe.FindAllStringSubmatch(a, -1) {
			inArgv[m[1]] = true
		}
	}
	vars = slashPaths(vars)
	for _, p := range Placeholders(fix) {
		v := vars[p]
		k, known := placeholderKinds[p]
		if !known {
			return Fix{}, refuse(check, p, "is not a known placeholder")
		}
		if why, ok := checkValue(k, v, inArgv[p]); !ok {
			return Fix{}, refuse(check, p, why)
		}
		for _, q := range step.Questions {
			if q.ID != p || q.Type.IsList() || q.Type == QBool {
				continue
			}
			for _, r := range q.Rules {
				if why, ok := ValidateValue(r, v); !ok {
					return Fix{}, refuse(check, p, why)
				}
			}
		}
	}
	fill := func(s string) string {
		return placeholderRe.ReplaceAllStringFunc(s, func(m string) string { return vars[m[1:len(m)-1]] })
	}
	out := Fix{Text: fill(fix.Text), Command: make([]string, 0, len(fix.Command)), Runnable: fix.Runnable, Outward: fix.Outward, Why: fill(fix.Why)}
	for _, a := range fix.Command {
		out.Command = append(out.Command, fill(a))
	}
	for _, c := range fix.Choices {
		fc := FixChoice{ID: c.ID, Label: fill(c.Label), Command: make([]string, 0, len(c.Command))}
		for _, a := range c.Command {
			fc.Command = append(fc.Command, fill(a))
		}
		out.Choices = append(out.Choices, fc)
	}
	return out, nil
}

// unknownValue stands in TextFix for a placeholder with no usable value.
const unknownValue = "…"

// TextFix is fix as text only, for a result whose FillFix refused (a
// value missing or failing its rule, e.g. {repo} of a url that is not on
// GitHub): each placeholder whose value passes its kind's rule is filled,
// every other one reads "…", and the command is dropped (never runnable).
// A check row then shows a readable sentence, never the E_FIX_REFUSED
// text of the refusal nor a raw {placeholder}.
func TextFix(fix Fix, vars map[string]string) Fix {
	fill := func(s string) string {
		return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
			p := m[1 : len(m)-1]
			k, known := placeholderKinds[p]
			if v := vars[p]; known && v != "" {
				if _, ok := checkValue(k, v, false); ok {
					return v
				}
			}
			return unknownValue
		})
	}
	return Fix{Text: fill(fix.Text), Command: []string{}, Outward: fix.Outward, Why: fill(fix.Why)}
}

// refuse builds the E_FIX_REFUSED error (ONB 7.2).
func refuse(check CheckID, field, why string) error {
	return errs.New(errs.FixRefused, "fix for "+string(check)+" refused: "+field+" "+why).
		WithHint("correct the answer or the setup entry (lore setup edit), then run lore setup check again")
}

// fixKey selects a fix row: by check, then optionally by step and variant.
type fixKey struct {
	check   CheckID
	step    StepID
	variant FixVariant
}

// argv builds a Command template.
func argv(a ...string) []string { return a }

// Command words, named once so the fix rows read like the commands.
const (
	cmdLore = "lore"
	cmdGit  = "git"
	cmdGH   = "gh"
)

// fixes is every fix text of section 4 and 6.1. A row with a step or a
// variant overrides the check's default row (step "" and variant "").
var fixes = map[fixKey]Fix{
	// Shown, not run: config check only lists the problems, so a Run it
	// would "finish" and leave the check failing; the person edits the file.
	{check: CheckSetupConfig}: {Text: "setups[{setup}] in ~/.lore/config.yaml does not validate; lore config check lists every problem, then edit the file",
		Command: argv(cmdLore, "config", "check")},
	{check: CheckSetupOrgEntry}: {Text: "orgs[{org}] is missing, so setup {setup} has no identity or store; join again with lore setup join --invite <invite>, or lore setup remove --name {setup}"},
	{check: CheckSetupNameFree}: {Text: "setup {name} already exists; pick another name or run lore setup edit --name {name}"},

	{check: CheckIdentityEmailFormat}: {Text: "Type the address you use for this work, e.g. you@company.example"},
	{check: CheckIdentityInviteEmail}: {Text: "This invite is for {invite_email}. Use that address, or ask {inviter} for a new invite to {email}"},
	// git config is the person's own: shown, never run (4.2).
	{check: CheckIdentityGitEmail}: {Text: "git commits here will carry {git_email}. To use {email}: git config --global user.email {email}",
		Command: argv(cmdGit, "config", "--global", "user.email", "{email}")},
	{check: CheckIdentityGitEmail, variant: FixRepo}: {Text: "{repo_root} commits as {git_email}. To use {email}: git -C {repo_root} config user.email {email}",
		Command: argv(cmdGit, "-C", "{repo_root}", "config", "user.email", "{email}")},
	// SOURCE_OF_TRUTH rows 7 and 9: one source per copied datum.
	{check: CheckStoreAddress}: {Text: "Store {store_id} is reached at two addresses on this machine, and lore writes only to {url}. Run it to point every place naming the store at {url}: lore store set-url --name {store_id} --url {url} (to keep the other address, run lore store set-url --name {store_id} --url <that-address> instead)",
		Command: argv(cmdLore, "store", "set-url", "--name", "{store_id}", "--url", "{url}"), Runnable: true},
	{check: CheckOrgSource}: {Text: "The {org} entry in ~/.lore/config.yaml is older than, or differs from, the store's lore-org.yaml (the team's source for its stores and repo patterns). Run it to copy them in: lore org refresh --org {org}",
		Command: argv(cmdLore, "org", "refresh", "--org", "{org}"), Runnable: true},
	{check: CheckIdentityRoster}:                       {Text: "Your email is not on the {org} roster yet; finish the join step or run lore actor contact add --actor <you> --type email --value <your-email>"},
	{check: CheckIdentityPeriod}:                       {Text: "You are marked as left in {org}. If you are back, ask an admin of {org} to run lore member add --email {email} (it reopens a stint that ended today)"},
	{check: CheckIdentityPeriod, variant: FixNoPeriod}: {Text: "{org} has no period (stint) for you yet, so lore cannot tell since when you are a member. Ask an admin of {org} to record your first one: lore actor period add --actor <you> --type employee --started-on <your-first-day>"},
	// SOURCE_OF_TRUTH row 1: the setup is the one source of who you are.
	{check: CheckIdentityTopLevel}: {Text: "The top-level identity: in ~/.lore/config.yaml ({top_identity}) is an old copy of setup {setup}'s identity. Run it to move what the setup lacks into it and remove the top-level block: lore identity migrate --keep setup --setup {setup}",
		Command: argv(cmdLore, "identity", "migrate", "--keep", "setup", "--setup", "{setup}"), Runnable: true},
	{check: CheckIdentityTopLevel, variant: FixIdentityConflict}: {Text: "The top-level identity: says {top_identity}, setup {setup} says {setup_identity}; lore uses the setup's and never picks for you. Run it to keep {setup_identity} and remove the top-level block (identity.top_level_keep keeps {top_identity} instead): lore identity migrate --keep setup --setup {setup}",
		Command: argv(cmdLore, "identity", "migrate", "--keep", "setup", "--setup", "{setup}"), Runnable: true},
	{check: CheckIdentityTopLevelKeep}: {Text: "Run it to keep the top-level {top_identity} as setup {setup}'s identity (it replaces {setup_identity}) and remove the top-level block (identity.top_level keeps {setup_identity} instead): lore identity migrate --keep top --setup {setup}",
		Command: argv(cmdLore, "identity", "migrate", "--keep", "top", "--setup", "{setup}"), Runnable: true},
	// SOURCE_OF_TRUTH rows 2-3: stale actor copies.
	{check: CheckIdentityActorCopies}: {Text: "Some stores hold an old copy of an actor (the detail names each). Run it to rewrite them from the source now (the next write to each store would too): lore actor refresh",
		Command: argv(cmdLore, "actor", "refresh"), Runnable: true},

	{check: CheckGitHubSSHKey}: {Text: "No key file at {path}. Pick the account again on the GitHub step (lore finds the key its route uses), or make one: ssh-keygen -t ed25519 -f ~/.ssh/id_{setup} -C {email}, then add its .pub at https://github.com/settings/keys",
		Command: argv("ssh-keygen", "-t", "ed25519", "-f", "~/.ssh/id_{setup}", "-C", "{email}")},
	{check: CheckGitHubSSHAuth}: {Text: "GitHub refused the key {alias} offers. Add that key's .pub at https://github.com/settings/keys while logged in as {user}; GitHub refuses a key that is already on another account, so then pick that account, or point {alias} at another key in ~/.ssh/config"},
	// Text only: the runnable fix for this cause is github.key_matches'
	// (one remedy per cause, realmac m1); this row points at it.
	{check: CheckGitHubSSHUser, variant: FixRecordedKey}: {Text: "The key this setup records, {ssh_key}, logs in as {got}, not {user}. The fix is github.key_matches' Run it: it points this setup at a route that logs in as {user}"},
	{check: CheckGitHubSSHUser}:                          {Text: "{alias} logs in as {got}, not {user}. In ~/.ssh/config point Host {alias} at {user}'s key (IdentityFile <that key>, IdentitiesOnly yes), or pick the {got} account"},
	{check: CheckGitHubKeyMatches}:                       {Text: "Nothing lore can use logs in as {user}. Run the GitHub step again so lore finds a route (an ssh alias that logs in as {user}, or https with {user}'s gh login: gh auth login), or add a Host block for {user}'s key to ~/.ssh/config"},
	{check: CheckGitHubKeyMatches, variant: FixKeyPair}: {Text: "Use {fix_alias} with {fix_key}, which log in as {user}: lore setup edit --name {setup} --route ssh --host-alias {fix_alias} --ssh-key {fix_key}",
		Command: argv(cmdLore, "setup", "edit", "--name", "{setup}", "--route", "ssh", "--host-alias", "{fix_alias}", "--ssh-key", "{fix_key}"), Runnable: true},
	{check: CheckGitHubKeyMatches, variant: FixKeyAliasOnly}: {Text: "Use {fix_alias}, which logs in as {user}, with its own key: lore setup edit --name {setup} --route ssh --host-alias {fix_alias} --no-ssh-key",
		Command: argv(cmdLore, "setup", "edit", "--name", "{setup}", "--route", "ssh", "--host-alias", "{fix_alias}", "--no-ssh-key"), Runnable: true},
	{check: CheckGitHubAliasConfig}: {Text: "Add 'IdentitiesOnly yes' under 'Host {alias}' in ~/.ssh/config so ssh does not offer your other keys first"},
	{check: CheckGitHubGHAccount}: {Text: "gh is optional; it only speeds up checks and lets lore offer 'create repo'. To add: gh auth login --hostname github.com",
		Command: argv(cmdGH, "auth", "login", "--hostname", "github.com")},
	{check: CheckGitHubGHMatches}: {Text: "Informational: gh's active account is {active}. lore runs gh for this setup as {user} (its own gh login), so nothing needs changing; only for gh you run by hand here: gh auth switch --user {user}",
		Command: argv(cmdGH, "auth", "switch", "--user", "{user}")},
	// A warning, never a refusal: a different identity can be intended.
	{check: CheckGitHubIdentityEmail}: {Text: "{email} is not on GitHub account {user}. Pick the account whose emails include {email}, add {email} at https://github.com/settings/emails while logged in as {user}, or change this setup's identity email on the Identity step"},

	{check: CheckStoreURL}: {Text: "Use an ssh url like git@github.com:you/lore-me.git; a token inside a url is never stored"},
	{check: CheckStoreRead}: {Text: "Create it: gh repo create {owner}/{repo} --private, or ask the owner for access",
		Command: argv(cmdGH, "repo", "create", "{owner}/{repo}", "--private"), Runnable: true, Outward: true},
	{check: CheckStoreRead, variant: FixUnreachable}:                 {Text: "Check your network or VPN; lore will retry"},
	{check: CheckStoreRead, variant: FixReachedBefore}:               {Text: "lore read {url} before and keeps its local copy (nothing is lost; writes wait for the next sync). If the store moved or was renamed: lore store set-url --name {store_name} --url <new-url>; if it was deleted: create an empty repository at {url}, then lore store push --store {store_name} --restore --yes from the clone with the most work; otherwise check that this setup's account and key still have access (ask an admin of the store)"},
	{check: CheckStoreRead, variant: FixNotGitHub}:                   {Text: "No access to {url}. Check the url and this setup's key, or ask the store's owner for access"},
	{check: CheckStoreRead, step: StepJoin}:                          {Text: "You have no access to {store} yet. Accept the GitHub invitation at https://github.com/notifications, or ask {by} to add {github_user}"},
	{check: CheckStoreRead, step: StepJoin, variant: FixUnreachable}: {Text: "Check your network or VPN; lore will retry"},
	{check: CheckStoreWrite}:                                         {Text: "You can read {repo} but not write it. Ask its owner for write access, or pick a repo you own"},
	{check: CheckStoreWrite, variant: FixNotGitHub}:                  {Text: "lore cannot write to {url}. For a folder store, check that the folder exists and that you may create files in it; for a git server, ask its owner for push access, or pick a store you can write"},
	{check: CheckStoreWrite, step: StepJoin}:                         {Text: "You can read {org}'s store but not write it. Ask {by} for write access (GitHub: push)"},
	// A write test that never reached the write right claims nothing about
	// it: the default "You can read … but not write it" is only for a
	// refused push (probe class no_write). Join rows repeat these, since a
	// step row outranks a step-less variant (FixFor).
	{check: CheckStoreWrite, variant: FixUnreachable}:                  {Text: "Check your network or VPN; lore will retry"},
	{check: CheckStoreWrite, variant: FixNoRead}:                       {Text: "lore cannot read {repo} either, so it could not test writing it. Fix store.read first, then re-check"},
	{check: CheckStoreWrite, variant: FixWriteUnknown}:                 {Text: "lore could not tell whether you can write {repo} (see the detail). Re-check; if it fails again, fix what the detail names"},
	{check: CheckStoreWrite, step: StepJoin, variant: FixUnreachable}:  {Text: "Check your network or VPN; lore will retry"},
	{check: CheckStoreWrite, step: StepJoin, variant: FixNoRead}:       {Text: "lore cannot read {org}'s store either, so it could not test writing it. Fix store.read first, then re-check"},
	{check: CheckStoreWrite, step: StepJoin, variant: FixWriteUnknown}: {Text: "lore could not tell whether you can write {org}'s store (see the detail). Re-check; if it fails again, fix what the detail names"},
	{check: CheckStoreWrite, step: StepJoin, variant: FixNotGitHub}:    {Text: "lore cannot write to {url}. For a git server that is not GitHub, ask {by} for push access"},
	// {store_name} is the checked store's own name (me in the personal
	// store step, the org's store name in doctor): never a fixed "me".
	{check: CheckStoreMarker}: {Text: "This repo is not a lore store yet; Save will run lore store init --name {store_name} --url {url}"},
	// The marker could not be looked for (F-11): an unread store is not
	// "not a store", and store init over the org's store is never the step.
	{check: CheckStoreMarker, variant: FixUnreachable}:   {Text: "The store at {url} did not answer, so lore could not look for its lore-store.yaml. Check your network or VPN; lore will retry"},
	{check: CheckStoreMarker, variant: FixReachedBefore}: {Text: "lore read {url} before and keeps its local copy, but the store now refuses this machine: your access was most likely removed, or the repo was moved or deleted. Do not run lore store init over it. If it moved or was renamed: lore store set-url --name {store_name} --url <new-url>; if it was deleted: create an empty repository at {url}, then lore store push --store {store_name} --restore --yes from the clone with the most work; otherwise ask an admin of the store whether you still have access"},
	{check: CheckStoreMarker, variant: FixNoRead}:        {Text: "lore cannot read {url}, so it cannot tell whether it is a lore store. Fix store.read first: the url, this setup's key, or access from the store's owner"},
	{check: CheckStorePrivate}: {Text: "This repo is public. Your personal notes would be readable by anyone: gh repo edit {owner}/{repo} --visibility private",
		Command: argv(cmdGH, "repo", "edit", "{owner}/{repo}", "--visibility", "private"), Runnable: true, Outward: true},
	{check: CheckStorePrivate, variant: FixGHOptional}:                 {Text: "Optional: install gh and run gh auth login so lore can check that {owner}/{repo} is private"},
	{check: CheckStorePrivate, step: StepTeam, variant: FixGHOptional}: {Text: "Optional: install gh and run gh auth login so lore can check that {owner}/{repo} is private"},
	{check: CheckStorePrivate, step: StepTeam}: {Text: "This repo is public. Your team's notes would be readable by anyone: gh repo edit {owner}/{repo} --visibility private",
		Command: argv(cmdGH, "repo", "edit", "{owner}/{repo}", "--visibility", "private"), Runnable: true, Outward: true},

	{check: CheckInviteParse}:                         {Text: "This is not a lore invite. Copy the whole line starting with lore-invite:v1:"},
	{check: CheckInviteFound}:                         {Text: "Invite {id} is not in {org}'s store. Ask {by} to run lore team invite --email {invite_email} again"},
	{check: CheckInviteFound, variant: FixMemberJoin}: {Text: "{email} is not on the org's roster. Ask an admin to run lore member add --email {email}, then join again (an outsider joins with an invite line instead)"},
	{check: CheckInviteStatus}:                        {Text: "Invite {id} is {status}. Ask {by} for a new one"},
	{check: CheckInviteEmail}:                         {Text: "This invite is for {invite_email}. Use that address, or ask {inviter} for a new invite to {email}"},
	{check: CheckInvitePending}: {Text: "Invite {id} for {invite_email} is still pending. Resend it (lore team invite resend --ref {id}) or revoke it (lore team invite revoke --ref {id})",
		Command: argv(cmdLore, "team", "invite", "resend", "--ref", "{id}"), Runnable: true},
	// --grant (Q9): runs only after an explicit confirm.
	{check: CheckInviteGitHubAccess}: {Text: "{github} has no access to {owner}/{repo} yet. Grant push access: gh api -X PUT repos/{owner}/{repo}/collaborators/{github} -f permission=push",
		Command: argv(cmdGH, "api", "-X", "PUT", "repos/{owner}/{repo}/collaborators/{github}", "-f", "permission=push"), Runnable: true, Outward: true},

	{check: CheckOrgFree}:     {Text: "This repo holds files but is not a lore store; give a new or empty private repo, or your team's existing lore store"},
	{check: CheckOrgNameFree}: {Text: "An orgs[] entry named {org} already exists; pick another name, or lore org rename"},

	{check: CheckLiveMode}: {Text: "Autopush is on, but the remote of {repo_root} refused a dry-run push of refs/lore/live. Get push access, or set live.autopush: false on setup {setup}"},

	{check: CheckToolsSkillInstalled}: {Text: "Install the lore skill for {tool}: lore skill install --tool {tool}",
		Command: argv(cmdLore, "skill", "install", "--tool", "{tool}"), Runnable: true},
	{check: CheckToolsSkillInstalled, variant: FixAfterUpgrade}: {Text: "Upgrade the lore on your PATH to {bin_v} or later first, then install the skill: lore skill install --tool {tool}"},
	// Runs in the repo: the FixRunner uses the result's repo as cwd.
	{check: CheckToolsSkillVersion, variant: FixAfterUpgrade}: {Text: "Upgrade the lore on your PATH to {bin_v} or later first, then reinstall the skill: lore skill install --tool {tool}"},
	{check: CheckToolsSkillVersion}: {Text: "The {tool} skill is from lore {skill_v}; you run {bin_v}. Reinstall: lore skill install --tool {tool}",
		Command: argv(cmdLore, "skill", "install", "--tool", "{tool}"), Runnable: true},
	{check: CheckToolsSkillVersion, variant: FixNoVersionLine}: {Text: "The {tool} skill has no version line and differs from lore {bin_v}'s skill. Reinstall: lore skill install --tool {tool}",
		Command: argv(cmdLore, "skill", "install", "--tool", "{tool}"), Runnable: true},
	{check: CheckToolsSkillInstalled, variant: FixLinked}: linkedSkillFix,
	{check: CheckToolsSkillVersion, variant: FixLinked}:   linkedSkillFix,
	{check: CheckToolsSkillModified, variant: FixLinked}:  linkedSkillFix,
	{check: CheckToolsSkillModified}: {Text: "The {tool} skill was edited by hand. Reinstalling overwrites the edit (lore keeps a SKILL.md.bak copy): lore skill install --tool {tool}",
		Command: argv(cmdLore, "skill", "install", "--tool", "{tool}"), Runnable: true},
	// Runs in the repo: the FixRunner uses the result's repo as cwd.
	// Not a plain `lore render`: it wires only the repo's context.targets,
	// so it cannot write the file of a tool missing from them (the owner's
	// "Run it, still failing" on CLAUDE.md). --add-target saves the tool in
	// context.targets and writes its pointer file in one run.
	{check: CheckToolsPointerFile}: {Text: "This adds {tool} to the repo's context.targets and writes its pointer file (importing .lore/LORE.md) in {repo_root}",
		Command: argv(cmdLore, "render", "--add-target", "{tool}"), Runnable: true},
	// The pointer file is a link out of the repo: render refuses to write
	// through it, so the fix names the two ways out and runs neither.
	{check: CheckToolsPointerFile, variant: FixLinked}: {Text: "{tool}'s pointer file in {repo_root} is a symbolic link lore does not write through (the check says why). Pick one: point the link at an existing regular file of the same name (lore then writes its pointer block through the link), or remove the link and run lore render --add-target {tool} (lore writes a plain file)"},
	{check: CheckToolsHooks}: {Text: "Install the session hooks: lore setup --agent-hooks --yes",
		Command: argv(cmdLore, "setup", "--agent-hooks", "--yes"), Runnable: true},

	{check: CheckSecretsBackendPresent}: {Text: "{backend} is not on PATH; references to {backend}:// will not resolve on this machine"},

	{check: CheckFoldersExist}:   {Text: "{folder} is not a folder on this machine; create it, or change the glob with lore setup edit --name {setup} --folder <glob>"},
	{check: CheckFoldersOverlap}: {Text: "{folder} matches setups {setup} and {other} equally; make one folder glob more specific (lore setup edit) or run with --setup"},
	{check: CheckFoldersShadow}:  {Text: "{other_folder} stays with {other}"},
	// Shown, not run: --default takes default: true from the other setup.
	{check: CheckFoldersReachable}: {Text: "Add a folder on the Folders step (lore picks {setup} inside it), or make {setup} the default for folders no setup matches: lore setup edit --name {setup} --default",
		Command: argv(cmdLore, "setup", "edit", "--name", "{setup}", "--default")},

	// Runs in the repo: the FixRunner uses the result's repo as cwd.
	// The text never repeats the command: the page and the CLI show the
	// command next to it.
	{check: CheckRepoInit}: {Text: "lore is not set up in this repo yet (no .lore/ folder); this sets it up",
		Command: argv(cmdLore, "init"), Runnable: true},
	{check: CheckRepoInit, variant: FixInitTargets}: {Text: "lore is not set up in this repo yet (no .lore/ folder); this sets it up and writes the files {targets} read",
		Command: argv(cmdLore, "init", "--target", "{targets}"), Runnable: true},
	{check: CheckRepoOrg}:    {Text: "this repo is org {repo_org} (from {source}) but setup {setup} is org {org}; run with --setup <a setup of {repo_org}> or fix {source}"},
	{check: CheckRepoStores}: {Text: "Store {store} in .lore/stores.yaml is not readable with this setup's key; ask its owner for access, or check the key (lore doctor --setup {setup})"},
}

// FixRow is one row of the fix catalog: the check, the step and variant
// it is specific to ("" = any), and its fix template.
type FixRow struct {
	Check   CheckID
	Step    StepID
	Variant FixVariant
	Fix     Fix
}

// FixRows is every row of the fix catalog, sorted by check, step and
// variant, each Command a fresh copy. It exists so a test can hold every
// runnable fix to its promise (running it makes its check pass), and a
// new runnable row cannot be added without a case or a stated reason.
func FixRows() []FixRow {
	out := make([]FixRow, 0, len(fixes))
	for k, f := range fixes {
		f.Command = slices.Clone(f.Command)
		out = append(out, FixRow{Check: k.check, Step: k.step, Variant: k.variant, Fix: f})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		if a.Step != b.Step {
			return a.Step < b.Step
		}
		return a.Variant < b.Variant
	})
	return out
}

// Choice ids of a linked skill folder's fix (setup.fix --choice).
const (
	ChoiceLinkReplace = "replace"
	ChoiceLinkUpdate  = "update"
)

// linkedSkillFix is the tools.skill_* fix of a skill folder that is a
// symbolic link lore does not own: a plain install refuses
// (E_SKILL_LINKED), so the person picks one of two commands. One line,
// the two commands as choices, the explanation behind Details.
var linkedSkillFix = Fix{
	Text:    "{tool}'s skill folder is a link lore does not own; pick one",
	Command: []string{},
	Why: "A plain lore skill install refuses to write through a symbolic link. Replace the link: lore puts its own copy of the skill where the link was, and the folder the link pointed to is left as it is. " +
		"Update the target: lore writes lore {bin_v}'s skill into the folder the link points to, so every tool sharing that folder gets it.",
	Choices: []FixChoice{
		{ID: ChoiceLinkReplace, Label: "Replace the link with lore's own copy", Command: argv(cmdLore, "skill", "install", "--tool", "{tool}", "--link", "replace")},
		{ID: ChoiceLinkUpdate, Label: "Write lore {bin_v}'s skill into the link's target", Command: argv(cmdLore, "skill", "install", "--tool", "{tool}", "--link", "update")},
	},
}

// FixFor is the fix template of check id in step for variant v: the most
// specific row of (step, variant), (step), (variant), default. Command is
// a fresh copy, never nil.
func FixFor(id CheckID, step StepID, v FixVariant) Fix {
	for _, k := range []fixKey{{id, step, v}, {id, step, FixDefault}, {id, "", v}, {id, "", FixDefault}} {
		if k.step != "" && step == "" {
			continue
		}
		if f, ok := fixes[k]; ok {
			f.Command = slices.Clone(f.Command)
			if f.Command == nil {
				f.Command = []string{}
			}
			if f.Choices != nil {
				cs := make([]FixChoice, len(f.Choices))
				for i, c := range f.Choices {
					cs[i] = FixChoice{ID: c.ID, Label: c.Label, Command: slices.Clone(c.Command)}
				}
				f.Choices = cs
			}
			return f
		}
	}
	return Fix{Command: []string{}}
}
