package probe

import (
	"strings"
	"testing"
)

const ghStatusMulti = `github.com
  ✓ Logged in to github.com account mira (keyring)
  - Active account: false
  - Git operations protocol: ssh
  - Token: gho_************************************
  - Token scopes: 'gist', 'read:org', 'repo'

  ✓ Logged in to github.com account mira-acme (keyring)
  - Active account: true
  - Git operations protocol: ssh
  - Token: gho_************************************

  X Failed to log in to github.com account tomas (keyring)
  - Active account: false
  - The token in keyring is invalid.
`

const ghStatusOld = `github.com
  ✓ Logged in to github.com as mira (oauth_token)
  ✓ Git operations for github.com configured to use ssh protocol.
  ✓ Token: *******************
`

func TestGHAuthStatus(t *testing.T) {
	s := newSandbox(t)
	fakeBin(t, s.bin, "gh", `for a in "$@"; do [ "$a" = "--show-token" ] && { echo "gho_SHOULD_NEVER_BE_ASKED"; exit 0; }; done
case "$FAKE_GH" in
  multi) printf '%s' "$GH_OUT";;
  old) printf '%s' "$GH_OUT" >&2;;
  none) echo "You are not logged into any GitHub hosts. To log in, run: gh auth login" >&2; exit 1;;
esac`)
	p := s.prober(Options{})

	t.Setenv("FAKE_GH", "multi")
	t.Setenv("GH_OUT", ghStatusMulti)
	r := p.GHAuthStatus(ctxT(t))
	if r.Class != ClassOK || r.Active != "mira-acme" || len(r.Accounts) != 3 {
		t.Fatalf("multi: %+v", r)
	}
	if a := r.Accounts[2]; a.User != "tomas" || a.OK || a.Active {
		t.Errorf("failed account: %+v", a)
	}
	if a := r.Accounts[0]; a.User != "mira" || !a.OK || a.Active {
		t.Errorf("first account: %+v", a)
	}

	t.Setenv("FAKE_GH", "old")
	t.Setenv("GH_OUT", ghStatusOld)
	if r := p.GHAuthStatus(ctxT(t)); r.Class != ClassOK || r.Active != "mira" || len(r.Accounts) != 1 {
		t.Errorf("old: %+v", r)
	}

	t.Setenv("FAKE_GH", "none")
	if r := p.GHAuthStatus(ctxT(t)); r.Class != ClassNotLoggedIn || len(r.Accounts) != 0 {
		t.Errorf("none: %+v", r)
	}

	t.Setenv("PATH", t.TempDir())
	if r := p.GHAuthStatus(ctxT(t)); r.Class != ClassToolMissing {
		t.Errorf("no gh: %+v", r)
	}
}

func TestGHRepoAndCollaborator(t *testing.T) {
	s := newSandbox(t)
	fakeBin(t, s.bin, "gh", `case "$*" in
  *repos/acme-hub/lore-team/collaborators/mira*) exit 0;;
  *collaborators/*) echo "gh: Not Found (HTTP 404)" >&2; exit 1;;
  *repos/acme-hub/lore-team\ --jq*) printf 'true\nfalse\n';;
  *repos/mira/lore-me\ --jq*) printf 'false\ntrue\n';;
  *repos/acme-hub/partial\ --jq*) printf 'true\nnull\n';;
  *repos/acme-hub/hidden*) echo "gh: Not Found (HTTP 404)" >&2; exit 1;;
  *) echo "To get started with GitHub CLI, please run:  gh auth login" >&2; exit 4;;
esac`)
	p := s.prober(Options{})
	cases := []struct {
		owner, repo   string
		class         Class
		private, push Tri
	}{
		{"acme-hub", "lore-team", ClassOK, TriYes, TriNo},
		{"mira", "lore-me", ClassOK, TriNo, TriYes},
		{"acme-hub", "partial", ClassOK, TriYes, TriUnknown},
		{"acme-hub", "hidden", ClassNoAccess, TriUnknown, TriUnknown},
		{"agency", "x", ClassNotLoggedIn, TriUnknown, TriUnknown},
		{"-bad", "x", ClassBadInput, TriUnknown, TriUnknown},
	}
	for _, c := range cases {
		r := p.GHRepo(ctxT(t), c.owner, c.repo)
		if r.Class != c.class || r.Private != c.private || r.PushHint != c.push {
			t.Errorf("%s/%s: %+v", c.owner, c.repo, r)
		}
	}
	if r := p.GHCollaborator(ctxT(t), "acme-hub", "lore-team", "mira"); r.Class != ClassOK || !r.Collaborator {
		t.Errorf("collaborator: %+v", r)
	}
	if r := p.GHCollaborator(ctxT(t), "acme-hub", "lore-team", "tomas"); r.Class != ClassOK || r.Collaborator || !strings.Contains(r.Detail, "not a collaborator") {
		t.Errorf("not collaborator: %+v", r)
	}
	if r := p.GHCollaborator(ctxT(t), "acme-hub", "lore-team", "a/b"); r.Class != ClassBadInput {
		t.Errorf("bad user: %+v", r)
	}
}

// TestGHAsAccount: GHRepoAs and GHCollaboratorAs ask as the named gh login
// (its token in the child's GH_TOKEN), never as gh's active account, and
// the token stays out of the trace. The fake gh answers a private repo of
// mira only with mira's token, as GitHub does (realmac item 9).
func TestGHAsAccount(t *testing.T) {
	s := newSandbox(t)
	mira := "fake_tok_" + strings.Repeat("m", 12) // built at run time, not key-shaped
	fakeBin(t, s.bin, "gh", `case "$*" in
  "auth token --hostname github.com --user mira") echo "`+mira+`";;
  "auth token"*) echo "no oauth token found for github.com account" >&2; exit 1;;
  *repos/mira/lore-me*) [ "$GH_TOKEN" = "`+mira+`" ] || { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
    case "$*" in *collaborators*) exit 0;; *) printf 'true\ntrue\n';; esac;;
  *) echo "gh: Not Found (HTTP 404)" >&2; exit 1;;
esac`)
	t.Setenv("GH_TOKEN", "")
	p := s.prober(Options{})
	cases := []struct {
		account string
		class   Class
		private Tri
		collab  bool
	}{
		{"mira", ClassOK, TriYes, true},             // the owner's login sees its private repo
		{"", ClassNoAccess, TriUnknown, false},      // gh's active account (no token) does not
		{"tomas", ClassNoAccess, TriUnknown, false}, // no gh login for the account: asks as the active one
		{"-x", ClassNoAccess, TriUnknown, false},    // not a login name: never passed to gh auth token
	}
	for _, c := range cases {
		ctx, tr := WithTrace(ctxT(t))
		r := p.GHRepoAs(ctx, "mira", "lore-me", c.account)
		if r.Class != c.class || r.Private != c.private {
			t.Errorf("GHRepoAs as %q: %+v, want %s private=%s", c.account, r, c.class, c.private)
		}
		cr := p.GHCollaboratorAs(ctx, "mira", "lore-me", "ana", c.account)
		if cr.Collaborator != c.collab {
			t.Errorf("GHCollaboratorAs as %q: %+v, want collaborator=%v", c.account, cr, c.collab)
		}
		for _, call := range tr.Calls() {
			if strings.Contains(call.Command+call.Output, mira) {
				t.Errorf("as %q: trace call %+v shows the token", c.account, call)
			}
		}
	}
}
