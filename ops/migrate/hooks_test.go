package migrate

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
)

// recordHooks records each point and fails at stopAt.
type recordHooks struct {
	seen   []Point
	stopAt Point
}

var errStop = errors.New("stopped by the hook")

func (h *recordHooks) At(_ context.Context, p Point) error {
	h.seen = append(h.seen, p)
	if p == h.stopAt {
		return errStop
	}
	return nil
}

// invokeRun runs migrate.run on f under ctx.
func invokeRun(ctx context.Context, t *testing.T, f *fakeRewriter, input string) error {
	t.Helper()
	e := Ops(f)[0]
	in, err := e.Decode(op.JSONDecoder{Body: []byte(input)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Invoke(ctx, in)
	return err
}

// TestHooksPointsInOrder: a run reaches team-locked, begun is skipped (the
// fake has no Journal), settings, live, one table point per table with rows
// to rewrite (an empty table is no boundary), then finish; a dry run reaches
// no table or finish point. No hook in the context: the run is unchanged.
func TestHooksPointsInOrder(t *testing.T) {
	mem, err := ids.ParseAny(fixedMem)
	if err != nil {
		t.Fatal(err)
	}
	newFake := func() *fakeRewriter {
		return &fakeRewriter{tables: []row.Table{"memories", "rules"}, v1: map[row.Table][]ids.AnyID{"memories": {mem}}}
	}
	h := &recordHooks{}
	if err := invokeRun(WithHooks(context.Background(), h), t, newFake(), `{}`); err != nil {
		t.Fatal(err)
	}
	want := []Point{PointTeamLocked, PointSettings, PointLive, PointTable, PointFinish}
	if !slices.Equal(h.seen, want) {
		t.Fatalf("points %v, want %v", h.seen, want)
	}
	dry := &recordHooks{}
	if err := invokeRun(WithHooks(context.Background(), dry), t, newFake(), `{"dry_run":true}`); err != nil {
		t.Fatal(err)
	}
	if want := []Point{PointTeamLocked, PointSettings, PointLive}; !slices.Equal(dry.seen, want) {
		t.Fatalf("dry-run points %v, want %v", dry.seen, want)
	}
	if err := invokeRun(context.Background(), t, newFake(), `{}`); err != nil {
		t.Fatalf("a run with no hook: %v", err)
	}
}

// TestHooksErrorStopsTheRun: an error at a point ends the run there with
// that error (nothing after it runs) and the lock is still released.
func TestHooksErrorStopsTheRun(t *testing.T) {
	f := &fakeRewriter{tables: []row.Table{"memories"}}
	h := &recordHooks{stopAt: PointSettings}
	if err := invokeRun(WithHooks(context.Background(), h), t, f, `{}`); !errors.Is(err, errStop) {
		t.Fatalf("err %v, want the hook's", err)
	}
	if slices.Contains(f.calls, "live") || slices.Contains(f.calls, "rewrite:memories") {
		t.Fatalf("steps ran after the stop: %v", f.calls)
	}
	if f.unlocked != 1 {
		t.Fatalf("lock released %d times, want 1", f.unlocked)
	}
}
