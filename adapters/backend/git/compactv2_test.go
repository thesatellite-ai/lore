package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// laterDate is the commit date of the "kept" commits these tests make: an
// hour after the cutoff they pass (now + 1h), so every other commit is old.
var laterDate = time.Now().Add(2 * time.Hour)

// commitLater commits files in the hand store as author dated laterDate,
// and pushes.
func (h *handStore) commitLater(author string, files map[string]string) {
	h.t.Helper()
	for p, body := range files {
		full := filepath.Join(h.dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			h.t.Fatal(err)
		}
		gitT(h.t, h.dir, "add", p)
	}
	date := "@" + strconv.FormatInt(laterDate.Unix(), 10) + " +0000"
	cmd := exec.Command("git", "-c", "user.name=x", "-c", "user.email="+author, "commit", "--quiet", "--no-verify", "--author", "x <"+author+">", "-m", "later edit")
	cmd.Dir = h.dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("commit: %v %s", err, out)
	}
	gitT(h.t, h.dir, "push", "--quiet", "origin", testBranch)
}

// cutoffNow is the cutoff the tests compact with: everything made now is
// older, the laterDate commits are kept.
func cutoffNow() time.Time { return time.Now().Add(time.Hour) }

// asWho opens a lore clone of remote on the org folder committing as email.
func asWho(t *testing.T, remote, email string) *Backend {
	t.Helper()
	b, err := OpenClone(context.Background(), remote, filepath.Join(t.TempDir(), "clones", "host", "store"), rowfs.FolderOrg, store.OpenOptions{},
		WithMerger(newestMerger{}), WithSigner(Signature{Name: strings.Split(email, "@")[0], Email: email}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// heldWorld is an org store where the member max made himself admin by
// hand (a held roster change), then the admin wrote one later memory.
type heldWorld struct {
	o          orgTeam
	maxPeriod  string // the path of max's stint
	trusted    string // its accepted blob (member)
	raw        string // its held blob (admin)
	keptMemory ids.AnyID
}

func newHeldWorld(t *testing.T) heldWorld {
	t.Helper()
	w := heldWorld{o: newOrgTeam(t)}
	w.maxPeriod = orgPath("actor_periods", w.o.max.period)
	w.trusted = gitT(t, w.o.h.dir, "rev-parse", "HEAD:"+w.maxPeriod)
	w.o.h.commit(w.o.max.email, map[string]string{w.maxPeriod: w.o.max.periodRow("admin", "")})
	w.raw = gitT(t, w.o.h.dir, "rev-parse", "HEAD:"+w.maxPeriod)
	w.keptMemory = mustID[ids.Memory](t)
	w.o.h.commitLater(w.o.boss.email, map[string]string{orgPath("memories", w.keptMemory): stampedRow(w.keptMemory, w.o.boss.actor, "2026-01-05T00:00:00.000Z", "kept")})
	return w
}

// TestCompactSnapshotIsTheTrustedView (devco:D-1003): a change held back
// at the cut (a member made himself admin by hand) is in the snapshot as
// its last accepted version, never the raw held one, and stays so in the
// kept commits; a fresh clone reads the member's role. Before the fix the
// snapshot took the raw tree and a fresh clone trusted the forged role.
func TestCompactSnapshotIsTheTrustedView(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newHeldWorld(t)
	boss := asWho(t, w.o.h.remote, w.o.boss.email)
	res, err := boss.Compact(ctx, CompactRequest{Cutoff: cutoffNow()})
	if err != nil || res.Nothing || res.Kept != 1 || res.Held != 1 || res.Archive != ArchiveRefPrefix+"0" {
		t.Fatalf("Compact = %+v, %v; want one kept commit, one held change, archive epoch-0", res, err)
	}
	root := gitT(t, w.o.h.remote, "rev-list", "--max-parents=0", testBranch)
	if got := gitT(t, w.o.h.remote, "rev-parse", root+":"+w.maxPeriod); got != w.trusted {
		t.Fatalf("the snapshot holds %s for the held stint, want the accepted %s (raw %s)", got, w.trusted, w.raw)
	}
	if got := gitT(t, w.o.h.remote, "rev-parse", testBranch+":"+w.maxPeriod); got != w.trusted {
		t.Fatalf("the compacted tip holds %s for the held stint, want the accepted %s", got, w.trusted)
	}
	if got := gitT(t, w.o.h.remote, "rev-parse", res.Archive); got == "" {
		t.Fatal("no archive ref on the remote")
	}
	fresh := orgFolderClone(t, w.o.h.remote)
	if role, _ := readField(t, fresh, w.o.max.period, "org_role"); role != "member" {
		t.Fatalf("a fresh clone reads max's role as %q, want member", role)
	}
	// a fresh clone downloads the new history only, never the archived
	// one: not with the store's own fetch, nor with the org link code's
	// fetch of every refs/lore/* ref (adapters/orgcfg fetchRefspecs)
	oldTip := gitT(t, w.o.h.remote, "rev-parse", res.Archive)
	gitT(t, fresh.Dir(), "fetch", "--quiet", "origin", "+refs/lore/*:refs/lore-remote/origin/*")
	if _, err := tryGit(t, fresh.Dir(), "cat-file", "-e", oldTip+"^{commit}"); err == nil {
		t.Fatalf("a fresh clone holds the archived tip %s: it downloaded the old history", oldTip)
	}
	if _, read := readField(t, fresh, w.keptMemory, "body"); !read {
		t.Fatal("the kept memory is gone")
	}
	if wr, _ := readField(t, fresh, w.keptMemory, "updated_by"); wr != w.o.boss.actor.String() {
		t.Fatalf("the kept memory names %q as writer", wr)
	}
	v, err := fresh.VerifyEpoch(ctx)
	if err != nil || !v.Verified || v.Epoch != 1 {
		t.Fatalf("VerifyEpoch on a fresh clone = %+v, %v; want verified epoch 1", v, err)
	}
}

// TestCompactSnapshotKeepsStatedWriters: the stamps in a snapshot count
// as written by the writers the rows state (the replay trusts the
// snapshot root as the trusted view it was made from), so a later commit
// that puts a row back to its snapshot version (a conflict settled with
// --take other) is read with that writer, as before the compaction.
// Judged as the compactor's commit, the snapshot's stamps named nobody
// and the revert was credited to whoever committed it.
func TestCompactSnapshotKeepsStatedWriters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	o := newOrgTeam(t)
	dee := newPerson(t, "dee@example.test")
	o.h.commit(o.boss.email, map[string]string{orgPath("actors", dee.actor): dee.actorRow(), orgPath("actor_periods", dee.period): dee.periodRow("member", "")})
	m := mustID[ids.Memory](t)
	maxVersion := stampedRow(m, o.max.actor, "2026-01-02T00:00:00.000Z", "max wrote it")
	o.h.commit(o.max.email, map[string]string{orgPath("memories", m): maxVersion})
	o.h.commitLater(dee.email, map[string]string{orgPath("memories", m): stampedRow(m, dee.actor, "2026-01-03T00:00:00.000Z", "dee changed it")})
	o.h.commitLater(dee.email, map[string]string{orgPath("memories", m): maxVersion})
	if wr, _ := readField(t, orgFolderClone(t, o.h.remote), m, "updated_by"); wr != o.max.actor.String() {
		t.Fatalf("before the compaction the settled row names %q, want max", wr)
	}
	boss := asWho(t, o.h.remote, o.boss.email)
	if res, err := boss.Compact(ctx, CompactRequest{Cutoff: cutoffNow()}); err != nil || res.Kept != 2 {
		t.Fatalf("Compact = %+v, %v; want the two later commits kept", res, err)
	}
	if wr, _ := readField(t, orgFolderClone(t, o.h.remote), m, "updated_by"); wr != o.max.actor.String() {
		t.Fatalf("after the compaction a fresh clone names %q as the settled row's writer, want max %s", wr, o.max.actor)
	}
}

// TestCompactRefusesANonAdmin: only an admin by the store's own roster
// compacts.
func TestCompactRefusesANonAdmin(t *testing.T) {
	t.Parallel()
	w := newHeldWorld(t)
	maxB := asWho(t, w.o.h.remote, w.o.max.email)
	before := gitT(t, w.o.h.remote, "rev-parse", testBranch)
	if _, err := maxB.Compact(context.Background(), CompactRequest{Cutoff: cutoffNow()}); err == nil || !strings.Contains(err.Error(), "not an admin") {
		t.Fatalf("Compact by a member = %v, want a refusal naming admin", err)
	}
	if gitT(t, w.o.h.remote, "rev-parse", testBranch) != before {
		t.Fatal("a refused compaction changed the remote")
	}
}

// forge rewrites the store as b would, with snap's tree overridden when
// raw is set (laundering the held change), and force-pushes it.
func forge(t *testing.T, b *Backend, raw bool) {
	t.Helper()
	ctx := context.Background()
	syncOK(t, b)
	h := gitT(t, b.Dir(), "rev-parse", "HEAD")
	chain, err := b.firstParentChain(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	cut := 1 // the commit before the later one
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		t.Fatal(err)
	}
	var held map[string]heldBlob
	if !raw {
		a, err := b.replayAt(ctx, chain[cut].id)
		if err != nil {
			t.Fatal(err)
		}
		if held, err = b.heldAt(ctx, a, chain[cut].id); err != nil {
			t.Fatal(err)
		}
	}
	tip, idMap, err := b.rewriteFrom(ctx, chain[cut].id, h, held, b.snapshotMeta(ctx, env, chain[cut], len(chain)-cut, cutoffNow(), 1))
	if err != nil {
		t.Fatal(err)
	}
	m, err := b.markerAt(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	m.Epoch = 1
	cp := Checkpoint{V: checkpointVersion, Kind: CheckpointCompact, FromEpoch: 0, ToEpoch: 1, OldTip: h, Archive: archiveRef(0), Cut: chain[cut].id}
	if tip, err = b.epochCommitV2(ctx, tip, m, cp); err != nil {
		t.Fatal(err)
	}
	if _, err := b.publishRewrite(ctx, CompactResult{FromEpoch: 0, ToEpoch: 1, Archive: archiveRef(0)}, h, h, tip, idMap[chain[cut].id], idMap); err != nil {
		t.Fatal(err)
	}
}

// TestForgedEpochIsRefused (devco:D-1003): a higher epoch force-pushed by
// a member (with or without a checkpoint), or by an admin whose snapshot
// launders a held change, is refused by every clone: it stays on its own
// history, and the refusal names who rewrote the store and --verify.
// Before the fix any higher epoch was adopted as it came.
func TestForgedEpochIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		forge      func(t *testing.T, w heldWorld)
	}{
		{"no checkpoint", "no compaction checkpoint", func(t *testing.T, w heldWorld) {
			ctx := context.Background()
			m := asWho(t, w.o.h.remote, w.o.max.email)
			syncOK(t, m)
			h := gitT(t, m.Dir(), "rev-parse", "HEAD")
			mk, err := m.markerAt(ctx, h)
			if err != nil {
				t.Fatal(err)
			}
			mk.Epoch = 1
			raw, _ := mk.Encode()
			blob := strings.TrimSpace(gitIn(t, m.Dir(), string(raw), "hash-object", "-w", "--stdin"))
			tree, err := m.withFile(ctx, gitT(t, m.Dir(), "rev-parse", "HEAD^{tree}"), rowfs.MarkerFile, blob)
			if err != nil {
				t.Fatal(err)
			}
			env, _ := m.commitEnv(ctx, Signature{})
			tip, err := m.commitTree(ctx, tree, nil, "lore: snapshot", env)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.forcePush(ctx, tip, h); err != nil {
				t.Fatal(err)
			}
		}},
		{"member with a checkpoint", "not an admin", func(t *testing.T, w heldWorld) {
			forge(t, asWho(t, w.o.h.remote, w.o.max.email), false)
		}},
		{"admin laundering a held change", "not the trusted view", func(t *testing.T, w heldWorld) {
			forge(t, asWho(t, w.o.h.remote, w.o.boss.email), true)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newHeldWorld(t)
			c := orgFolderClone(t, w.o.h.remote)
			syncOK(t, c)
			before := gitT(t, c.Dir(), "rev-parse", "HEAD")
			tc.forge(t, w)
			rep, err := c.Sync(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(rep.Refused) != 1 || !strings.Contains(rep.Refused[0].Reason, tc.want) || !strings.Contains(rep.Refused[0].Reason, "lore store compact --store "+w.o.h.remote+" --verify") {
				t.Fatalf("sync after a forged epoch = %+v; want one refusal naming %q and --verify", rep.Refused, tc.want)
			}
			if gitT(t, c.Dir(), "rev-parse", "HEAD") != before || c.Epoch(context.Background()) != 0 {
				t.Fatal("the clone moved onto the forged epoch")
			}
			if role, _ := readField(t, c, w.o.max.period, "org_role"); role != "member" {
				t.Fatalf("after the forged epoch max reads as %q", role)
			}
			// the printed --verify checks the epoch the sync refused
			if v, err := c.VerifyEpoch(context.Background()); err != nil || v.Verified || v.Epoch != 1 || !strings.Contains(v.Reason, tc.want) {
				t.Fatalf("VerifyEpoch after the refusal = %+v, %v; want epoch 1 not verified, naming %q", v, err, tc.want)
			}
		})
	}
}

// gitIn runs git in dir with stdin.
func gitIn(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// TestAdoptAndUndoEpoch: a member's clone adopts a verified compaction and
// keeps its rows, an undo puts the archived history back as epoch 2 with
// the writes made since, and the member adopts that too (verified); the
// authority verdict on the held change is the same at every step.
func TestAdoptAndUndoEpoch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newHeldWorld(t)
	member := orgFolderClone(t, w.o.h.remote)
	syncOK(t, member)
	oldTip := gitT(t, member.Dir(), "rev-parse", "HEAD")
	boss := asWho(t, w.o.h.remote, w.o.boss.email)
	if _, err := boss.Compact(ctx, CompactRequest{Cutoff: cutoffNow()}); err != nil {
		t.Fatal(err)
	}
	if rep := syncOK(t, member); len(rep.Refused) > 0 || member.Epoch(ctx) != 1 {
		t.Fatalf("member sync = %+v at epoch %d; want the compaction adopted", rep, member.Epoch(ctx))
	}
	if role, _ := readField(t, member, w.o.max.period, "org_role"); role != "member" {
		t.Fatalf("after adoption max reads as %q", role)
	}
	after := mustID[ids.Memory](t)
	write(t, boss, store.Change{Row: newGen(t).row(after, "written after the compaction", hlc.HLC{})})
	syncOK(t, boss)
	res, err := boss.UndoEpoch(ctx, UndoRequest{Epoch: 0})
	if err != nil || res.ToEpoch != 2 || res.Archive != ArchiveRefPrefix+"1" {
		t.Fatalf("UndoEpoch = %+v, %v", res, err)
	}
	if anc := gitT(t, w.o.h.remote, "merge-base", oldTip, testBranch); anc != oldTip {
		t.Fatal("the undone history does not hold the archived tip")
	}
	if rep := syncOK(t, member); len(rep.Refused) > 0 || member.Epoch(ctx) != 2 {
		t.Fatalf("member sync after undo = %+v at epoch %d", rep, member.Epoch(ctx))
	}
	get(t, member, after)
	if role, _ := readField(t, member, w.o.max.period, "org_role"); role != "member" {
		t.Fatalf("after the undo max reads as %q", role)
	}
	fresh := orgFolderClone(t, w.o.h.remote)
	if v, err := fresh.VerifyEpoch(ctx); err != nil || !v.Verified {
		t.Fatalf("VerifyEpoch after undo = %+v, %v", v, err)
	}
	if _, err := boss.UndoEpoch(ctx, UndoRequest{Epoch: 5}); err == nil {
		t.Fatal("an undo of an epoch that is not the newest compaction was accepted")
	}
}

// TestBloblessCloneAdoptsACompaction: a blobless clone (store.clone_filter
// blob:none, SG-3b) re-does the rewrite from trees alone and adopts it.
func TestBloblessCloneAdoptsACompaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newHeldWorld(t)
	c := openBlobless(t, w.o.h.remote)
	syncOK(t, c)
	boss := asWho(t, w.o.h.remote, w.o.boss.email)
	if _, err := boss.Compact(ctx, CompactRequest{Cutoff: cutoffNow()}); err != nil {
		t.Fatal(err)
	}
	if rep := syncOK(t, c); len(rep.Refused) > 0 || c.Epoch(ctx) != 1 {
		t.Fatalf("blobless sync after compaction = %+v at epoch %d; want it adopted", rep, c.Epoch(ctx))
	}
}

// TestLayerLandsAcrossTheEpoch: an open layer whose base is before the
// cutoff pulls the cut back to its base, is re-made on the compacted
// history and lands after the compaction.
func TestLayerLandsAcrossTheEpoch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	base := filepath.Join(t.TempDir(), "clones", "host", "store")
	now := time.Now()
	old := openPushAt(t, remote, base, WithMerger(newestMerger{}), fixedClock(now.Add(-48*time.Hour)))
	g := newGen(t)
	write(t, old, store.Change{Row: g.row(newID(t), "seed", hlc.HLC{})})
	syncOK(t, old)
	key := layerKey("feat/across")
	inLayer := newID(t)
	layerWrite(t, old, key, store.Change{Row: g.row(inLayer, "on the branch", hlc.HLC{})})
	if _, err := old.LayerPush(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		write(t, old, store.Change{Row: g.row(newID(t), "older", hlc.HLC{})})
	}
	syncOK(t, old)
	a := openPushAt(t, remote, base, WithMerger(newestMerger{}), fixedClock(now))
	write(t, a, store.Change{Row: g.row(newID(t), "new", hlc.HLC{})})
	syncOK(t, a)
	// a layer landed after the cutoff: its trash ref reaches the old tip's
	// commits until the compaction moves it
	landed := layerKey("feat/landed")
	layerWrite(t, a, landed, store.Change{Row: g.row(newID(t), "landed later", hlc.HLC{})})
	if _, err := a.LayerPush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.LayerLand(ctx, LandRequest{Key: landed, Subject: "lore: land feat/landed"}); err != nil {
		t.Fatal(err)
	}
	oldRoot := gitT(t, remote, "rev-list", "--max-parents=0", testBranch)
	res, err := a.Compact(ctx, CompactRequest{Cutoff: now.Add(-24 * time.Hour)})
	if err != nil || res.Nothing || res.Layers != 2 || res.LayerNote != "" {
		t.Fatalf("Compact = %+v, %v; want the layer re-made and pushed", res, err)
	}
	if res.Kept < 4 {
		t.Fatalf("kept %d commits; the layer base should have pulled the cut back to it", res.Kept)
	}
	// no layer or trash ref on the remote reaches the old history any more
	for _, ref := range strings.Fields(gitT(t, remote, "for-each-ref", "--format=%(refname)", "refs/lore/layer/", "refs/lore/layer-trash/")) {
		if _, err := tryGit(t, remote, "merge-base", "--is-ancestor", oldRoot, ref); err == nil {
			t.Fatalf("%s still reaches the old history after the compaction", ref)
		}
	}
	land, err := a.LayerLand(ctx, LandRequest{Key: key, Subject: "lore: land feat/across"})
	if err != nil || !land.Pushed {
		t.Fatalf("land after compaction = %+v, %v", land, err)
	}
	get(t, a, inLayer)
}

// TestNoRefReachesTheOldHistory (coordinator decision for devco:D-1003):
// a signals ref and a live ref built on a pre-cut commit and a layer
// landed and trashed before the cut are moved (or archived) by the
// compaction, so a fresh clone that fetches every refs/lore/* ref (the
// org link code's refspec) holds no commit of the replaced history.
func TestNoRefReachesTheOldHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	base := filepath.Join(t.TempDir(), "clones", "host", "store")
	now := time.Now()
	old := openPushAt(t, remote, base, WithMerger(newestMerger{}), fixedClock(now.Add(-48*time.Hour)))
	g := newGen(t)
	write(t, old, store.Change{Row: g.row(newID(t), "seed", hlc.HLC{})})
	syncOK(t, old)
	early := layerKey("feat/early")
	layerWrite(t, old, early, store.Change{Row: g.row(newID(t), "landed early", hlc.HLC{})})
	if _, err := old.LayerPush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := old.LayerLand(ctx, LandRequest{Key: early, Subject: "lore: land feat/early"}); err != nil {
		t.Fatal(err)
	}
	write(t, old, store.Change{Row: g.row(newID(t), "older", hlc.HLC{})})
	syncOK(t, old)
	preCut := gitT(t, old.Dir(), "rev-parse", "HEAD")
	// a claim and a live row on top of a commit the compaction squashes
	for _, ref := range []string{"refs/lore/signals/main", "refs/lore/live/main"} {
		c := gitIn(t, old.Dir(), "", "-c", "user.name=x", "-c", "user.email=x@example.test", "commit-tree", preCut+"^{tree}", "-p", preCut, "-m", ref)
		gitT(t, old.Dir(), "push", "--quiet", "origin", strings.TrimSpace(c)+":"+ref)
	}
	a := openPushAt(t, remote, base, WithMerger(newestMerger{}), fixedClock(now))
	write(t, a, store.Change{Row: g.row(newID(t), "new", hlc.HLC{})})
	syncOK(t, a)
	res, err := a.Compact(ctx, CompactRequest{Cutoff: now.Add(-24 * time.Hour)})
	if err != nil || res.Nothing {
		t.Fatalf("Compact = %+v, %v", res, err)
	}
	oldSet := map[string]bool{}
	for _, c := range strings.Fields(gitT(t, remote, "rev-list", res.Archive)) {
		oldSet[c] = true
	}
	fresh := filepath.Join(t.TempDir(), "fresh")
	gitT(t, filepath.Dir(fresh), "clone", "--quiet", "--no-local", remote, fresh)
	gitT(t, fresh, "fetch", "--quiet", "origin", "+refs/lore/*:refs/lore-remote/origin/*")
	for _, c := range strings.Fields(gitT(t, fresh, "rev-list", "--all")) {
		if oldSet[c] {
			t.Fatalf("a fresh clone fetching every refs/lore/* ref holds %s of the replaced history (refs: %s)", c, gitT(t, fresh, "for-each-ref", "--format=%(refname)"))
		}
	}
	for _, ref := range []string{"refs/lore/signals/main", "refs/lore/live/main"} {
		if gitT(t, remote, "rev-parse", ref) == "" {
			t.Fatalf("%s is gone; it should be moved, not dropped", ref)
		}
	}
}
