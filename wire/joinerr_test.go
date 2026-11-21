package wire

import (
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestReplaceErrNestedJoin: a refusal inside a join inside a join (open's
// fail joined it with the agent key's settle and then with the close) is
// still swapped for its exact form (devco:D-840: the placeholder hint
// stayed and "--to <org>/<folder>" was lost).
func TestReplaceErrNestedJoin(t *testing.T) {
	old := errs.New(errs.NotAStore, "not set up").WithHint("link it: --project <folder>")
	repl := old.WithHint("write to that folder with --to acme/sync")
	closeErr := errors.New("close failed")
	for name, err := range map[string]error{
		"itself":      old,
		"one join":    errors.Join(old, closeErr),
		"nested join": errors.Join(errors.Join(old, nil), closeErr),
		"deeper":      errors.Join(errors.Join(errors.Join(old, nil), nil)),
	} {
		got := replaceErr(err, old, repl)
		var e *errs.Error
		if !errors.As(got, &e) || e.Hint != repl.Hint {
			t.Errorf("%s: replaceErr kept the old hint: %v", name, got)
		}
		if errors.Is(err, closeErr) && !errors.Is(got, closeErr) {
			t.Errorf("%s: replaceErr dropped the close error: %v", name, got)
		}
	}
}

// TestJoinFlat: joinFlat keeps one level whatever it is handed, drops nil
// parts and returns a lone part as itself (open's fail, devco:D-840).
func TestJoinFlat(t *testing.T) {
	a, b, c := errors.New("a"), errors.New("b"), errors.New("c")
	if got := joinFlat(nil, nil); got != nil {
		t.Fatalf("joinFlat(nil, nil) = %v, want nil", got)
	}
	if got := joinFlat(joinFlat(a, nil), nil); got != a { //nolint:errorlint // identity is the point
		t.Fatalf("a lone part came back wrapped: %#v", got)
	}
	got := joinFlat(errors.Join(errors.Join(a, nil), b), c)
	j, ok := got.(interface{ Unwrap() []error }) //nolint:errorlint // the join's own parts
	if !ok {
		t.Fatalf("not a join: %#v", got)
	}
	parts := j.Unwrap()
	if len(parts) != 3 || parts[0] != a || parts[1] != b || parts[2] != c { //nolint:errorlint // identity is the point
		t.Fatalf("parts = %v, want [a b c] one level deep", parts)
	}
}
