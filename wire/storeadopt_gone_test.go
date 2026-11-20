package wire

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/row"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// Fixed ids of the live tasks the repo deleted, trashed or purged (D-737).
const (
	goneTrashed   = "tsk_01a1f80a00007000800000000000a001" // lore task delete: in the trash on the live ref
	goneTombstone = "tsk_01a1f80a00007000800000000000a002" // lore trash purge: a tombstone locally, still open on the tracking copy
	gonePurged    = "tsk_01a1f80a00007000800000000000a003" // lore live purge: off the local ref, still open on the tracking copy
	goneOpenTrk   = "tsk_01a1f80a00007000800000000000a004" // open, only on the tracking copy: adopted as before
)

// Live stamps of the gone tasks: the open copies are older than the
// deletion.
const (
	goneOpenHLC = "2026-11-05T09:00:00.000Z/0000/" + otherActor
	goneDelHLC  = "2026-11-06T09:00:00.000Z/0000/" + otherActor
)

// liveTask is a v0.1.17 live document of an open task; extra is spliced
// into the object (e.g. a deleted_at), "" for none.
func liveTask(id, title, stamp, extra string) string {
	return `{"_table":"tasks","_v":1,"attempts":0,"claim_epoch":0,"commitment":"accepted","created_at":"2026-11-03T10:00:00.000000000Z",` + extra +
		`"id":"` + id + `","priority":"high","project_id":"` + adoptProject + `","status":"todo","title":"` + title +
		`","updated_at":"2026-11-03T10:00:00.000000000Z","_live":{"hlc":"` + stamp + `"}}`
}

// liveTombstone is a live tombstone of a task as lore trash purge leaves
// it: the row's deletion marker plus the deletion in the _live block.
func liveTombstone(id, stamp string) string {
	return `{"_deleted":true,"_table":"tasks","_v":1,"id":"` + id + `","_live":{"deleted":true,"hlc":"` + stamp + `"}}`
}

// liveTaskPath is a task's path on the live ref (project at the repo top).
func liveTaskPath(id string) string { return liveRootFolder + "/tasks/" + id + ".json" }

// addLiveFiles writes files on top of what ref (in repo) holds, as one new
// commit whose parent is that ref's commit (writeLiveRef starts from an
// empty tree); a file whose content is "" is removed.
func addLiveFiles(t *testing.T, repo, ref string, files map[string]string) {
	t.Helper()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "index"),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}
	gitIn(t, repo, env, "read-tree", ref)
	for path, content := range files {
		if content == "" {
			gitIn(t, repo, env, "update-index", "--force-remove", path)
			continue
		}
		tmp := filepath.Join(t.TempDir(), "blob")
		if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		oid := gitIn(t, repo, env, "hash-object", "-w", tmp)
		gitIn(t, repo, env, "update-index", "--add", "--cacheinfo", "100644,"+oid+","+path)
	}
	tree := gitIn(t, repo, env, "write-tree")
	commit := gitIn(t, repo, env, "commit-tree", tree, "-p", ref, "-m", "live")
	gitIn(t, repo, env, "update-ref", ref, commit)
}

// goneLiveRepo is adoptRepo plus the gone tasks, with the history a real
// clone has: the pushed live ref (the merge base) holds the three tasks
// open; since then a teammate added a fourth open task (the tracking copy
// only), and this clone, not pushed yet, trashed the first, purged the
// second from the trash (a tombstone, its id on purged.json) and purged
// the third from live (file gone, id on purged.json).
func goneLiveRepo(t *testing.T) string {
	t.Helper()
	repo := adoptRepo(t)
	addLiveFiles(t, repo, liveRefLocal, map[string]string{
		liveTaskPath(goneTrashed):   liveTask(goneTrashed, "deleted, in the trash", goneOpenHLC, `"deleted_at":null,`),
		liveTaskPath(goneTombstone): liveTask(goneTombstone, "purged from the trash", goneOpenHLC, ""),
		liveTaskPath(gonePurged):    liveTask(gonePurged, "purged from live", goneOpenHLC, ""),
	})
	gitIn(t, repo, nil, "update-ref", liveRefTracking, liveRefLocal) // pushed: the merge base
	addLiveFiles(t, repo, liveRefTracking, map[string]string{
		liveTaskPath(goneOpenTrk): liveTask(goneOpenTrk, "still open", goneOpenHLC, ""),
	})
	addLiveFiles(t, repo, liveRefLocal, map[string]string{
		liveTaskPath(goneTrashed):   liveTask(goneTrashed, "deleted, in the trash", goneDelHLC, `"deleted_at":"2026-11-06T09:00:00.000000000Z",`),
		liveTaskPath(goneTombstone): liveTombstone(goneTombstone, goneDelHLC),
		liveTaskPath(gonePurged):    "",
		live.PurgedPath:             `{"ids":["` + goneTombstone + `","` + gonePurged + `"]}`,
	})
	return repo
}

// goneUnrelatedRepo is goneLiveRepo's sides with no common history (a
// remote rewritten by a compaction): the tracking copy still holds the
// purged task open; readLiveRows merges with no base, and the purge list
// alone keeps it gone.
func goneUnrelatedRepo(t *testing.T) string {
	t.Helper()
	repo := adoptRepo(t)
	addLiveFiles(t, repo, liveRefLocal, map[string]string{live.PurgedPath: `{"ids":["` + gonePurged + `"]}`})
	writeLiveRef(t, repo, liveRefTracking, map[string]string{
		liveTaskPath(gonePurged): liveTask(gonePurged, "purged from live", goneOpenHLC, ""),
	})
	return repo
}

// TestReadLiveRowsAppliesPurges (devco D-737): the live rows every bulk
// reader gets are the refs' current rows, the local ref and its tracking
// copy resolved over their merge base as lore sync integrates them. Not
// pushed yet: a trash stays trashed and a tombstone stays a deletion
// although the tracking copy still holds both open, a purged id is gone
// from every ref, and the teammate's new open task is read. With no common
// history the purge list alone keeps the purged task gone. Before, the
// pair was merged with no base and purged.json unread: the stale open
// copies overruled the trash and brought the purged task back.
func TestReadLiveRowsAppliesPurges(t *testing.T) {
	t.Parallel()
	read := func(repo string) map[string]row.Row {
		rows, err := readLiveRows(background(), repo, []string{liveRefLocal, liveRefTracking}, liveRootFolder, liveTables(), false)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]row.Row{}
		for _, lr := range rows {
			got[lr.row.ID.String()] = lr.row
		}
		return got
	}
	got := read(goneLiveRepo(t))
	if _, back := got[gonePurged]; back {
		t.Fatalf("the live-purged task came back from the tracking copy: %v", slices.Sorted(maps.Keys(got)))
	}
	if r, ok := got[goneTombstone]; !ok || !r.Deleted {
		t.Fatalf("the trash-purged task is not read as its tombstone: %+v", r)
	}
	if r, ok := got[goneTrashed]; !ok || r.Deleted || !trashed(r) {
		t.Fatalf("the trashed task is not read as in the trash: %+v", r)
	}
	for _, id := range []string{goneOpenTrk, adoptLiveTask} {
		if r, ok := got[id]; !ok || r.Deleted || trashed(r) {
			t.Fatalf("%s is not read as an open live row: %+v", id, r)
		}
	}
	if _, back := read(goneUnrelatedRepo(t))[gonePurged]; back {
		t.Fatal("with no common history the purged task came back from the tracking copy")
	}
}

// TestStoreAdoptSkipsGoneLiveRows (devco D-737): adopt never counts,
// copies or lists as a near-duplicate a live row the repo deleted, trashed
// or purged, without a lore live compact first. The owner's repo had
// deleted its open live tasks and run lore trash purge and lore live
// purge; adopt still said "from the repo's live ref: 38" and copied them.
// The dry run and the run count the open live rows only and name the gone
// ones as left in the repo; the folder never holds a gone id; the gone
// rows' tombstone and trash copy stay on the live ref (no stale clone
// pushes them back), and only the adopted rows leave it.
func TestStoreAdoptSkipsGoneLiveRows(t *testing.T) {
	t.Parallel()
	repo, storeDir, _, _ := folderStoreRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpFolderAdd, `{"store":"northwind","folder":"client_x"}`, true); err != nil {
		t.Fatal(err)
	}
	src := goneLiveRepo(t)
	in := `{"target":"northwind/client_x","from":"` + filepath.ToSlash(src) + `","remove_source":true`
	const leftGone = "tasks: 2 rows (" + adoptGoneWhy + ")" // the trashed task and the tombstone; the live-purged one is gone entirely

	dry, err := adoptRun(t, a, in+`,"dry_run":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	// live: the live-only task, the mission, the newer live memory, the tracking-only task
	if dry.Live != 4 || dry.LiveRemoved != 3 || !slices.Contains(dry.Left, leftGone) || len(dry.Duplicates) != 0 {
		t.Fatalf("dry run: %+v\n%s", dry, dry.Text())
	}
	out, err := adoptRun(t, a, in+`}`, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Live != 4 || out.LiveRemoved != 3 || !slices.Contains(out.Left, leftGone) {
		t.Fatalf("run: %+v\n%s", out, out.Text())
	}
	for p := range treeSums(t, storeDir) {
		for _, id := range []string{goneTrashed, goneTombstone, gonePurged} {
			if strings.Contains(p, id) {
				t.Fatalf("the folder holds the gone task %s: %s", id, p)
			}
		}
	}
	if _, in := treeSums(t, storeDir)["client_x/tasks/"+goneOpenTrk+".json"]; !in {
		t.Fatalf("the open tracking-only task was not adopted: %v", treeSums(t, storeDir))
	}
	left := liveRefPaths(t, src)
	for _, id := range []string{goneTrashed, goneTombstone} {
		if !slices.Contains(left, liveTaskPath(id)) {
			t.Fatalf("%s left the live ref: %v", id, left)
		}
	}
	if slices.Contains(left, liveTaskPath(adoptLiveTask)) {
		t.Fatalf("the adopted live task is still on the live ref: %v", left)
	}
}
