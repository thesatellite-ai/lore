package renderhost

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// TestTrendCountsARenderOfTheSameTextOnce is devco:D-944 (bug hunt H-202):
// since a write re-renders LORE.md (devco:D-921), the `lore render` after
// it records the same text a second time, and `lore render --trend` listed
// every render twice. A run of records holding the same text is one point
// (the newest); a different text in between keeps both.
func TestTrendCountsARenderOfTheSameTextOnce(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	a, err := wire.New(wire.Options{RepoDir: repo, Home: t.TempDir(), FlagActor: "human:alice@example.com",
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	p, err := a.Create(ctx, store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": text(t, "demo")})
	if err != nil {
		t.Fatal(err)
	}
	project := p.Row.ID.String()
	t.Chdir(repo)
	// a write's render, the render after it (same text), a new text, its
	// repeat, then the first text again
	for _, body := range []string{"one\n", "one\n", "three\n", "three\n", "one\n"} {
		if _, rerr := a.RecordRender(ctx, wire.RenderRecord{ProjectID: project, TargetPath: LoreMDRel, Text: body,
			SHA256: sha256Hex([]byte(body)), TotalBytes: len(body), ScopeSummary: "{}"}); rerr != nil {
			t.Fatal(rerr)
		}
	}
	f := &renderFlags{trend: true, asJSON: true, outPath: LoreMDRel}
	if err := New(Options{App: a}).runRender(ctx, f); err != nil {
		t.Fatal(err)
	}
	var got trendOut
	if err := json.Unmarshal([]byte(f.out().String()), &got); err != nil {
		t.Fatalf("--trend --json: %v\n%s", err, f.out().String())
	}
	var sizes []int
	for _, r := range got.Renders {
		sizes = append(sizes, r.Bytes)
	}
	if len(sizes) != 3 || sizes[0] != 4 || sizes[1] != 6 || sizes[2] != 4 {
		t.Fatalf("trend points %v, want one per run of the same text: [4 6 4]", sizes)
	}
}
