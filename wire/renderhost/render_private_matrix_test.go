package renderhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// Store ids of the private render matrix (arbitrary sto_ ids): one plain
// internal store, one org store attached through two folders, one
// shareable store.
const (
	privMatrixInnerID = "sto_01a1a5aca7f57659be7320c5f4b700d1"
	privMatrixOrgID   = "sto_01a1a5aca7f57659be7320c5f4b700d2"
	privMatrixShareID = "sto_01a1a5aca7f57659be7320c5f4b700d3"
)

// privSrc is one attached store of a private render test world: its
// attach name, its URL and the word its one memory carries.
type privSrc struct {
	name, url, word string
}

// v017StoreAt is a folder store whose marker is a v0.1.17 one (no id, no
// visibility: internal), as `lore store attach` meets it before the
// store is upgraded.
func v017StoreAt(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, rowfs.LoreDir, rowfs.DataDir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rowfs.MarkerFile), []byte("lore_store: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return "file://" + filepath.ToSlash(dir)
}

// gitIn runs git in dir and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// privWorld is an App over a fresh git repo with srcs attached (each
// holding a project and one memory with its word) plus one memory in the
// repo store ("repoword"). files are written into the repo before the
// App opens (a CLAUDE.md makes claude_code the target).
func privWorld(t *testing.T, srcs []privSrc, files map[string]string) (*wire.App, string) {
	t.Helper()
	ctx := context.Background()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	if err := os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(rel)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var yaml strings.Builder
	yaml.WriteString("stores:\n")
	for _, s := range srcs {
		yaml.WriteString("  - name: " + s.name + "\n    url: " + s.url + "\n")
	}
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(attach.FileName)), []byte(yaml.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := wire.New(wire.Options{RepoDir: repo, Home: t.TempDir(), FlagActor: "human:alice@example.com",
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	all := append([]privSrc{{name: string(store.SourceRepo), word: "repoword"}}, srcs...)
	for _, s := range all {
		target := store.Source(s.name)
		p, cerr := a.Create(ctx, target, schema.TableProjects, map[string]value.Value{"name": text(t, "p-"+s.name)})
		if cerr != nil {
			t.Fatalf("project in %s: %v", s.name, cerr)
		}
		if _, cerr = a.Create(ctx, target, schema.TableMemories, map[string]value.Value{
			"project_id": text(t, p.Row.ID.String()), "kind": text(t, schema.MemoryKindCore),
			"body": text(t, "note "+s.word), "source_kind": text(t, "manual")}); cerr != nil {
			t.Fatalf("memory in %s: %v", s.name, cerr)
		}
	}
	return a, repo
}

// TestRenderPrivateStoresMatrix (later:111): which store goes to which
// private file. A plain internal store gets <sto_id>.md; an org store
// attached through two folders (same sto_ id) gets ONE file holding both
// folders' rows and one index line naming both; a v0.1.17 marker without
// an id gets a name- stem; a shareable store's rows and the repo's own
// rows are in no private file; index.md lists exactly the three files.
func TestRenderPrivateStoresMatrix(t *testing.T) {
	ctx := context.Background()
	a, repo := privWorld(t, []privSrc{
		{name: "share", url: internalStoreAt(t, privMatrixShareID, rowfs.VisibilityShareable), word: "shareword"},
		{name: "inner", url: internalStoreAt(t, privMatrixInnerID, rowfs.VisibilityInternal), word: "innerword"},
		{name: "orga", url: internalStoreAt(t, privMatrixOrgID, rowfs.VisibilityInternal), word: "orgaword"},
		{name: "orgb", url: internalStoreAt(t, privMatrixOrgID, rowfs.VisibilityInternal), word: "orgbword"},
		{name: "old_v1", url: v017StoreAt(t), word: "oldword"},
	}, nil)
	h := New(Options{App: a, Home: t.TempDir()})
	var w strings.Builder
	if err := h.renderPrivate(ctx, &w, repo, false, false); err != nil {
		t.Fatal(err)
	}
	p, err := h.planPrivate(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{ // file → words it must hold
		privMatrixInnerID + privateFileExt:            {"innerword"},
		privMatrixOrgID + privateFileExt:              {"orgaword", "orgbword"},
		privateNoIDPrefix + "old_v1" + privateFileExt: {"oldword"},
	}
	never := []string{"shareword", "repoword"}
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want)+1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("render dir holds %v, want %d store files + %s", names, len(want), privateIndexFile)
	}
	for file, words := range want {
		b, rerr := os.ReadFile(filepath.Join(p.dir, file))
		if rerr != nil {
			t.Fatalf("%s: %v", file, rerr)
		}
		for _, word := range words {
			if !strings.Contains(string(b), word) {
				t.Errorf("%s lacks %q:\n%s", file, word, b)
			}
		}
		for _, word := range never {
			if strings.Contains(string(b), word) {
				t.Errorf("%s holds %q (not internal):\n%s", file, word, b)
			}
		}
		if !strings.HasPrefix(string(b), privateHeader+"\n") {
			t.Errorf("%s does not start with the private header", file)
		}
	}
	idx, err := os.ReadFile(filepath.Join(p.dir, privateIndexFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx), privMatrixOrgID+privateFileExt+"  store orga, orgb  ") {
		t.Errorf("index does not name both org folders on one line:\n%s", idx)
	}
	if strings.Contains(string(idx), privMatrixShareID) || strings.Contains(string(idx), "share") {
		t.Errorf("index names the shareable store:\n%s", idx)
	}
	if !strings.Contains(w.String(), "(3 stores, 4 rows") {
		t.Errorf("summary line %q, want 3 stores (the org store's two folders count once) and 4 rows", w.String())
	}
}

// TestRenderPrivateLocalFile (later:111): with claude_code the target
// (CLAUDE.md in the repo), CLAUDE.local.md keeps the user's lines,
// loses the v0.1.17 single-file import and gains one @ line per internal
// store, and is excluded through .git/info/exclude; a second render
// changes nothing. A symlinked or git-tracked CLAUDE.local.md is refused
// E_LEAK_BLOCKED and left as it was.
func TestRenderPrivateLocalFile(t *testing.T) {
	ctx := context.Background()
	const local = "CLAUDE.local.md"
	const user = "# my notes\nuse the staging db\n"
	srcs := func(t *testing.T) []privSrc {
		return []privSrc{{name: "inner", url: internalStoreAt(t, privMatrixInnerID, rowfs.VisibilityInternal), word: "innerword"}}
	}

	t.Run("user lines kept, old import replaced", func(t *testing.T) {
		a, repo := privWorld(t, srcs(t), map[string]string{"CLAUDE.md": "# repo\n", local: user + "@~/.lore/render/prj_0123.md\n"})
		h := New(Options{App: a, Home: t.TempDir()})
		var w strings.Builder
		if err := h.renderPrivate(ctx, &w, repo, false, false); err != nil {
			t.Fatal(err)
		}
		p, err := h.planPrivate(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		want := user + "@" + privateImportDir + p.repoKey + "/" + privMatrixInnerID + privateFileExt + "\n"
		got, err := os.ReadFile(filepath.Join(repo, local))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q (%v), want %q", local, got, err, want)
		}
		if !strings.Contains(w.String(), local+" holds only the import lines") {
			t.Errorf("summary line %q does not name %s", w.String(), local)
		}
		ex, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
		if err != nil || !strings.Contains(string(ex), local) {
			t.Errorf(".git/info/exclude %q (%v) does not exclude %s", ex, err, local)
		}
		w.Reset()
		if err = h.renderPrivate(ctx, &w, repo, false, false); err != nil {
			t.Fatal(err)
		}
		if again, _ := os.ReadFile(filepath.Join(repo, local)); string(again) != want {
			t.Errorf("second render changed %s: %q", local, again)
		}
	})

	t.Run("symlink refused", func(t *testing.T) {
		a, repo := privWorld(t, srcs(t), map[string]string{"CLAUDE.md": "# repo\n"})
		elsewhere := filepath.Join(t.TempDir(), "notes.md")
		if err := os.WriteFile(elsewhere, []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(repo, local)); err != nil {
			t.Fatal(err)
		}
		h := New(Options{App: a, Home: t.TempDir()})
		var w strings.Builder
		if err := h.renderPrivate(ctx, &w, repo, false, false); !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("render over a symlinked %s: %v, want E_LEAK_BLOCKED naming the symlink", local, err)
		}
		if got, _ := os.ReadFile(elsewhere); string(got) != user {
			t.Errorf("the symlink target was written: %q", got)
		}
	})

	t.Run("tracked refused", func(t *testing.T) {
		a, repo := privWorld(t, srcs(t), map[string]string{"CLAUDE.md": "# repo\n", local: user})
		gitIn(t, repo, "add", "--", local)
		h := New(Options{App: a, Home: t.TempDir()})
		var w strings.Builder
		if err := h.renderPrivate(ctx, &w, repo, false, false); !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "git tracks it") {
			t.Fatalf("render over a tracked %s: %v, want E_LEAK_BLOCKED naming git", local, err)
		}
		if got, _ := os.ReadFile(filepath.Join(repo, local)); string(got) != user {
			t.Errorf("the tracked file was written: %q", got)
		}
	})
}

// TestPrivateRepoKey (later:312): the render dir is named by _meta.json's
// repo_id when it carries a non-empty string, else by the prj_ project
// id (no file, broken JSON, no key, empty, not a string); and once the
// repo identity is stamped (`lore init` / `lore sync` call
// EnsureRepoIdentity) the private render lands under that repo id.
func TestPrivateRepoKey(t *testing.T) {
	const pid = "prj_0123"
	cases := []struct {
		name, meta string // meta "" = no _meta.json
		want       string
	}{
		{"no file", "", pid},
		{"broken json", "{", pid},
		{"no key", `{"project_id":"prj_x"}`, pid},
		{"empty", `{"repo_id":""}`, pid},
		{"not a string", `{"repo_id":7}`, pid},
		{"stamped", `{"repo_id":"rep_abc","project_id":"prj_x"}`, "rep_abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if c.meta != "" {
				dir := filepath.Join(root, rowfs.LoreDir, rowfs.DataDir)
				if err := os.MkdirAll(dir, 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, rowfs.MetaFileName), []byte(c.meta), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := privateRepoKey(root, pid); got != c.want {
				t.Errorf("privateRepoKey = %q, want %q", got, c.want)
			}
		})
	}

	t.Run("after EnsureRepoIdentity", func(t *testing.T) {
		ctx := context.Background()
		a, repo := privApp(t)
		home := t.TempDir()
		h := New(Options{App: a, Home: home})
		before, err := h.planPrivate(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		if before.repoKey != before.projectID {
			t.Fatalf("unstamped repo key %q, want the project id %q", before.repoKey, before.projectID)
		}
		pid, err := a.Project(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.SeedRepoMeta(ctx, pid); err != nil { // what `lore init` writes first
			t.Fatal(err)
		}
		repoID, _, err := a.EnsureRepoIdentity(ctx)
		if err != nil || repoID == "" {
			t.Fatalf("EnsureRepoIdentity: %q %v", repoID, err)
		}
		var w strings.Builder
		if err = h.renderPrivate(ctx, &w, repo, false, false); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(filepath.Join(home, renderDirName, repoID, privTestInnerID+privateFileExt)); err != nil {
			t.Fatalf("private file not under the stamped repo id %s: %v", repoID, err)
		}
		if _, err = os.Stat(filepath.Join(home, renderDirName, before.projectID)); !os.IsNotExist(err) {
			t.Errorf("a render dir under the project id exists too: %v", err)
		}
	})
}
