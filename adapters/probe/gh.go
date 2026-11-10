package probe

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// gh program and api paths.
const (
	ghBin = "gh"
	// ghRepoJQ prints .private then .permissions.push, one per line (Q6:
	// permissions.push is a hint; the dry-run push is the truth).
	ghRepoJQ = ".private, .permissions.push"
	// jsonTrue / jsonFalse are jq's boolean output.
	jsonTrue  = "true"
	jsonFalse = "false"
)

// ghEnv keeps gh quiet and non-interactive.
var ghEnv = []string{envGHPrompt + "=1", envGHNoUpdate + "=1", envNoColor + "=1", envLCAll + "=C"}

// GHAccount is one gh login for github.com.
type GHAccount struct {
	User   string `json:"user"`
	Active bool   `json:"active"`
	// OK is false when gh lists the login but says it failed (expired or
	// revoked token).
	OK bool `json:"ok"`
}

// GHAuthResult is the github.gh_account / github.gh_matches probe (ONB 4.3).
type GHAuthResult struct {
	Outcome
	Accounts []GHAccount `json:"accounts,omitempty"`
	// Active is the active account's user, "" when none.
	Active string `json:"active,omitempty"`
}

// gh auth status lines (gh 2.40+ and the older one-account form). The token
// lines ("- Token: gho_****") are never matched or kept.
var (
	reGHAccount    = regexp.MustCompile(`Logged in to \S+ (?:account|as) ([A-Za-z0-9][A-Za-z0-9-]*)`)
	reGHFailed     = regexp.MustCompile(`Failed to log in to \S+ (?:account|as) ([A-Za-z0-9][A-Za-z0-9-]*)`)
	reGHActiveLine = regexp.MustCompile(`(?i)active account:\s*(true|false)`)
)

// ghNotLoggedInWords mark gh's "no login" answers.
var ghNotLoggedInWords = []string{"not logged into", "gh auth login", "no oauth token", "bad credentials", "http 401"}

// GHAuthStatus runs gh auth status --hostname github.com (never
// --show-token). Classes: ok (Accounts set), not_logged_in, timeout,
// tool_missing, failed.
func (p *Prober) GHAuthStatus(ctx context.Context) GHAuthResult {
	start := time.Now()
	cctx, done, ok := p.slot(ctx, p.o.GHTimeout)
	defer done()
	if !ok {
		return GHAuthResult{Outcome: timedOut(start)}
	}
	r := p.run(cctx, cmdSpec{bin: ghBin, args: []string{"auth", "status", "--hostname", GitHubHost}, env: ghEnv})
	if r.timedOut || r.missing || r.startErr {
		o := failure(r, ghBin, classifyGH)
		o.Took = time.Since(start)
		return GHAuthResult{Outcome: o}
	}
	out := GHAuthResult{Accounts: parseGHAuth(r.stdout + "\n" + r.stderr)}
	for _, a := range out.Accounts {
		if a.Active {
			out.Active = a.User
		}
	}
	switch {
	case len(out.Accounts) > 0:
		out.Outcome = Outcome{Class: ClassOK, Took: time.Since(start)}
	case r.exit == 0:
		out.Outcome = Outcome{Class: ClassFailed, Detail: "gh auth status named no account", Took: time.Since(start)}
	default:
		out.Outcome = Outcome{Class: ClassNotLoggedIn, Detail: "gh has no login for " + GitHubHost, Took: time.Since(start)}
	}
	return out
}

// parseGHAuth reads gh auth status text. An "Active account" line applies
// to the account above it; the old one-account form has none, so a single
// account is the active one.
func parseGHAuth(text string) []GHAccount {
	var accts []GHAccount
	sawActive := false
	for l := range strings.SplitSeq(text, "\n") {
		switch {
		case reGHFailed.MatchString(l):
			accts = append(accts, GHAccount{User: reGHFailed.FindStringSubmatch(l)[1]})
		case reGHAccount.MatchString(l):
			accts = append(accts, GHAccount{User: reGHAccount.FindStringSubmatch(l)[1], OK: true})
		case reGHActiveLine.MatchString(l) && len(accts) > 0:
			sawActive = true
			accts[len(accts)-1].Active = strings.EqualFold(reGHActiveLine.FindStringSubmatch(l)[1], jsonTrue)
		}
	}
	if !sawActive && len(accts) == 1 {
		accts[0].Active = true
	}
	return accts
}

// classifyGH maps gh api / gh auth stderr to a class.
func classifyGH(stderr string) Class {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "http 404"):
		return ClassNoAccess
	case containsAny(low, ghNotLoggedInWords):
		return ClassNotLoggedIn
	case strings.Contains(low, "http 403"):
		return ClassAuthDenied
	case containsAny(low, unreachableWords), strings.Contains(low, "error connecting to"):
		return ClassUnreachable
	}
	return ClassFailed
}

// reGHName is a GitHub owner, repo or user name.
var reGHName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// Tri is a yes / no / unknown answer (gh omits permissions for a token
// without access, for example).
type Tri string

// Tri values.
const (
	TriYes     Tri = "yes"
	TriNo      Tri = "no"
	TriUnknown Tri = "unknown"
)

func triOf(s string) Tri {
	switch strings.TrimSpace(s) {
	case jsonTrue:
		return TriYes
	case jsonFalse:
		return TriNo
	}
	return TriUnknown
}

// GHRepoResult is the store.private probe plus the Q6 push hint.
type GHRepoResult struct {
	Outcome
	Private Tri `json:"private"`
	// PushHint is gh's permissions.push for the active gh account: a hint
	// only (Q6), since gh's account may differ from the ssh key's.
	PushHint Tri `json:"push_hint"`
}

// GHRepo runs gh api repos/{owner}/{repo} --jq '.private, .permissions.push'.
// Classes: ok, no_access (404), not_logged_in, auth_denied (403),
// unreachable, timeout, tool_missing, bad_input, failed.
func (p *Prober) GHRepo(ctx context.Context, owner, repo string) GHRepoResult {
	return p.GHRepoAs(ctx, owner, repo, "")
}

// envGHToken is the variable gh reads a token from; set only in a child's
// hidden env, never shown.
const envGHToken = "GH_TOKEN"

// asAccount is the hidden env that makes a gh child act as login account:
// GH_TOKEN with its token (nil for "" or when gh has no login for it, so
// gh's active account answers). Call it before taking a probe slot:
// reading the token takes its own.
func (p *Prober) asAccount(ctx context.Context, account string) []string {
	if account == "" {
		return nil
	}
	tok, o := p.ghTokenRaw(ctx, account)
	if o.Class != ClassOK || tok == "" {
		return nil
	}
	return []string{envGHToken + "=" + tok}
}

// GHRepoAs is GHRepo asked as gh login account ("" = gh's active account),
// so a setup's private repo is read by the account that owns it, not by
// whichever login gh has active (realmac item 9: the active account gets
// 404 for another account's private repo). The token goes to the child's
// hidden env only. When gh has no login for account, it asks as the
// active account, as GHRepo does.
func (p *Prober) GHRepoAs(ctx context.Context, owner, repo, account string) GHRepoResult {
	start := time.Now()
	if !reGHName.MatchString(owner) || !reGHName.MatchString(repo) {
		return GHRepoResult{Outcome: Outcome{Class: ClassBadInput, Detail: "owner and repo must be GitHub names"}, Private: TriUnknown, PushHint: TriUnknown}
	}
	// Before taking this probe's slot: asAccount takes its own.
	spec := cmdSpec{bin: ghBin, args: []string{"api", "repos/" + owner + "/" + repo, "--jq", ghRepoJQ}, env: ghEnv, hiddenEnv: p.asAccount(ctx, account)}
	cctx, done, ok := p.slot(ctx, p.o.GHTimeout)
	defer done()
	if !ok {
		return GHRepoResult{Outcome: timedOut(start), Private: TriUnknown, PushHint: TriUnknown}
	}
	r := p.run(cctx, spec)
	if !r.ok() {
		o := failure(r, ghBin, classifyGH)
		o.Took = time.Since(start)
		return GHRepoResult{Outcome: o, Private: TriUnknown, PushHint: TriUnknown}
	}
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	out := GHRepoResult{Outcome: Outcome{Class: ClassOK, Took: time.Since(start)}, Private: TriUnknown, PushHint: TriUnknown}
	if len(lines) > 0 {
		out.Private = triOf(lines[0])
	}
	if len(lines) > 1 {
		out.PushHint = triOf(lines[1])
	}
	return out
}

// GHCollaboratorResult is the invite.github_access probe (ONB 6.1).
type GHCollaboratorResult struct {
	Outcome
	// Collaborator: GitHub answered 204. A 404 is "not a collaborator, or
	// the repo is hidden from the gh account" (class ok, false, Detail says so).
	Collaborator bool `json:"collaborator"`
}

// GHCollaborator runs gh api repos/{owner}/{repo}/collaborators/{user}
// (204 yes, 404 no). Classes: ok, not_logged_in, auth_denied, unreachable,
// timeout, tool_missing, bad_input, failed.
func (p *Prober) GHCollaborator(ctx context.Context, owner, repo, user string) GHCollaboratorResult {
	return p.GHCollaboratorAs(ctx, owner, repo, user, "")
}

// GHCollaboratorAs is GHCollaborator asked as gh login account ("" = gh's
// active account; see GHRepoAs).
func (p *Prober) GHCollaboratorAs(ctx context.Context, owner, repo, user, account string) GHCollaboratorResult {
	start := time.Now()
	if !reGHName.MatchString(owner) || !reGHName.MatchString(repo) || !reGHName.MatchString(user) {
		return GHCollaboratorResult{Outcome: Outcome{Class: ClassBadInput, Detail: "owner, repo and user must be GitHub names"}}
	}
	spec := cmdSpec{bin: ghBin, args: []string{"api", "--silent", "repos/" + owner + "/" + repo + "/collaborators/" + user}, env: ghEnv, hiddenEnv: p.asAccount(ctx, account)}
	cctx, done, ok := p.slot(ctx, p.o.GHTimeout)
	defer done()
	if !ok {
		return GHCollaboratorResult{Outcome: timedOut(start)}
	}
	r := p.run(cctx, spec)
	if r.ok() {
		return GHCollaboratorResult{Outcome: Outcome{Class: ClassOK, Took: time.Since(start)}, Collaborator: true}
	}
	o := failure(r, ghBin, classifyGH)
	o.Took = time.Since(start)
	if o.Class == ClassNoAccess {
		o.Class = ClassOK
		o.Detail = user + " is not a collaborator on " + owner + "/" + repo + ", or the repo is hidden from the gh account"
	}
	return GHCollaboratorResult{Outcome: o}
}
