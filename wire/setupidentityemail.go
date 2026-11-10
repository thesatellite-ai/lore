package wire

// setupidentityemail.go — github.identity_email (realmac N3): the setup's
// identity email is one of its GitHub account's emails. The emails are read
// with THAT account's own gh token from /user/emails (the same call
// setup.discover makes); a token without the user:email scope, or an API
// that does not answer, gives "could not read", never a false warning.
// The decision is onboard.IdentityEmailMatch.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/ghapi"
	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// Reasons the account's emails could not be read (fixed text: never gh's
// or the API's own, so no failure path can echo the token).
const (
	whyNoGHLogin   = "gh has no login for it"
	whyNoEmailRead = "its gh token lacks the user:email scope: gh auth refresh -h github.com -s user:email"
	whyAPIFailed   = "the GitHub API did not answer"
)

// identityEmail is github.identity_email.
func (sp setupProber) identityEmail(ctx context.Context, r opssetup.ProbeRequest) opssetup.ProbeResult {
	user := pick(r.Answers, onboard.QIDGitHubAccount, r.Setup.GitHub.User)
	// identityEmails: a member/admin setup's identity is its org entry's.
	emails := sp.identityEmails(r)
	in := onboard.IdentityEmailIn{Emails: emails, User: user}
	if len(emails) > 0 && user != "" {
		in.Readable, in.AccountEmails, in.Why = accountEmails(ctx, sp.p, ghAPIClient(), user)
		if !in.Readable {
			in.Gitconfig = gitconfigEmails(ctx, sp.p, user)
		}
	}
	st, detail := onboard.IdentityEmailMatch(in)
	v := map[string]string{onboard.VarUser: user, onboard.VarSetup: r.Setup.Name}
	if len(emails) > 0 {
		v[onboard.VarEmail] = emails[0]
	}
	return result(st, detail, v)
}

// accountEmails reads user's GitHub emails with user's own gh token:
// readable=false with a fixed reason when they cannot be listed.
func accountEmails(ctx context.Context, p *probe.Prober, api ghapi.Client, user string) (bool, []string, string) {
	tok, o := p.GHToken(ctx, user)
	if o.Class != probe.ClassOK {
		return false, nil, whyNoGHLogin
	}
	cctx, cancel := context.WithTimeout(ctx, discoverTimeout)
	defer cancel()
	u, err := api.User(cctx, tok)
	if err != nil {
		return false, nil, whyAPIFailed
	}
	if !u.CanReadEmails() {
		return false, nil, whyNoEmailRead
	}
	es, err := api.Emails(cctx, tok)
	switch {
	case err == nil:
	case ghapi.StatusOf(err) == 403 || ghapi.StatusOf(err) == 404:
		return false, nil, whyNoEmailRead
	default:
		return false, nil, whyAPIFailed
	}
	out := make([]string, 0, len(es))
	for _, e := range es {
		if s := strings.TrimSpace(e.Email); s != "" {
			out = append(out, s)
		}
	}
	return true, out, ""
}

// gitconfigEmails are the includeIf emails the gitconfig ties to user (the
// same link the account card shows; local reads only).
func gitconfigEmails(ctx context.Context, p *probe.Prober, user string) []string {
	var logins []string
	for _, a := range p.GHLogins().Accounts {
		logins = append(logins, a.User)
	}
	var incs []opssetup.GitIncludeFact
	for _, in := range p.IncludeIdentities(ctx) {
		incs = append(incs, opssetup.GitIncludeFact{Condition: in.Condition, Folder: in.Folder, Path: in.Path, Name: in.Name, Email: in.Email})
	}
	return opssetup.GitconfigEmailsOf(user, logins, incs)
}
