package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestAmbiguousCopyHintKeepsTheNewest pins devco:D-934 (bug hunt H-195):
// the E_AMBIGUOUS_ID hint for a row live in two stores names the move that
// keeps the NEWEST copy (the one list reads show), wherever it is. Before,
// it named `row move --id X --to <last place>`, which kept the farther
// store's copy even when the edit was in the nearer one. Copies at one
// version offer each place (nothing is lost either way).
func TestAmbiguousCopyHintKeepsTheNewest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	created := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name             string
		repoAt, agencyAt time.Time
		want, never      []string
	}{
		{name: "edit in the nearer store", repoAt: created.Add(time.Hour), agencyAt: created,
			want:  []string{"lore row move --from " + string(agency) + " --where id=", " --to repo --yes` keeps the newest copy, in repo"},
			never: []string{"--to " + string(agency) + " --yes"}},
		{name: "edit in the farther store", repoAt: created, agencyAt: created.Add(time.Hour),
			want:  []string{"lore row move --from repo --where id=", " --to " + string(agency) + " --yes` keeps the newest copy, in " + string(agency)},
			never: []string{"--to repo --yes"}},
		{name: "one version", repoAt: created, agencyAt: created,
			want: []string{"to keep the copy in repo: `lore row move --from " + string(agency), "to keep the copy in " + string(agency) + ": `lore row move --from repo"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newF15World(t, false)
			id := mustID(t, ids.PrefixMemory)
			w.putCopy(t, store.SourceRepo, id, created, c.repoAt, "repo text")
			w.putCopy(t, agency, id, created, c.agencyAt, "agency text")
			_, _, err := w.eng.Get(ctx, Ref{ID: id})
			if !errs.Has(err, errs.AmbiguousID) {
				t.Fatalf("Get = %v, want E_AMBIGUOUS_ID", err)
			}
			hint := hintOf(err)
			for _, s := range c.want {
				if !strings.Contains(hint, s) {
					t.Errorf("hint lacks %q:\n%s", s, hint)
				}
			}
			for _, s := range c.never {
				if strings.Contains(hint, s) {
					t.Errorf("hint keeps the older copy (%q):\n%s", s, hint)
				}
			}
		})
	}
}

// TestMoveRefusesToKeepAnOlderTargetCopy pins devco:D-934 (bug hunt
// H-195): a move into a store that already holds a live copy of the row
// keeps that copy and tombstones the source (the interrupted-move rule).
// When the source's copy is NEWER (an edit after `lore store adopt`
// copied the row), that silently lost the edit; now the move is refused
// naming the move that keeps the newer copy, and nothing changes. The
// other direction (older source, newer target) and copies at one version
// still settle as before, so an interrupted move still finishes.
func TestMoveRefusesToKeepAnOlderTargetCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	created := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	body := func(w *f15World, n store.Source, id ids.AnyID) string {
		got, _, err := w.eng.getRow(ctx, n, schema.TableMemories, id)
		if err != nil {
			t.Fatal(err)
		}
		r, err := got.Decode()
		if err != nil {
			t.Fatal(err)
		}
		if r.Deleted {
			return "(tombstone)"
		}
		return r.Fields["body"].String()
	}
	t.Run("newer source, older target: refused, nothing changed", func(t *testing.T) {
		w := newF15World(t, false)
		id := mustID(t, ids.PrefixMemory)
		w.putCopy(t, store.SourceRepo, id, created, created.Add(time.Hour), "edited")
		w.putCopy(t, agency, id, created, created, "old")
		_, err := w.eng.MoveWith(ctx, Ref{ID: id, Store: store.SourceRepo}, agency, MoveOptions{})
		if !errs.Has(err, errs.Refused) {
			t.Fatalf("move = %v, want E_REFUSED", err)
		}
		if h := hintOf(err); !strings.Contains(h, "lore row move --from "+string(agency)+" --where id="+id.String()+" --to repo --yes") {
			t.Errorf("hint does not name the move that keeps the newer copy:\n%s", h)
		}
		if got := body(w, store.SourceRepo, id); got != "edited" {
			t.Errorf("repo copy = %q, want the edit kept", got)
		}
		if got := body(w, agency, id); got != "old" {
			t.Errorf("agency copy = %q, want it untouched", got)
		}
	})
	for _, c := range []struct {
		name            string
		srcAt, targetAt time.Time
		wantTarget      string
	}{
		{name: "older source, newer target: source tombstoned", srcAt: created, targetAt: created.Add(time.Hour), wantTarget: "target text"},
		{name: "one version: source tombstoned", srcAt: created, targetAt: created, wantTarget: "target text"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newF15World(t, false)
			id := mustID(t, ids.PrefixMemory)
			w.putCopy(t, store.SourceRepo, id, created, c.srcAt, "source text")
			w.putCopy(t, agency, id, created, c.targetAt, "target text")
			if _, err := w.eng.MoveWith(ctx, Ref{ID: id, Store: store.SourceRepo}, agency, MoveOptions{}); err != nil {
				t.Fatal(err)
			}
			if got := body(w, store.SourceRepo, id); got != "(tombstone)" {
				t.Errorf("repo copy = %q, want a tombstone", got)
			}
			if got := body(w, agency, id); got != c.wantTarget {
				t.Errorf("agency copy = %q, want %q", got, c.wantTarget)
			}
		})
	}
}

// hintOf is err's hint ("" when err carries none).
func hintOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}
