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
