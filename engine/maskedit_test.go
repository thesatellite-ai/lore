package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestEditFromAMaskedReadKeepsTheStoredValue (devco:D-717, SECURITY): a
// row whose key no allow entry accepts reads "[redacted <rule>]" (R-14).
// `lore row edit` of that view with a change elsewhere in the body (and
// `memory edit --body` with the text copied from show) stored the mask
// over the key. Now each mask kept exactly as printed keeps the stored
// value (and is not asked about as new input), a removed mask removes it,
// and a changed mask or a removed twin is refused with nothing written.
func TestEditFromAMaskedReadKeepsTheStoredValue(t *testing.T) {
	t.Parallel()
	second := "AKIA" + strings.Repeat("Z", 16)
	w := newBulkWorld(t)
	w.eng.readScanner = w.eng.scanner // the app masks reads with the write rules
	ctx := context.WithValue(context.Background(), callKey{}, call{op: "row.edit"})

	stored := func(ref Ref) string {
		t.Helper()
		_, r, err := w.eng.locate(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		return r.Fields["body"].String()
	}
	read := func(ref Ref) string {
		t.Helper()
		_, r, err := w.eng.Get(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		return r.Fields["body"].String()
	}
	edit := func(ref Ref, body string) (Written, error) {
		return w.eng.Update(ctx, ref, map[string]value.Value{"body": txt(body)})
	}

	orig := "deploy " + fakeKey + " for staging"
	ref := w.keyedMem(t, orig)
	masked := read(ref)
	i, j := strings.Index(masked, ReadRedacted), strings.Index(masked, "] for")
	if strings.Contains(masked, fakeKey) || i != len("deploy ") || j < i {
		t.Fatalf("the keyed row reads %q, want it masked", masked)
	}
	tok := masked[i : j+1] // "[redacted <rule>]" of the test scanner
	rule := strings.TrimSuffix(strings.TrimPrefix(tok, ReadRedacted), "]")

	if _, err := edit(ref, masked); err != nil || stored(ref) != orig {
		t.Fatalf("saving the masked view unchanged: %v; stored %q, want %q", err, stored(ref), orig)
	}
	if _, err := edit(ref, masked+" and prod"); err != nil {
		t.Fatalf("a change elsewhere in the field: %v, want it saved", err)
	}
	if got := stored(ref); got != orig+" and prod" {
		t.Fatalf("after a change elsewhere the row holds %q, want %q", got, orig+" and prod")
	}

	for _, changed := range []string{
		strings.Replace(read(ref), tok, ReadRedacted+rule+"x]", 1),
		strings.Replace(read(ref), tok, ReadRedacted+rule[:3], 1),
	} {
		_, err := edit(ref, changed)
		if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "nothing written") || strings.Contains(err.Error(), fakeKey) {
			t.Fatalf("an edit inside the mask (%q): %v, want E_INVALID_INPUT naming nothing of the value", changed, err)
		}
		var e *errs.Error
		if !errors.As(err, &e) || !strings.Contains(e.Hint, "--secret-ref <manager>://<locator>") || !strings.Contains(e.Hint, "--secret-unstored") {
			t.Fatalf("the refusal does not give the way to change a secret: %+v", e)
		}
		if got := stored(ref); got != orig+" and prod" {
			t.Fatalf("a refused edit changed the row to %q", got)
		}
	}

	if _, err := edit(ref, "deploy for staging, key rotated away"); err != nil || strings.Contains(stored(ref), "AKIA") {
		t.Fatalf("removing the mask: %v; stored %q, want the key gone", err, stored(ref))
	}

	t.Run("the trash reads masked", func(t *testing.T) {
		gone := w.keyedMem(t, "trashed "+fakeKey+" note")
		if _, err := w.eng.Delete(ctx, gone); err != nil {
			t.Fatal(err)
		}
		find := func(c context.Context) string {
			all, err := w.eng.Trashed(c)
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range all {
				if x.Row.ID == gone.ID {
					return x.Row.Fields["body"].String()
				}
			}
			t.Fatalf("%s is not in the trash", gone.ID)
			return ""
		}
		if got := find(ctx); strings.Contains(got, fakeKey) || !strings.Contains(got, ReadRedacted) {
			t.Fatalf("trash list/show read %q: the stored key, want it masked", got)
		}
		if got := find(WithRawRead(ctx)); got != "trashed "+fakeKey+" note" {
			t.Fatalf("the trashed row holds %q, want the stored text (a restore brings it back)", got)
		}
	})

	t.Run("two masks of one rule", func(t *testing.T) {
		twin := w.keyedMem(t, "old "+fakeKey+" new "+second)
		m := read(twin)
		if strings.Count(m, tok) != 2 {
			t.Fatalf("the twin row reads %q", m)
		}
		if _, err := edit(twin, m+"!"); err != nil || stored(twin) != "old "+fakeKey+" new "+second+"!" {
			t.Fatalf("both masks kept: %v; stored %q", err, stored(twin))
		}
		one := strings.Replace(read(twin), "old "+tok+" ", "", 1)
		if _, err := edit(twin, one); !errs.Has(err, errs.InvalidInput) {
			t.Fatalf("one of two same-rule masks removed: %v, want a refusal (lore cannot tell which value stays)", err)
		}
	})
}
