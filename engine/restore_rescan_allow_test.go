package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// TestRestoreRescanHonoursAllows (DELETION 3.3 step 3, later 248): under
// on_detect refuse, a trashed row hand-edited to hold a secret restores
// when a secret_allows entry accepts exactly that finding (same row,
// field, rule and match), keeping the value as written; an entry that
// differs in any one of the four does not accept it (E_SECRET_DETECTED,
// both rows of the batch stay in the trash).
func TestRestoreRescanHonoursAllows(t *testing.T) {
	t.Parallel()
	refuse, err := policy.Resolve([]policy.Entry{{Level: policy.LevelRepo, Key: policy.KeyOnDetect, Value: txt("refuse")}})
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	body := "key " + fakeKey + " end"
	cases := []struct {
		name  string
		allow func(row ids.AnyID) scan.Allow
		ok    bool
	}{
		{"exact finding", func(r ids.AnyID) scan.Allow {
			return scan.Allow{RowID: r, Field: "body", RuleID: awsRule, MatchHash: scan.HashMatch(fakeKey)}
		}, true},
		{"another row", func(ids.AnyID) scan.Allow {
			return scan.Allow{RowID: otherID.Any(), Field: "body", RuleID: awsRule, MatchHash: scan.HashMatch(fakeKey)}
		}, false},
		{"another field", func(r ids.AnyID) scan.Allow {
			return scan.Allow{RowID: r, Field: "title", RuleID: awsRule, MatchHash: scan.HashMatch(fakeKey)}
		}, false},
		{"another rule", func(r ids.AnyID) scan.Allow {
			return scan.Allow{RowID: r, Field: "body", RuleID: "generic-api-key", MatchHash: scan.HashMatch(fakeKey)}
		}, false},
		{"another match", func(r ids.AnyID) scan.Allow {
			return scan.Allow{RowID: r, Field: "body", RuleID: awsRule, MatchHash: scan.HashMatch(fakeKey + "X")}
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			st := &fixedSettings{set: refuse}
			f := setup(t, WithSettings(allowsLater{st}))
			dirty, clean := f.memory(t, "will be edited"), f.memory(t, "stays clean")
			st.allows = []scan.Allow{c.allow(dirty.ID)}
			bid, err := ids.New[ids.Batch]()
			if err != nil {
				t.Fatal(err)
			}
			bctx := WithBatch(ctx, Batch{ID: bid, Kind: BatchKindDelete})
			for _, id := range []ids.AnyID{dirty.ID, clean.ID} {
				if _, err = f.eng.Delete(bctx, Ref{ID: id}); err != nil {
					t.Fatal(err)
				}
			}
			handEdit(t, f, dirty.ID, body)
			res, err := f.eng.Restore(ctx, bid.String())
			if !c.ok {
				if !errs.Has(err, errs.SecretDetected) {
					t.Fatalf("restore: %v, want E_SECRET_DETECTED", err)
				}
				if all, terr := f.eng.Trashed(ctx); terr != nil || len(all) != 2 {
					t.Fatalf("trash after a refused restore = %d rows (%v), want both", len(all), terr)
				}
				return
			}
			if err != nil || len(res.Rows) != 2 {
				t.Fatalf("restore with the finding allowed: %v, %d rows", err, len(res.Rows))
			}
			_, got, err := f.eng.Get(ctx, Ref{ID: dirty.ID})
			if err != nil {
				t.Fatal(err)
			}
			if _, deleted := trash.Read(got); deleted {
				t.Fatal("restored row still in the trash")
			}
			if b := got.Fields["body"].String(); b != body {
				t.Fatalf("restored body %q, want it kept as written", b)
			}
		})
	}
}

// allowsLater reads the allow list through a pointer, so a case sets it
// after the row ids exist.
type allowsLater struct{ *fixedSettings }
