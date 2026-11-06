package bulk

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

func items(n int) []Item {
	out := make([]Item, n)
	for i := range out {
		out[i] = Item{ID: "res_" + strconv.Itoa(i), Label: "host" + strconv.Itoa(i)}
	}
	return out
}

// XQ2: above the limit without a yes the action asks, listing exactly the
// first limit rows and the count of the rest; at the limit or confirmed it
// passes.
func TestCheck(t *testing.T) {
	cases := []struct {
		name      string
		n, limit  int
		confirmed bool
		asks      bool
	}{
		{"below", 5, 20, false, false},
		{"at the limit", 20, 20, false, false},
		{"one over", 21, 20, false, true},
		{"25 over 20", 25, 20, false, true},
		{"confirmed", 25, 20, true, false},
		{"limit 0 asks for any row", 1, 0, false, true},
		{"empty never asks", 0, 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Check("resource delete", items(tc.n), tc.limit, tc.confirmed)
			if got := errs.Has(err, errs.NeedsConfirm); got != tc.asks {
				t.Fatalf("asks = %v (%v), want %v", got, err, tc.asks)
			}
			if !tc.asks {
				return
			}
			msg := err.Error()
			if tc.limit > 0 && !strings.Contains(msg, "res_"+strconv.Itoa(tc.limit-1)+" (host") {
				t.Errorf("last shown row missing: %s", msg)
			}
			if strings.Contains(msg, "res_"+strconv.Itoa(tc.limit)+" ") {
				t.Errorf("row past the limit listed: %s", msg)
			}
			if !strings.Contains(msg, "and "+strconv.Itoa(tc.n-tc.limit)+" more") {
				t.Errorf("count of the rest missing: %s", msg)
			}
		})
	}
}

type fakePolicy struct{ set policy.Set }

func (f fakePolicy) Policy(context.Context, store.Source) (policy.Set, error) { return f.set, nil }

func TestPreviewRows(t *testing.T) {
	ctx := context.Background()
	if n, err := PreviewRows(ctx, struct{}{}, store.SourceRepo); err != nil || n != DefaultPreviewRows {
		t.Fatalf("no Policier: %d %v", n, err)
	}
	if n, err := PreviewRows(ctx, fakePolicy{policy.Defaults()}, store.SourceRepo); err != nil || n != 20 {
		t.Fatalf("defaults: %d %v, want XQ2 20", n, err)
	}
	set, err := policy.Resolve([]policy.Entry{{Level: policy.LevelRepo, Key: policy.KeyBatchPreviewRows, Value: value.Int(3)}})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := PreviewRows(ctx, fakePolicy{set}, store.SourceRepo); err != nil || n != 3 {
		t.Fatalf("policy row: %d %v", n, err)
	}
}

func TestBeginRefusesUnknownKind(t *testing.T) {
	if _, _, err := Begin(context.Background(), Kind("nope")); err == nil {
		t.Fatal("unknown kind accepted")
	}
	_, id, err := Begin(context.Background(), KindDelete)
	if err != nil || !strings.HasPrefix(id.String(), "bat_") {
		t.Fatalf("Begin = %s %v", id, err)
	}
}

func TestAskOutsideRun(t *testing.T) {
	if err := Ask(context.Background(), "delete x?"); !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "delete x?") {
		t.Fatalf("Ask = %v, want E_NEEDS_CONFIRM (no caller has said yes)", err)
	}
	// the question travels in the envelope (--json, REST: CLI_REFERENCE 1.4/1.7)
	if q := errs.EnvelopeOf(Ask(context.Background(), "delete x?")).Error.Question; q != "delete x?" {
		t.Fatalf("Ask question = %q, want %q", q, "delete x?")
	}
}
