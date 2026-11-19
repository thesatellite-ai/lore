package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/op"
)

// lpaIn is the input of the preview probe below: a --dry-run and one
// declared read mode, as render.run has (--check).
type lpaIn struct {
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"write nothing"`
	Check  bool `json:"check,omitempty" flag:"check" desc:"write nothing"`
	Out    bool `json:"out,omitempty" flag:"out" desc:"a mode that writes"`
}

// TestLocalPreviewRunsAsARead (devco:D-682): a Local operation that may
// write (render.run: permission write) runs as a read when its input asks
// for a preview only (--dry-run, or a mode its spec names in ReadModes).
// Before, render --dry-run in a store folder with no project row ran
// unmarked, so the store-here open made and committed "lore: project add
// prj_…: <folder>". A writing run stays unmarked and may make the row.
func TestLocalPreviewRunsAsARead(t *testing.T) {
	t.Parallel()
	a := newTestApp(t, t.TempDir())
	probe := op.Define(op.Spec{Name: "probe.run", Summary: "probe", Local: true, Permission: op.PermWrite, Access: op.AccessRead, ReadModes: []string{"check"}},
		func(ctx context.Context, _ lpaIn) (lraOut, error) {
			acc, _ := engine.AccessOf(ctx)
			return lraOut{Access: string(acc)}, nil
		})
	for _, tc := range []struct {
		body string
		want op.Access
	}{
		{`{"dry_run":true}`, op.AccessRead},
		{`{"check":true}`, op.AccessRead},
		{`{}`, ""},
		{`{"out":true}`, ""},
	} {
		res, err := a.runLocal(context.Background(), probe, op.JSONDecoder{Body: []byte(tc.body)}, op.Caller{})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := res.Value().Interface().(lraOut)
		if !ok {
			t.Fatalf("result %v", res.Value())
		}
		if got.Access != string(tc.want) {
			t.Errorf("input %s ran with access %q, want %q", tc.body, got.Access, tc.want)
		}
	}
}
