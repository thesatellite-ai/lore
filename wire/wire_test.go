package wire

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Fixed ids of the fixtures (any 32 hex digits form a valid id).
const (
	otherActor  = "act_9a2a5805459c73998a43b50c959507da"
	fixtureMem  = "mem_01a1a59b891c70b789144cd3e711fb92"
	folderStore = "sto_01a1a5aca7f57659be7320c5f4b70002"
)

// gitRepo makes an empty git work tree.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return dir
}

// withProject opens a and writes the repo's project row (what `lore init`
// does), returning nothing: Project() then finds it.
func withProject(t *testing.T, a *App) {
	t.Helper()
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	name, err := value.TextOf("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Create(background(), store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": name}); err != nil {
		t.Fatal(err)
	}
}

// run runs the named operation through the App's Run path with a JSON
// input, returning the output JSON.
func run(t *testing.T, a *App, name, input string, yes bool) (string, error) {
	t.Helper()
	e, ok := a.Registry().Lookup(name)
	if !ok {
		t.Fatalf("no op %s", name)
	}
	res, err := a.Run(background(), e, op.JSONDecoder{Body: []byte(input)}, op.Caller{Yes: yes})
	if err != nil {
		return "", err
	}
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(b), nil
}

// idOf reads the "id" of a write output.
func idOf(t *testing.T, out string) string {
	t.Helper()
	var w opsmemory.WriteOut
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatal(err)
	}
	return w.ID
}

// rowFile reads a repo row file as a JSON object.
func rowFile(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// memPath is a memory's row file in a repo.
func memPath(repo, id string) string {
	return filepath.Join(repo, ".lore", "data", schema.TableMemories, id+".json")
}

// TestMemoryEndToEnd drives the memory operations through the real
// backends, cache and engine: a v2 row file with _hlc, list, search, edit,
// delete (tombstone), and a fresh App sees the same rows.
func TestMemoryEndToEnd(t *testing.T) {
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)

	out, err := run(t, a, opsmemory.OpAdd, `{"text":"Retry the gateway twice on 502"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	id := idOf(t, out)
	f := rowFile(t, memPath(repo, id))
	if string(f["_v"]) != "2" || len(f["_hlc"]) == 0 {
		t.Fatalf("row file is not v2 with _hlc: %v", f)
	}
	if out, err = run(t, a, opsmemory.OpSearch, `{"query":"gateway"}`, false); err != nil || !strings.Contains(out, id) {
		t.Fatalf("search: %s %v", out, err)
	}
	if _, err = run(t, a, opsmemory.OpEdit, `{"id":"`+id+`","body":"Retry three times"}`, false); err != nil {
		t.Fatal(err)
	}
	if out, err = run(t, a, opsmemory.OpSearch, `{"query":"gateway"}`, false); err != nil || strings.Contains(out, id) {
		t.Fatalf("search after edit still finds the old text: %s %v", out, err)
	}
	if _, err = run(t, a, opsmemory.OpDelete, `{"id":"`+id+`"}`, false); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("delete without yes: %v", err)
	}
	if _, err = run(t, a, opsmemory.OpDelete, `{"id":"`+id+`"}`, true); err != nil {
		t.Fatal(err)
	}
	if f = rowFile(t, memPath(repo, id)); string(f["_deleted"]) != "true" {
		t.Fatalf("no tombstone: %v", f)
	}
	b := newTestApp(t, repo)
	if out, err = run(t, b, opsmemory.OpList, `{}`, false); err != nil || strings.Contains(out, id) {
		t.Fatalf("a deleted memory is listed: %s %v", out, err)
	}
}

// TestSecretNotWritten: a key-shaped body (built at run time) is refused
// for a caller that cannot confirm, and no row file appears.
func TestSecretNotWritten(t *testing.T) {
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	key := "AKIA" + strings.Repeat("Q", 16)
	_, err := run(t, a, opsmemory.OpAdd, `{"body":"deploy with `+key+`"}`, false)
	if !errs.Has(err, errs.NeedsConfirm) && !errs.Has(err, errs.SecretDetected) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(repo, ".lore", "data", schema.TableMemories)); len(entries) != 0 {
		t.Fatalf("a refused memory was written: %v", entries)
	}
}

// TestFolderStoreSharedWithoutSync: two repos attach one file:// store;
// a memory added from one is listed by the other with no sync step, and
// deleting every cache file loses nothing.
func TestFolderStoreSharedWithoutSync(t *testing.T) {
	storeDir := t.TempDir()
	sid, err := ids.Parse[ids.Store](folderStore)
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Init(storeDir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	yaml := "stores:\n  - name: shared\n    url: file://" + filepath.ToSlash(storeDir) + "\n"
	repos := []string{gitRepo(t), gitRepo(t)}
	for _, r := range repos {
		if err = os.MkdirAll(filepath.Join(r, ".lore"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(r, filepath.FromSlash(attach.FileName)), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := newTestApp(t, repos[0])
	withProject(t, a)
	out, err := run(t, a, opsmemory.OpAdd, `{"body":"folder stores need no sync","to":"shared"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	id := idOf(t, out)

	home := t.TempDir()
	actor, err := ids.Parse[ids.Actor](otherActor)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Options{RepoDir: repos[1], Home: home, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	if out, err = run(t, b, opsmemory.OpSearch, `{"query":"sync"}`, false); err != nil || !strings.Contains(out, id) {
		t.Fatalf("other repo does not see it: %s %v", out, err)
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	removeCaches(t, repos[1], home)
	c, err := New(Options{RepoDir: repos[1], Home: home, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if out, err = run(t, c, opsmemory.OpSearch, `{"query":"sync"}`, false); err != nil || !strings.Contains(out, id) {
		t.Fatalf("lost after deleting the caches: %s %v", out, err)
	}
}

// removeCaches deletes the repo cache and every per-store cache file.
func removeCaches(t *testing.T, repo, home string) {
	t.Helper()
	var files []string
	for _, pattern := range []string{filepath.Join(repo, filepath.FromSlash(RepoCacheFile)) + "*", filepath.Join(home, cacheDir, cacheStoresDir, "*", "*", "*")} {
		got, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, got...)
	}
	if len(files) == 0 {
		t.Fatal("no cache files found to delete")
	}
	for _, f := range files {
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
	}
}

// v1Memory is a v0.1.17 memory row file; by is its created_by_actor_id
// and its updated_by when non-empty.
func v1Memory(project, by string) string {
	updatedBy := ""
	if by != "" {
		updatedBy = `,"updated_by":"` + by + `"`
	}
	return `{"_table":"memories","_v":1,"archived_at":null,"body":"old note","created_at":"2026-11-04T08:09:56.252002659Z",` +
		`"id":"` + fixtureMem + `","kind":"retrieved","project_id":"` + project + `","source_kind":"manual",` +
		`"updated_at":"2026-11-04T08:09:56.252002894Z"` + updatedBy + `}`
}

// writeV1 puts a v1 memory into repo's data folder.
func writeV1(t *testing.T, repo, doc string) {
	t.Helper()
	dir := filepath.Join(repo, ".lore", "data", schema.TableMemories)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fixtureMem+".json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// projectID returns the repo's project id.
func projectID(t *testing.T, a *App) string {
	t.Helper()
	p, err := a.Project(background())
	if err != nil {
		t.Fatal(err)
	}
	return p.String()
}

// TestFormatQuestion (DOCS O6): v1 rows by another writer make the first
// 0.1.18 write ask (E_NEEDS_CONFIRM naming lore migrate run); after the
// migration, writes go through. Rows by the caller alone never ask.
func TestFormatQuestion(t *testing.T) {
	for _, c := range []struct {
		name string
		by   string
		ask  bool
	}{{"teammate wrote v1", otherActor, true}, {"only my rows", testActor, false}, {"unknown writer", "", false}} {
		t.Run(c.name, func(t *testing.T) {
			repo := gitRepo(t)
			seed := newTestApp(t, repo)
			withProject(t, seed)
			project := projectID(t, seed)
			if err := seed.Close(); err != nil {
				t.Fatal(err)
			}
			// The project row seed wrote is v2: remove it so the repo looks
			// like v0.1.17 (only v1 rows), then add it back as v1.
			removeV2Projects(t, repo, project)
			writeV1(t, repo, v1Memory(project, c.by))

			a := newTestApp(t, repo)
			_, err := run(t, a, opsmemory.OpAdd, `{"body":"new note"}`, false)
			if c.ask != errs.Has(err, errs.NeedsConfirm) {
				t.Fatalf("ask = %v, err = %v", c.ask, err)
			}
			if !c.ask {
				return
			}
			if !strings.Contains(errs.EnvelopeOf(err).Error.Hint, MigrateCommand) {
				t.Fatalf("hint does not name %s: %+v", MigrateCommand, errs.EnvelopeOf(err))
			}
			if _, err := run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
				t.Fatal(err)
			}
			if _, err := run(t, a, opsmemory.OpAdd, `{"body":"new note"}`, false); err != nil {
				t.Fatalf("after migrate: %v", err)
			}
		})
	}
}

// removeV2Projects rewrites the project row as a v1 file (no _hlc, no
// envelope actors), so the repo holds only v0.1.17 rows.
func removeV2Projects(t *testing.T, repo, project string) {
	t.Helper()
	p := filepath.Join(repo, ".lore", "data", schema.TableProjects, project+".json")
	doc := `{"_table":"projects","_v":1,"archived_at":null,"created_at":"2026-11-04T08:09:55.851510991Z","id":"` + project +
		`","name":"demo","origin_url":null,"updated_at":"2026-11-04T08:09:55.851510991Z"}`
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestMigrateChangesOnlyTheFormat: migrate.run rewrites a v1 memory as v2
// and the only keys that change are _v and _hlc; a dry run changes nothing;
// a second run finds nothing to do.
func TestMigrateChangesOnlyTheFormat(t *testing.T) {
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	writeV1(t, repo, v1Memory(projectID(t, a), ""))
	before := rowFile(t, memPath(repo, fixtureMem))

	out, err := run(t, a, opsmigrate.OpRun, `{"dry_run":true}`, false)
	if err != nil || !strings.Contains(out, fixtureMem) {
		t.Fatalf("dry run: %s %v", out, err)
	}
	if !maps.EqualFunc(before, rowFile(t, memPath(repo, fixtureMem)), func(x, y json.RawMessage) bool { return string(x) == string(y) }) {
		t.Fatal("a dry run changed the file")
	}
	if _, err = run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	after := rowFile(t, memPath(repo, fixtureMem))
	if string(after["_v"]) != "2" || len(after["_hlc"]) == 0 {
		t.Fatalf("not rewritten: %v", after)
	}
	for k, v := range before {
		if k != "_v" && string(after[k]) != string(v) {
			t.Fatalf("%s changed: %s → %s", k, v, after[k])
		}
	}
	for k := range after {
		if _, had := before[k]; !had && k != "_hlc" {
			t.Fatalf("new key %s", k)
		}
	}
	if out, err = run(t, a, opsmigrate.OpRun, `{}`, false); err != nil || !strings.Contains(out, `"count":0`) {
		t.Fatalf("second run: %s %v", out, err)
	}
}

// TestCanonical: the scp form becomes ssh://, a folder becomes ?folder=,
// a password is refused without echoing it, a file url stays a file url.
func TestCanonical(t *testing.T) {
	secret := "pw" + strings.Repeat("x", 6)
	for _, c := range []struct {
		in   attach.Store
		want string
		code errs.Code
	}{
		{attach.Store{Name: "a", URL: "git@github.com:acme/kb.git"}, "ssh://git@github.com/acme/kb.git", ""},
		{attach.Store{Name: "a", URL: "https://example.com/acme/kb.git", Folder: "acme-hub"}, "https://example.com/acme/kb.git?folder=acme-hub", ""},
		{attach.Store{Name: "a", URL: "file:///srv/kb", Folder: "org"}, "file:///srv/kb?folder=org", ""},
		{attach.Store{Name: "a", URL: "https://bob:" + secret + "@example.com/kb.git"}, "", errs.URLUserinfo},
	} {
		got, err := canonical(c.in)
		if c.code != "" {
			if !errs.Has(err, c.code) || strings.Contains(err.Error(), secret) {
				t.Fatalf("%s: want %s without the password, got %v", c.in.URL, c.code, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("%s: got %q %v, want %q", c.in.URL, got, err, c.want)
		}
	}
}
