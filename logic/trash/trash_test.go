package trash

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

func TestMarkReadClear(t *testing.T) {
	t.Parallel()
	b, err := ids.New[ids.Batch]()
	if err != nil {
		t.Fatal(err)
	}
	a, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := value.TextOf("keep me")
	r := row.Row{Table: "memories", Fields: map[string]value.Value{"body": body}}
	at := time.Date(2026, 11, 4, 10, 12, 40, 0, time.UTC)
	m, err := Mark(r, Stamp{At: at, By: a, Reason: "dup", Batch: b})
	if err != nil {
		t.Fatal(err)
	}
	in, ok := Read(m)
	if !ok || !in.At.Equal(at) || in.By != a.String() || in.Reason != "dup" || in.Batch != b.String() {
		t.Fatalf("read = %+v %v", in, ok)
	}
	if Is(r) || Is(row.Row{Deleted: true, Fields: m.Fields}) {
		t.Fatal("an alive row or a tombstone counted as trashed")
	}
	c := Clear(m)
	if Is(c) || c.Fields["body"].String() != "keep me" || len(c.Fields) != 1 {
		t.Fatalf("clear = %+v", c.Fields)
	}
	if _, still := r.Fields[FieldDeletedAt]; still {
		t.Fatal("Mark changed its input")
	}
}

func TestReasonAndExpired(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"two\nlines", strings.Repeat("x", MaxReason+1)} {
		if _, err := Reason(bad); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("Reason(%q) = %v", bad[:5], err)
		}
	}
	if s, err := Reason("  ok  "); err != nil || s != "ok" {
		t.Fatalf("Reason = %q %v", s, err)
	}
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	old := now.Add(-91 * 24 * time.Hour)
	for _, c := range []struct {
		days int64
		want bool
	}{{0, false}, {-1, false}, {90, true}, {365, false}} {
		if got := Expired(old, now, c.days); got != c.want {
			t.Errorf("Expired(91d ago, %d) = %v", c.days, got)
		}
	}
	if Expired(time.Time{}, now, 1) {
		t.Error("an unknown delete time expired")
	}
}
