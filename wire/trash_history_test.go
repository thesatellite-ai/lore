package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opstrash "github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// histHost is the host of the test store url; a global gitconfig rewrites
// it to bare repos in a temp folder (url.insteadOf), as the old stores
// tests do.
const histHost = "example.test"

// histGitWorld points https://example.test/ at a folder of bare repos and
// returns that folder; every git call of the test sees the config.
func histGitWorld(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	hosts := filepath.Join(root, "hosts")
	if err := os.MkdirAll(hosts, 0o750); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "gitconfig")
	body := "[url \"" + hosts + "/\"]\n\tinsteadOf = https://" + histHost + "/\n" +
		"[user]\n\tname = t\n\temail = t@example.com\n[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return hosts
}

// histAddMemory adds a memory to store to ("" = repo) and returns its id.
func histAddMemory(t *testing.T, a *App, to, body string) string {
	t.Helper()
	in := `{"body":"` + body + `"}`
	if to != "" {
		in = `{"body":"` + body + `","to":"` + to + `"}`
	}
	out, err := run(t, a, opsmemory.OpAdd, in, true)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal([]byte(out), &m); err != nil || m.ID == "" {
		t.Fatalf("memory add: %s %v", out, err)
	}
	return m.ID
}

// histPurge purges id (a memory) in src.
func histPurge(t *testing.T, a *App, src store.Source, id string) {
	t.Helper()
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eng.Purge(background(), engine.Ref{Store: src, Table: schema.TableMemories, ID: aid}); err != nil {
		t.Fatalf("purge %s: %v", id, err)
	}
}

// histCommitLive points ref at a new commit (parent: the ref's tip, if
// any) whose tree holds exactly files.
func histCommitLive(t *testing.T, repo, ref string, files map[string]string) {
	t.Helper()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "index")}
	for p, content := range files {
		tmp := filepath.Join(t.TempDir(), "blob")
		if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		oid := gitIn(t, repo, env, "hash-object", "-w", tmp)
		gitIn(t, repo, env, "update-index", "--add", "--cacheinfo", "100644,"+oid+","+p)
	}
	tree := gitIn(t, repo, env, "write-tree")
	args := []string{"commit-tree", tree, "-m", "live"}
	if liveRefExists(background(), repo, ref) {
		args = append(args, "-p", ref)
	}
	commit := gitIn(t, repo, env, args...)
	gitIn(t, repo, env, "update-ref", ref, commit)
}

// histByID indexes PurgedHistory's rows by id.
func histByID(t *testing.T, a *App) map[string]opstrash.HistoryRow {
	t.Helper()
	rows, err := a.PurgedHistory(background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]opstrash.HistoryRow{}
	for _, r := range rows {
		if _, dup := out[r.ID]; dup {
			t.Errorf("row %s listed twice", r.ID)
		}
		out[r.ID] = r
	}
	return out
}

// TestPurgedHistoryPlaces pins P3-DEL-HIST-1 (DELETION 3.6): --from-history
// walks the repo checkout, every attached git store's clone and the live
// refs. Found: a memory purged in the repo (purger from its tombstone), a
// memory purged in a git store (Store = the store), a live-only memory
// removed from refs/lore/live (Ref named, no purger). Not found: a
// memory that left the live ref because it landed (its store holds it),
// a live row of a table that never rides live, a memory still alive.
func TestPurgedHistoryPlaces(t *testing.T) {
	hosts := histGitWorld(t)
	bare := filepath.Join(hosts, "acme", "kb")
	if err := os.MkdirAll(bare, 0o750); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bare, nil, "init", "-q", "--bare", "-b", "main")

	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpInit, `{"name":"kb","url":"https://`+histHost+`/acme/kb"}`, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openWith(t, repo, Options{Home: a.opts.Home})

	repoGone := histAddMemory(t, a, "", "repo purged")
	alive := histAddMemory(t, a, "", "still here")
	storeGone := histAddMemory(t, a, "kb", "store purged")
	metaGit(t, repo, "add", "-A")
	metaGit(t, repo, "commit", "-q", "-m", "rows")
	histPurge(t, a, store.SourceRepo, repoGone)
	histPurge(t, a, "kb", storeGone)
	metaGit(t, repo, "add", "-A")
	metaGit(t, repo, "commit", "-q", "-m", "purge")

	pid, err := a.Project(background())
	if err != nil {
		t.Fatal(err)
	}
	proj := pid.String()
	liveGone := "mem_0e45506e64357c94a87e70ef1c6bb001"
	landed := alive
	other := "res_0e45506e64357c94a87e70ef1c6bb002"
	histCommitLive(t, repo, liveRefLocal, map[string]string{
		livePath(liveGone): liveMemory(liveGone, proj, "live only", liveNewer),
		livePath(landed):   liveMemory(landed, proj, "landed", liveNewer),
		liveRootFolder + "/" + schema.TableResources + "/" + other + ".json": strings.Replace(strings.Replace(liveMemory(other, proj, "a resource", liveNewer), `"_table":"memories"`, `"_table":"resources"`, 1), `"kind":"retrieved",`, ``, 1),
		"_live/keep.json": `{}`,
	})
	histCommitLive(t, repo, liveRefLocal, map[string]string{"_live/keep.json": `{}`})

	got := histByID(t, a)
	r, ok := got[repoGone]
	if !ok || r.Store != store.SourceRepo || r.Ref != "" || r.PurgedBy == "" || !strings.Contains(string(r.Doc), "repo purged") {
		t.Errorf("repo purge: %+v (found %v)", r, ok)
	}
	r, ok = got[storeGone]
	if !ok || r.Store != "kb" || r.Ref != "" || r.PurgedBy == "" || !strings.Contains(string(r.Doc), "store purged") {
		t.Errorf("store purge: %+v (found %v)", r, ok)
	}
	r, ok = got[liveGone]
	if !ok || r.Store != store.SourceRepo || r.Ref != liveRefLocal || r.PurgedBy != "" {
		t.Errorf("live removal: %+v (found %v)", r, ok)
	} else if back, derr := row.Decode(r.Doc); derr != nil || back.Table != schema.TableMemories || back.ID.String() != liveGone {
		t.Errorf("live content not a row: %v %s", derr, r.Doc)
	}
	for _, id := range []string{alive, other} {
		if _, bad := got[id]; bad {
			t.Errorf("%s listed, but it is not gone (or never rides live)", id)
		}
	}

	// through the op: show names the live ref, restore stays E_PURGED (DQ4)
	out, err := run(t, a, opstrash.OpShow, `{"id":"`+liveGone+`","from_history":true}`, true)
	if err != nil || !strings.Contains(out, "on "+liveRefLocal) || !strings.Contains(out, "live only") {
		t.Errorf("trash show --from-history: %s %v", out, err)
	}
	out, err = run(t, a, opstrash.OpList, `{"from_history":true,"to":"kb"}`, true)
	if err != nil || !strings.Contains(out, storeGone) || strings.Contains(out, repoGone) {
		t.Errorf("trash list --from-history --to kb: %s %v", out, err)
	}
}
