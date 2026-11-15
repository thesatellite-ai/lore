package coord

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/logic/queue"
)

// unsyncedRows is a person's row store whose writes stay in their clone
// (a live row waits for lore sync): the holder never sees the revoke's row
// change, only the signals ref, as in the swarm of devco:S-12.
type unsyncedRows struct{ gitRows }

func (unsyncedRows) Stage(context.Context, ...RowWrite) ([]signals.Companion, error) {
	return nil, nil
}

// TestRevokeFencesHolderOffTask (devco:S-12, S-13): after a person's
// revoke the holder's next write names the person and the revoke time
// (never an expiry still in the future), its task next skips the task and
// lists it as revoked, a direct claim is refused, another actor may take
// it, and a person's reassign lifts the fence.
func TestRevokeFencesHolderOffTask(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", Doc{keyPriority: str("urgent")})
	wd.addTask("tsk_2", nil)
	a, b, lead := wd.agent("a"), wd.agent("b"), wd.agent("lead")
	lead.env.Caller.Label = "@lead"
	lead.env.Rows = unsyncedRows{lead.env.Rows.(gitRows)}
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	if removed, err := Override(ctx, lead.env, "tsk_1", OverrideRevoke, nil); err != nil || removed == nil {
		t.Fatalf("revoke: %v %v", removed, err)
	}

	// S-12: the holder's error names the person, not an expiry.
	err := a.done("tsk_1")
	var fe *FencedError
	if !asFenced(err, &fe) || fe.Kind != FenceLost {
		t.Fatalf("holder's done after a revoke: %v", err)
	}
	if !strings.Contains(fe.Detail, "was revoked by @lead at") || strings.Contains(fe.Detail, "expired") {
		t.Fatalf("holder must be told who revoked it, never an expiry: %q", fe.Detail)
	}

	// S-13: task next skips it and says why; a direct claim is refused.
	rows := a.env.Rows.(gitRows)
	v, err := a.env.Signals.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q, err := Queue(ctx, a.env, v, NextRequest{List: rows.List})
	if err != nil {
		t.Fatal(err)
	}
	if !waitsAs(q, "tsk_1", queue.WaitRevoked, "revoked from you by @lead") {
		t.Fatalf("dry-run must list tsk_1 as revoked from the holder: %+v", q.Waiting)
	}
	res, err := Next(ctx, a.env, NextRequest{List: rows.List})
	if err != nil || res.Task != "tsk_2" {
		t.Fatalf("task next after a revoke must pick other work, got %q %v", res.Task, err)
	}
	if err := Release(ctx, a.env, "tsk_2"); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); !isRevokedErr(err) {
		t.Fatalf("a direct claim of a task revoked from you must be refused: %v", err)
	}

	// Another actor is not fenced; once it lets go, a reassign lifts the fence.
	if _, err := Claim(ctx, b.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatalf("another actor may claim a revoked task: %v", err)
	}
	if err := Release(ctx, b.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Override(ctx, lead.env, "tsk_1", OverrideReassign, func(d Doc) { d.SetStr(keyAssignee, "act_a") }); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.env.Signals.Read(ctx); func() bool { _, ok := RevokeMarkerOf(v, folder, "tsk_1"); return ok }() {
		t.Fatal("a reassign must drop the revoke marker")
	}
	if _, err := Claim(ctx, a.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatalf("after a person's reassign the agent may claim again: %v", err)
	}
}

func asFenced(err error, fe **FencedError) bool {
	var f *FencedError
	ok := errors.As(err, &f)
	if ok {
		*fe = f
	}
	return ok
}

func isRevokedErr(err error) bool { _, ok := IsRevoked(err); return ok }

func waitsAs(q queue.Result, id string, kind queue.WaitKind, reason string) bool {
	for _, w := range q.Waiting {
		if w.Task.ID == id && w.Kind == kind && strings.Contains(w.Reason, reason) {
			return true
		}
	}
	return false
}
