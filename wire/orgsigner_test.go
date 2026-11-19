package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// Q-R2-1: storeSigner.owner names the org identity for any spelling of an
// org store's url (folder and branch ignored) and nothing for another
// store, a missing hook or a failing hook (FLAG4 then signs).
func TestStoreSignerOwnerMatchesOrgStores(t *testing.T) {
	t.Parallel()
	org := OrgSigner{Name: "Northwind", Email: "lore@northwind.invalid", StoreURLs: []string{"git@github.com:northwind/lore.git"}}
	signers := func(context.Context) ([]OrgSigner, error) { return []OrgSigner{org}, nil }
	s := storeIdentityFunc(Options{OrgSigners: signers})
	ctx := context.Background()
	for _, raw := range []string{
		"git@github.com:northwind/lore.git",
		"ssh://git@github.com/northwind/lore.git?folder=acme&branch=work",
	} {
		sig, ok := s.owner(ctx, raw)
		if !ok || sig != (git.Signature{Name: org.Name, Email: org.Email}) {
			t.Fatalf("owner(%q) = %v, %v; want the org identity", raw, sig, ok)
		}
	}
	if _, ok := s.owner(ctx, "git@github.com:someone/personal.git"); ok {
		t.Fatal("a store outside every org must have no owner (FLAG4)")
	}
	if _, ok := storeIdentityFunc(Options{}).owner(ctx, org.StoreURLs[0]); ok {
		t.Fatal("no OrgSigners hook: no owner")
	}
	failing := func(context.Context) ([]OrgSigner, error) { return nil, errors.New("config unreadable") }
	if _, ok := storeIdentityFunc(Options{OrgSigners: failing}).owner(ctx, org.StoreURLs[0]); ok {
		t.Fatal("a failing hook names no owner")
	}
}

// Q-R2-1, D-714: a commit lore makes itself (adopt, store marker,
// migration, folder add/remove) in an org store's worktree is signed by the
// org identity whatever git's identity there says (another email, or the
// org email beside another user.name: adopt took that git user.name while
// org link signed as the org identity); in any other store the git
// identity wins (FLAG4).
func TestStoreSignerEnvForOrgStoreWorktree(t *testing.T) {
	t.Parallel()
	org := OrgSigner{Name: "Northwind", Email: "lore@northwind.invalid", StoreURLs: []string{"git@github.com:northwind/lore.git"}}
	s := storeIdentityFunc(Options{OrgSigners: func(context.Context) ([]OrgSigner, error) { return []OrgSigner{org}, nil }})
	ctx := context.Background()
	mk := func(origin string) string {
		dir := filepath.Join(t.TempDir(), "work")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", origin}, {"config", "user.name", "Git Person"}, {"config", "user.email", "person@gmail.invalid"}} {
			if _, err := (gitRun{dir: dir, args: args}).output(ctx); err != nil {
				t.Fatalf("git %v: %v", args, err)
			}
		}
		return dir
	}
	env, err := s.envFor(ctx, mk("ssh://git@github.com/northwind/lore.git"))
	if err != nil || !strings.Contains(strings.Join(env, " "), "GIT_AUTHOR_EMAIL=lore@northwind.invalid") {
		t.Fatalf("org store env = %v, %v; want the org identity", env, err)
	}
	same := mk("ssh://git@github.com/northwind/lore.git")
	if _, err := (gitRun{dir: same, args: []string{"config", "user.email", "lore@northwind.invalid"}}).output(ctx); err != nil {
		t.Fatal(err)
	}
	if env, err = s.envFor(ctx, same); err != nil || !strings.Contains(strings.Join(env, " "), "GIT_AUTHOR_NAME=Northwind") || !strings.Contains(strings.Join(env, " "), "GIT_COMMITTER_NAME=Northwind") {
		t.Fatalf("devco:D-714 org store whose git user is Git Person with the org email: env = %v, %v; want the org identity", env, err)
	}
	env, err = s.envFor(ctx, mk("git@github.com:someone/personal.git"))
	if err != nil || len(env) != 0 {
		t.Fatalf("plain store env = %v, %v; want none (git identity wins, FLAG4)", env, err)
	}
}

// FLAG4/r3-sign: App.LoreSignature is exactly storeSigner.fallback for the
// App's options (the signer a core store commit uses when git has no
// identity), so the old binary's storeSignature, which calls it, names the
// same signer as a core store commit. A human key with an e-mail signs with
// that e-mail; an agent key signs with a synthetic address.
func TestLoreSignatureIsStoreFallback(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ actor, email string }{
		{testEmail, "alice@example.com"},
		{"agent:frontend_2", "agent.frontend_2@"},
	} {
		t.Run(c.actor, func(t *testing.T) {
			a, err := New(Options{RepoDir: gitRepo(t), Home: t.TempDir(), FlagActor: c.actor})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			name, email, ok := a.LoreSignature(background())
			if !ok || name == "" {
				t.Fatalf("LoreSignature = %q %q %v", name, email, ok)
			}
			fb := storeIdentityFunc(a.opts).fallback(background())
			if fb.Name != name || fb.Email != email {
				t.Fatalf("LoreSignature %q <%s> differs from the store fallback %q <%s>", name, email, fb.Name, fb.Email)
			}
			if !strings.HasPrefix(email, c.email) {
				t.Fatalf("email %q, want prefix %q", email, c.email)
			}
		})
	}
}

// devco:D-714: every store commit of an org has one author, whichever op
// makes it: the org-file commits (org link, unlink, org config, org store
// add/remove/audience, set-url: storeSignature) and the backend and wire
// commits (adopt, writes, merges: OrgSigners → storeSigner) name the same
// orgs[<org>].identity, also when the identity has no name (storeSignature
// fell back to the org's label, OrgSigners to the email's local part) or
// its name carries spaces.
func TestOrgCommitSignerOneAuthorForEveryOp(t *testing.T) {
	t.Parallel()
	for _, id := range []orgconf.OrgIdentity{
		{Key: "human:ada@acme.test", Name: "Ada Admin"},
		{Key: "human:ada@acme.test"},
		{Key: "human:ada@acme.test", Name: " Ada Admin "},
	} {
		o := orgconf.Org{Name: "acme", Identity: id, Stores: []orgconf.Store{{URL: "git@github.com:acme-co/lore-team.git"}}}
		home := t.TempDir()
		r := &OrgResolution{home: home}
		writeSignerConfig(t, home, o)
		signers, err := r.OrgSigners(context.Background())
		if err != nil || len(signers) != 1 {
			t.Fatalf("OrgSigners = %v, %v", signers, err)
		}
		ref := storeSignature(&o)
		if ref.Name != signers[0].Name || ref.Email != signers[0].Email {
			t.Fatalf("identity %+v: org link signs %q <%s>, adopt and writes sign %q <%s>; want one author", id, ref.Name, ref.Email, signers[0].Name, signers[0].Email)
		}
		if ref.Email != "ada@acme.test" {
			t.Fatalf("identity %+v: email %q", id, ref.Email)
		}
	}
	if sig := storeSignature(&orgconf.Org{Name: "acme"}); sig.Name != fallbackStoreName {
		t.Fatalf("an org without an identity signs %q, want lore's last resort", sig.Name)
	}
}

// writeSignerConfig writes ~/.lore/config.yaml under home with the one org o.
func writeSignerConfig(t *testing.T, home string, o orgconf.Org) {
	t.Helper()
	raw, err := yaml.Marshal(orgconf.Config{Orgs: []orgconf.Org{o}})
	if err != nil {
		t.Fatal(err)
	}
	p := orgcfg.ConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
