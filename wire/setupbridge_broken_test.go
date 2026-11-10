package wire_test

// setupbridge_broken_test.go — the setup bookkeeping survives a repo whose
// attached store is broken: the repo's full open fails (E_NOT_A_STORE),
// and setup_progress is served by the machine-only App instead of that
// failure blocking setup.plan / check / apply (the steps page showed only
// E_NOT_A_STORE for a member setup active in such a repo).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/wire"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

func TestSetupStoreWithABrokenAttachedStore(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	ctx := context.Background()
	h := bridgeHarness(t)
	bare := filepath.Join(t.TempDir(), "team-notes.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	sy := filepath.Join(h.Repo, ".lore", "stores.yaml")
	if err := os.WriteFile(sy, []byte("stores:\n  - name: team_notes\n    url: file://"+bare+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A repo `lore init` made has .lore/data/_meta.json (opssetup.LoreRepoAt);
	// the harness writes rows only, so the marker is added here.
	meta := `{"_v": 2, "features": ["hlc", "policy"], "min_lore_version": "0.1.18"}`
	if err := os.WriteFile(filepath.Join(h.Repo, ".lore", "data", "_meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	if !opssetup.LoreRepoAt(h.Repo) {
		t.Fatalf("harness repo %s is not a lore repo; the test would not reach the repo branch", h.Repo)
	}
	app, err := wire.New(wire.StandaloneOptions(h.Repo, h.Home, h.Repo, "test"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.Engine(ctx); !errs.Has(err, errs.NotAStore) {
		t.Fatalf("repo open with an empty store attached = %v, want E_NOT_A_STORE", err)
	}
	st, err := app.SetupStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := onboard.Progress{Setup: "acme", Step: onboard.StepKind, Status: onboard.StepDone,
		Answers: onboard.Answers{"kind": {Text: "member"}, "name": {Text: "acme"}}}
	if err = st.PutProgress(ctx, p); err != nil {
		t.Fatalf("PutProgress with a broken store attached: %v", err)
	}
	if got, gerr := st.Progress(ctx, "acme"); gerr != nil || len(got) != 1 {
		t.Fatalf("Progress = %+v, %v", got, gerr)
	}
	// The fallback App is closed with the App (no engine left open).
	if err = app.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
