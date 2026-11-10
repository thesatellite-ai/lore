package migrate

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
)

// markRewriter is fakeRewriter that also records, per step, whether the
// step's context carried WithRun's mark.
type markRewriter struct {
	fakeRewriter
	marks []string
}

func (m *markRewriter) note(step string, ctx context.Context) {
	if InRun(ctx) {
		step += "=marked"
	}
	m.marks = append(m.marks, step)
}

func (m *markRewriter) MigrateSettings(ctx context.Context, dryRun bool) (Settings, error) {
	m.note("settings", ctx)
	return m.fakeRewriter.MigrateSettings(ctx, dryRun)
}

func (m *markRewriter) MigrateLive(ctx context.Context, dryRun bool) ([]string, error) {
	m.note("live", ctx)
	return m.fakeRewriter.MigrateLive(ctx, dryRun)
}

func (m *markRewriter) RewriteV1(ctx context.Context, table row.Table, dryRun bool) ([]ids.AnyID, error) {
	m.note("rewrite:"+table.String(), ctx)
	return m.fakeRewriter.RewriteV1(ctx, table, dryRun)
}

// TestRunMarksItsOwnWrites (F-MIGRATE-1): a writing `migrate run` carries
// WithRun's mark into every step (settings incl. the machine-local copy,
// live rows, each table rewrite), so the composition root's O6 format
// guard lets the run's own writes through and one run finishes; a dry run
// writes nothing and carries no mark; a context outside a run is unmarked.
func TestRunMarksItsOwnWrites(t *testing.T) {
	if InRun(context.Background()) {
		t.Fatal("a plain context is marked as a migration run")
	}
	for _, dry := range []bool{false, true} {
		m := &markRewriter{fakeRewriter: fakeRewriter{tables: []row.Table{"memories", "rules"}}}
		input := `{}`
		if dry {
			input = `{"dry_run":true}`
		}
		e := Ops(m)[0]
		in, err := e.Decode(op.JSONDecoder{Body: []byte(input)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Invoke(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		want := []string{"settings", "live", "rewrite:memories", "rewrite:rules"}
		if !dry {
			for i := range want {
				want[i] += "=marked"
			}
		}
		if !slices.Equal(m.marks, want) {
			t.Fatalf("dry=%v: steps and marks = %v, want %v", dry, m.marks, want)
		}
	}
}
