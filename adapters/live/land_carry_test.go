package live

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
)

// landCarryFixture is a clone whose local live ref holds one write the
// remote has not seen, behind a FaultRemote that counts pushes.
func landCarryFixture(t *testing.T) (*gitremotetest.World, *Ref, *gitremote.FaultRemote) {
	t.Helper()
	w := gitremotetest.NewWorld(t)
	a := w.Clone("a")
	fault := gitremote.NewFaultRemote(a.Remote)
	r := newRef(t, a, fault)
	write(t, r, put("tsk_2", File{Body: row("tsk_2", "waiting", "low")}))
	return w, r, fault
}

// landCarried lands landItem("x") carrying the live ref (LandRequest.Live).
func landCarried(t *testing.T, r *Ref) LandResult {
	t.Helper()
	res, err := r.Land(context.Background(), LandRequest{Branch: "main", Items: []LandItem{landItem("x")}, Layout: testLayout, Live: true})
	if err != nil {
		t.Fatalf("land: %+v %v", res, err)
	}
	return res
}

// liveOnOrigin reports whether the remote's live ref is the local one.
func liveOnOrigin(t *testing.T, w *gitremotetest.World, r *Ref) bool {
	t.Helper()
	s, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s.Local != "" && w.OriginRef(RefLive) == s.Local
}

// devco L-12-land: lore sync's landing sends the waiting live writes and
// the landing commit in ONE push (atomic), not a push of the live ref and
// another of the branch.
func TestLandCarriesLiveInOnePush(t *testing.T) {
	w, r, fault := landCarryFixture(t)
	res := landCarried(t, r)
	if res.Status != LandDone || res.Live == nil || res.Live.Status != PushDone {
		t.Fatalf("land: %+v live %+v", res, res.Live)
	}
	if n := fault.Pushes(); n != 1 {
		t.Fatalf("the landing made %d pushes, want 1 carrying both refs", n)
	}
	if !liveOnOrigin(t, w, r) || w.OriginRef("refs/heads/main") != res.Commit {
		t.Fatal("the remote does not hold both the live ref and the landing commit")
	}
	if s, _ := r.Snapshot(context.Background()); s.Ahead() {
		t.Fatal("the live ref still counts as waiting after the push that carried it")
	}
}

// A remote without atomic push: the live ref goes first on its own, then
// the branch, so the branch never holds rows the remote's live ref lacks.
func TestLandCarryWithoutAtomicPushesLiveFirst(t *testing.T) {
	w, r, fault := landCarryFixture(t)
	fault.SetNoAtomic(true)
	res := landCarried(t, r)
	if res.Status != LandDone || res.Live == nil || res.Live.Status != PushDone {
		t.Fatalf("land: %+v live %+v", res, res.Live)
	}
	if !liveOnOrigin(t, w, r) || w.OriginRef("refs/heads/main") != res.Commit {
		t.Fatal("both refs must reach the remote")
	}
}

// A protected branch refuses the atomic push, which applies neither ref:
// the live writes still go out on their own, the landing is reported
// protected (it rides on the next code push) and main does not move.
func TestLandCarryProtectedStillDeliversLive(t *testing.T) {
	w, r, fault := landCarryFixture(t)
	fault.Protect("refs/heads/main")
	before := w.OriginRef("refs/heads/main")
	res := landCarried(t, r)
	if res.Status != LandProtected || res.Live == nil || res.Live.Status != PushDone {
		t.Fatalf("want protected with the live ref delivered: %+v live %+v", res, res.Live)
	}
	if !liveOnOrigin(t, w, r) {
		t.Fatal("the live writes must still reach the remote")
	}
	if w.OriginRef("refs/heads/main") != before {
		t.Fatal("protected main must not change")
	}
}

// The live ref refused (a hook on refs/lore/*): nothing lands, and the
// caller is told why; main does not move.
func TestLandCarryLiveRefusedLandsNothing(t *testing.T) {
	w, r, fault := landCarryFixture(t)
	fault.Protect(RefLive)
	before := w.OriginRef("refs/heads/main")
	res := landCarried(t, r)
	if res.Status != LandRefused || res.Live == nil || res.Live.Status != PushRefused {
		t.Fatalf("want refused with the live outcome: %+v live %+v", res, res.Live)
	}
	if w.OriginRef("refs/heads/main") != before || w.OriginRef(RefLive) != "" {
		t.Fatal("nothing may land when the live ref did not go out")
	}
}

// Offline: nothing is sent, the landing says offline, the writes wait.
func TestLandCarryOffline(t *testing.T) {
	w, r, fault := landCarryFixture(t)
	fault.SetOffline(true)
	before := w.OriginRef("refs/heads/main")
	res, err := r.Land(context.Background(), LandRequest{Branch: "main", Items: []LandItem{landItem("x")}, Layout: testLayout, Live: true})
	if err != nil || res.Status != LandOffline {
		t.Fatalf("want offline: %+v %v", res, err)
	}
	if w.OriginRef("refs/heads/main") != before || w.OriginRef(RefLive) != "" {
		t.Fatal("an offline landing must change nothing on the remote")
	}
}
