package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	"github.com/thesatellite-ai/lore/schema"
)

// countingRender is a render host that only counts its calls: the render
// step must not reach it after a failed migrate step.
type countingRender struct{ calls *int }

func (r countingRender) Render(context.Context, opsrender.RunIn) (opsrender.Answer, error) {
	*r.calls++
	return opsrender.Answer{Text: "rendered\n"}, nil
}

// RenderedCopies implements opsrender.CopyChecker: LORE.md is always
// stale, so a render step that is reached renders.
func (countingRender) RenderedCopies(context.Context, string) ([]opsrender.CopyFinding, error) {
	return []opsrender.CopyFinding{{Kind: opsrender.CopyStale, File: ".lore/LORE.md", Message: "stale", Fix: "lore render"}}, nil
}

// TestFixMigrateFailedRendersNothing (E-6): when lore fix --migrate's
// migrate step fails, the render step says "skipped: the migrate step
// failed" and render never runs (it wrote AGENTS.md, LORE.full.md and
// LORE.md while the step reported it skipped).
func TestFixMigrateFailedRendersNothing(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	v1Row(t, repo, schema.TableMemories, commitLineMemA, `"project_id":"`+projectID(t, a)+`","body":"one"`)
	// a tag link the fold cannot read: the migrate step fails on it
	v1Row(t, repo, entityTagsTable, "etg_01a1a59b891c70b789144cd3e711fc0e", `"entity_id":5,"entity_table":"memories","tag_id":"tag_01a1a59b891c70b789144cd3e711fc0f"`)
	// a format-2 project with rows still at v1 (a teammate's branch
	// merged after the migration): only lore fix --migrate finishes it
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(rowfs.RepoMetaPath)), []byte(`{"_v":1,"project_id":"`+projectID(t, a)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rowfs.WriteProjectFormat(repo, rowfs.ProjectFormatCurrent, rowfs.ProjectMigration{By: "Ann Example <ann@example.test>", At: "2026-11-13T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	// a rendered copy from before, so a migrate that changed rows renders
	if err := os.WriteFile(filepath.Join(repo, ".lore", "LORE.md"), []byte("# old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	a.opts.Context.Render = countingRender{calls: &calls}
	steps := a.FixSteps(opsdoctor.WithMigrate(background()), false)
	var mig, rend opsdoctor.FixStep
	for _, s := range steps {
		switch s.Step {
		case opsdoctor.StepMigrate:
			mig = s
		case opsdoctor.StepRender:
			rend = s
		}
	}
	if !mig.Failed {
		t.Fatalf("migrate step did not fail: %+v", mig)
	}
	if rend.Result != renderSkippedMigrateFailed || calls != 0 {
		t.Fatalf("render step %q after a failed migrate, render ran %d time(s); want skipped and never run", rend.Result, calls)
	}
}
