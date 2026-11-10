package wire_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// TestRenderHistoryThroughTheMachineStore: a recorded render lands in
// <home>/state/machine.db (never the repo), reads back with every field,
// survives a reopen, is scoped to its project (one machine store holds
// every repo, P3-LOCAL-2), comes back oldest first, and a record missing
// a required field is refused (P3-LOCAL-5).
func TestRenderHistoryThroughTheMachineStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := opstest.New(t)
	first := wire.RenderRecord{ProjectID: "prj_a", RepoID: "rep_1", TargetPath: ".lore/LORE.md", Text: "# one",
		SHA256: "aa", TotalBytes: 5, ScopeSummary: `{"included":{}}`, ActorID: opstest.Actor}
	id1, err := h.App.RecordRender(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Text, second.SHA256, second.TotalBytes = "# two!", "bb", 6
	if _, err = h.App.RecordRender(ctx, second); err != nil {
		t.Fatal(err)
	}
	other := first
	other.ProjectID = "prj_b"
	if _, err = h.App.RecordRender(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(h.Home, filepath.FromSlash(wire.MachineStoreFile))); err != nil {
		t.Fatalf("machine store file: %v", err)
	}
	if got, _ := filepath.Glob(filepath.Join(h.Repo, ".lore", "data", "render_histories", "*")); len(got) != 0 {
		t.Fatalf("render records reached the repo: %v", got)
	}

	for _, app := range []*wire.App{h.App, h.Reopen().App} {
		recs, rerr := app.RenderHistory(ctx, "prj_a")
		if rerr != nil || len(recs) != 2 {
			t.Fatalf("RenderHistory = %d records, %v; want the project's 2", len(recs), rerr)
		}
		got := recs[0]
		if got.ID != id1 || got.CreatedAt.IsZero() {
			t.Fatalf("oldest = %s at %v; want %s with a time", got.ID, got.CreatedAt, id1)
		}
		got.ID, got.CreatedAt = "", first.CreatedAt
		if got != first {
			t.Errorf("read back %+v, want %+v", got, first)
		}
		if recs[1].Text != "# two!" || recs[1].TotalBytes != 6 {
			t.Errorf("newest = %+v", recs[1])
		}
	}
	if recs, rerr := h.App.RenderHistory(ctx, "prj_none"); rerr != nil || len(recs) != 0 {
		t.Fatalf("unknown project = %d, %v; want none", len(recs), rerr)
	}
	bad := first
	bad.Text = ""
	if _, err = h.App.RecordRender(ctx, bad); errs.DefOf(errs.CodeOf(err)).Class != errs.ClassBadInput {
		t.Fatalf("missing text = %v; want a bad-input refusal", err)
	}
}
