package git

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// oldHistoryWorld is heldWorld with n more old commits by the admin
// before the kept one: a history the replay at the cut would walk again.
func oldHistoryWorld(t *testing.T, n int) heldWorld {
	t.Helper()
	w := heldWorld{o: newOrgTeam(t)}
	w.maxPeriod = orgPath("actor_periods", w.o.max.period)
	w.trusted = gitT(t, w.o.h.dir, "rev-parse", "HEAD:"+w.maxPeriod)
	w.o.h.commit(w.o.max.email, map[string]string{w.maxPeriod: w.o.max.periodRow("admin", "")})
	w.raw = gitT(t, w.o.h.dir, "rev-parse", "HEAD:"+w.maxPeriod)
	for i := range n {
		m := mustID[ids.Memory](t)
		w.o.h.commitLocal(w.o.boss.email, map[string]string{orgPath("memories", m): stampedRow(m, w.o.boss.actor, fmt.Sprintf("2026-01-04T00:00:%02d.000Z", i%60), "old "+strconv.Itoa(i))})
	}
	gitT(t, w.o.h.dir, "push", "--quiet", "origin", testBranch)
	w.keptMemory = mustID[ids.Memory](t)
	w.o.h.commitLater(w.o.boss.email, map[string]string{orgPath("memories", w.keptMemory): stampedRow(w.keptMemory, w.o.boss.actor, "2026-01-05T00:00:00.000Z", "kept")})
	return w
}

// TestCompactReplaysNoHistoryToTheCut (devco:D-1004, store growth SG-6):
// the compacting clone and an adopting clone take the files held at the
// cut from the replay they already hold at their tip (its hold log), so
// neither replays the old history again: a handful of commits replayed,
// not the 40 before the cut. The snapshot is still the trusted view (the
// held self-promotion is not in it) and the member's clone adopts it.
// Before the fix each replayed the whole history to the cut (9 s at 10k
// commits, 84 s at 100k) on top of the replay it held.
func TestCompactReplaysNoHistoryToTheCut(t *testing.T) {
	ctx := context.Background()
	const old = 40
	w := oldHistoryWorld(t, old)
	boss := asWho(t, w.o.h.remote, w.o.boss.email)
	syncOK(t, boss)
	authorityNow(t, boss) // the replay a clone holds after any read
	member := orgFolderClone(t, w.o.h.remote)
	syncOK(t, member)
	authorityNow(t, member)

	before := replayCount()
	res, err := boss.Compact(ctx, CompactRequest{Cutoff: cutoffNow()})
	if err != nil || res.Held != 1 || res.Kept != 1 {
		t.Fatalf("Compact = %+v, %v; want one held change, one kept commit", res, err)
	}
	if n := replayCount() - before; n > 3 {
		t.Fatalf("the compaction replayed %d commits; the %d before the cut must come from the replay at the tip", n, old)
	}
	root := gitT(t, w.o.h.remote, "rev-list", "--max-parents=0", testBranch)
	if got := gitT(t, w.o.h.remote, "rev-parse", root+":"+w.maxPeriod); got != w.trusted {
		t.Fatalf("the snapshot holds %s for the held stint, want the accepted %s", got, w.trusted)
	}

	before = replayCount()
	if rep := syncOK(t, member); len(rep.Refused) > 0 || member.Epoch(ctx) != 1 {
		t.Fatalf("member sync = %+v at epoch %d; want the compaction adopted", rep, member.Epoch(ctx))
	}
	if role, _ := readField(t, member, w.o.max.period, "org_role"); role != "member" {
		t.Fatalf("after adoption max reads as %q", role)
	}
	if n := replayCount() - before; n > 3 {
		t.Fatalf("the adoption replayed %d commits; the old history must come from the replay the clone holds", n)
	}
}

// TestCompactCarriesCreditAcrossTheCut (STORE_GROWTH_SPEC 9, authority
// continuity, devco:D-1004): a row max wrote before the cut naming the
// admin as its writer is read with max credited (C35) before the
// compaction, and still after it on the compacting clone and on a
// member's clone that adopted it: each carries its replay over the old
// ─▶ new id map instead of replaying the new history from the snapshot.
// Before, both replayed the new history, whose snapshot vouches for the
// writer each row states, and the row read as the admin's.
func TestCompactCarriesCreditAcrossTheCut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	o := newOrgTeam(t)
	spoof := mustID[ids.Memory](t)
	o.h.commit(o.max.email, map[string]string{orgPath("memories", spoof): stampedRow(spoof, o.boss.actor, "2026-01-02T00:00:01.000Z", "says the admin")})
	kept := mustID[ids.Memory](t)
	o.h.commitLater(o.boss.email, map[string]string{orgPath("memories", kept): stampedRow(kept, o.boss.actor, "2026-01-05T00:00:00.000Z", "kept")})
	boss := asWho(t, o.h.remote, o.boss.email)
	member := orgFolderClone(t, o.h.remote)
	for _, b := range []*Backend{boss, member} {
		syncOK(t, b)
		if wr, _ := readField(t, b, spoof, "updated_by"); wr != o.max.actor.String() {
			t.Fatalf("before the compaction the row names %q, want max (credited)", wr)
		}
	}
	if _, err := boss.Compact(ctx, CompactRequest{Cutoff: cutoffNow()}); err != nil {
		t.Fatal(err)
	}
	if rep := syncOK(t, member); len(rep.Refused) > 0 || member.Epoch(ctx) != 1 {
		t.Fatalf("member sync = %+v at epoch %d; want the compaction adopted", rep, member.Epoch(ctx))
	}
	for name, b := range map[string]*Backend{"the compacting clone": boss, "the adopting clone": member} {
		forgetAuthority(b) // a new process: the saved replay
		if wr, _ := readField(t, b, spoof, "updated_by"); wr != o.max.actor.String() {
			t.Errorf("after the compaction %s reads the row as written by %q, want max %s (the credit from before the cut)", name, wr, o.max.actor)
		}
	}
}

// TestFastWriteMatchesCommitTree: fastWrite makes exactly the commits
// commit-tree (with overlaid's tree) makes for the same tree, edits,
// parents, people, dates and message, so a clone that re-does a rewrite
// written either way gets the same ids (devco:D-1004).
func TestFastWriteMatchesCommitTree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newHeldWorld(t)
	b := orgFolderClone(t, w.o.h.remote)
	syncOK(t, b)
	h := gitT(t, b.Dir(), "rev-parse", "HEAD")
	tree := gitT(t, b.Dir(), "rev-parse", "HEAD^{tree}")
	parent := gitT(t, b.Dir(), "rev-parse", "HEAD~1")
	held := map[string]heldBlob{w.maxPeriod: {raw: w.raw, trusted: w.trusted}, "org/memories/gone.json": {raw: w.raw}}
	cur := map[string]string{w.maxPeriod: w.raw, "org/memories/gone.json": w.raw}
	metas := []commitMeta{
		{env: identEnv("Ann Example", "ann@example.test", "1700000000 +0130", "Bob", "bob@example.test", "1700000100 -0700"), msg: "subject\n\nbody line\n\nLore-X: 1"},
		{env: identEnv("Cy", "cy@partner.test", "@1700000200 +0000", "Cy", "cy@partner.test", "@1700000200 +0000"), msg: "one line"},
		{env: identEnv("Dee", "dee@example.test", "1700000300 +0000", "Dee", "dee@example.test", "1700000300 +0000"), msg: "  padded\n\n\tindented"},
	}
	var want []string
	for i, m := range metas {
		parents := []string{h}
		if i == 1 {
			parents = nil
		}
		if i == 2 {
			parents = []string{want[0], parent}
		}
		tr, err := b.overlaid(ctx, tree, cur, held)
		if err != nil {
			t.Fatal(err)
		}
		id, err := b.commitTree(ctx, tr, parents, m.msg, m.env)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, id)
	}
	fc := make([]fastCommit, len(metas))
	for i, m := range metas {
		fc[i] = fastCommit{tree: tree, edits: overlayEdits(cur, held), parents: []string{h}, meta: m}
	}
	fc[1].parents = nil
	fc[2].parents = []string{markOf(0), parent}
	got, err := b.fastWrite(ctx, fc)
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("commit %d: fastWrite made %s, commit-tree %s:\n%s\n---\n%s", i, got[i], want[i], gitT(t, b.Dir(), "cat-file", "commit", got[i]), gitT(t, b.Dir(), "cat-file", "commit", want[i]))
		}
	}
	// metasOf reads them back as metaOf does
	read, err := b.metasOf(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range want {
		one, err := b.metaOf(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(read[id]) != fmt.Sprint(one) {
			t.Errorf("metasOf(%s) = %+v, metaOf %+v", short(id), read[id], one)
		}
	}
	if refs := gitT(t, b.Dir(), "for-each-ref", "refs/lore-tmp/"); refs != "" {
		t.Errorf("fastWrite left its ref behind: %s", refs)
	}
}

// identEnv is a commitMeta env.
func identEnv(an, ae, ad, cn, ce, cd string) []string {
	return []string{envAuthorName + "=" + an, envAuthorEmail + "=" + ae, "GIT_AUTHOR_DATE=" + ad,
		envCommitterName + "=" + cn, envCommitterEmail + "=" + ce, "GIT_COMMITTER_DATE=" + cd}
}

// TestHeldAtCutEqualsReplayAtCut is the equality property of heldAtCut
// (devco:D-1004): over random histories (roster changes by the admin and
// a member, borrowed writers, merges onto HEAD, reverts of held changes)
// the files held at any commit of HEAD's first-parent line, read from the
// replay at HEAD's hold log, are exactly those of a full replay at that
// commit, with the same trusted versions.
func TestHeldAtCutEqualsReplayAtCut(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3, 4, 5} {
		t.Run(strconv.FormatUint(seed, 10), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			o := newOrgTeam(t)
			b := orgFolderClone(t, o.h.remote)
			r := rand.New(rand.NewPCG(seed, seed*104729))
			randomHistory(t, b.work, o, r, 30, func() { authorityNow(t, b) })
			h, err := b.head(ctx)
			if err != nil {
				t.Fatal(err)
			}
			tip, err := b.authorityOf(ctx, h)
			if err != nil {
				t.Fatal(err)
			}
			chain, err := b.firstParentChain(ctx, h)
			if err != nil {
				t.Fatal(err)
			}
			heldSeen := 0
			for _, c := range chain {
				got, err := b.heldAtCut(ctx, tip, c.id)
				if err != nil {
					t.Fatal(err)
				}
				commits, err := b.replayCommits(ctx, c.id)
				if err != nil {
					t.Fatal(err)
				}
				at, err := b.replay(ctx, c.id, commits)
				if err != nil {
					t.Fatal(err)
				}
				want, err := b.heldAt(ctx, at, c.id)
				if err != nil {
					t.Fatal(err)
				}
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Fatalf("seed %d at %s: heldAtCut = %v, a full replay there holds %v", seed, short(c.id), got, want)
				}
				heldSeen += len(want)
			}
			if heldSeen == 0 {
				t.Errorf("seed %d: no commit had a held file: the history tests nothing", seed)
			}
		})
	}
}

// randomHistory makes steps random commits in the clone at dir (read, a
// replay, now and then): roster stints of
// the member and the contractor (the member making himself admin is held),
// memories with borrowed writers, reverts, and merges onto HEAD.
func randomHistory(t *testing.T, dir string, o orgTeam, r *rand.Rand, steps int, read func()) {
	t.Helper()
	people := []person{o.boss, o.max, o.cy}
	mems := []ids.AnyID{mustID[ids.Memory](t), mustID[ids.Memory](t), mustID[ids.Memory](t)}
	tick := 0
	// commitIn, allowing a commit that changes nothing (the same stint again)
	commitIn := func(t *testing.T, dir, author, rel, body string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		gitT(t, dir, "add", rel)
		gitT(t, dir, "-c", "user.name=x", "-c", "user.email="+author, "commit", "--quiet", "--no-verify", "--allow-empty", "--author", "x <"+author+">", "-m", "edit")
	}
	commit := func() {
		tick++
		wall := fmt.Sprintf("2026-02-01T00:%02d:%02d.000Z", tick/60, tick%60)
		author := people[r.IntN(len(people))]
		switch r.IntN(4) {
		case 0:
			commitIn(t, dir, author.email, orgPath("actor_periods", o.max.period), o.max.periodRowAt(wall, []string{"admin", "member"}[r.IntN(2)], ""))
		case 1:
			commitIn(t, dir, author.email, orgPath("actor_periods", o.cy.period), o.cy.contractorPeriod())
		default:
			id := mems[r.IntN(len(mems))]
			writer := people[r.IntN(len(people))]
			commitIn(t, dir, author.email, orgPath("memories", id), stampedRow(id, writer.actor, wall, "v"+strconv.Itoa(tick)))
		}
	}
	commit()
	for step := range steps {
		switch r.IntN(6) {
		case 0: // a merge onto HEAD from a side line
			side := "side" + strconv.Itoa(step) + "-" + strconv.FormatUint(r.Uint64(), 36)
			gitT(t, dir, "checkout", "--quiet", "-b", side, "HEAD~"+strconv.Itoa(min(1, step)))
			commit()
			gitT(t, dir, "checkout", "--quiet", testBranch)
			gitT(t, dir, "-c", "user.name=x", "-c", "user.email="+o.boss.email, "merge", "--quiet", "--no-edit", "--no-ff", "-X", "theirs", side)
		case 1: // the admin puts max's stint back (a revert of a held change)
			commitIn(t, dir, o.boss.email, orgPath("actor_periods", o.max.period), o.max.periodRow("member", ""))
		default:
			commit()
		}
		if read != nil && r.IntN(4) == 0 {
			read() // a read: the replay carried on from here
		}
	}
	if strings.TrimSpace(gitT(t, dir, "status", "--porcelain")) != "" {
		t.Fatal("the random history left the clone dirty")
	}
}

// TestCarriedReplayKeepsVerdicts is the property of the replay carried
// over a compaction (STORE_GROWTH_SPEC 9, devco:D-1004): over random
// histories (held self-promotions and their reverts, borrowed writers,
// merges) compacted at a random point, an adopting clone's replay after
// the compaction is its replay before it: the same roster and stamps,
// accepted versions, credits and admins, the same version served for
// every file, and the same holds, but for the ones the snapshot resolved
// (a file held at the cut and not changed since: the new history carries
// its trusted version, so nothing is held back, as on a fresh clone).
// Before the fix the clone replayed the new history from the snapshot,
// which vouches for the writer each row states: credits and stamps
// changed.
func TestCarriedReplayKeepsVerdicts(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3, 4} {
		t.Run(strconv.FormatUint(seed, 10), func(t *testing.T) {
			ctx := context.Background()
			o := newOrgTeam(t)
			r := rand.New(rand.NewPCG(seed, seed*7907))
			randomHistory(t, o.h.dir, o, r, 12+r.IntN(12), nil)
			gitT(t, o.h.dir, "push", "--quiet", "origin", testBranch)
			// the commits after the cut are dated after the cutoff
			later := "@" + strconv.FormatInt(laterDate.Unix(), 10) + " +0000"
			t.Setenv("GIT_COMMITTER_DATE", later)
			t.Setenv("GIT_AUTHOR_DATE", later)
			randomHistory(t, o.h.dir, o, r, 1+r.IntN(8), nil)
			gitT(t, o.h.dir, "push", "--quiet", "origin", testBranch)
			t.Setenv("GIT_COMMITTER_DATE", "")
			t.Setenv("GIT_AUTHOR_DATE", "")

			member := orgFolderClone(t, o.h.remote)
			syncOK(t, member)
			before := authorityNow(t, member)
			oldTip := before.head
			boss := asWho(t, o.h.remote, o.boss.email)
			if res, err := boss.Compact(ctx, CompactRequest{Cutoff: cutoffNow()}); err != nil || res.Nothing {
				t.Fatalf("Compact = %+v, %v", res, err)
			}
			if rep := syncOK(t, member); len(rep.Refused) > 0 || member.Epoch(ctx) != 1 {
				t.Fatalf("member sync = %+v at epoch %d; want the compaction adopted", rep, member.Epoch(ctx))
			}
			forgetAuthority(member) // a new process: the saved replay
			after := authorityNow(t, member)
			sameVerdicts(t, member, before, oldTip, after)
		})
	}
}

// sameVerdicts fails t when the replay after an adopted compaction
// (after) differs from the one before (before, at oldTip) in anything a
// read or the next commit's verdict uses.
func sameVerdicts(t *testing.T, b *Backend, before *authority, oldTip string, after *authority) {
	t.Helper()
	ctx := context.Background()
	norm := func(a *authority) savedAuthority {
		s, ok := a.saved("test")
		if !ok {
			t.Fatal("the authority has no admin set")
		}
		for p, c := range s.Credited {
			c.Commit = ""
			s.Credited[p] = c
		}
		s.Head, s.Held, s.Trusted, s.Refused, s.Admins, s.Snaps, s.Snap, s.OrgFile, s.Holds = "", nil, nil, nil, nil, nil, 0, nil, nil
		return s
	}
	if g, w := norm(after), norm(before); fmt.Sprint(g) != fmt.Sprint(w) {
		t.Errorf("roster, stamps, accepted versions or credits differ after the compaction:\n got  %+v\n want %+v", g, w)
	}
	if fmt.Sprint(after.snap) != fmt.Sprint(before.snap) {
		t.Errorf("the admins at the tip differ: got %+v, want %+v", after.snap, before.snap)
	}
	// the version each file serves: the trusted one while held, else the tree's
	serve := func(a *authority, tip string) map[string]string {
		out := map[string]string{}
		for _, l := range strings.Split(gitT(t, b.Dir(), "ls-tree", "-r", tip), "\n") {
			f := strings.Fields(l)
			if len(f) == 4 && isRowFile(f[3]) {
				out[f[3]] = f[2]
			}
		}
		for p := range a.held {
			if a.trusted[p] == "" {
				delete(out, p)
			} else {
				out[p] = a.trusted[p]
			}
		}
		return out
	}
	h, err := b.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if g, w := serve(after, h), serve(before, oldTip); fmt.Sprint(g) != fmt.Sprint(w) {
		t.Errorf("the versions served differ after the compaction:\n got  %v\n want %v", g, w)
	}
	for p := range after.held {
		if _, ok := before.held[p]; !ok {
			t.Errorf("%s is held after the compaction only", p)
		}
	}
	for p, hc := range before.held {
		if _, ok := after.held[p]; !ok && b.hasCommit(ctx, hc.Commit) {
			if anc, _ := b.isAncestor(ctx, hc.Commit, h); anc {
				t.Errorf("%s was held by %s, a commit the new history kept, and is not held any more", p, short(hc.Commit))
			}
		}
	}
}

// TestRewriteStartsNoGitPerKeptCommit (devco:D-1004): re-making the kept
// commits (each with the held file overlaid) starts a fixed number of git
// processes however many commits are kept: one cat-file for their people
// and messages, one fast-import for every commit. Before, each kept commit
// started about five (git log, read-tree, update-index, write-tree,
// commit-tree): 66 ms a commit, minutes for a 1M → 10k compaction.
func TestRewriteStartsNoGitPerKeptCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const kept = 20
	w := newHeldWorld(t) // one kept commit already
	for i := range kept - 1 {
		m := mustID[ids.Memory](t)
		w.o.h.commitLater(w.o.boss.email, map[string]string{orgPath("memories", m): stampedRow(m, w.o.boss.actor, fmt.Sprintf("2026-01-06T00:00:%02d.000Z", i), "kept")})
	}
	b := asWho(t, w.o.h.remote, w.o.boss.email)
	syncOK(t, b)
	h := gitT(t, b.Dir(), "rev-parse", "HEAD")
	chain, err := b.firstParentChain(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	cut := chain[kept]
	a, err := b.replayAt(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	held, err := b.heldAtCut(ctx, a, cut.id)
	if err != nil || len(held) != 1 {
		t.Fatalf("held at the cut = %v, %v; want the self-promotion", held, err)
	}
	env, err := b.commitEnv(ctx, Signature{})
	if err != nil {
		t.Fatal(err)
	}
	calls := countGit(t, b)
	tip, idMap, err := b.rewriteFrom(ctx, cut.id, h, held, b.snapshotMeta(ctx, env, cut, len(chain)-kept, cutoffNow(), 1))
	if err != nil || len(idMap) != kept+1 {
		t.Fatalf("rewriteFrom = %s, %d ids, %v; want %d", tip, len(idMap), err, kept+1)
	}
	if n := calls("\n"); n >= kept {
		t.Fatalf("re-making %d kept commits started %d git processes; want a fixed few (the snapshot, one read, one write)", kept, n)
	}
	if got := gitT(t, b.Dir(), "rev-parse", tip+":"+w.maxPeriod); got != w.trusted {
		t.Fatalf("the re-made tip holds %s for the held stint, want the accepted %s", got, w.trusted)
	}
}

// TestCompactSnapshotHoldsTheAcceptedOrgFile (devco:D-1004, the class of
// D-1003's trusted-view snapshot): a member's change to lore-org.yaml
// before the cut, held back by the org file check, is not what the
// snapshot carries: the snapshot and the kept commits hold the admin's
// accepted version, so a fresh clone (which trusts the snapshot root)
// never reads the member's edit. Before, the snapshot replaced held row
// files only and took the org file raw: the held edit became the
// snapshot's, accepted by every fresh clone.
func TestCompactSnapshotHoldsTheAcceptedOrgFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	o := newOrgTeam(t)
	orgFile := rowfs.FolderOrg + "/" + orgFileName
	o.h.commit(o.boss.email, map[string]string{orgFile: "name: acme\nstores: []\n"})
	accepted := gitT(t, o.h.dir, "rev-parse", "HEAD:"+orgFile)
	o.h.commit(o.max.email, map[string]string{orgFile: "name: acme\nstores:\n  - url: git@example.invalid:max/own.git\n"})
	kept := mustID[ids.Memory](t)
	o.h.commitLater(o.boss.email, map[string]string{orgPath("memories", kept): stampedRow(kept, o.boss.actor, "2026-01-05T00:00:00.000Z", "kept")})
	boss := asWho(t, o.h.remote, o.boss.email)
	res, err := boss.Compact(ctx, CompactRequest{Cutoff: cutoffNow()})
	if err != nil || res.Held != 1 {
		t.Fatalf("Compact = %+v, %v; want the member's org file edit counted as held", res, err)
	}
	root := gitT(t, o.h.remote, "rev-list", "--max-parents=0", testBranch)
	for _, rev := range []string{root, testBranch} {
		if got := gitT(t, o.h.remote, "rev-parse", rev+":"+orgFile); got != accepted {
			t.Errorf("%s holds %s for %s, want the admin's accepted %s", rev, got, orgFile, accepted)
		}
	}
	if rep := syncOK(t, orgFolderClone(t, o.h.remote)); len(rep.Refused) > 0 {
		t.Fatalf("a fresh clone refused the compacted history: %+v", rep.Refused)
	}
}

// TestOrgFileCheckReadsTheSavedReplay (devco:D-1004): the org file check
// of a new process (OrgFileChanges, through a temporary backend made
// before any open of the clone) reads the replay the clone keeps in its
// git folder instead of replaying the whole history in memory. Before, it
// replayed everything, and on a compacted clone that replay (from the
// snapshot) replaced the one carried over the compaction.
func TestOrgFileCheckReadsTheSavedReplay(t *testing.T) {
	ctx := context.Background()
	w := oldHistoryWorld(t, 10)
	b := orgFolderClone(t, w.o.h.remote)
	syncOK(t, b)
	h := authorityNow(t, b).head // saved in the clone's git folder
	forgetAuthority(b)           // a new process: nothing in memory,
	authorityFiles.Delete(filepath.Clean(b.work))
	defer b.keepAuthorityAt(b.authorityAt) // no open of the clone yet
	before := replayCount()
	if _, ok := OrgFileChanges(ctx, b.work, h); !ok {
		t.Fatal("OrgFileChanges did not answer at the clone's HEAD")
	}
	if n := replayCount() - before; n != 0 {
		t.Fatalf("the org file check of a new process replayed %d commits; want the saved replay read", n)
	}
	if _, err := os.Stat(filepath.Join(b.work, ".git", authorityFile)); err != nil {
		t.Fatalf("no saved replay in the clone's git folder: %v", err)
	}
}
