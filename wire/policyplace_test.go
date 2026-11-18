package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/ids"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	"github.com/thesatellite-ai/lore/store"
)

// TestPolicyPlaceNamesTheStoreRoot guards devco D-658: a value set at a
// store root came back as the literal "<store>", so the budget warning
// printed `lore policy set … --to <store> --yes`, which no shell runs.
// The place is the attached store holding the row, and the printed
// --to works where it was printed (the owner's guard: run it).
func TestPolicyPlaceNamesTheStoreRoot(t *testing.T) {
	t.Parallel()
	storeDir := t.TempDir()
	sid, err := ids.Parse[ids.Store](folderStore)
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Init(storeDir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	repo := gitRepo(t)
	if err = os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	yaml := "stores:\n  - name: northwind\n    url: file://" + filepath.ToSlash(storeDir) + "\n"
	if err = os.WriteFile(filepath.Join(repo, filepath.FromSlash(attach.FileName)), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, repo)
	withProject(t, a)
	key := policy.KeyContextBudgetInternal
	if _, err = run(t, a, opssettings.OpPolicySet, `{"key":"`+string(key)+`","value":"900","to":"northwind"}`, true); err != nil {
		t.Fatalf("policy set at the store root: %v", err)
	}
	to, company, ok := a.PolicyPlace(background(), key)
	if !ok || to != "northwind" || !company {
		t.Fatalf("devco:D-658: PolicyPlace = (%q, %v, %v), want (northwind, true, true)", to, company, ok)
	}
	if strings.Contains(to, "<") {
		t.Fatalf("devco:D-658: a placeholder place %q", to)
	}
	// the printed raise runs as printed and changes the value it names
	if _, err = run(t, a, opssettings.OpPolicySet, `{"key":"`+string(key)+`","value":"22000","to":"`+to+`"}`, true); err != nil {
		t.Fatalf("devco:D-658: the printed --to %s does not work: %v", to, err)
	}
	set, err := a.Policy(background(), store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	if eff, has := set.Get(key); !has || eff.Value == nil || !strings.Contains(eff.Value.String(), "22000") {
		t.Fatalf("devco:D-658: after the printed raise the budget is %+v, want 22000", eff)
	}
}
