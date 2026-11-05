package wire_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/wire"
)

// asClientStoreID is the attached internal store of the --as-client
// fixture (arbitrary hex).
const asClientStoreID = "sto_0e45506e64357c94a87e70ef1c6bbc01"

// asClientNote is an internal note of 12 words: 8 of them in a tracked
// file is a text finding.
const asClientNote = "client is slow to pay so always send the estimate before the call"

// asClientRepo is an own repo (RepoIsClient false) with the internal
// folder store "kb" attached, an internal memory in it and the repo's
// project; .lore/data is committed.
func asClientRepo(t *testing.T) (*opstest.H, string) {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.Parse[ids.Store](asClientStoreID)
	if err != nil {
		t.Fatal(err)
	}
	if err := folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	h := opstest.Bare(t, func(o *wire.Options) {
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
	})
	if err := os.MkdirAll(filepath.Join(h.Repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	yaml := "stores:\n  - name: kb\n    url: file://" + filepath.ToSlash(dir) + "\n"
	if err := os.WriteFile(filepath.Join(h.Repo, filepath.FromSlash(attach.FileName)), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	h.SeedProject()
	mem := h.ID(h.MustYes("memory.add", `{"body":`+opstest.Quote(asClientNote)+`,"to":"kb"}`))
	commitAll(t, h.Repo)
	return h, mem
}

// commitAll stages and commits everything in repo (hooks off).
func commitAll(t *testing.T, repo string) {
	t.Helper()
	for _, cmd := range []*exec.Cmd{
		exec.Command("git", "add", "-A"),
		exec.Command("git", "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--no-verify", "--allow-empty", "-m", "x"),
	} {
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", cmd.Args, err, out)
		}
	}
}

// TestCheckAsClient (RES 3.9, later 515): a clean own repo passes; a
// tracked file quoting 8 words of an internal store note, an internal id,
// or a secret is refused E_LEAK_BLOCKED, naming the file and the note by
// id, never the note's words or the secret.
func TestCheckAsClient(t *testing.T) {
	t.Setenv("CI", "")
	h, mem := asClientRepo(t)
	ctx := context.Background()
	if err := h.App.CheckAsClient(ctx, h.Repo, false); err != nil {
		t.Fatalf("clean repo: %v", err)
	}
	secret := "ACMETOK-" + strings.Repeat("7", 12)
	h.MustYes("secrets.rule.add", `{"id":"acme-tok","regex":"ACMETOK-[0-9]{12}","keywords":"acmetok"}`)
	words := strings.Fields(asClientNote)
	cases := []struct {
		name, file, body string
		want             []string
	}{
		{"internal text", "docs/notes.md", "# notes\n" + strings.Join(words[0:8], " ") + "\n", []string{"docs/notes.md:2", "matches internal note " + mem}},
		{"internal id", "src/a.go", "// see " + mem + "\n", []string{"src/a.go:1", "names a row of store kb"}},
		{"secret", "deploy.env", "TOKEN=" + secret + "\n", []string{"deploy.env:1", "acme-tok"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(h.Repo, filepath.FromSlash(c.file))
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			commitAll(t, h.Repo)
			err := h.App.CheckAsClient(ctx, h.Repo, false)
			if !errs.Has(err, errs.LeakBlocked) {
				t.Fatalf("want E_LEAK_BLOCKED, got %v", err)
			}
			msg := err.Error()
			for _, w := range c.want {
				if !strings.Contains(msg, w) {
					t.Errorf("report lacks %q:\n%s", w, msg)
				}
			}
			if strings.Contains(msg, strings.Join(words[0:8], " ")) || strings.Contains(msg, secret) {
				t.Errorf("report prints a value:\n%s", msg)
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			commitAll(t, h.Repo)
		})
	}
	// CI: the count only.
	if err := os.WriteFile(filepath.Join(h.Repo, "leak.md"), []byte("// "+mem+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commitAll(t, h.Repo)
	t.Setenv("CI", "true")
	err := h.App.CheckAsClient(ctx, h.Repo, false)
	if !errs.Has(err, errs.LeakBlocked) || strings.Contains(err.Error(), mem) || !strings.Contains(err.Error(), "details hidden in CI") {
		t.Fatalf("CI report: %v", err)
	}
}

// TestCheckAsClientNeedsProject: outside a project (no _meta.json) the
// check refuses E_INVALID_INPUT instead of passing.
func TestCheckAsClientNeedsProject(t *testing.T) {
	h := opstest.Bare(t)
	if err := h.App.CheckAsClient(context.Background(), h.Repo, false); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("want E_INVALID_INPUT, got %v", err)
	}
}
