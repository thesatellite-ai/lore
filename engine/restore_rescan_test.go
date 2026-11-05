package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// handEdit rewrites the stored row id's body in the repo backend, as a
// person editing the trashed file would (no engine checks).
func handEdit(t *testing.T, f *fixture, id ids.AnyID, body string) {
	t.Helper()
	ctx := context.Background()
	b := f.repo.Open(store.OpenOptions{})
	sr, err := b.Get(ctx, schema.TableMemories, id)
	if err != nil {
		t.Fatal(err)
	}
	r, err := sr.Decode()
	if err != nil {
		t.Fatal(err)
	}
	r.Fields["body"] = txt(body)
	r.HLC = hlc.Next(sr.HLC, f.eng.wall(), testActor)
	next, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Write(ctx, []store.Change{{Row: next, Expect: sr.HLC}}); err != nil {
		t.Fatal(err)
	}
}

// TestRestoreRescansForSecrets (DELETION 3.3 step 3, 4.6): a trashed row
// hand-edited to hold a secret is screened again on restore: ask without
// a confirmer → E_NEEDS_CONFIRM, refuse → E_SECRET_DETECTED, and on a
// refusal nothing of the batch comes back (the clean row stays trashed
// too); redact restores the batch with the match replaced. The error never
// repeats the secret.
func TestRestoreRescansForSecrets(t *testing.T) {
	cases := []struct {
		mode string
		want errs.Code
	}{
		{"ask", errs.NeedsConfirm},
		{"refuse", errs.SecretDetected},
		{"redact", ""},
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			ctx := context.Background()
			set, err := policy.Resolve([]policy.Entry{{Level: policy.LevelRepo, Key: policy.KeyOnDetect, Value: txt(c.mode)}})
			if err != nil {
				t.Fatal(err)
			}
			f := setup(t, WithSettings(fixedSettings{set: set}))
			dirty, clean := f.memory(t, "will be edited"), f.memory(t, "stays clean")
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
			handEdit(t, f, dirty.ID, "key "+fakeKey+" end")
			res, err := f.eng.Restore(ctx, bid.String())
			if c.want != "" {
				if !errs.Has(err, c.want) {
					t.Fatalf("restore: %v, want %s", err, c.want)
				}
				if strings.Contains(err.Error(), fakeKey) {
					t.Fatal("the error repeats the secret")
				}
				all, terr := f.eng.Trashed(ctx)
				if terr != nil || len(all) != 2 {
					t.Fatalf("trash after a refused restore = %d rows (%v), want both", len(all), terr)
				}
				return
			}
			if err != nil {
				t.Fatalf("redact restore: %v", err)
			}
			if len(res.Rows) != 2 {
				t.Fatalf("restored %d rows, want 2", len(res.Rows))
			}
			_, got, err := f.eng.Get(ctx, Ref{ID: dirty.ID})
			if err != nil {
				t.Fatal(err)
			}
			if _, deleted := trash.Read(got); deleted {
				t.Fatal("restored row still in the trash")
			}
			if body := got.Fields["body"].String(); body != "key "+refs.Unstored+" end" || strings.Contains(body, fakeKey) {
				t.Fatalf("restored body %q, want the match redacted", body)
			}
		})
	}
}
