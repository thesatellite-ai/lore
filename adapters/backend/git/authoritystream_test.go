package git

import (
	"bytes"
	"context"
	"encoding/gob"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitfast"
	"github.com/thesatellite-ai/lore/model/ids"
)

// TestFullReplayDecidesInChunks (bug hunt H-279, devco:D-1017): the full
// roster replay reads the history a chunk of commits at a time, never
// every row version at once (a first sync of a 1M-commit store held ~4 GB
// that way), and decides every commit exactly as the whole-history replay
// does: here with one commit per chunk, across a held admin stint, a
// member's must rule, a member's edit of an admin's must rule and a
// second edit of it (whose accepted version an earlier chunk read).
func TestFullReplayDecidesInChunks(t *testing.T) {
	o := newOrgTeam(t)
	stint := mustID[ids.ActorPeriod](t)
	must, forged := prefixedID(t, ids.PrefixRule), prefixedID(t, ids.PrefixRule)
	o.h.commit(o.boss.email, map[string]string{orgPath("rules", must): ruleAt(must, o.boss.actor, "2026-01-02T00:00:00.000Z", "must", "review every migration")})
	o.h.commit(o.max.email, map[string]string{orgPath("actor_periods", stint): o.max.periodRowAt("2026-01-03T00:00:00.000Z", "admin", "")})
	o.h.commit(o.max.email, map[string]string{orgPath("rules", forged): ruleAt(forged, o.boss.actor, "2026-01-04T00:00:00.000Z", "must", "always push to main")})
	o.h.commit(o.max.email, map[string]string{orgPath("rules", must): ruleAt(must, o.max.actor, "2026-01-05T00:00:00.000Z", "must", "skip the review")})
	o.h.commit(o.max.email, map[string]string{orgPath("rules", must): ruleAt(must, o.max.actor, "2026-01-06T00:00:00.000Z", "must", "skip it twice")})
	for i := range 6 {
		own := mustID[ids.Memory](t)
		o.h.commit(o.max.email, map[string]string{orgPath("memories", own): stampedRow(own, o.max.actor, "2026-01-07T00:00:0"+string(rune('0'+i))+".000Z", "mine")})
	}
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
	replayBlobsPeak.Store(0)
	whole, err := b.replay(ctx, h, commits)
	if err != nil {
		t.Fatal(err)
	}
	wholePeak := replayBlobsPeak.Load()
	// one commit reads its own versions and, for a rule, the one it
	// replaced and the accepted one
	most := 0
	for _, c := range commits {
		most = max(most, len(c.changes)+2)
	}
	if len(whole.held) < 3 {
		t.Fatalf("the fixture holds %d file(s), want the stint and both rules: it lost its point", len(whole.held))
	}

	was := replayChunkCommits
	replayChunkCommits = 1
	t.Cleanup(func() { replayChunkCommits = was })
	replayBlobsPeak.Store(0)
	chunked, err := b.fullReplay(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if peak := replayBlobsPeak.Load(); peak > int64(most) || peak >= wholePeak {
		t.Errorf("the replay decoded %d row versions at once with one commit per chunk, want at most %d (the whole history is %d)", peak, most, wholePeak)
	}
	sameAuthority(t, chunked, whole)
}

// TestStreamReplayLogChunks: the replay log is handed over in chunks of
// replayChunkCommits commits, oldest first, every commit once, and a
// consumer's error stops the read and comes back as it was made.
func TestStreamReplayLogChunks(t *testing.T) {
	o := newOrgTeam(t)
	for range 4 {
		own := mustID[ids.Memory](t)
		o.h.commit(o.max.email, map[string]string{orgPath("memories", own): stampedRow(own, o.max.actor, "2026-01-02T00:00:00.000Z", "mine")})
	}
	b := orgFolderClone(t, o.h.remote)
	ctx := context.Background()
	h, err := b.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all, err := b.replayCommits(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	was := replayChunkCommits
	replayChunkCommits = 2
	t.Cleanup(func() { replayChunkCommits = was })
	var got []string
	sizes := []int{}
	if err := b.streamReplayLog(ctx, h, func(chunk []replayCommit) error {
		sizes = append(sizes, len(chunk))
		for _, c := range chunk {
			got = append(got, c.id)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(all) {
		t.Fatalf("streamed %d commits, want %d", len(got), len(all))
	}
	for i := range all {
		if got[i] != all[i].id {
			t.Fatalf("commit %d is %s, want %s (the replay's order)", i, got[i], all[i].id)
		}
	}
	for i, n := range sizes {
		if n > 2 || (n < 2 && i != len(sizes)-1) {
			t.Errorf("chunk sizes %v, want 2 each but the last", sizes)
			break
		}
	}
	stop := errFixture("stop here")
	calls := 0
	err = b.streamReplayLog(ctx, h, func([]replayCommit) error { calls++; return stop })
	if err != stop || calls != 1 {
		t.Errorf("a consumer's error came back as %v after %d call(s), want it as made after 1", err, calls)
	}
}

// errFixture is an error a test's consumer returns.
type errFixture string

// Error is the text.
func (e errFixture) Error() string { return string(e) }

// TestPaceReplayGC: once a long replay ran, the process collects at
// replayGCPercent (the CLI's 400 let a first sync's heap grow ~6x past
// the replay it keeps); a slower pace (a person's GOGC, or GOGC=off) is
// left as it is.
func TestPaceReplayGC(t *testing.T) {
	was := debug.SetGCPercent(400)
	t.Cleanup(func() { debug.SetGCPercent(was) })
	paceReplayGC()
	if got := debug.SetGCPercent(30); got != replayGCPercent {
		t.Errorf("after a long replay the pace is %d, want %d", got, replayGCPercent)
	}
	paceReplayGC()
	if got := debug.SetGCPercent(-1); got != 30 {
		t.Errorf("a pace of 30 became %d, want it left as it was", got)
	}
	paceReplayGC()
	if got := debug.SetGCPercent(was); got != -1 {
		t.Errorf("GOGC=off became %d, want it left off", got)
	}
}

// TestSavedReplayInParts: the saved replay is a header and then its
// stamps and admins in parts (gob builds each value it writes whole in
// memory: one value was hundreds of MB at 1M commits, H-279), read back
// as the replay it was; a file cut short between parts is not used.
func TestSavedReplayInParts(t *testing.T) {
	o := newOrgTeam(t)
	for range 3 {
		own := mustID[ids.Memory](t)
		o.h.commit(o.max.email, map[string]string{orgPath("memories", own): stampedRow(own, o.max.actor, "2026-01-02T00:00:00.000Z", "mine")})
	}
	b := orgFolderClone(t, o.h.remote)
	was := authorityPartRows
	authorityPartRows = 1
	t.Cleanup(func() { authorityPartRows = was })
	a := authorityNow(t, b)
	b.saveAuthority(a)
	raw, err := os.ReadFile(b.authorityPath())
	if err != nil {
		t.Fatal(err)
	}
	dec := gob.NewDecoder(bytes.NewReader(raw))
	var header savedAuthority
	if err := dec.Decode(&header); err != nil {
		t.Fatal(err)
	}
	if n := len(header.Stamps) + len(header.Admins) + len(header.Accepted) + len(header.Refused); n != 0 {
		t.Errorf("the header holds %d entries that grow with the history, want none (they come in parts)", n)
	}
	parts := 0
	for {
		var part savedPart
		if err := dec.Decode(&part); err != nil {
			t.Fatalf("part %d: %v", parts, err)
		}
		parts++
		if part.Last {
			break
		}
	}
	if want := len(a.state.stamps) + len(a.admins) + len(a.accepted) + len(a.refused); parts < want {
		t.Errorf("%d parts for %d entries, want one each", parts, want)
	}
	back := b.loadAuthority()
	if back == nil {
		t.Fatal("the replay written in parts was not read back")
	}
	sameAuthority(t, back, a)
	if err := os.WriteFile(b.authorityPath(), raw[:len(raw)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if b.loadAuthority() != nil {
		t.Error("a replay file cut short was used")
	}
}

// TestBulkBlobReadRunsGit (bug hunt H-279, devco:D-1017): the replay's
// read of bulkBlobs or more row versions runs git itself, never gitfast's
// in-process inflate (92% of a first sync's CPU at 100k commits: it has
// no delta cache), while its small reads, and any other read however
// big (a doctor's rows, read in id order, where gitfast is faster), are
// still answered in-process. Proven with a runner whose git does not
// exist: gitfast answers those, the replay's bulk read has to reach git
// and fails.
func TestBulkBlobReadRunsGit(t *testing.T) {
	o := newOrgTeam(t)
	b := orgFolderClone(t, o.h.remote)
	h, err := b.head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	blob := strings.TrimSpace(gitT(t, b.work, "rev-parse", h+":"+orgPath("actors", o.boss.actor)))
	s := gitfast.New()
	defer s.Close()
	ctx := gitfast.With(context.Background(), s)
	nogit := &Backend{git: runner{bin: filepath.Join(t.TempDir(), "no-git")}, work: b.work, mode: modeClone}
	small, err := nogit.replayBlobs(ctx, []string{blob})
	if err != nil || len(small[blob]) == 0 {
		t.Fatalf("a one-blob read was not answered in-process: %v", err)
	}
	many := make([]string, bulkBlobs)
	for i := range many {
		many[i] = blob
	}
	if got, err := nogit.readBlobs(ctx, many); err != nil || len(got[blob]) == 0 {
		t.Errorf("a row read of %d blobs was not answered in-process: %v", len(many), err)
	}
	if _, err := nogit.replayBlobs(ctx, many); err == nil {
		t.Errorf("the replay's read of %d blobs was answered without git, want it run by git (bulk)", len(many))
	}
}
