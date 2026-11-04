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
