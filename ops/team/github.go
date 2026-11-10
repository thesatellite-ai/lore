package team

import (
	"context"
	"net/url"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// GitHub access of an invite (ONB 4.6): check invite.github_access (warn)
// asks gh whether the invited user is a collaborator of the org store's
// repo; --grant adds them with push, only after an explicit confirm
// (outward). lore never reads a token: gh holds the login.

// GitHubRunner runs `gh <args>` as account (the setup's gh_account, ""
// for gh's active one). A non-zero exit is a GHResult, not an error; err
// is for gh missing or not startable. Output is the combined tail and
// never holds a secret (gh api prints the response body).
type GitHubRunner interface {
	RunGH(ctx context.Context, account string, args []string) (GHResult, error)
}

// GHResult is one gh run.
type GHResult struct {
	Exit   int
	Output string
}

// gh api arguments (4.6).
const (
	ghAPI         = "api"
	ghMethodFlag  = "-X"
	ghMethodPut   = "PUT"
	ghFieldFlag   = "-f"
	ghPermPush    = "permission=push"
	ghCommand     = "gh"
	ghNotFoundTag = "HTTP 404"
	// ghLoginTag is in gh's output when it has no login ("To get started
	// with GitHub CLI, please run: gh auth login").
	ghLoginTag = "gh auth login"
	// githubHost is the only host whose collaborators gh api can manage.
	githubHost = "github.com"
	gitSuffix  = ".git"
)

// Repo is an owner/name pair on github.com.
type Repo struct {
	Owner string
	Name  string
}

// collaboratorPath is the REST path of user on r.
func (r Repo) collaboratorPath(user string) string {
	return "repos/" + r.Owner + "/" + r.Name + "/collaborators/" + user
}

// GitHubRepo reads owner/name from a store url: git@github.com:o/r(.git),
// ssh://git@github.com/o/r, https://github.com/o/r, or a host alias of
// the form git@github.com-<alias>:o/r (ONB 4.3 host aliases). ok is false
// for any other host or shape (a file:// store has no GitHub access).
func GitHubRepo(storeURL string) (Repo, bool) {
	s := strings.TrimSpace(storeURL)
	var host, path string
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return Repo{}, false
		}
		host, path = u.Hostname(), u.Path
	case strings.Contains(s, ":"):
		hp, p, _ := strings.Cut(s, ":")
		if at := strings.LastIndex(hp, "@"); at >= 0 {
			hp = hp[at+1:]
		}
		host, path = hp, p
	default:
		return Repo{}, false
	}
	if host != githubHost && !strings.HasPrefix(host, githubHost+"-") {
		return Repo{}, false
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(path, gitSuffix), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Repo{}, false
	}
	return Repo{Owner: parts[0], Name: parts[1]}, true
}

// GrantArgv is the gh command that gives user push on r (4.6), printed as
// the fix when --grant is not given.
func GrantArgv(r Repo, user string) []string {
	return []string{ghCommand, ghAPI, ghMethodFlag, ghMethodPut, r.collaboratorPath(user), ghFieldFlag, ghPermPush}
}

// probeArgv is the check invite.github_access: 204 = collaborator, 404 =
// no access yet.
func probeArgv(r Repo, user string) []string {
	return []string{ghAPI, r.collaboratorPath(user)}
}

// accessState is the outcome of the access check.
type accessState int

const (
	accessUnknown accessState = iota
	accessYes
	accessNo
)

// githubAccess asks gh whether user is a collaborator of r. A failed or
// missing gh is accessUnknown (the check is a warning, never a refusal).
func githubAccess(ctx context.Context, g GitHubRunner, account string, r Repo, user string) (accessState, string) {
	res, err := g.RunGH(ctx, account, probeArgv(r, user))
	switch {
	case err != nil:
		return accessUnknown, err.Error()
	case res.Exit == 0:
		return accessYes, ""
	case strings.Contains(res.Output, ghNotFoundTag):
		return accessNo, ""
	}
	return accessUnknown, strings.TrimSpace(res.Output)
}

// grantAccess runs the GrantArgv command (the caller has the confirm).
func grantAccess(ctx context.Context, g GitHubRunner, account string, r Repo, user string) error {
	argv := GrantArgv(r, user)
	res, err := g.RunGH(ctx, account, argv[1:])
	if err != nil {
		return errs.Wrap(errs.Unsupported, "gh could not run to grant "+user+" access to "+r.Owner+"/"+r.Name, err).
			WithHint(strings.Join(argv, " "))
	}
	if res.Exit != 0 {
		return errs.New(errs.Refused, "gh refused to grant "+user+" push on "+r.Owner+"/"+r.Name+": "+lastLine(res.Output)).
			WithHint("check that gh is logged in as an admin of " + r.Owner + " (gh auth status), then: " + strings.Join(argv, " "))
	}
	return nil
}

// lastLine is the last non-empty line of s.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// ghNotLoggedIn is the detail of an access check gh could not run for
// want of a login.
const ghNotLoggedIn = "gh is not logged in (run gh auth login), so access was not checked"

// ghUnknownDetail is the detail of an access check gh could not answer:
// the not-logged-in case in words, else gh's last line.
func ghUnknownDetail(output string) string {
	if strings.Contains(output, ghLoginTag) {
		return ghNotLoggedIn
	}
	if l := lastLine(output); l != "" {
		return "gh could not check access: " + l
	}
	return "gh could not check access"
}
