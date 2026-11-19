package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
)

// keyedMem puts a memory holding body straight into the bulk world's
// source store, past the write path's scan: a row written before the
// scan existed (or by an older lore), the shape a bulk move meets.
func (w *bulkWorld) keyedMem(t *testing.T, body string) Ref {
	t.Helper()
	ctx := context.Background()
	ref := w.mem(t, "placeholder")
	raw, err := w.src.Get(ctx, ref.Table, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := raw.Decode()
	if err != nil {
		t.Fatal(err)
	}
	r.Fields["body"] = txt(body)
	r.HLC = w.eng.clock.Now(r.HLC)
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.srcData.Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr, Expect: raw.HLC}}); err != nil {
		t.Fatal(err)
	}
	return ref
}

// TestMoveManySecretsPlannedForTheBatch (devco:D-717): a bulk move screens
// every row before any write. Without an answer the refusal names EVERY
// row holding a key (it named only the first) and the printed reruns, and
// nothing is written; with one --secret-ref per finding the refs are
// counted against the whole batch and each row gets its own (each row's
// move saw all refs for its one finding and refused E_INVALID_INPUT).
func TestMoveManySecretsPlannedForTheBatch(t *testing.T) {
	t.Parallel()
	const rerun = "lore move --from acme/old --to acme/new --yes"
	second := "AKIA" + strings.Repeat("Z", 16)

	t.Run("refused names every row", func(t *testing.T) {
		t.Parallel()
		w := newBulkWorld(t)
		refs := []Ref{w.keyedMem(t, "key "+fakeKey+" end"), w.mem(t, "plain words"), w.keyedMem(t, "deploy "+second)}
		s0, d0 := w.src.count(), w.dst.count()
		ctx := context.WithValue(context.Background(), callKey{}, call{op: "row.move"})
		_, err := w.eng.MoveMany(ctx, refs, bulkDst, MoveOptions{SecretRerun: rerun})
		if !errs.Has(err, errs.NeedsConfirm) && !errs.Has(err, errs.SecretDetected) {
			t.Fatalf("bulk move of two keyed rows without an answer: %v", err)
		}
		msg := err.Error()
		for _, want := range []string{refs[0].ID.String(), refs[2].ID.String(), "2 row(s)", "nothing written"} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal %q does not name %q", msg, want)
			}
		}
		if strings.Contains(msg, fakeKey) || strings.Contains(msg, second) {
			t.Fatalf("refusal prints the value: %q", msg)
		}
		var e *errs.Error
		if !errors.As(err, &e) || !strings.Contains(e.Hint, rerun+" --secret-unstored") || !strings.Contains(e.Hint, rerun+` --not-a-secret --reason "<why>"`) {
			t.Fatalf("refusal hint does not print the reruns: %+v", e)
		}
		if ds, ss := w.dst.count()-d0, w.src.count()-s0; ds != 0 || ss != 0 {
			t.Fatalf("a refused bulk move wrote target %d source %d times, want 0", ds, ss)
		}
		for _, r := range refs {
			if !w.liveIn(t, bulkSrc, r) || w.liveIn(t, bulkDst, r) {
				t.Fatalf("%s moved although the batch was refused", r.ID)
			}
		}
	})

	t.Run("one ref per finding across the batch", func(t *testing.T) {
		t.Parallel()
		w := newBulkWorld(t)
		refs := []Ref{w.keyedMem(t, "key "+fakeKey+" end"), w.mem(t, "plain words"), w.keyedMem(t, "deploy "+second)}
		ctx := context.WithValue(context.Background(), callKey{}, call{op: "row.move", caller: op.Caller{Secret: op.SecretChoice{Refs: []string{"env://ONE", "env://TWO"}}}})
		if _, err := w.eng.MoveMany(ctx, refs, bulkDst, MoveOptions{SecretRerun: rerun}); err != nil {
			t.Fatalf("two --secret-ref values for two findings in two rows: %v", err)
		}
		want := map[int]string{0: "key ${secret:env://ONE} end", 1: "plain words", 2: "deploy ${secret:env://TWO}"}
		for i, r := range refs {
			_, now, err := w.eng.locate(context.Background(), Ref{Store: bulkDst, Table: r.Table, ID: r.ID})
			if err != nil {
				t.Fatal(err)
			}
			if got := now.Fields["body"].String(); got != want[i] {
				t.Errorf("row %d body = %v, want %q", i, got, want[i])
			}
		}
	})

	t.Run("three refs for two findings", func(t *testing.T) {
		t.Parallel()
		w := newBulkWorld(t)
		refs := []Ref{w.keyedMem(t, "key "+fakeKey+" end"), w.keyedMem(t, "deploy "+second)}
		ctx := context.WithValue(context.Background(), callKey{}, call{op: "row.move", caller: op.Caller{Secret: op.SecretChoice{Refs: []string{"env://A", "env://B", "env://C"}}}})
		_, err := w.eng.MoveMany(ctx, refs, bulkDst, MoveOptions{})
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "2 finding(s) in 2 row(s)") {
			t.Fatalf("three refs for two findings: %v", err)
		}
	})
}
