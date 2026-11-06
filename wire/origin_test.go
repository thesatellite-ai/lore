package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
)

// originMachine is the fixed machine id the provenance tests stamp.
const originMachine = "mch_0cd630f071ab154c3b6a0f44e9366337ec0fb87c4e688d19fc5e84f1bf8dd980"

// newOriginApp is newTestApp with the provenance options set: the work
// dir, a version and a fixed machine id.
func newOriginApp(t *testing.T, repo, workDir string, client bool) *App {
	t.Helper()
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: repo, WorkDir: workDir, Home: t.TempDir(), Actor: actor, Version: "0.1.18",
		Machine:      func() (ids.MachineID, error) { return ids.ParseMachine(originMachine) },
		RepoIsClient: func(context.Context) (bool, error) { return client, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}

// gitCommit makes one empty commit in repo and returns its full id.
func gitCommit(t *testing.T, repo string) string {
	t.Helper()
	env := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	for _, cmd := range []*exec.Cmd{
		exec.Command("git", "checkout", "-q", "-b", "feat/vpn"),
		exec.Command("git", "commit", "-q", "--allow-empty", "-m", "first"),
	} {
		cmd.Dir, cmd.Env = repo, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", cmd.Args, err, out)
		}
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// originOfFile reads a row file's _o and _w as objects.
func originOfFile(t *testing.T, path string) (o map[string]json.RawMessage, w map[string]string) {
	t.Helper()
	f := rowFile(t, path)
	if raw, ok := f["_o"]; ok {
		if err := json.Unmarshal(raw, &o); err != nil {
			t.Fatal(err)
		}
	}
	if raw, ok := f["_w"]; ok {
		if err := json.Unmarshal(raw, &w); err != nil {
			t.Fatal(err)
		}
	}
	return o, w
}

// TestOriginDirInMonorepoFolder is PROV6: a row made in a monorepo
// subfolder records `_o.dir` relative to the root with forward slashes;
// one made at the root has no dir. In our own repo both carry the git HEAD
// (branch and full commit); in a client repo the local-only branch is
// dropped (PROV8). A repo row's `_w` is `{v}` either way: no machine id in
// any code repo (Q-R3-9; store rows keep m, engine tests).
func TestOriginDirInMonorepoFolder(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	commit := gitCommit(t, repo)
	sub := filepath.Join(repo, "services", "billing")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		work, dir string
		client    bool
	}{{sub, `"services/billing"`, false}, {repo, "", false}, {sub, `"services/billing"`, true}} {
		a := newOriginApp(t, repo, c.work, c.client)
		if c.work == sub && !c.client {
			withProject(t, a) // once: the repo holds one project
		}
		out, err := run(t, a, opsmemory.OpAdd, `{"text":"where was I made"}`, false)
		if err != nil {
			t.Fatal(err)
		}
		o, w := originOfFile(t, memPath(repo, idOf(t, out)))
		if string(o["dir"]) != c.dir {
			t.Fatalf("work %s: _o.dir = %s, want %q (%v)", c.work, o["dir"], c.dir, o)
		}
		var g bytes.Buffer
		if err := json.Compact(&g, o["git"]); err != nil {
			t.Fatal(err)
		}
		wantGit, wantW := `{"branch":"feat/vpn","commit":"`+commit+`","state":"branch"}`, map[string]string{"v": "0.1.18"}
		if c.client {
			wantGit = `{"commit":"` + commit + `","state":"branch"}`
		}
		if g.String() != wantGit {
			t.Fatalf("client %v: _o.git = %s", c.client, g.String())
		}
		if strings.Contains(string(o["dir"]), repo) {
			t.Fatal("absolute path stored")
		}
		if len(w) != len(wantW) || w["m"] != wantW["m"] || w["v"] != wantW["v"] {
			t.Fatalf("client %v: _w = %v", c.client, w)
		}
	}
}

// TestMigrateWritesNoOrigin is PROV10: `lore migrate run` rewrites a
// v0.1.17 row without inventing an `_o` (or a `_w`), even with the
// provenance facts at hand and a legacy source_ref in the row, which it
// never reads into one.
func TestMigrateWritesNoOrigin(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	gitCommit(t, repo)
	a := newOriginApp(t, repo, repo, false)
	withProject(t, a)
	doc := strings.Replace(v1Memory(projectID(t, a), ""), `"source_kind":"manual"`, `"source_kind":"file","source_ref":"docs/old.md"`, 1)
	writeV1(t, repo, doc)
	if _, err := run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	f := rowFile(t, memPath(repo, fixtureMem))
	if string(f["_v"]) != "2" {
		t.Fatalf("not migrated: %v", f)
	}
	if _, has := f["_o"]; has {
		t.Fatalf("migrate wrote _o: %s", f["_o"])
	}
	if _, has := f["_w"]; has {
		t.Fatalf("migrate wrote _w: %s", f["_w"])
	}
	if string(f["source_ref"]) != `"docs/old.md"` {
		t.Fatalf("source_ref changed: %s", f["source_ref"])
	}
}
