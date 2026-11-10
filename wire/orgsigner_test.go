package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
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

// Q-R2-1: a commit lore makes itself (store marker, migration) in an org
// store's worktree is signed by the org identity even when git has one; in
// any other store the git identity wins (FLAG4).
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
