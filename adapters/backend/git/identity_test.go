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

// Q-R2-1 with one author (owner report 2026-11-12): an org store's
// commits and merges are signed by the org identity when the clone's git
// identity uses another (personal) email; when the clone's git identity
// uses the org identity's email, git signs, as a hand edit there would. A
// store without an owner keeps FLAG4 (the git identity wins), and an
// unusable owner signature is ignored.
func TestOwnerSignerAndGitIdentity(t *testing.T) {
	noGitIdentity(t)
	org := Signature{Name: "Northwind", Email: "lore@northwind.invalid"}
	remote := newRemote(t, true)
	a := clone(t, remote, WithSigner(org))
	b := clone(t, remote, WithSigner(org))
	same := clone(t, remote, WithSigner(org))
	plain := clone(t, remote)
	unusable := clone(t, remote, WithSigner(Signature{Name: "No Email"}))
	g := newGen(t)
	for _, c := range []*Backend{a, b, plain, unusable} {
		gitT(t, c.Dir(), "config", "user.name", "Git Person")
		gitT(t, c.Dir(), "config", "user.email", "person@gmail.invalid")
	}
	gitT(t, same.Dir(), "config", "user.name", "git-handle")
	gitT(t, same.Dir(), "config", "user.email", "LORE@northwind.invalid")
	write(t, a, store.Change{Row: g.row(newID(t), "a", hlc.HLC{})})
	syncOK(t, a)
	write(t, b, store.Change{Row: g.row(newID(t), "b", hlc.HLC{})})
	syncOK(t, b) // merges a's commit
	want := "Northwind|lore@northwind.invalid|Northwind|lore@northwind.invalid"
	for _, rev := range []string{"HEAD", "HEAD^2"} {
		if got := gitT(t, b.Dir(), "log", "-1", "--format=%an|%ae|%cn|%ce", rev); got != want {
			t.Fatalf("org store %s signed %q, want %q (a personal email never signs)", rev, got, want)
		}
	}
	write(t, same, store.Change{Row: g.row(newID(t), "same", hlc.HLC{})})
	if got := gitT(t, same.Dir(), "log", "-1", "--format=%an|%ae"); got != "git-handle|LORE@northwind.invalid" {
		t.Fatalf("org store whose git identity uses the org email signed %q, want the git identity", got)
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

// brief 2.1: a hand edit (store.PersonAuthored) in an org store is
// authored by the person, the org identity stays the committer: the lore
// identity when it is usable (no personal git e-mail in an org store),
// else the git author of the clone. A plain lore write there keeps the
// org identity on both sides, and a store without an owner signature keeps
// the FLAG4 rule for hand edits too.
func TestHandEditAuthoredByPerson(t *testing.T) {
	noGitIdentity(t)
	org := Signature{Name: "Northwind", Email: "lore@northwind.invalid"}
	lore := Signature{Name: "Mira Lore", Email: "mira@northwind.invalid"}
	remote := newRemote(t, true)
	withLore := clone(t, remote, WithSigner(org), WithIdentity(func(context.Context) Signature { return lore }))
	gitOnly := clone(t, remote, WithSigner(org))
	plain := clone(t, remote)
	for _, c := range []*Backend{withLore, gitOnly, plain} {
		gitT(t, c.Dir(), "config", "user.name", "Git Person")
		gitT(t, c.Dir(), "config", "user.email", "person@example.invalid")
	}
	g := newGen(t)
	hand := store.WithPersonAuthor(store.WithCommitMessage(context.Background(), "hand edit: tasks x"))
	cases := []struct {
		name string
		b    *Backend
		ctx  context.Context
		want string
	}{
		{"org store, hand edit, lore identity", withLore, hand, "Mira Lore|mira@northwind.invalid|Northwind|lore@northwind.invalid"},
		{"org store, hand edit, git author", gitOnly, hand, "Git Person|person@example.invalid|Northwind|lore@northwind.invalid"},
		{"org store, lore write", withLore, context.Background(), "Northwind|lore@northwind.invalid|Northwind|lore@northwind.invalid"},
		{"plain store, hand edit", plain, hand, "Git Person|person@example.invalid|Git Person|person@example.invalid"},
	}
	for _, c := range cases {
		if err := c.b.Write(c.ctx, []store.Change{{Row: g.row(newID(t), c.name, hlc.HLC{})}}); err != nil {
			t.Fatalf("%s: write: %v", c.name, err)
		}
		if got := gitT(t, c.b.Dir(), "log", "-1", "--format=%an|%ae|%cn|%ce"); got != c.want {
			t.Errorf("%s: commit by %q, want %q", c.name, got, c.want)
		}
	}
	// author.* beats user.* for the git author, as in git itself.
	gitT(t, gitOnly.Dir(), "config", "author.email", "author@example.invalid")
	if err := gitOnly.Write(hand, []store.Change{{Row: g.row(newID(t), "author", hlc.HLC{})}}); err != nil {
		t.Fatal(err)
	}
	if got, want := gitT(t, gitOnly.Dir(), "log", "-1", "--format=%an|%ae"), "Git Person|author@example.invalid"; got != want {
		t.Errorf("author.email: commit by %q, want %q", got, want)
	}
}
