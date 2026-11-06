package host

// live_signature_test.go — later 66 (FLAG4): who signs the commits lore
// makes on refs/lore/live. A repo whose git knows the person (user.name
// and user.email) signs as them, author and committer; a repo with no git
// identity signs as the lore identity (LiveConfig.Signer, cmd/lore's
// wire.App.LoreSignature); with no usable lore identity either, the fixed
// liveSignatureFallback. Git config is never changed.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// sigSigner is a LiveConfig.Signer answering fixed values.
func sigSigner(name, email string, ok bool) func(context.Context) (string, string, bool) {
	return func(context.Context) (string, string, bool) { return name, email, ok }
}

// TestLiveCommitSignature: matrix of git identity × lore identity.
func TestLiveCommitSignature(t *testing.T) {
	fallback := liveSignatureFallback.Name + " <" + liveSignatureFallback.Email + ">"
	cases := []struct {
		name    string
		gitName string // "" = the repo's git has no identity
		signer  func(context.Context) (string, string, bool)
		want    string
	}{
		{name: "git identity wins over the lore identity", gitName: "Repo Person", signer: sigSigner("Ada", "ada@example.com", true), want: "Repo Person <repo@example.com>"},
		{name: "git identity, no signer", gitName: "Repo Person", want: "Repo Person <repo@example.com>"},
		{name: "no git identity: lore identity", signer: sigSigner("Ada", "ada@example.com", true), want: "Ada <ada@example.com>"},
		{name: "no git identity: agent lore identity", signer: sigSigner("CI", "agent.ci@lore.invalid", true), want: "CI <agent.ci@lore.invalid>"},
		{name: "no git identity, no signer", want: fallback},
		{name: "no git identity, identity unresolved", signer: sigSigner("", "", false), want: fallback},
		{name: "no git identity, empty e-mail", signer: sigSigner("Ada", "", true), want: fallback},
		{name: "no git identity, angle bracket in name", signer: sigSigner("Ada <x>", "ada@example.com", true), want: fallback},
		{name: "no git identity, newline in e-mail", signer: sigSigner("Ada", "ada@example.com\nx", true), want: fallback},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			t.Setenv(EnvLive, "")
			t.Setenv(EnvSync, "")
			for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
				t.Setenv(k, "")
			}
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			gitT(t, dir, "init", "-q", "-b", "main")
			gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "init")
			if c.gitName != "" {
				gitT(t, dir, "config", "user.name", c.gitName)
				gitT(t, dir, "config", "user.email", "repo@example.com")
			}
			h := &liveHost{cfg: LiveConfig{Home: filepath.Join(home, ".lore"), WorkDir: dir, Version: "0.1.18", Stderr: &strings.Builder{}, Signer: c.signer}}
			ctx := context.WithValue(context.Background(), liveRunKey{}, &liveRun{h: h, sessions: map[string]*liveSession{}})
			b := &liveBackend{inner: memstore.NewData().Open(store.OpenOptions{}), top: dir, folder: live.RootFolder}
			s := b.session(ctx)
			if s == nil {
				t.Fatal("live mode must apply in a git repo with LORE_LIVE unset")
			}
			t.Cleanup(s.release)
			if werr := b.Write(ctx, []store.Change{{Row: ltRow(t, schema.TableTasks, ltTaskA, 1, "signed", false)}}); werr != nil {
				t.Fatal(werr)
			}
			out, err := (gitref.Exec{}).Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"log", "-1", "--format=%an <%ae>|%cn <%ce>", live.RefLive}})
			if err != nil {
				t.Fatalf("read the live commit: %v %s", err, out)
			}
			if got, want := strings.TrimSpace(string(out)), c.want+"|"+c.want; got != want {
				t.Fatalf("live commit author|committer = %q, want %q", got, want)
			}
			if c.gitName == "" {
				if cfg, cerr := (gitref.Exec{}).Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"config", "--get", "user.name"}}); cerr == nil {
					t.Fatalf("git config gained a user.name: %q", cfg)
				}
			}
		})
	}
}
