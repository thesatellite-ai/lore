package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// noGitIdentity gives the test a git with no identity at all: no system
// or global config, and none of the identity variables set (restored by
// t.Setenv's cleanup). Not parallel: it changes the process environment.
func noGitIdentity(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{envAuthorName, envAuthorEmail, envCommitterName, envCommitterEmail, envEmail} {
		t.Setenv(k, "") // registers the restore
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
}

// FLAG4: with no git identity, a store commit and the merge of a
// concurrent writer are signed with the opener's author (the lore
// identity) instead of failing with "Committer identity unknown".
func TestNoGitIdentityUsesFallback(t *testing.T) {
	noGitIdentity(t)
	lore := Signature{Name: "Mira Lore", Email: "mira@example.invalid"}
	remote := newRemote(t, true)
	who := WithIdentity(func(context.Context) Signature { return lore })
	a, b, g := clone(t, remote, who), clone(t, remote, who), newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "a", hlc.HLC{})})
	syncOK(t, a)
	write(t, b, store.Change{Row: g.row(newID(t), "b", hlc.HLC{})})
	syncOK(t, b) // three-way merge of a's commit: needs a committer identity
	want := "Mira Lore|mira@example.invalid|Mira Lore|mira@example.invalid"
	for _, rev := range []string{"HEAD", "HEAD^2"} {
		if got := gitT(t, b.Dir(), "log", "-1", "--format=%an|%ae|%cn|%ce", rev); got != want {
			t.Fatalf("%s signed %q, want %q", rev, got, want)
		}
	}
	if got := gitT(t, b.Dir(), "log", "-1", "--format=%s", "HEAD"); got != mergeSubject {
		t.Fatalf("HEAD is %q, want the merge commit", got)
	}
}

// FLAG4: a git identity, even one set only in the clone's own config,
// wins over the lore identity; lore never writes it into git config.
func TestGitIdentityWins(t *testing.T) {
	noGitIdentity(t)
	asked := 0
	who := WithIdentity(func(context.Context) Signature {
		asked++
		return Signature{Name: "Mira Lore", Email: "mira@example.invalid"}
	})
	b, g := clone(t, newRemote(t, true), who), newGen(t)
	gitT(t, b.Dir(), "config", "user.name", "Git Person")
	gitT(t, b.Dir(), "config", "user.email", "person@example.invalid")
	write(t, b, store.Change{Row: g.row(newID(t), "a", hlc.HLC{})})
	if got, want := gitT(t, b.Dir(), "log", "-1", "--format=%an|%ae|%cn|%ce"), "Git Person|person@example.invalid|Git Person|person@example.invalid"; got != want {
		t.Fatalf("signed %q, want %q", got, want)
	}
	if asked != 0 {
		t.Fatalf("the lore identity was resolved %d times while git had one", asked)
	}
	// Half an identity is none: git would refuse the commit, so lore signs.
	gitT(t, b.Dir(), "config", "--unset", "user.email")
	write(t, b, store.Change{Row: g.row(newID(t), "b", hlc.HLC{})})
	if got, want := gitT(t, b.Dir(), "log", "-1", "--format=%an|%ae"), "Mira Lore|mira@example.invalid"; got != want {
		t.Fatalf("half identity signed %q, want %q", got, want)
	}
	if out, err := tryGit(t, b.Dir(), "config", "--get", "user.email"); err == nil {
		t.Fatalf("lore wrote git config user.email = %q", out)
	}
}

// An unusable lore identity (no e-mail) falls back to the opener's author.
func TestUnusableIdentityFallsBackToAuthor(t *testing.T) {
	noGitIdentity(t)
	who := WithIdentity(func(context.Context) Signature { return Signature{Name: "No Mail"} })
	b, g := clone(t, newRemote(t, true), who), newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "a", hlc.HLC{})})
	if got, want := gitT(t, b.Dir(), "log", "-1", "--format=%an|%ae"), DefaultAuthor.Name+"|"+DefaultAuthor.Email; got != want {
		t.Fatalf("signed %q, want %q", got, want)
	}
}

// Q-R2-1: a store with an owner signature (an org store) is signed by it
// even when git has an identity, merges included; a store without one keeps
// the FLAG4 rule (the git identity wins), and an unusable owner signature
// is ignored.
func TestOwnerSignerBeatsGitIdentity(t *testing.T) {
	noGitIdentity(t)
	org := Signature{Name: "Northwind", Email: "lore@northwind.invalid"}
	remote := newRemote(t, true)
	a := clone(t, remote, WithSigner(org))
	b := clone(t, remote, WithSigner(org))
	plain := clone(t, remote)
	unusable := clone(t, remote, WithSigner(Signature{Name: "No Email"}))
	g := newGen(t)
	for _, c := range []*Backend{a, b, plain, unusable} {
		gitT(t, c.Dir(), "config", "user.name", "Git Person")
		gitT(t, c.Dir(), "config", "user.email", "person@gmail.invalid")
	}
	write(t, a, store.Change{Row: g.row(newID(t), "a", hlc.HLC{})})
	syncOK(t, a)
	write(t, b, store.Change{Row: g.row(newID(t), "b", hlc.HLC{})})
	syncOK(t, b) // merges a's commit
	want := "Northwind|lore@northwind.invalid|Northwind|lore@northwind.invalid"
	for _, rev := range []string{"HEAD", "HEAD^2"} {
		if got := gitT(t, b.Dir(), "log", "-1", "--format=%an|%ae|%cn|%ce", rev); got != want {
			t.Fatalf("org store %s signed %q, want %q", rev, got, want)
		}
	}
	person := "Git Person|person@gmail.invalid"
	for name, c := range map[string]*Backend{"plain": plain, "unusable signer": unusable} {
		write(t, c, store.Change{Row: g.row(newID(t), name, hlc.HLC{})})
		if got := gitT(t, c.Dir(), "log", "-1", "--format=%an|%ae"); got != person {
			t.Fatalf("%s store signed %q, want the git identity %q (FLAG4)", name, got, person)
		}
	}
	if env, ok := SignerEnv(org); !ok || len(env) != 4 {
		t.Fatalf("SignerEnv(org) = %v, %v", env, ok)
	}
	if _, ok := SignerEnv(Signature{Name: "x", Email: "a<b"}); ok {
		t.Fatal("SignerEnv must refuse a signature that breaks an ident line")
	}
}
