//go:build teame2e

package teame2e

// P2 — live rows and landing (LORE_TEAM_SPEC.md F1–F6, F8, E1–E4, E6,
// cookbook 1, 2, 7, 16, side effects 11, 14, 22, 23, 27, 28, 34).

import (
	"strconv"
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// t0 is the fixed start of every clock-driven scenario.
var t0 = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

// maxAge is the default live.max_age (TEAM-026).
const maxAge = 7 * 24 * time.Hour

// autopushOff is the env that keeps one command off the network (§1).
const autopushOff = th.EnvAutopush + "=0"

// pushLiveFile writes one file onto a remote's live ref the way a stranger
// with push access could (no lore involved).
func pushLiveFile(t *testing.T, c *th.Clone, ref, path, body string) {
	t.Helper()
	c.Git("fetch", "-q", "origin", "+"+ref+":refs/harness/live")
	idx := c.Dir + "/.git/harness-index"
	g := c.With("GIT_INDEX_FILE=" + idx)
	g.Git("read-tree", "refs/harness/live")
	c.WriteFile(".git/harness-blob", body)
	b := g.Git("hash-object", "-w", ".git/harness-blob")
	g.Git("update-index", "--add", "--cacheinfo", "100644,"+b+","+path)
	tree := g.Git("write-tree")
	sha := g.Git("commit-tree", tree, "-p", "refs/harness/live", "-m", "hand edit")
	c.Git("push", "-q", "origin", sha+":"+ref)
}

// ---------------------------------------------------------------- F1 lookup

// F1 table row "in both → edits the live row"; fixed rule 4: live wins over
// any branch copy, whatever branch the reader is on.
func TestF01_LiveCopyWinsOverBranchCopy(t *testing.T) {
	rt := newRepoTeam(t, th.Flag("no-live", "task", "add"), th.Flag("live", "task", "edit"))
	id := rt.addTask(t, rt.c1, "title A", "--no-live")
	rt.c1.CommitAll("task A")
	rt.c1.Git("push", "-q", "origin", "main")
	rt.c2.Git("pull", "-q")
	rt.c2.Git("checkout", "-q", "-b", "feat/x")

	rt.c1.MustLore("task", "edit", id, "--title", "title B", "--live")
	rt.c2.MustLore("task", "show", id).Says(t, "title B").Never(t, "title A")
	if body, _ := rt.c2.ReadFile(mainRowPath("", tblTasks, id)); !strings.Contains(body, "title A") {
		t.Fatalf("the branch copy must stay untouched: %s", body)
	}

	rt.c2.MustLore("task", "edit", id, "--priority", "high")
	if r := mustRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id)); rawString(t, r, "priority") != "high" || r.Title != "title B" {
		t.Fatalf("an edit of a live row must edit the live row: %+v", r)
	}
	if st := rt.c2.Status(); st != "" {
		t.Fatalf("editing a live row must not touch the working tree: %q", st)
	}
}

// F1 step 2: a tombstone stops the search; the old branch copy never comes back.
func TestF01_TombstoneStopsLookup(t *testing.T) {
	rt := newRepoTeam(t, th.Flag("no-live", "task", "add"), th.Flag("live", "task", "edit"))
	id := rt.addTask(t, rt.c1, "doomed", "--no-live")
	rt.c1.CommitAll("task")
	rt.c1.Git("push", "-q", "origin", "main")
	rt.c2.Git("pull", "-q")
	rt.c2.Git("checkout", "-q", "-b", "feat/old")

	rt.c1.MustLore("task", "edit", id, "--title", "doomed (live)", "--live")
	// Ported to DQ8 / FA-DEL-1: a plain `delete --confirm` is a soft delete
	// (the row goes to the trash with its body), so the body-less tombstone
	// this case checks is what `--purge --confirm` writes.
	rt.c1.MustLore("task", "delete", id, "--purge", "--confirm")
	tomb := mustRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id))
	if tomb.Live == nil || !tomb.Live.Deleted || tomb.Title != "" || tomb.Body != "" {
		t.Fatalf("a deleted live row is a tombstone without its body (§3.3): %+v", tomb)
	}
	rt.c2.Lore("task", "show", id).Refused(t, "deleted")
}

// F1 edge case + side effect 12: a row that landed is "not found" on an
// older branch, and the error points at `lore sync peek --ref origin/main`.
func TestF01_LandedRowOnOlderBranchHintsPeek(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("land"))
	rt.c2.Git("checkout", "-q", "-b", "feat/old")
	id := rt.addTask(t, rt.c1, "finish me")
	rt.c1.MustLore("task", "done", id)
	rt.c1.MustLore("sync").Says(t, "landed on main") // landing waits for sync (U-2)
	rt.c1.MustLore("sync")                           // main fetched: the row leaves live
	if _, ok := rt.liveTask(t, id); ok {
		t.Fatal("a done row whose main copy matches must have left live")
	}
	rt.c2.Lore("task", "show", id).Refused(t, "sync peek", "origin/main")
}

// F1 edge case: detached HEAD reads the commit's tree as branch data.
func TestF01_DetachedHEADReadsCommitData(t *testing.T) {
	rt := newRepoTeam(t, th.Flag("no-live", "task", "add"))
	id := rt.addTask(t, rt.c1, "on a commit", "--no-live")
	rt.c1.CommitAll("task")
	rt.c1.Git("checkout", "-q", "--detach")
	rt.c1.MustLore("task", "show", id).Says(t, "on a commit")
}

// F1: `--live` on a row found only in branch data writes the new version
// into live; the branch file is not edited.
func TestF01_EditWithLiveMovesBranchRowIntoLive(t *testing.T) {
	rt := newRepoTeam(t, th.Flag("no-live", "task", "add"), th.Flag("live", "task", "edit"))
	id := rt.addTask(t, rt.c1, "branch only", "--no-live")
	rt.c1.CommitAll("task")
	if _, ok := rt.liveTask(t, id); ok {
		t.Fatal("--no-live must not write live")
	}
	rt.c1.MustLore("task", "edit", id, "--title", "now shared", "--live")
	if r := mustRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id)); r.Title != "now shared" {
		t.Fatalf("live copy: %+v", r)
	}
	if body, _ := rt.c1.ReadFile(mainRowPath("", tblTasks, id)); !strings.Contains(body, "branch only") {
		t.Fatalf("branch file must keep its version: %s", body)
	}
	rt.c2.MustLore("task", "show", id).Says(t, "now shared")
}

// ---------------------------------------------------------------- F2 write

// F2 example, §3.2: a task is live by default, reaches a teammate on another
// branch at once, and touches no branch, working folder or index.
func TestF02_TaskIsLiveByDefaultAndReachesOtherBranch(t *testing.T) {
	rt := newRepoTeam(t)
	rt.c1.Git("checkout", "-q", "-b", "feat/login")
	rt.c2.Git("checkout", "-q", "-b", "feat/payments")
	mainBefore := rt.app.Rev(refMain)

	r := rt.c2.MustLore("task", "add", "fix login timeout", "--tasklist", rt.tasklist)
	r.Says(t, "live", "pushed")
	id := mustID(t, r, "tsk")
	if st := rt.c2.Status(); st != "" {
		t.Fatalf("a live write must leave the working tree and index alone: %q", st)
	}
	if rt.app.Rev(refMain) != mainBefore || rt.app.Has("refs/heads/feat/payments") {
		t.Fatal("a live write must not touch any branch on origin")
	}
	row := mustRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id))
	if row.Title != "fix login timeout" || row.Live == nil || row.Live.LiveSince == "" || row.Live.HLC == "" {
		t.Fatalf("live file must be the row plus its _live block (§3.3): %+v", row)
	}
	if !hasItem(listedTitles(t, rt.c1), "fix login timeout") {
		t.Fatal("dev1 on feat/login must see the task on the next command")
	}
}

// F2 steps 4 and C2: the first write ever is one parentless commit with the
// fixed message, and the tree carries _live/epoch and _live/meta.json.
func TestF02_FirstWriteIsParentlessWithFixedMessage(t *testing.T) {
	rt := newRepoTeam(t)
	rt.addTask(t, rt.c1, "first")
	if n, p := rt.app.CommitCount(refLive), rt.app.Parents(refLive); n != 1 || p != 0 {
		t.Fatalf("first live write: %d commits, %d parents; want 1, 0", n, p)
	}
	if subj := rt.app.Log(refLive)[0]; !strings.HasPrefix(subj, "lore: live") {
		t.Fatalf("fixed message expected, got %q", subj)
	}
	files := rt.app.Files(refLive)
	if !hasItem(files, liveEpochPath) || !hasItem(files, liveMetaPath) {
		t.Fatalf("live tree must hold %s and %s: %v", liveEpochPath, liveMetaPath, files)
	}
}

// F2 step 3: the same write twice makes no commit and no push.
func TestF02_SameWriteTwiceIsANoOp(t *testing.T) {
	rt := newRepoTeam(t)
	id := rt.addTask(t, rt.c1, "stable")
	rt.c1.MustLore("task", "edit", id, "--priority", "high")
	n := rt.app.CommitCount(refLive)
	if n == 0 {
		t.Fatal("the live write never reached origin")
	}
	rt.c1.MustLore("task", "edit", id, "--priority", "high")
	if got := rt.app.CommitCount(refLive); got != n {
		t.Fatalf("an unchanged tree must not commit: %d → %d", n, got)
	}
}

// E2 / F3: a write made offline is not lost when a teammate writes meanwhile;
// the next command once online pushes it.
func TestE02_OfflineWriteSurvivesTeammateWrite(t *testing.T) {
	rt := newRepoTeam(t)
	rt.dev1.Offline()
	r := rt.c1.MustLore("task", "add", "written offline", "--tasklist", rt.tasklist)
	r.Says(t, "not pushed")
	a := mustID(t, r, "tsk")
	if _, ok := rt.liveTask(t, a); ok {
		t.Fatal("an offline write cannot be on origin")
	}
	b := rt.addTask(t, rt.c2, "written online")
	rt.dev1.Online()
	rt.c1.MustLore("task", "list")
	for _, id := range []string{a, b} {
		if _, ok := rt.liveTask(t, id); !ok {
			t.Fatalf("%s lost after reconnect; live has %v", id, rt.app.Files(refLive))
		}
	}
	titles := listedTitles(t, rt.c2)
	if !hasItem(titles, "written offline") || !hasItem(titles, "written online") {
		t.Fatalf("dev2 must see both: %v", titles)
	}
}

// §1 LORE_LIVE_AUTOPUSH=0: the write stays local and lore runs no push on its
// own; an explicit `lore sync` pushes it.
func TestF02_AutopushOffNeverTouchesTheNetwork(t *testing.T) {
	rt := newRepoTeam(t)
	pushes := len(rt.w.GitPushes("dev1"))
	r := rt.c1.With(autopushOff).MustLore("task", "add", "quiet", "--tasklist", rt.tasklist)
	id := mustID(t, r, "tsk")
	if got := len(rt.w.GitPushes("dev1")); got != pushes {
		t.Fatalf("autopush off must not push (%d new pushes)", got-pushes)
	}
	if _, ok := rt.liveTask(t, id); ok {
		t.Fatal("autopush off: nothing on origin yet")
	}
	if rt.c1.LocalRef(refLive) == "" {
		t.Fatal("the write must sit on the local live ref")
	}
	rt.c1.MustLore("sync")
	if _, ok := rt.liveTask(t, id); !ok {
		t.Fatal("lore sync must push the waiting write")
	}
}

// F2 edge case + §6.5: a secret in a row is refused before it reaches any ref.
func TestF02_SecretIsRefusedBeforeTheLiveRef(t *testing.T) {
	rt := newRepoTeam(t)
	secret := "AKIA" + "IOSFODNN7EXAMPLE" // built at run time (convention 8)
	r := rt.c1.Lore("task", "add", "rotate keys", "--tasklist", rt.tasklist, "--body", "aws key "+secret)
	r.Says(t, "secret")
	clean := rt.addTask(t, rt.c1, "rotate keys, no secret here")
	if _, ok := rt.liveTask(t, clean); !ok {
		t.Fatal("control: a clean row must reach live, or this test proves nothing")
	}
	if found := rt.app.AllObjectsContain(secret); len(found) != 0 {
		t.Fatal("a secret reached origin")
	}
}

// F2 edge case + §7.4 version floor: a binary below _live/meta.json's
// min_lore_version refuses to write (E_OLD_LORE) and stays read-only.
func TestF02_OldBinaryBelowVersionFloorIsReadOnly(t *testing.T) {
	rt := newRepoTeam(t)
	id := rt.addTask(t, rt.c1, "readable")
	pushLiveFile(t, rt.c2, refLive, liveMetaPath, `{"min_lore_version":"999.0.0","features":["live","signals"]}`)
	rt.c1.Lore("task", "edit", id, "--priority", "high").Fails(t, th.ExitGuard, "E_OLD_LORE")
	rt.c1.MustLore("task", "show", id).Says(t, "readable")
}

// F2 + side effect 32: a delete writes a tombstone, never removes the file.
// Ported to DELETION SYSTEM / DQ8 / FA-DEL-1: a plain `delete --confirm` is a
// soft delete (the row goes to the trash with its content), and only
// `--purge --confirm` leaves the tombstone this case checks.
func TestF02_DeleteWritesATombstone(t *testing.T) {
	rt := newRepoTeam(t)
	id := rt.addTask(t, rt.c1, "to delete")
	rt.c1.MustLore("task", "delete", id, "--purge", "--confirm")
	tomb := mustRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id))
	if tomb.Live == nil || !tomb.Live.Deleted {
		t.Fatalf("tombstone expected: %+v", tomb)
	}
	rt.c2.Lore("task", "show", id).Refused(t, "deleted")
}

// E1 / F2 / F4: two clones edit the same live task in the same instant;
// both edits survive and exactly two commits are added.
func TestE01_Race_TwoClonesEditSameLiveTask(t *testing.T) {
	rt := newRepoTeam(t)
	id := rt.addTask(t, rt.c1, "shared")
	rt.c2.MustLore("task", "list")
	before := rt.app.CommitCount(refLive)
	res := rt.w.Simultaneously(
		rt.c1.Prepare("task", "edit", id, "--title", "renamed by dev1"),
		rt.c2.Prepare("task", "edit", id, "--priority", "urgent"),
	)
	for _, r := range res {
		r.OK(t)
	}
	row := mustRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id))
	if row.Title != "renamed by dev1" || rawString(t, row, "priority") != "urgent" {
		t.Fatalf("both edits must survive: %+v", row)
	}
	if got := rt.app.CommitCount(refLive) - before; got != 2 {
		t.Fatalf("two writes must add exactly two commits, added %d", got)
	}
}

// F2 "two writers at once": four clones add four tasks in the same second;
// every one is on origin afterwards.
func TestF02_Race_FourWritersSameSecond(t *testing.T) {
	rt := newRepoTeam(t)
	clones := []*th.Clone{rt.c1, rt.c2}
	for _, n := range []string{"dev3", "dev4"} {
		a := rt.w.Human(n, n+"@acme.example")
		a.WriteConfig(th.Config{Live: th.FreshFetch()})
		c := a.Clone(rt.app, "app")
		c.MustLore("task", "list")
		clones = append(clones, c)
	}
	var ps []*th.Pending
	for i, c := range clones {
		ps = append(ps, c.Prepare("task", "add", "parallel "+strconv.Itoa(i), "--tasklist", rt.tasklist))
	}
	var ids []string
	for _, r := range rt.w.Simultaneously(ps...) {
		ids = append(ids, mustID(t, r.OK(t), "tsk"))
	}
	for _, id := range ids {
		if _, ok := rt.liveTask(t, id); !ok {
			t.Fatalf("%s lost in the race; live has %v", id, rt.app.Files(refLive))
		}
	}
}

// ---------------------------------------------------------------- F3 fetch

// F3 example + C7: a read in a fresh clone shows the live rows and wires
// nothing (owner item 6); the first sync installs the tracking
// refspec, never a refspec that force-writes into refs/lore/*.
func TestF03_FirstSyncInstallsTrackingRefspec(t *testing.T) {
	rt := newRepoTeam(t)
	rt.addTask(t, rt.c1, "seen through tracking")
	fresh := rt.dev2.Clone(rt.app, "app-fresh")
	// a read never wires git (owner item 6): no refspec after it, and it
	// still shows the live rows
	if !hasItem(listedTitles(t, fresh), "seen through tracking") {
		t.Fatal("a fresh clone must show live rows on its first command")
	}
	if specs := fresh.Git("config", "--get-all", "remote.origin.fetch"); strings.Contains(specs, loreRefspec) {
		t.Fatalf("a read installed the tracking refspec: %q", specs)
	}
	fresh.MustLore("sync") // the first sync (or write) wires the clone
	specs := fresh.Git("config", "--get-all", "remote.origin.fetch")
	if !strings.Contains(specs, loreRefspec) {
		t.Fatalf("tracking refspec missing: %q", specs)
	}
	for _, s := range strings.Split(specs, "\n") {
		if _, dst, ok := strings.Cut(s, ":"); ok && strings.HasPrefix(dst, "refs/lore/") {
			t.Fatalf("a refspec must never write into refs/lore/* directly: %q", s)
		}
	}
	if fresh.LocalRef(trackingPrefix+"live") == "" {
		t.Fatal("the first sync must fetch the live ref into its tracking ref")
	}
}

// F3 "nothing is ever force-written into a local refs/lore/* ref": a plain
// git fetch by the user after an unpushed live write loses nothing.
func TestF03_PlainGitFetchNeverClobbersUnpushedLiveWrite(t *testing.T) {
	rt := newRepoTeam(t)
	r := rt.c1.With(autopushOff).MustLore("task", "add", "mine, unpushed", "--tasklist", rt.tasklist)
	mine := mustID(t, r, "tsk")
	theirs := rt.addTask(t, rt.c2, "theirs, pushed")
	rt.c1.Git("fetch", "-q", "origin")
	titles := listedTitles(t, rt.c1)
	if !hasItem(titles, "mine, unpushed") || !hasItem(titles, "theirs, pushed") {
		t.Fatalf("after git fetch both must be visible: %v", titles)
	}
	rt.c1.MustLore("sync")
	for _, id := range []string{mine, theirs} {
		if _, ok := rt.liveTask(t, id); !ok {
			t.Fatalf("%s missing on origin after sync", id)
		}
	}
}

// F3 step 1 / TEAM-025: a command fetches only when the last fetch is older
// than live.fetch_ttl; `lore sync` always fetches.
func TestF03_FetchIsCachedForFetchTTL(t *testing.T) {
	rt := newRepoTeam(t)
	rt.dev1.WriteConfig(th.Config{Live: &th.Live{FetchTTL: "60s", Autopush: th.Bool(true)}})
	rt.dev1.SetNow(t0)
	rt.c1.MustLore("task", "list")
	rt.addTask(t, rt.c2, "fresh news")
	rt.dev1.SetNow(t0.Add(30 * time.Second))
	if hasItem(listedTitles(t, rt.c1), "fresh news") {
		t.Fatal("within fetch_ttl the command must use the cached fetch")
	}
	rt.dev1.SetNow(t0.Add(61 * time.Second))
	if !hasItem(listedTitles(t, rt.c1), "fresh news") {
		t.Fatal("past fetch_ttl the command must fetch")
	}
	rt.addTask(t, rt.c2, "more news")
	rt.dev1.SetNow(t0.Add(62 * time.Second))
	rt.c1.MustLore("sync")
	if !hasItem(listedTitles(t, rt.c1), "more news") {
		t.Fatal("lore sync must always fetch")
	}
}

// F3 edge case, §6.6, side effect 28: a remote live ref that was deleted or
// rewritten without a higher epoch stops writes, keeps a local backup and
// `lore live restore` puts the rows back.
func TestSideEffect28_RewrittenRemoteLiveStopsWrites(t *testing.T) {
	for _, variant := range []string{"deleted", "rewritten"} {
		t.Run(variant, func(t *testing.T) {
			rt := newRepoTeam(t, th.Cmd("live", "restore"))
			id := rt.addTask(t, rt.c1, "keep me")
			rt.c2.MustLore("task", "list")
			if variant == "deleted" {
				rt.app.DeleteRef(refLive)
			} else {
				rt.app.SetRef(refLive, rt.app.Rev(refMain))
			}
			rt.c2.Lore("task", "add", "after rewrite", "--tasklist", rt.tasklist).Refused(t, "lore live restore")
			if len(strings.TrimSpace(rt.c2.Git("for-each-ref", refBackupLive))) == 0 {
				t.Fatal("the local live ref must be backed up under " + refBackupLive)
			}
			rt.c2.MustLore("live", "restore")
			if _, ok := rt.liveTask(t, id); !ok {
				t.Fatalf("restore must put the rows back: %v", rt.app.Files(refLive))
			}
			if len(rt.app.Refs("refs/lore-backup/")) != 0 {
				t.Fatal("backups are local only, never pushed")
			}
		})
	}
}

// ---------------------------------------------------------------- F4 merge

// F4 / E2E "delete on A, edit on B": modify beats delete, the row stays live,
// lore says so, and the row lands later.
func TestF04_DeleteVersusEditKeepsTheEditedRow(t *testing.T) {
	rt := newRepoTeam(t, capLand)
	id := rt.addTask(t, rt.c1, "contested")
	rt.c2.MustLore("task", "list")
	rt.c2.With(autopushOff).MustLore("task", "edit", id, "--title", "edited by dev2")
	rt.c1.MustLore("task", "delete", id, "--confirm")
	rt.c2.MustLore("sync").Says(t, "deleted", "kept")
	row := mustRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id))
	if row.Live == nil || row.Live.Deleted || row.Title != "edited by dev2" {
		t.Fatalf("modify must beat delete with the full edited row: %+v", row)
	}
	rt.c2.MustLore("task", "done", id)
	rt.c2.MustLore("sync") // landing waits for sync (U-2)
	if _, ok := rt.mainTask(t, id); !ok {
		t.Fatal("the surviving row must land")
	}
}

// F4 scalars: the side with the higher hlc wins, not the side that pushed
// last and not the laptop with the wrong clock.
func TestF04_HigherHLCWinsForScalarsNotPushOrder(t *testing.T) {
	rt := newRepoTeam(t)
	rt.dev1.SetNow(t0.Add(10 * time.Minute))
	rt.dev2.SetNow(t0) // dev2's laptop clock is ten minutes behind
	id := rt.addTask(t, rt.c1, "priority fight")
	rt.c2.MustLore("task", "list")
	rt.dev2.SetNow(t0.Add(5 * time.Minute))
	rt.c2.With(autopushOff).MustLore("task", "edit", id, "--priority", "low")
	rt.dev1.SetNow(t0.Add(20 * time.Minute))
	rt.c1.With(autopushOff).MustLore("task", "edit", id, "--priority", "high")
	rt.c1.MustLore("sync")
	rt.c2.MustLore("sync")
	row := mustRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id))
	if got := rawString(t, row, "priority"); got != "high" {
		t.Fatalf("the later edit by hlc must win even when pushed first: priority=%q", got)
	}
}

// F4 text fields: both edits kept with markers; the row is a conflict that
// `lore sync conflicts` lists and that cannot land.
func TestF04_TextClashKeepsBothAndBlocksLanding(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("land"))
	id := rt.addTask(t, rt.c1, "base title")
	rt.c2.MustLore("task", "list")
	rt.c1.With(autopushOff).MustLore("task", "edit", id, "--title", "title from dev1")
	rt.c2.With(autopushOff).MustLore("task", "edit", id, "--title", "title from dev2")
	rt.c1.MustLore("sync")
	// The second sync merges both titles with markers, names the row and
	// exits E_STALE (23) until it is settled (docs/sync/conflicts-and-duplicates.md).
	rt.c2.Lore("sync").Fails(t, 23, "E_STALE", id, "lore sync resolve")
	row := mustRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id))
	if !strings.Contains(row.Title, "title from dev1") || !strings.Contains(row.Title, "title from dev2") {
		t.Fatalf("both text edits must be kept: %q", row.Title)
	}
	rt.c2.MustLore("sync", "conflicts").Says(t, id)
	rt.c2.Lore("land", id).Refused(t, "conflict")
	if _, ok := rt.mainTask(t, id); ok {
		t.Fatal("a row with conflict markers must never land")
	}
}

// ---------------------------------------------------------------- F5 landing

// E3: unprotected repo, a done row lands on origin/main on `lore sync`
// (never by itself, owner rule U-2) without its _live block, the row leaves
// live, and the user's unpushed local main commit is neither pushed nor
// moved.
func TestE03_DoneLandsOnMainAndLeavesLive(t *testing.T) {
	rt := newRepoTeam(t, capLand)
	id := rt.addTask(t, rt.c1, "ship it")
	rt.c1.WriteFile("local.txt", "not for origin yet")
	rt.c1.CommitAll("local only")
	localMain := rt.c1.Head()

	rt.c1.MustLore("task", "done", id).Never(t, "landed on main")
	if _, ok := rt.mainTask(t, id); ok {
		t.Fatal("task done must not land by itself (U-2)")
	}
	rt.c1.MustLore("sync").Says(t, "landed on main")
	row := mustRow(t, rt.app, refMain, mainRowPath("", tblTasks, id))
	if row.Status != statusDone {
		t.Fatalf("landed status %q", row.Status)
	}
	if _, has := row.Raw["_live"]; has {
		t.Fatal("the landed file must not carry the _live block")
	}
	if subj := rt.app.Log(refMain)[0]; !strings.HasPrefix(subj, "lore: land 1 row") {
		t.Fatalf("fixed landing message expected, got %q", subj)
	}
	if hasItem(rt.app.Files(refMain), "local.txt") {
		t.Fatal("landing must never push the user's local main commits")
	}
	if rt.c1.LocalRef("refs/heads/main") != localMain {
		t.Fatal("landing must never move local main")
	}
	rt.c2.MustLore("sync")
	if _, ok := rt.liveTask(t, id); ok {
		t.Fatal("finished and equal on main → the row leaves live")
	}
}

// F5 "max-age copy" (rule 6c, TEAM-015, side effect 9): an open row older
// than live.max_age is copied to main and STAYS live; the copy is refreshed
// once per max age while the row differs.
func TestF05_MaxAgeCopyStaysLiveWhileOpen(t *testing.T) {
	rt := newRepoTeam(t, capLand)
	rt.dev1.SetNow(t0)
	id := rt.addTask(t, rt.c1, "long running")
	rt.dev1.SetNow(t0.Add(6 * 24 * time.Hour))
	rt.c1.MustLore("sync")
	if _, ok := rt.mainTask(t, id); ok {
		t.Fatal("no copy before max age")
	}
	rt.dev1.SetNow(t0.Add(maxAge + time.Minute))
	rt.c1.MustLore("sync")
	copied := mustRow(t, rt.app, refMain, mainRowPath("", tblTasks, id))
	if copied.Status != statusTodo || copied.Title != "long running" {
		t.Fatalf("copy: %+v", copied)
	}
	live := mustRow(t, rt.app, refLive, liveRowPath(rootFolder, tblTasks, id))
	if live.Live == nil || live.Live.LastCopyAt == "" {
		t.Fatalf("the row stays live and records last_copy_at: %+v", live)
	}
	rt.dev1.SetNow(t0.Add(8 * 24 * time.Hour))
	rt.c1.MustLore("task", "edit", id, "--title", "long running, renamed")
	rt.c1.MustLore("sync")
	if r := mustRow(t, rt.app, refMain, mainRowPath("", tblTasks, id)); r.Title != "long running" {
		t.Fatal("the copy refreshes once per max age, not on every edit")
	}
	rt.dev1.SetNow(t0.Add(2*maxAge + 2*time.Minute))
	rt.c1.MustLore("sync")
	if r := mustRow(t, rt.app, refMain, mainRowPath("", tblTasks, id)); r.Title != "long running, renamed" {
		t.Fatalf("the copy must refresh after another max age: %+v", r)
	}
}

// F5 "lore land … --dry-run shows the plan" and `lore land --all` copies an
// open row while keeping it live.
func TestF05_LandDryRunChangesNothing(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("land"), th.Flag("dry-run", "land"))
	id := rt.addTask(t, rt.c1, "plan me")
	mainBefore, liveBefore := rt.app.Rev(refMain), rt.app.Rev(refLive)
	rt.c1.MustLore("land", "--all", "--dry-run").Says(t, id)
	if rt.app.Rev(refMain) != mainBefore || rt.app.Rev(refLive) != liveBefore {
		t.Fatal("--dry-run must not change any ref")
	}
	rt.c1.MustLore("land", "--all")
	if _, ok := rt.mainTask(t, id); !ok {
		t.Fatal("land --all must copy the open row to main")
	}
	if _, ok := rt.liveTask(t, id); !ok {
		t.Fatal("an open row stays live after `lore land`")
	}
}

// F5 `lore land --mission`: only that mission's rows land.
func TestF05_LandByMission(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("land"), th.Flag("mission", "land"))
	m := mustID(t, rt.c1.MustLore("mission", "add", "Invite landing page"), "msn")
	in := rt.addTask(t, rt.c1, "in the mission", "--mission", m)
	out := rt.addTask(t, rt.c1, "outside")
	rt.c1.MustLore("land", "--mission", m)
	if _, ok := rt.mainTask(t, in); !ok {
		t.Fatal("the mission's task must land")
	}
	if _, ok := rt.mainTask(t, out); ok {
		t.Fatal("a task outside the mission must not land")
	}
}

// Rule 6 / TEAM-027 / cookbook 7: a rule written --live on a feature branch
// lands on main on the next `lore sync` (never by itself, owner rule U-2)
// and leaves live once main matches; a later edit lands again.
func TestRecipe07_UrgentRuleLandsFromAFeatureBranch(t *testing.T) {
	rt := newRepoTeam(t, capLand, th.Flag("live", "rule", "add"), th.Flag("live", "rule", "edit"))
	rt.c1.Git("checkout", "-q", "-b", "feat/x")
	r := rt.c1.MustLore("rule", "add", "--body", "never run migrations from the app container", "--live")
	id := mustID(t, r, "rul")
	rt.c1.MustLore("sync")
	if !strings.Contains(mustRow(t, rt.app, refMain, mainRowPath("", tblRules, id)).Body, "never run migrations") {
		t.Fatal("a rule is finished on every write and must land on the next sync")
	}
	if rt.app.Has("refs/heads/feat/x") {
		t.Fatal("landing must not need the feature branch")
	}
	rt.c2.MustLore("sync")
	if _, ok := readRow(t, rt.app, refLive, liveRowPath(rootFolder, tblRules, id)); ok {
		t.Fatal("the landed rule must leave live")
	}
	// Ported to LORE_TEAM_SPEC rule 4 and F1 (coordinator 2026-11-02,
	// default-branch read-through only): a row that landed on main and
	// left live is not found on an older feature branch (the hint names
	// `lore sync peek`); v0.1.17 found it only because its SQLite kept
	// every row it had seen. Bringing main into the branch finds it again.
	rt.c1.Lore("rule", "edit", id, "--body", "never run migrations from any container", "--live").Refused(t, "lore sync peek")
	rt.c1.Git("merge", "-q", "--no-edit", "origin/main")
	rt.c1.MustLore("rule", "edit", id, "--body", "never run migrations from any container", "--live")
	rt.c1.MustLore("sync")
	if !strings.Contains(mustRow(t, rt.app, refMain, mainRowPath("", tblRules, id)).Body, "any container") {
		t.Fatal("each live write of a rule lands a new copy")
	}
	if !hasItem(listedTitlesOrBodies(t, rt.c2, "rule"), "never run migrations from any container") {
		t.Fatal("dev2 must see the new rule text")
	}
}

// listedTitlesOrBodies returns the bodies `lore <kind> list --json` shows.
func listedTitlesOrBodies(t *testing.T, c *th.Clone, kind string) []string {
	t.Helper()
	type listed struct {
		Data []struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"data"`
	}
	l := th.DecodeJSON[listed](t, c.Lore(kind, "list", "--json"))
	var out []string
	for _, d := range l.Data {
		out = append(out, d.Title, d.Body)
	}
	return out
}

// F5 edge case + side effect 22: a row reopened after it landed stays live
// (its live blob differs from main) and lands again when finished.
func TestSideEffect22_ReopenedRowStaysLiveAndLandsAgain(t *testing.T) {
	rt := newRepoTeam(t, capLand, th.Flag("live", "task", "edit"))
	id := rt.addTask(t, rt.c1, "flaky")
	rt.c1.MustLore("task", "done", id)
	rt.c1.MustLore("sync")
	rt.c2.MustLore("task", "edit", id, "--status", statusTodo, "--live")
	rt.c1.MustLore("sync")
	if r, ok := rt.liveTask(t, id); !ok || r.Status != statusTodo {
		t.Fatalf("a reopened row must stay live: %+v %v", r, ok)
	}
	if r := mustRow(t, rt.app, refMain, mainRowPath("", tblTasks, id)); r.Status != statusDone {
		t.Fatalf("main keeps the landed done copy until the next landing: %q", r.Status)
	}
	rt.c2.MustLore("task", "done", id)
	rt.c2.MustLore("sync") // lands again (landing waits for sync, U-2)
	rt.c1.MustLore("sync") // main fetched: the row leaves live
	if _, ok := rt.liveTask(t, id); ok {
		t.Fatal("done again → lands again and leaves live")
	}
}

// F5 "two people land the same row": one file per id; the second landing
// commits nothing.
func TestF05_Race_TwoLandingsOfOneRowMakeOneCommit(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("land"))
	id := rt.addTask(t, rt.c1, "land me once")
	rt.c2.MustLore("task", "list")
	before := rt.app.CommitCount(refMain)
	for _, r := range rt.w.Simultaneously(rt.c1.Prepare("land", id), rt.c2.Prepare("land", id)) {
		r.OK(t)
	}
	if got := rt.app.CommitCount(refMain) - before; got != 1 {
		t.Fatalf("landing the same row twice must add one commit, added %d", got)
	}
	if n := len(rt.app.FilesMatching(refMain, id)); n != 1 {
		t.Fatalf("one id is one file, found %d", n)
	}
}

// F5 edge case + side effect 11: a tombstone lands as a file delete plus a
// _purged.json id and stays in live, so an old branch copy cannot return.
func TestSideEffect11_TombstoneLandsAsDeleteAndPurge(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("land"))
	id := rt.addTask(t, rt.c1, "remove later")
	rt.c1.MustLore("land", id)
	rt.c2.Git("pull", "-q")
	rt.c2.Git("checkout", "-q", "-b", "feat/old-copy")
	// Ported to DELETION SYSTEM, DQ8, FA-DEL-1: a plain delete is soft (the row
	// goes to the trash and keeps its content), so the v0.1.17 tombstone
	// is a purge now: --purge --confirm leaves the tombstone that lands.
	rt.c1.MustLore("task", "delete", id, "--purge", "--confirm")
	rt.c1.MustLore("sync")
	if _, ok := rt.mainTask(t, id); ok {
		t.Fatal("a landed tombstone deletes the main file")
	}
	if purged, _ := rt.app.Read(refMain, purgedFile); !strings.Contains(purged, id) {
		t.Fatalf("_purged.json must list the id: %q", purged)
	}
	if tomb, ok := rt.liveTask(t, id); !ok || tomb.Live == nil || !tomb.Live.Deleted {
		t.Fatal("the tombstone stays in live for at least max_age")
	}
	rt.c2.Lore("task", "show", id).Refused(t, "deleted")
}

// ---------------------------------------------------------------- F6 ride-along

// newProtectedTeam is a repo team whose origin refuses pushes to main.
func newProtectedTeam(t *testing.T) *repoTeam {
	t.Helper()
	// Protected main only matters once rows land (ride-along, F6).
	rt := newRepoTeam(t, capLand)
	rt.app.ProtectMain()
	return rt
}

// commitSubjects lists the clone's last n commit subjects (HEAD), newest first.
func commitSubjects(c *th.Clone, n int) []string {
	return strings.Split(c.Git("log", "--format=%s", "-n", strconv.Itoa(n), "HEAD"), "\n")
}

// rideTask makes dev1 finish a task on feat/login of a protected repo, sync
// (the landing attempt main refuses as protected: a write never lands by
// itself, owner rule U-2) and commit code, which must add the ride commit.
func rideTask(t *testing.T, rt *repoTeam) string {
	t.Helper()
	rt.c1.Git("checkout", "-q", "-b", "feat/login")
	id := rt.addTask(t, rt.c1, "fix login timeout")
	rt.c1.MustLore("task", "done", id)
	rt.c1.MustLore("sync").Says(t, "protected")
	if _, ok := rt.mainTask(t, id); ok {
		t.Fatal("protected main cannot have received the row")
	}
	rt.c1.WriteFile("src/login.go", "package src\n")
	rt.c1.CommitAll("fix login timeout")
	return id
}

// E4 / F6: on a protected main the row rides on the next commit of a code
// branch; index and work tree stay clean; merging the PR lands it.
func TestE04_RideAlongOnProtectedMain(t *testing.T) {
	rt := newProtectedTeam(t)
	id := rideTask(t, rt)
	subj := commitSubjects(rt.c1, 2)
	if !strings.HasPrefix(subj[0], "lore: land 1 row") || subj[1] != "fix login timeout" {
		t.Fatalf("expected the ride commit on top of the user's commit: %v", subj)
	}
	if st := rt.c1.Status(); st != "" {
		t.Fatalf("git status must be clean after the ride commit: %q", st)
	}
	if r := rt.c1.TryGit("diff", "--cached", "--quiet"); r.Code != 0 {
		t.Fatal("the ride commit must leave no staged changes")
	}
	rt.c1.Git("push", "-q", "-u", "origin", "feat/login")
	if _, ok := readRow(t, rt.app, "refs/heads/feat/login", mainRowPath("", tblTasks, id)); !ok {
		t.Fatal("the pushed branch must carry the row")
	}
	rt.c1.Git("checkout", "-q", "main")
	rt.c1.Git("merge", "-q", "--no-ff", "--no-edit", "feat/login")
	rt.c1.AdminPush("origin", "main")
	rt.c2.MustLore("sync")
	if _, ok := rt.liveTask(t, id); ok {
		t.Fatal("once main holds the row it leaves live")
	}
}

// E4 "PR closed unmerged → row still live" and it rides again.
func TestE04_ClosedPRRowStaysLiveAndRidesAgain(t *testing.T) {
	rt := newProtectedTeam(t)
	id := rideTask(t, rt)
	rt.c1.Git("push", "-q", "-u", "origin", "feat/login")
	rt.c1.Git("push", "-q", "origin", "--delete", "feat/login")
	rt.c1.Git("checkout", "-q", "main")
	rt.c1.Git("branch", "-q", "-D", "feat/login")
	rt.c1.MustLore("sync")
	if _, ok := rt.liveTask(t, id); !ok {
		t.Fatal("a closed PR never landed the row: it must stay live")
	}
	rt.c1.Git("checkout", "-q", "-b", "feat/second-try")
	rt.c1.WriteFile("src/other.go", "package src\n")
	rt.c1.CommitAll("second try")
	if !strings.HasPrefix(commitSubjects(rt.c1, 1)[0], "lore: land 1 row") {
		t.Fatal("the waiting row must ride again on the next branch")
	}
}

// F6 skip list: no ride commit on the default branch or on a detached HEAD;
// a dirty landed path skips the ride and says why.
func TestF06_NoRideWhereItMustNotRide(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, rt *repoTeam, id string)
		says  string
	}{
		{"on default branch", func(t *testing.T, rt *repoTeam, id string) {}, ""},
		{"detached HEAD", func(t *testing.T, rt *repoTeam, id string) { rt.c1.Git("checkout", "-q", "--detach") }, ""},
		{"dirty landed path", func(t *testing.T, rt *repoTeam, id string) {
			rt.c1.Git("checkout", "-q", "-b", "feat/dirty")
			rt.c1.WriteFile(mainRowPath("", tblTasks, id), "{\"id\":\""+id+"\",\"title\":\"hand edit\"}\n")
		}, "skip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := newProtectedTeam(t)
			id := rt.addTask(t, rt.c1, "waits")
			rt.c1.MustLore("task", "done", id)
			rt.c1.MustLore("sync").Says(t, "protected")
			tc.setup(t, rt, id)
			rt.c1.WriteFile("src/a.go", "package src\n")
			rt.c1.Git("add", "src/a.go")
			r := rt.c1.TryGit("commit", "-m", "code only")
			if r.Code != 0 {
				t.Fatalf("the user's commit must succeed: %s", r.Stderr)
			}
			if tc.says != "" && !strings.Contains(r.Out(), tc.says) {
				t.Fatalf("a skipped ride must say why: %s", r.Out())
			}
			if s := commitSubjects(rt.c1, 1)[0]; strings.HasPrefix(s, "lore: land") {
				t.Fatalf("no ride commit expected (%s)", tc.name)
			}
			if _, ok := rt.liveTask(t, id); !ok {
				t.Fatal("the row keeps waiting in live")
			}
		})
	}
}

// F6 "amend or reset drops the ride commit: pre-push adds it again" and asks
// for one more push.
func TestF06_ResetDropsRideAndPrePushAddsItBack(t *testing.T) {
	rt := newProtectedTeam(t)
	id := rideTask(t, rt)
	rt.c1.Git("reset", "-q", "--hard", "HEAD~1")
	r := rt.c1.TryGit("push", "-u", "origin", "feat/login")
	if r.Code == 0 || !strings.Contains(r.Out(), "git push again") {
		t.Fatalf("pre-push must add the ride commit and stop this push: %s", r.Out())
	}
	if !strings.HasPrefix(commitSubjects(rt.c1, 1)[0], "lore: land 1 row") {
		t.Fatal("pre-push must have added a fresh ride commit")
	}
	rt.c1.Git("push", "-q", "-u", "origin", "feat/login")
	if _, ok := readRow(t, rt.app, "refs/heads/feat/login", mainRowPath("", tblTasks, id)); !ok {
		t.Fatal("the second push carries the row")
	}
}

// F6 "--no-verify skips pre-push: the rows keep waiting and ride later".
func TestF06_NoVerifyPushLeavesRowsWaiting(t *testing.T) {
	rt := newProtectedTeam(t)
	id := rideTask(t, rt)
	rt.c1.Git("reset", "-q", "--hard", "HEAD~1")
	rt.c1.Git("push", "-q", "--no-verify", "-u", "origin", "feat/login")
	if _, ok := readRow(t, rt.app, "refs/heads/feat/login", mainRowPath("", tblTasks, id)); ok {
		t.Fatal("--no-verify pushed no ride commit")
	}
	if _, ok := rt.liveTask(t, id); !ok {
		t.Fatal("the row keeps waiting in live")
	}
	rt.c1.WriteFile("src/later.go", "package src\n")
	rt.c1.CommitAll("later")
	if !strings.HasPrefix(commitSubjects(rt.c1, 1)[0], "lore: land 1 row") {
		t.Fatal("the row rides on the next commit")
	}
}

// F6 re-entry guard (LORE_RIDE_ACTIVE): one ride commit per user commit, and
// none once the branch already carries the current rows.
func TestF06_OneRideCommitPerCommitNoReentry(t *testing.T) {
	rt := newProtectedTeam(t)
	rideTask(t, rt)
	rt.c1.WriteFile("src/more.go", "package src\n")
	rt.c1.CommitAll("more work")
	n := 0
	for _, s := range commitSubjects(rt.c1, 10) {
		if strings.HasPrefix(s, "lore: land") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("exactly one ride commit expected on the branch, got %d", n)
	}
}

// Side effect 34: several projects waiting in a monorepo ride in one commit.
func TestSideEffect34_MonorepoOneRideCommitForAllProjects(t *testing.T) {
	rt := newRepoTeam(t, capLand)
	web := rt.c1.In("web")
	web.MustLore("init", "--non-interactive", "--name=web")
	webTL := createTasklist(t, web)
	rt.c1.CommitAll("web project")
	rt.c1.AdminPush("origin", "main")
	rt.app.ProtectMain()

	rt.c1.Git("checkout", "-q", "-b", "feat/both")
	root := rt.addTask(t, rt.c1, "root task")
	webTask := mustID(t, web.MustLore("task", "add", "web task", "--tasklist", webTL), "tsk")
	rt.c1.MustLore("task", "done", root)
	web.MustLore("task", "done", webTask)
	rt.c1.MustLore("sync").Says(t, "protected")
	rt.c1.WriteFile("src/x.go", "package src\n")
	rt.c1.CommitAll("code")
	if s := commitSubjects(rt.c1, 2); !strings.HasPrefix(s[0], "lore: land 2 rows") || strings.HasPrefix(s[1], "lore: land") {
		t.Fatalf("one ride commit for both projects expected: %v", s)
	}
	changed := rt.c1.Git("show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(changed, mainRowPath("", tblTasks, root)) || !strings.Contains(changed, mainRowPath("web", tblTasks, webTask)) {
		t.Fatalf("ride commit must hold both rows: %s", changed)
	}
}

// ---------------------------------------------------------------- F8 compaction

// F8: compaction rewrites the live ref to one parentless commit with the same
// rows and a higher epoch; the backup stays local; teammates keep working.
func TestF08_CompactKeepsRowsAndRaisesEpoch(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("live", "compact"), th.Flag("dry-run", "live", "compact"))
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, rt.addTask(t, rt.c1, "row "+strconv.Itoa(i)))
	}
	epochBefore, _ := rt.app.Read(refLive, liveEpochPath)
	revBefore := rt.app.Rev(refLive)
	rt.c1.MustLore("live", "compact", "--dry-run")
	if rt.app.Rev(refLive) != revBefore {
		t.Fatal("--dry-run must not change the ref")
	}
	rt.c1.MustLore("live", "compact")
	if n, p := rt.app.CommitCount(refLive), rt.app.Parents(refLive); n != 1 || p != 0 {
		t.Fatalf("compacted live: %d commits, %d parents; want 1, 0", n, p)
	}
	epochAfter, _ := rt.app.Read(refLive, liveEpochPath)
	if atoiTrim(t, epochAfter) != atoiTrim(t, epochBefore)+1 {
		t.Fatalf("epoch must rise by one: %q → %q", epochBefore, epochAfter)
	}
	for _, id := range ids {
		if _, ok := rt.liveTask(t, id); !ok {
			t.Fatalf("compaction lost %s", id)
		}
	}
	if rt.c1.Git("for-each-ref", refBackupLive) == "" {
		t.Fatal("compaction keeps a local backup")
	}
	if len(rt.app.Refs("refs/lore-backup/")) != 0 {
		t.Fatal("backups are never pushed")
	}
	extra := rt.addTask(t, rt.c2, "after compaction")
	if _, ok := rt.liveTask(t, extra); !ok {
		t.Fatal("a teammate's next write must work after the epoch change")
	}
}

func atoiTrim(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("epoch %q is not a number: %v", s, err)
	}
	return n
}

// E6 / F8 reader path: a clone holding a pending write while another clone
// compacts re-applies only its pending write; nothing is lost.
func TestE06_CompactionWhileACloneHoldsAPendingWrite(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("live", "compact"))
	rt.dev1.Offline()
	pending := mustID(t, rt.c1.MustLore("task", "add", "pending on A", "--tasklist", rt.tasklist), "tsk")
	b1 := rt.addTask(t, rt.c2, "B one")
	b2 := rt.addTask(t, rt.c2, "B two")
	rt.c2.MustLore("live", "compact")
	epoch, _ := rt.app.Read(refLive, liveEpochPath)
	rt.dev1.Online()
	rt.c1.MustLore("task", "list")
	for _, id := range []string{pending, b1, b2} {
		if _, ok := rt.liveTask(t, id); !ok {
			t.Fatalf("%s missing after the reader path: %v", id, rt.app.Files(refLive))
		}
	}
	if now, _ := rt.app.Read(refLive, liveEpochPath); atoiTrim(t, now) < atoiTrim(t, epoch) {
		t.Fatal("the epoch only grows")
	}
}

// F8 edge case: a writer pushing at the moment of compaction loses nothing.
func TestF08_Race_CompactionVersusWriter(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("live", "compact"))
	old := rt.addTask(t, rt.c1, "old row")
	rt.c2.MustLore("task", "list")
	res := rt.w.Simultaneously(
		rt.c2.Prepare("live", "compact"),
		rt.c1.Prepare("task", "add", "racing row", "--tasklist", rt.tasklist),
	)
	res[0].OK(t)
	racing := mustID(t, res[1].OK(t), "tsk")
	rt.c1.MustLore("sync")
	rt.c2.MustLore("sync")
	for _, id := range []string{old, racing} {
		if _, ok := rt.liveTask(t, id); !ok {
			t.Fatalf("%s lost in the compaction race", id)
		}
	}
}

// F8 edge case: a clone offline across two compactions still pushes its write.
func TestF08_OfflineAcrossTwoCompactions(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("live", "compact"))
	rt.dev1.Offline()
	pending := mustID(t, rt.c1.MustLore("task", "add", "slept through two", "--tasklist", rt.tasklist), "tsk")
	rt.addTask(t, rt.c2, "between")
	rt.c2.MustLore("live", "compact")
	rt.addTask(t, rt.c2, "between again")
	rt.c2.MustLore("live", "compact")
	rt.dev1.Online()
	rt.c1.MustLore("task", "list")
	if _, ok := rt.liveTask(t, pending); !ok {
		t.Fatal("a write held across two compactions must be pushed")
	}
}

// ---------------------------------------------------------------- §6.6 runbook, durability

// §6.6 leak cleanup: `lore live purge` drops a row from every clone, and a
// clone that still had it (with a pending edit) never pushes it back.
func TestLeak_LivePurgeDropsRowFromEveryClone(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("live", "purge"), th.Cmd("live", "compact"))
	id := rt.addTask(t, rt.c1, "client is slow to pay, chase the invoice")
	rt.c2.MustLore("task", "list")
	rt.c2.With(autopushOff).MustLore("task", "edit", id, "--priority", "urgent")
	rt.c1.MustLore("live", "purge", id, "--confirm")
	if purged, _ := rt.app.Read(refLive, livePurgedPath); !strings.Contains(purged, id) {
		t.Fatalf("%s must list the id: %q", livePurgedPath, purged)
	}
	rt.c1.MustLore("live", "compact")
	for _, rev := range strings.Split(rt.w.Git(rt.app.Dir, "rev-list", refLive), "\n") {
		if strings.Contains(rt.w.Git(rt.app.Dir, "ls-tree", "-r", "--name-only", rev), id) {
			t.Fatal("compaction must remove the purged row from the ref's history")
		}
	}
	rt.c2.MustLore("sync")
	if _, ok := rt.liveTask(t, id); ok {
		t.Fatal("a stale clone must never push a purged id back")
	}
	if hasItem(listedTitles(t, rt.c2), "client is slow to pay, chase the invoice") {
		t.Fatal("the purged row must disappear from the stale clone")
	}
}

// Side effect 23: `lore live restore` never re-pushes a row already on
// origin/main or a purged id.
func TestSideEffect23_RestoreSkipsLandedAndPurgedRows(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("live", "restore"), th.Cmd("live", "purge"))
	landed := rt.addTask(t, rt.c1, "landed")
	rt.c1.MustLore("task", "done", landed)
	purged := rt.addTask(t, rt.c1, "purged")
	rt.c1.MustLore("live", "purge", purged, "--confirm")
	keep := rt.addTask(t, rt.c1, "still open")
	rt.c1.MustLore("sync")
	rt.app.DeleteRef(refLive)
	rt.c1.Lore("task", "list")
	rt.c1.MustLore("live", "restore")
	if _, ok := rt.liveTask(t, keep); !ok {
		t.Fatal("restore must bring the open row back")
	}
	for _, id := range []string{landed, purged} {
		if _, ok := rt.liveTask(t, id); ok {
			t.Fatalf("restore must not re-push %s", id)
		}
	}
}

// Rule 8 + side effect 14: forks, mirrors and ZIPs carry branches only, so an
// open row is missing there until its max-age copy lands; doctor warns.
func TestSideEffect14_ForksAndZipsSeeOnlyLandedRows(t *testing.T) {
	rt := newRepoTeam(t, capLand, th.Cmd("doctor"))
	rt.dev1.SetNow(t0)
	rt.addTask(t, rt.c1, "only in live for now")
	rt.c1.Lore("doctor").Says(t, "only in live")
	fork := rt.w.Remote(slugAppFork)
	rt.c1.Git("push", "-q", fork.URL(), "+refs/heads/*:refs/heads/*")
	if len(fork.Refs("refs/lore/")) != 0 {
		t.Fatal("a fork copies branches only")
	}
	forker := rt.w.Human("forker", "forker@else.example")
	forker.WriteConfig(th.Config{Live: th.FreshFetch()})
	fc := forker.Clone(fork, "app-fork")
	if hasItem(listedTitles(t, fc), "only in live for now") {
		t.Fatal("a fork cannot see a row that never landed")
	}
	rt.dev1.SetNow(t0.Add(maxAge + time.Hour))
	rt.c1.MustLore("sync")
	rt.c1.Git("fetch", "-q", "origin")
	rt.c1.Git("push", "-q", fork.URL(), "+refs/remotes/origin/main:refs/heads/main")
	fc.Git("pull", "-q")
	if !hasItem(listedTitles(t, fc), "only in live for now") {
		t.Fatal("after the max-age copy the fork and any ZIP of main carry the row")
	}
}

// Side effect 27: a host that refuses refs/lore/* keeps writes local and
// doctor says why.
func TestSideEffect27_HostRefusingLoreRefs(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("doctor"))
	rt.app.RefuseLoreRefs()
	rt.c1.MustLore("task", "add", "cannot be shared", "--tasklist", rt.tasklist).Says(t, "not pushed")
	rt.c1.Lore("doctor").Says(t, "refs/lore")
}

// ---------------------------------------------------------------- cookbook 1, 16

// Cookbook 1: solo, no remote — tasks are live locally, nothing touches the
// network, and live.defaults.tasks: false behaves exactly as today.
func TestRecipe01_SoloDeveloperNoRemote(t *testing.T) {
	w := newTeamWorld(t, th.Flag("live", "task", "add"))
	solo := w.Human("solo", "solo@example.com")
	dir := w.Root + "/work/solo/app"
	w.Git(w.Root, "init", "-q", "-b", "main", dir)
	c := solo.OpenClone(nil, dir)
	c.WriteFile("README.md", "# app\n")
	c.CommitAll("init")
	c.MustLore("init", "--non-interactive", "--name=app")
	tl := createTasklist(t, c)
	c.MustLore("task", "add", "solo work", "--tasklist", tl)
	if c.LocalRef(refLive) == "" {
		t.Fatal("tasks are live by default even with no remote (local live ref)")
	}
	if n := len(w.GitPushes("solo")); n != 0 {
		t.Fatalf("no remote: lore must not try to push (%d push calls)", n)
	}
	solo.WriteConfig(th.Config{Live: &th.Live{Defaults: map[string]bool{"tasks": false}}})
	id := mustID(t, c.MustLore("task", "add", "like today", "--tasklist", tl), "tsk")
	if _, ok := c.ReadFile(mainRowPath("", tblTasks, id)); !ok {
		t.Fatal("live.defaults.tasks: false must write .lore/data as today")
	}
}

// Cookbook 16 / §2: one live ref per repo, one folder per project; each
// project reads only its own folder.
func TestRecipe16_MonorepoOneLiveRefFolderPerProject(t *testing.T) {
	rt := newRepoTeam(t)
	web := rt.c1.In("web")
	web.MustLore("init", "--non-interactive", "--name=web")
	webTL := createTasklist(t, web)
	rt.c1.CommitAll("web project")
	rt.c1.AdminPush("origin", "main")
	root := rt.addTask(t, rt.c1, "fix header", "--live")
	webTask := mustID(t, web.MustLore("task", "add", "use the design tokens", "--tasklist", webTL, "--live"), "tsk")
	files := rt.app.Files(refLive)
	for _, want := range []string{liveEpochPath, liveMetaPath, liveRowPath(rootFolder, tblTasks, root), liveRowPath("web", tblTasks, webTask)} {
		if !hasItem(files, want) {
			t.Fatalf("live tree must hold %s: %v", want, files)
		}
	}
	if len(rt.app.Refs("refs/lore/live/")) != 0 {
		t.Fatal("a code repo has exactly one live ref, never refs/lore/live/<x>")
	}
	if hasItem(listedTitles(t, rt.c1), "use the design tokens") || hasItem(listedTitles(t, web), "fix header") {
		t.Fatal("each project reads only its own folder")
	}
}

// F5 + brief "crash mid-landing": lore is killed just before or just after
// the push of the landing commit (the landing `lore sync` runs, U-2); the
// next `lore sync` finishes the job with no lost row and no duplicate
// landing commit.
func TestF05_CrashMidLandingRecovers(t *testing.T) {
	for _, when := range []string{th.KillBefore, th.KillAfter} {
		t.Run("killed "+when+" the main push", func(t *testing.T) {
			rt := newRepoTeam(t, capLand)
			id := rt.addTask(t, rt.c1, "crash test")
			counter := rt.w.Root + "/kill-" + when
			crash := rt.c1.With(th.EnvKillOn+"=:refs/heads/main", th.EnvKillWhen+"="+when, th.EnvKillCounter+"="+counter)
			rt.c1.MustLore("task", "done", id)
			if r := crash.Lore("sync"); r.Code == 0 {
				t.Fatalf("the crash injection did not fire: %s", r)
			}
			_, onMain := rt.mainTask(t, id)
			if onMain != (when == th.KillAfter) {
				t.Fatalf("after a kill %s the push, main has the row = %v", when, onMain)
			}
			rt.c1.MustLore("sync")
			landed := mustRow(t, rt.app, refMain, mainRowPath("", tblTasks, id))
			if landed.Status != statusDone {
				t.Fatalf("recovery must land the finished row: %+v", landed)
			}
			before := rt.app.CommitCount(refMain)
			rt.c2.MustLore("sync")
			if rt.app.CommitCount(refMain) != before {
				t.Fatal("recovery must not land the same row twice")
			}
			if _, ok := rt.liveTask(t, id); ok {
				t.Fatal("after recovery the finished row leaves live")
			}
		})
	}
}

// F2 crash: lore is killed after its live commit is made locally but before
// the push; the next command pushes it (the local ref is the queue).
func TestF02_CrashBeforeLivePushIsRecovered(t *testing.T) {
	rt := newRepoTeam(t)
	rt.addTask(t, rt.c1, "warm up")
	crash := rt.c1.With(th.EnvKillOn+"=:refs/lore/live", th.EnvKillWhen+"="+th.KillBefore, th.EnvKillCounter+"="+rt.w.Root+"/kill-live")
	r := crash.Lore("task", "add", "made before the crash", "--tasklist", rt.tasklist)
	if r.Code == 0 {
		t.Fatalf("the crash injection did not fire: %s", r)
	}
	rt.c1.MustLore("task", "list")
	if !hasItem(listedTitles(t, rt.c2), "made before the crash") {
		t.Fatal("a live write committed locally before a crash must reach teammates on the next command")
	}
}

// Side effect 13: on a quiet repo with protected main, `lore sync status`
// lists the rows waiting to ride, with their age.
func TestSideEffect13_SyncStatusListsRowsWaitingOnProtectedMain(t *testing.T) {
	rt := newProtectedTeam(t)
	id := rt.addTask(t, rt.c1, "waits for a push")
	rt.c1.MustLore("task", "done", id)
	rt.c1.MustLore("sync").Says(t, "protected") // the landing attempt main refuses
	rt.c1.MustLore("sync", "status").Says(t, id, "protected")
}
