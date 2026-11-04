package wire

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// Live-ref fixture stamps: far past (older than any row the test writes
// through the core) and far future (newer than any such row).
const (
	liveOld    = "2001-01-01T00:00:00.000Z/0000/" + otherActor
	liveNewer  = "2099-01-01T00:00:00.000Z/0000/" + otherActor
	liveNewest = "2099-06-01T00:00:00.000Z/0000/" + otherActor
)

// Fixed ids of rows that exist only on the live ref.
const (
	liveOnlyMem    = "mem_01a1a59b891c70b789144cd3e711fb01"
	liveTrackedMem = "mem_01a1a59b891c70b789144cd3e711fb02"
)

// gitIn runs git in dir with extra environment and returns trimmed stdout.
func gitIn(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	out, err := gitRun{dir: dir, env: append(os.Environ(), env...), args: args}.output(background())
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// writeLiveRef points ref (in repo) at a commit whose tree holds files
// (slash path → content), the way v0.1.17 kept its live ref: never in the
// work tree or HEAD's history.
func writeLiveRef(t *testing.T, repo, ref string, files map[string]string) {
	t.Helper()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "index"),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}
	for path, content := range files {
		tmp := filepath.Join(t.TempDir(), "blob")
		if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		oid := gitIn(t, repo, env, "hash-object", "-w", tmp)
		gitIn(t, repo, env, "update-index", "--add", "--cacheinfo", "100644,"+oid+","+path)
	}
	tree := gitIn(t, repo, env, "write-tree")
	commit := gitIn(t, repo, env, "commit-tree", tree, "-m", "live")
	gitIn(t, repo, env, "update-ref", ref, commit)
}

// liveMemory is a v0.1.17 live document of a memory: a `_v: 1` row plus
// the `_live` block carrying its last-edit stamp.
func liveMemory(id, project, body, stamp string) string {
	return `{"_table":"memories","_v":1,"body":"` + body + `","created_at":"2026-11-04T08:09:56.252002659Z",` +
		`"id":"` + id + `","kind":"retrieved","project_id":"` + project + `","source_kind":"manual",` +
		`"updated_at":"2026-11-04T08:09:56.252002894Z","_live":{"hlc":"` + stamp + `"}}`
}

// livePath is a memory's path on the live ref (project at the repo top).
func livePath(id string) string {
	return liveRootFolder + "/" + schema.TableMemories + "/" + id + ".json"
}

// fieldString reads a string field of a row file ("" when absent).
func fieldString(t *testing.T, f map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := f[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%s is not a string: %s", key, raw)
	}
	return s
}

// TestMigrateLiveRepo: lore migrate run brings the repo's v0.1.17 live-ref
// rows of core-owned tables into the repo store. Matrix of the store side:
// absent (written as _v 2 with the live HLC), present and older (merged:
// the newer live field wins), present and newer (left alone), tombstoned
// (never resurrected, even by a newer live copy); plus a row on both the
// local live ref and its tracking copy (merged: the newer side wins), the
// ref's own `_live/` files and a non-core table (ignored). The dry run
// changes nothing, the note names the rows until the run, a second run
// is a no-op.
func TestMigrateLiveRepo(t *testing.T) {
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	project := projectID(t, a)
	add := func(body string) string {
		out, err := run(t, a, opsmemory.OpAdd, `{"body":"`+body+`"}`, false)
		if err != nil {
			t.Fatal(err)
		}
		return idOf(t, out)
	}
	older, newer, gone := add("store older"), add("store newer"), add("store gone")
	if _, err := run(t, a, opsmemory.OpDelete, `{"id":"`+gone+`"}`, true); err != nil {
		t.Fatal(err)
	}
	goneBefore := rowFile(t, memPath(repo, gone))
	if len(goneBefore["deleted_at"]) == 0 { // delete is a soft delete: the row goes to the trash (deleted_at set)
		t.Fatalf("fixture: %s is not in the trash: %v", gone, goneBefore)
	}
	newerBefore := rowFile(t, memPath(repo, newer))

	writeLiveRef(t, repo, liveRefLocal, map[string]string{
		livePath(liveOnlyMem):    liveMemory(liveOnlyMem, project, "live only", liveNewer),
		livePath(older):          liveMemory(older, project, "live wins", liveNewer),
		livePath(newer):          liveMemory(newer, project, "stale live", liveOld),
		livePath(gone):           liveMemory(gone, project, "back from the dead", liveNewest),
		livePath(liveTrackedMem): liveMemory(liveTrackedMem, project, "local copy", liveNewer),
		"_live/meta.json":        `{"not":"a row"}`,
		liveRootFolder + "/not_a_core_table/x.json": `{"_table":"not_a_core_table","_v":1,"id":"x"}`,
	})
	writeLiveRef(t, repo, liveRefTracking, map[string]string{
		livePath(liveTrackedMem): liveMemory(liveTrackedMem, project, "remote copy", liveNewest),
	})

	note := a.LiveNote(background())
	if !strings.Contains(note, "3 memories") || !strings.Contains(note, MigrateCommand) {
		t.Fatalf("note before the run: %q", note)
	}

	got, err := a.MigrateLive(background(), true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		schema.TableMemories + "/" + liveOnlyMem,
		schema.TableMemories + "/" + older,
		schema.TableMemories + "/" + liveTrackedMem,
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("dry run lines: %v, want %v", got, want)
	}
	if _, serr := os.Stat(memPath(repo, liveOnlyMem)); !os.IsNotExist(serr) {
		t.Fatalf("the dry run wrote a row (%v)", serr)
	}

	if _, err = run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}

	only := rowFile(t, memPath(repo, liveOnlyMem))
	if string(only[row.KeyVersion]) != "2" || fieldString(t, only, row.KeyHLC) != liveNewer || fieldString(t, only, "body") != "live only" {
		t.Fatalf("live-only row: %v", only)
	}
	if _, has := only[liveBlockKey]; has {
		t.Fatal("the _live block reached the row file")
	}
	merged := rowFile(t, memPath(repo, older))
	if fieldString(t, merged, "body") != "live wins" || fieldString(t, merged, row.KeyHLC) != liveNewer {
		t.Fatalf("older store row not merged with the newer live copy: %v", merged)
	}
	if after := rowFile(t, memPath(repo, newer)); !jsonEqual(after, newerBefore) {
		t.Fatalf("a newer store row changed: %v → %v", newerBefore, after)
	}
	if after := rowFile(t, memPath(repo, gone)); !jsonEqual(after, goneBefore) {
		t.Fatalf("a trashed row changed (resurrected?): %v → %v", goneBefore, after)
	}
	if _, err = run(t, a, opsmemory.OpShow, `{"id":"`+gone+`"}`, false); err == nil {
		t.Fatal("the trashed memory is visible again")
	}
	tracked := rowFile(t, memPath(repo, liveTrackedMem))
	if fieldString(t, tracked, "body") != "remote copy" || fieldString(t, tracked, row.KeyHLC) != liveNewest {
		t.Fatalf("local vs tracking copy not merged newest-wins: %v", tracked)
	}
	if _, serr := os.Stat(filepath.Join(repo, ".lore", "data", "not_a_core_table")); !os.IsNotExist(serr) {
		t.Fatal("a non-core table was brought in")
	}

	if note = a.LiveNote(background()); note != "" {
		t.Fatalf("note after the run: %q", note)
	}
	if again, aerr := a.MigrateLive(background(), false); aerr != nil || len(again) != 0 {
		t.Fatalf("second run: %v %v", again, aerr)
	}
}

// jsonEqual compares two decoded row files key by key.
func jsonEqual(x, y map[string]json.RawMessage) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if string(y[k]) != string(v) {
			return false
		}
	}
	return true
}

// TestLiveRowPaths: the live-tree path rules (row files are
// <folder…>/<table>/<id>.json; `_live/` is the ref's own; `_root` is the
// top project; nested folders keep every segment).
func TestLiveRowPaths(t *testing.T) {
	for _, c := range []struct {
		path   string
		table  row.Table
		ok     bool
		folder string
	}{
		{"_root/memories/m.json", "memories", true, "_root"},
		{"apps/web/tasks/t.json", "tasks", true, "apps/web"},
		{"_live/meta/x.json", "", false, "_live"},
		{"_other/memories/m.json", "", false, "_other"},
		{"memories/m.json", "", false, ""},
		{"_root/memories/m.txt", "", false, "_root"},
		{"çlient/memories/m.json", "memories", true, "çlient"},
		{"", "", false, ""},
	} {
		tb, ok := liveRowTable(c.path)
		if ok != c.ok || (ok && tb != c.table) {
			t.Errorf("liveRowTable(%q) = %q %v, want %q %v", c.path, tb, ok, c.table, c.ok)
		}
		if f := liveRowFolder(c.path); f != c.folder {
			t.Errorf("liveRowFolder(%q) = %q, want %q", c.path, f, c.folder)
		}
	}
}

// TestDecodeLiveRow: the `_live` block's hlc becomes `_hlc` and the block
// is dropped; a block without hlc or not an object leaves the v1 upcast's
// stamp (from updated_at); text
// that is not a JSON object is E_INVALID_INPUT.
func TestDecodeLiveRow(t *testing.T) {
	// With no live stamp the v1 upcast derives the HLC from updated_at
	// (hlc.FromUpdate; no updated_by, so no actor).
	const fromUpdate = "2026-11-04T08:09:56.252Z/0000/"
	base := `"_table":"memories","_v":1,"id":"` + fixtureMem + `","body":"b","project_id":"prj_01a1a5aca7f57659be7320c5f4b70009","updated_at":"2026-11-04T08:09:56.252002894Z"`
	for _, c := range []struct {
		name, doc, hlc string
		bad            bool
	}{
		{"stamp", `{` + base + `,"_live":{"hlc":"` + liveOld + `"}}`, liveOld, false},
		{"no block", `{` + base + `}`, fromUpdate, false},
		{"block without hlc", `{` + base + `,"_live":{"by":"x"}}`, fromUpdate, false},
		{"block not an object", `{` + base + `,"_live":"x"}`, fromUpdate, false},
		{"not json", `nope`, "", true},
		{"array", `[1]`, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, err := decodeLiveRow([]byte(c.doc))
			if c.bad {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.HLC.String() != c.hlc {
				t.Fatalf("hlc %q, want %q", r.HLC.String(), c.hlc)
			}
			if _, has := r.Fields[liveBlockKey]; has {
				t.Fatal("the _live block survived")
			}
		})
	}
}

// TestMergeRowNewerWins: two copies of one live row merge newer-wins per
// field with no conflict markers (they are versions along one history),
// keep a field only one side holds, and never undo a tombstone, whatever
// the argument order.
func TestMergeRowNewerWins(t *testing.T) {
	project := "prj_01a1a5aca7f57659be7320c5f4b70009"
	decode := func(doc string) row.Row {
		t.Helper()
		r, err := decodeLiveRow([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	old := decode(liveMemory(fixtureMem, project, "old text", liveOld))
	nw := decode(liveMemory(fixtureMem, project, "new text", liveNewer))
	extra := old
	extra.Fields = maps.Clone(old.Fields)
	tv, err := value.TextOf("only on the old side")
	if err != nil {
		t.Fatal(err)
	}
	extra.Fields["title"] = tv
	gone := nw
	gone.Deleted = true
	for _, c := range []struct {
		name     string
		a, b     row.Row
		body     string
		deleted  bool
		hasTitle bool
	}{
		{"newer second", old, nw, "new text", false, false},
		{"newer first", nw, old, "new text", false, false},
		{"one-sided field kept", extra, nw, "new text", false, true},
		{"newer tombstone wins", old, gone, "", true, false},
		{"tombstone first", gone, old, "", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, err := mergeRow(c.a, c.b)
			if err != nil {
				t.Fatal(err)
			}
			if m.Deleted != c.deleted {
				t.Fatalf("deleted = %v, want %v", m.Deleted, c.deleted)
			}
			if c.deleted {
				return
			}
			body, _ := m.Fields["body"].(value.Text)
			if body.String() != c.body || strings.Contains(body.String(), "<<<<<<<") {
				t.Fatalf("body %q, want %q", body.String(), c.body)
			}
			if _, has := m.Fields["title"]; has != c.hasTitle {
				t.Fatalf("title kept = %v, want %v", has, c.hasTitle)
			}
		})
	}
}
