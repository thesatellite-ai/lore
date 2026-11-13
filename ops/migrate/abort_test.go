package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
)

// aborting is a journaled fakeRewriter that records AbortRun.
type aborting struct {
	journaled
	aborts *int
}

func (a aborting) AbortRun(context.Context) ([]string, error) {
	*a.aborts++
	return []string{"branch (.lore/data): 3 files put back as they were"}, nil
}

var _ Aborter = aborting{}

// stopHooks stops the run at one point (a crash, not a failure).
type stopHooks struct{ at Point }

func (h stopHooks) At(_ context.Context, p Point) error {
	if p == h.at {
		return errs.New(errs.Internal, "stopped at "+string(p))
	}
	return nil
}

// TestRunFailedStepPutsBack (E-5): a step that fails after Begin puts the
// run's changes back and says so in the error, keeping its code; a test
// hook's stop (a crash) does not, so the next run resumes.
func TestRunFailedStepPutsBack(t *testing.T) {
	invoke := func(t *testing.T, f *fakeRewriter, ctx context.Context) (int, error) {
		t.Helper()
		n := 0
		e := Ops(aborting{journaled{f}, &n})[0]
		in, err := e.Decode(op.JSONDecoder{Body: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Invoke(ctx, in)
		return n, err
	}
	for at, code := range map[string]errs.Code{"settings": errs.ConfigInvalid, "live": errs.Internal, "memories": errs.Stale} {
		f := &fakeRewriter{tables: []row.Table{"memories"}, failAt: at}
		n, err := invoke(t, f, context.Background())
		if err == nil || n != 1 {
			t.Fatalf("fail at %s: aborts %d, err %v; want one abort and the error", at, n, err)
		}
		if !strings.Contains(err.Error(), "put back") || errs.CodeOf(err) != code {
			t.Fatalf("fail at %s: error %q (code %s) does not keep its code and say what was put back", at, err, errs.CodeOf(err))
		}
	}
	f := &fakeRewriter{tables: []row.Table{"memories"}, v1: map[row.Table][]ids.AnyID{}}
	n, err := invoke(t, f, WithHooks(context.Background(), stopHooks{at: PointSettings}))
	if err == nil || n != 0 {
		t.Fatalf("hook stop: aborts %d, err %v; want no abort (a crash resumes)", n, err)
	}
}
