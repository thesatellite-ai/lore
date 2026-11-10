package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// TestOpsRegisterAndRefuseWithoutHost: every operation defines (no tag
// panic), names are unique, and without a host each answers E_UNSUPPORTED
// (P3-SYNC-1).
func TestOpsRegisterAndRefuseWithoutHost(t *testing.T) {
	entries := Ops(nil)
	if _, err := op.NewRegistry(entries...); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 15 {
		t.Fatalf("got %d operations, want 15 (design-listed commands)", len(entries))
	}
	_, err := run(ops{}, Env.SyncStatus)(context.Background(), StatusIn{})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.Unsupported {
		t.Fatalf("no host: %v", err)
	}
}

// TestTextKeepsHostLines: the human output is the host's lines, one per
// line, and nothing for no lines.
func TestTextKeepsHostLines(t *testing.T) {
	if got := (DoneOut{Lines: []string{"a", "b"}}).Text(); got != "a\nb\n" {
		t.Fatalf("text %q", got)
	}
	if got := (RunOut{}).Text(); got != "" {
		t.Fatalf("empty text %q", got)
	}
}

// TestRunOffersYes (issue #23): `lore sync` may ask before writing the
// org store links of a moved repo, so sync.run's spec says it may ask and
// every surface offers the yes answer (`lore sync --yes`, REST "yes");
// that ask is its own, so nothing is asked up front. The other read verbs
// never ask.
func TestRunOffersYes(t *testing.T) {
	for _, e := range Ops(nil) {
		s := e.Spec()
		want := s.Name == OpRun
		if s.Name == OpPurge {
			continue // confirmed through `lore trash purge --confirm`, not --yes
		}
		if got := s.Confirm.CanAsk(); got != want {
			t.Errorf("%s offers yes %v, want %v", s.Name, got, want)
		}
		if s.Name == OpRun && (s.Confirm.Always || s.Confirm.When != "") {
			t.Errorf("%s must not ask up front: %+v", s.Name, s.Confirm)
		}
	}
}
