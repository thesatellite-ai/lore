package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
)

// TestFixAllIsNotRepoOnly (devco D-528): `lore fix --all` walks a folder of
// repos from wherever it starts, so the repo-only refusal (D-523) never
// stops it; a plain `lore fix` outside a repo still gets that refusal. The
// --all run itself re-runs the binary per repo, so its end-to-end guards
// are the journeys/stories that broke (FleetLinks, NoLoreRepoUntouched,
// CIShallowReadOnlyHome, UpgraderFixOldProjects, J1, J4).
func TestFixAllIsNotRepoOnly(t *testing.T) {
	dir := t.TempDir()
	a := openWith(t, dir, Options{})
	_, err := run(t, a, opsfix.OpRun, `{}`, false)
	if !errs.Has(err, errs.Unsupported) || !strings.Contains(err.Error(), "is not inside a code repo") {
		t.Fatalf("plain fix outside a repo: want the repo-only refusal, got %v", err)
	}
	e, ok := a.Registry().Lookup(opsfix.OpRun)
	if !ok {
		t.Fatal("no fix.run")
	}
	for body, want := range map[string]bool{
		`{}`:                             false,
		`{"all":true}`:                   true,
		`{"all":true,"dirs":["/tmp/x"]}`: true,
		`{"dry_run":true}`:               false,
	} {
		if got := fleetFix(e, op.JSONDecoder{Body: []byte(body)}); got != want {
			t.Errorf("fleetFix(%s) = %v, want %v", body, got, want)
		}
	}
}
