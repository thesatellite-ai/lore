package task_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// spanBoard is a fakeBoard that also answers OpenClaim (ClaimSpanner).
type spanBoard struct {
	fakeBoard
	open map[string]opstask.OpenClaim
}

func (b *spanBoard) OpenClaim(id string) (opstask.OpenClaim, bool) {
	c, ok := b.open[id]
	return c, ok
}

// spanTeam hands out a spanBoard.
type spanTeam struct {
	fakeTeam
	b *spanBoard
}

func (f *spanTeam) Claims(context.Context) (opstask.ClaimBoard, error) { return f.b, nil }

// TestTimeShowOpenClaim pins TIME_TRACKING 5.1 rule 1: a claim still on
// the board counts live in task time show (since → end), its holder named
// by display name in WHO; an expired one stops at its last heartbeat (D3).
func TestTimeShowOpenClaim(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	h := opstest.New(t)
	ann := newActor(h, "Ann")
	id := add(h, "claimed", "")
	since := time.Now().UTC().Add(-90 * time.Minute)
	b := &spanBoard{open: map[string]opstask.OpenClaim{id: {Holder: ann, Session: "tool-xyz", Since: since, End: since.Add(time.Hour), Expired: true}}}
	ctx := opstask.WithTeam(context.Background(), &spanTeam{b: b})
	res := mustCtx(h, ctx, opstask.OpTimeShow, `{"id":"`+id+`","entries":true}`)
	out, ok := res.Value().Interface().(opstask.TimeOut)
	if !ok {
		t.Fatalf("result is %T", res.Value().Interface())
	}
	d := out.Data
	if d.EffortSeconds != 3600 || len(d.OpenClaims) != 1 || !d.OpenClaims[0].Expired || d.OpenClaims[0].Session != "" {
		t.Fatalf("expired claim: effort %d claims %+v", d.EffortSeconds, d.OpenClaims)
	}
	if txt := out.Text(); !strings.Contains(txt, "open claim") || !strings.Contains(txt, "Ann") {
		t.Fatalf("text:\n%s", txt)
	}
}

// TestTimeShowOpenClaimMatrix (OV7, TIME 5.1 rule 1 + D3/D8): what an open
// claim adds to task time show, across lease state, session kind, holder
// shape and clock skew. A live lease counts to its end; an expired one to
// its last heartbeat; only a ses_ session becomes the worker's session; a
// holder that is not an actor id (an old binary's) and a claim with no
// start add nothing; an end before the start counts zero, never negative.
func TestTimeShowOpenClaimMatrix(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ann := newActor(h, "Ann")
	since := time.Now().UTC().Add(-3 * time.Hour)
	cases := []struct {
		name    string
		claim   opstask.OpenClaim
		effort  int64
		claims  int
		session string
	}{
		{"live lease", opstask.OpenClaim{Holder: ann, Session: "ses_0e45506e64357c94a87e70ef1c6bbb01", Since: since, End: since.Add(2 * time.Hour)}, 7200, 1, "ses_0e45506e64357c94a87e70ef1c6bbb01"},
		{"expired at heartbeat", opstask.OpenClaim{Holder: ann, Since: since, End: since.Add(30 * time.Minute), Expired: true}, 1800, 1, ""},
		{"tool session dropped", opstask.OpenClaim{Holder: ann, Session: "codex-123", Since: since, End: since.Add(time.Hour)}, 3600, 1, ""},
		{"skewed end", opstask.OpenClaim{Holder: ann, Since: since, End: since.Add(-time.Hour)}, 0, 1, ""},
		{"old binary holder", opstask.OpenClaim{Holder: "mira@laptop", Since: since, End: since.Add(time.Hour)}, 0, 0, ""},
		{"no start", opstask.OpenClaim{Holder: ann, End: since}, 0, 0, ""},
	}
	for _, c := range cases {
		id := add(h, c.name, "")
		b := &spanBoard{open: map[string]opstask.OpenClaim{id: c.claim}}
		ctx := opstask.WithTeam(context.Background(), &spanTeam{b: b})
		res := mustCtx(h, ctx, opstask.OpTimeShow, `{"id":"`+id+`"}`)
		out, ok := res.Value().Interface().(opstask.TimeOut)
		if !ok {
			t.Fatalf("result is %T", res.Value().Interface())
		}
		d := out.Data
		if d.EffortSeconds != c.effort || len(d.OpenClaims) != c.claims {
			t.Errorf("%s: effort %d claims %d, want %d %d", c.name, d.EffortSeconds, len(d.OpenClaims), c.effort, c.claims)
			continue
		}
		if c.claims == 1 && (d.OpenClaims[0].Session != c.session || d.OpenClaims[0].Expired != c.claim.Expired) {
			t.Errorf("%s: claim %+v", c.name, d.OpenClaims[0])
		}
	}
}
