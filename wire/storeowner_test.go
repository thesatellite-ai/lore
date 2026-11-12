package wire

// storeowner_test.go — owner item 39: a store clone's git identity, lore's
// commit identity and doctor's expectation follow the setup that OWNS the
// store, never the setup of the folder lore runs in. Run from a work
// folder (lore whoami = the work identity W), the personal store still
// gets the personal identity P; the org store gets W.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
)

const (
	ownerPersonalURL  = "git@github.com:pat/lore-me.git"
	ownerFreelanceURL = "git@github.com:pat-free/notes.git"
	ownerOrgURL       = "git@github.com:acme/hub.git"
	ownerOtherURL     = "git@github.com:someone/else.git"
)

// ownerConfig: a work setup (member of acme, identity W in orgs[acme])
// that inherits personal.store like the personal setup (solo, identity P,
// with an earlier email), and a freelance setup with its own personal
// store (identity F).
func ownerConfig(workDir string) string {
	return `personal:
  store: ` + ownerPersonalURL + `
orgs:
  - name: acme
    identity: {key: "human:pat@acme.test", name: Pat Work}
    stores:
      - url: ` + ownerOrgURL + `
setups:
  - name: work
    kind: member
    org: acme
    folders: ["` + workDir + `/**"]
  - name: personal
    kind: solo
    default: true
    identity: {key: "human:pat@home.test", name: Pat Home, emails: [pat@old-home.test]}
  - name: freelance
    kind: solo
    personal_store: ` + ownerFreelanceURL + `
    identity: {key: "human:pat@free.test", name: Pat Free}
`
}

// ownerSigner is a storeSigner running in a folder of the work setup.
func ownerSigner(t *testing.T) storeSigner {
	t.Helper()
	t.Setenv("LORE_ACTOR", "")
	t.Setenv("LORE_HUMAN", "")
	t.Setenv("LORE_SETUP", "")
	t.Setenv("LORE_ORG", "")
	t.Setenv("LORE_NO_DETECT", "1")
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION"} {
		t.Setenv(k, "")
	}
	home := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	repo := filepath.Join(work, "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "init", "-q")
	writeConfig(t, home, ownerConfig(work))
	acme := &identity.Org{Name: "acme", Identity: identity.Entry{Key: "human:pat@acme.test", Name: "Pat Work"}}
	return storeSigner{opts: Options{Home: home, RepoDir: repo,
		Org: func(context.Context) (*identity.Org, error) { return acme, nil },
		OrgSigners: func(context.Context) ([]OrgSigner, error) {
			return []OrgSigner{{StoreURLs: []string{ownerOrgURL}, Name: "Pat Work", Email: "pat@acme.test"}}, nil
		}}}
}

func TestStoreIdentityFollowsOwningSetup(t *testing.T) {
	s := ownerSigner(t)
	ctx := context.Background()

	// the case under test: the folder is the work setup's, lore whoami is W
	res, err := ResolveSetup(ctx, setupQueryFor(ctx, s.opts.Home, s.opts.RepoDir, nil))
	if err != nil || res.Active.Name != "work" {
		t.Fatalf("active setup = %q, %v; want work", res.Active.Name, err)
	}
	if sig := s.fallback(ctx); sig.Email != "pat@acme.test" {
		t.Fatalf("lore whoami here = %+v, want the work identity", sig)
	}

	cases := []struct {
		name, url, wantName, wantEmail string
	}{
		{"personal store (personal.store, inherited by the solo setup)", ownerPersonalURL, "Pat Home", "pat@home.test"},
		{"personal store, another spelling", "https://github.com/Pat/lore-me", "Pat Home", "pat@home.test"},
		{"a setup's own personal_store", ownerFreelanceURL, "Pat Free", "pat@free.test"},
		{"org store", ownerOrgURL, "Pat Work", "pat@acme.test"},
		{"a store no setup owns: the lore identity, as before", ownerOtherURL, "Pat Work", "pat@acme.test"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := s.cloneIdentity(ctx, c.url)
			if id.Name != c.wantName || id.Email != c.wantEmail {
				t.Fatalf("clone identity = %s <%s> (%s), want %s <%s>", id.Name, id.Email, id.Why, c.wantName, c.wantEmail)
			}
			if sig := s.identityFor(ctx, c.url); sig.Name != c.wantName || sig.Email != c.wantEmail {
				t.Fatalf("commit / hand-edit identity = %+v, want %s <%s>", sig, c.wantName, c.wantEmail)
			}
		})
	}
	if id := s.cloneIdentity(ctx, ownerPersonalURL); !strings.Contains(id.Why, "setup personal") {
		t.Fatalf("why = %q, want it to name the owning setup", id.Why)
	}
}

func TestStoreCommitEnvFollowsOwningSetup(t *testing.T) {
	s := ownerSigner(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "nogit"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(k, "")
	}
	ctx := context.Background()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "remote", "add", "origin", ownerPersonalURL)
	env, err := s.envFor(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "GIT_AUTHOR_EMAIL=pat@home.test") || !slices.Contains(env, "GIT_COMMITTER_EMAIL=pat@home.test") {
		t.Fatalf("commit env in the personal store = %q, want the personal identity", env)
	}
}

func TestDoctorStoreIdentityExpectsOwningSetup(t *testing.T) {
	s := ownerSigner(t)
	ctx := context.Background()
	who, err := resolveIdentity(ctx, s.opts)
	if err != nil {
		t.Fatal(err)
	}
	mine := who.PersonEmails() // the work folder's identity: W
	cases := []struct {
		name, url, got string
		ok             bool
	}{
		{"personal store committing as P", ownerPersonalURL, "pat@home.test", true},
		{"personal store, P's other email set by hand", ownerPersonalURL, "pat@old-home.test", true},
		{"personal store committing as W (the bug)", ownerPersonalURL, "pat@acme.test", false},
		{"org store committing as W", ownerOrgURL, "pat@acme.test", true},
		{"org store committing as P", ownerOrgURL, "pat@home.test", false},
		{"freelance store committing as P", ownerFreelanceURL, "pat@home.test", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, accept, org := s.expectedClone(ctx, c.url, mine)
			wantEmail := map[string]string{ownerPersonalURL: "pat@home.test", ownerOrgURL: "pat@acme.test", ownerFreelanceURL: "pat@free.test"}[c.url]
			if want.Email != wantEmail {
				t.Fatalf("doctor expects %s, want %s", want.Email, wantEmail)
			}
			if got := cloneIdentityAccepted(c.got, want.Email, accept, org); got != c.ok {
				t.Fatalf("clone committing as %s accepted = %v, want %v", c.got, got, c.ok)
			}
		})
	}
}

func TestPersonalStoreIdentityOwners(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("LORE_SETUP", "")
	// no setup inherits personal.store with an identity: the top-level identity owns it
	writeConfig(t, home, "identity: {key: \"human:top@home.test\", name: Top}\npersonal:\n  store: "+ownerPersonalURL+"\nsetups:\n  - name: work\n    kind: member\n    org: acme\norgs:\n  - name: acme\n    identity: {key: \"human:top@acme.test\"}\n")
	if p, ok := personalStoreIdentity(ctx, home, t.TempDir(), ownerPersonalURL); !ok || p.Sig.Email != "top@home.test" {
		t.Fatalf("personal.store with only a member setup inheriting = %+v %v, want the top-level identity", p, ok)
	}
	if _, ok := personalStoreIdentity(ctx, home, t.TempDir(), ownerOtherURL); ok {
		t.Fatal("a store nobody owns got a personal identity")
	}
	// two solo setups inherit it with different identities: the default one
	writeConfig(t, home, "personal:\n  store: "+ownerPersonalURL+"\nsetups:\n  - name: one\n    kind: solo\n    identity: {key: \"human:one@home.test\"}\n  - name: two\n    kind: solo\n    default: true\n    identity: {key: \"human:two@home.test\"}\n  - name: off\n    kind: solo\n    disabled: true\n    personal_store: "+ownerOtherURL+"\n    identity: {key: \"human:off@home.test\"}\n")
	if p, ok := personalStoreIdentity(ctx, home, t.TempDir(), ownerPersonalURL); !ok || p.Sig.Email != "two@home.test" {
		t.Fatalf("two inheriting owners = %+v %v, want the default setup's", p, ok)
	}
	if _, ok := personalStoreIdentity(ctx, home, t.TempDir(), ownerOtherURL); ok {
		t.Fatal("a disabled setup owns nothing")
	}
}
