package wire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestClientRepoHintsNeverSayInit pins I-13: in a client repo a refusal
// whose hint (or, with none, message) sends the person to lore init names
// the org link instead; outside one, and for a hint that warns against
// init, nothing changes.
func TestClientRepoHintsNeverSayInit(t *testing.T) {
	ctx := context.Background()
	client := &App{opts: Options{ClientRepo: func(context.Context) ClientRepo { return ClientRepo{Org: "acme"} }}}
	linked := &App{opts: Options{ClientRepo: func(context.Context) ClientRepo { return ClientRepo{Org: "acme", Folder: "portal"} }}}
	own := &App{opts: Options{ClientRepo: func(context.Context) ClientRepo { return ClientRepo{} }}}
	untouched := UntouchedError(ctx, t.TempDir())
	noHint := errs.New(errs.InvalidInput, "no lore project here; run lore init")
	warn := errs.New(errs.NotAStore, "this checkout has no .lore/").WithHint("switch branch; `lore init` would make a second project")
	cases := []struct {
		name string
		app  *App
		err  error
		says []string
		not  []string
	}{
		{"client repo: write refused", client, untouched, []string{"lore org link --org acme --project <folder>", "--to acme/<folder>", "lore store folder list --store acme"}, []string{"lore init sets"}},
		{"client repo: message only", client, noHint, []string{"lore org link --org acme"}, nil},
		{"linked client repo", linked, untouched, []string{"linked to acme/portal", "--to acme/portal"}, []string{"lore init sets", "lore org link"}},
		{"own repo", own, untouched, []string{"lore init sets this repo up"}, nil},
		{"no hook", &App{}, untouched, []string{"lore init sets this repo up"}, nil},
		{"a warning against init", client, warn, []string{"would make a second project"}, []string{"lore org link"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.app.ClientRepoHints(ctx, c.err)
			var e *errs.Error
			if !errors.As(got, &e) {
				t.Fatalf("not a lore error: %v", got)
			}
			if e.Code != errs.CodeOf(c.err) {
				t.Errorf("code %s, want %s", e.Code, errs.CodeOf(c.err))
			}
			for _, s := range c.says {
				if !strings.Contains(e.Hint, s) {
					t.Errorf("hint %q lacks %q", e.Hint, s)
				}
			}
			for _, s := range c.not {
				if strings.Contains(e.Hint, s) {
					t.Errorf("hint %q says %q", e.Hint, s)
				}
			}
		})
	}
	if got := client.ClientRepoHints(ctx, nil); got != nil {
		t.Errorf("nil error became %v", got)
	}
}

// TestWhereUsesClientRepoNotGone pins I-12: lore where lists a live client
// repo (a .git and no .lore/) as using its store caches, never as gone; a
// checkout that is not there still is.
func TestWhereUsesClientRepoNotGone(t *testing.T) {
	home := t.TempDir()
	client := filepath.Join(home, "code", "portal")
	if err := os.MkdirAll(filepath.Join(client, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(home, "code", "gone")
	root := filepath.Join(home, cacheDir)
	const used = "stores/sto_a/main/portal.db"
	for _, top := range []string{client, gone} {
		if err := sqlite.RecordUse(root, top, []sqlite.Use{{Path: used, Store: "acme"}}); err != nil {
			t.Fatal(err)
		}
	}
	c := whereUses(home)[used]
	if c == nil {
		t.Fatal("no use of the store cache")
	}
	repos := strings.Join(c.repos, "|")
	if !strings.Contains(repos, client+"|") && !strings.HasSuffix(repos, client) || strings.Contains(repos, client+whereGoneRepo) {
		t.Errorf("client repo not listed as a live user: %q", repos)
	}
	if !strings.Contains(repos, gone+whereGoneRepo) {
		t.Errorf("a gone checkout is not marked gone: %q", repos)
	}
}
