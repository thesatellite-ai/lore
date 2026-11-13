package wire

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestIDSearchSaysWhereItLooked (B2-21): a not-found answer for the id
// searched names the stores attached here and the org folders read, and
// keeps its code; another error, or a not-found of another id, is left as
// it was.
func TestIDSearchSaysWhereItLooked(t *testing.T) {
	const id = "tsk_01a1d1e955aa77ec8b5385a6740c1799"
	s := &idSearch{id: id, attached: []string{"repo", "acme"}, folders: []string{"acme/sync", "acme/core"}}
	got := s.withSearchHint(errs.New(errs.NotFound, `task "`+id+`" not found`))
	var e *errs.Error
	if !errors.As(got, &e) || e.Code != errs.NotFound {
		t.Fatalf("code lost: %v", got)
	}
	for _, want := range []string{"looked in the stores attached here (acme, repo)", "org's store folders on this machine (acme/core, acme/sync)"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("message %q lacks %q", e.Message, want)
		}
	}
	other := errs.New(errs.Internal, "boom")
	if s.withSearchHint(other) != other {
		t.Error("a non-not-found error was changed")
	}
	if (*idSearch)(nil).withSearchHint(other) != other {
		t.Error("no search: the error must pass through")
	}
}
