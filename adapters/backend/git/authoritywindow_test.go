package git

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
)

// TestReplayReadsOneCommitAWindowAtATime (bug hunt H-280, devco:D-1018):
// a commit that holds many row files (a compacted store's snapshot holds
// every one) is decided a window of files at a time, never with all its
// row versions decoded at once, and every file is decided exactly as a
// whole read decides it: here across a member's edit of an admin's must
// rule (judged against the version a window before read), a held admin
// stint and a forged writer, each among many ordinary rows.
func TestReplayReadsOneCommitAWindowAtATime(t *testing.T) {
	o := newOrgTeam(t)
	const rows = 24
	stint := mustID[ids.ActorPeriod](t)
	must := prefixedID(t, ids.PrefixRule)
	bulk := func(writer ids.AnyID, wall, body string) map[string]string {
		files := map[string]string{}
		for i := range rows {
			id := mustID[ids.Memory](t)
			files[orgPath("memories", id)] = stampedRow(id, writer, wall, body+" "+strconv.Itoa(i))
		}
		return files
	}
	first := bulk(o.boss.actor, "2026-01-02T00:00:00.000Z", "boss")
	first[orgPath("rules", must)] = ruleAt(must, o.boss.actor, "2026-01-02T00:00:00.000Z", "must", "review every migration")
	o.h.commit(o.boss.email, first)
	second := bulk(o.max.actor, "2026-01-03T00:00:00.000Z", "max")
	second[orgPath("rules", must)] = ruleAt(must, o.max.actor, "2026-01-03T00:00:00.000Z", "must", "skip the review")
	second[orgPath("actor_periods", stint)] = o.max.periodRowAt("2026-01-03T00:00:00.000Z", "admin", "")
	forged := mustID[ids.Memory](t)
	second[orgPath("memories", forged)] = stampedRow(forged, o.boss.actor, "2026-01-03T00:00:00.000Z", "in the boss's name")
	o.h.commit(o.max.email, second)
	third := bulk(o.max.actor, "2026-01-04T00:00:00.000Z", "max again")
	third[orgPath("rules", must)] = ruleAt(must, o.max.actor, "2026-01-04T00:00:00.000Z", "must", "skip it twice")
	o.h.commit(o.max.email, third)

	b := orgFolderClone(t, o.h.remote)
	ctx := context.Background()
	h, err := b.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := b.replayCommits(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	roster := map[string]bool{} // the versions read up front: every roster row's
	for _, c := range commits {
		for _, f := range c.changes {
			if isRowFile(f.path) && crossFile(tableOfPath(f.path)) {
				roster[f.newBlob] = true
			}
		}
	}
	replayBlobsPeak.Store(0)
	whole, err := b.fullReplay(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	wholePeak := replayBlobsPeak.Load()
	if wholePeak < rows {
		t.Fatalf("the replay read at most %d row versions at once, want a commit's %d: the fixture lost its point", wholePeak, rows)
	}
	if len(whole.held) < 3 {
		t.Fatalf("the fixture holds %d file(s), want the stint, the rule and the forged row: it lost its point", len(whole.held))
	}

	was, wasMin := replayWindowFiles, replayParseMin
	replayWindowFiles, replayParseMin = 2, 1 // and each window decoded on several goroutines
	t.Cleanup(func() { replayWindowFiles, replayParseMin = was, wasMin })
	replayBlobsPeak.Store(0)
	windowed, err := b.fullReplay(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	// the roster rows of the chunk, plus two files' versions: each a new
	// one and, for a rule, the one it replaced and the accepted one
	if peak, most := replayBlobsPeak.Load(), int64(len(roster)+2*3); peak > most {
		t.Errorf("the replay decoded %d row versions at once with a window of 2 files, want at most %d (a whole commit is %d)", peak, most, wholePeak)
	}
	sameAuthority(t, windowed, whole)
}

// TestSavedReplayHoldsOneSmallRecordPerFile (bug hunt H-280,
// devco:D-1018): the saved replay keeps each row file once, with binary
// ids, so a compacted store's replay (every row file of the store) stays
// small: 1.26M files were 320 MB with each path written twice, each blob
// id as hex and each stamp's actor id in full (~250 bytes a file).
func TestSavedReplayHoldsOneSmallRecordPerFile(t *testing.T) {
	o := newOrgTeam(t)
	const rows = 400
	files := map[string]string{}
	for i := range rows {
		id := mustID[ids.Memory](t)
		files[orgPath("memories", id)] = stampedRow(id, o.boss.actor, "2026-01-02T00:00:00.000Z", "note "+strconv.Itoa(i))
	}
	o.h.commit(o.boss.email, files)
	b := orgFolderClone(t, o.h.remote)
	a := authorityNow(t, b)
	if len(a.accepted) < rows {
		t.Fatalf("the replay holds %d files, want at least %d: the fixture lost its point", len(a.accepted), rows)
	}
	b.saveAuthority(a)
	info, err := os.Stat(b.authorityPath())
	if err != nil {
		t.Fatal(err)
	}
	// a path (~55 bytes here), the blob id, the stamp's two digests and
	// the gob framing
	if per := info.Size() / int64(len(a.accepted)); per > 140 {
		t.Errorf("the saved replay takes %d bytes per row file (%d bytes for %d files), want at most 140", per, info.Size(), len(a.accepted))
	}
	back := b.loadAuthority()
	if back == nil {
		t.Fatal("the saved replay was not read back")
	}
	sameAuthority(t, back, a)
}

// TestBigReplayIsHeldAsItsViews (bug hunt H-280, devco:D-1018): a replay
// over authorityHoldFiles row files is not kept whole in the process once
// it is saved: its row view and head view serve the reads (a held change
// still reads as its accepted version) and a later replay reads the saved
// one back with the same verdicts. A smaller replay is kept as before.
func TestBigReplayIsHeldAsItsViews(t *testing.T) {
	o := newOrgTeam(t)
	stint := mustID[ids.ActorPeriod](t)
	o.h.commit(o.max.email, map[string]string{orgPath("actor_periods", stint): o.max.periodRowAt("2026-01-03T00:00:00.000Z", "admin", "")})
	b := orgFolderClone(t, o.h.remote)
	ctx := context.Background()
	h, err := b.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cached := func() bool {
		authorityCache.Lock()
		defer authorityCache.Unlock()
		return authorityCache.byWork[b.work] != nil
	}
	was := authorityHoldFiles
	t.Cleanup(func() { authorityHoldFiles = was })
	authorityHoldFiles = 2
	a := authorityNow(t, b)
	if len(a.accepted) <= authorityHoldFiles || len(a.held) == 0 {
		t.Fatalf("the replay covers %d files and holds %d: the fixture lost its point", len(a.accepted), len(a.held))
	}
	if cached() {
		t.Fatal("a replay over authorityHoldFiles files is kept whole in the process")
	}
	view, err := b.rowAuthorityOf(ctx, h)
	if err != nil || view == nil || len(view.held) != len(a.held) {
		t.Fatalf("the row view after a big replay: %v, %v", view, err)
	}
	if _, v := cloneHeadView(ctx, b.work); v == nil || !v.view {
		t.Fatal("the org file check has no head view after a big replay")
	}
	again := authorityNow(t, b)
	sameAuthority(t, again, a)

	authorityHoldFiles = 1 << 30
	authorityCache.Lock()
	delete(authorityCache.byWork, b.work)
	authorityCache.Unlock()
	authorityNow(t, b)
	if !cached() {
		t.Error("a small replay is no longer kept whole in the process")
	}
}
