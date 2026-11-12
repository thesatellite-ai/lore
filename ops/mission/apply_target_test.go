package mission_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/mission"
	"github.com/thesatellite-ai/lore/wire"
)

// applyStoreID is the folder store of the apply target matrix (arbitrary hex).
const applyStoreID = "sto_0e45506e64357c94a87e70ef1c6bbb07"

// applyStore makes an initialised folder store with the folder client1
// and returns its directory.
func applyStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.Parse[ids.Store](applyStoreID)
	if err != nil {
		t.Fatal(err)
	}
	if err = folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "client1"), 0o750); err != nil {
		t.Fatal(err)
	}
	// a store folder is one with its _meta.json (rowfs.HasStoreFolder)
	if err = os.WriteFile(filepath.Join(dir, "client1", rowfs.MetaFileName), []byte(`{"_v":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// applyIn is mission apply's input for a plan whose mission goes to to.
func applyIn(t *testing.T, to string) string {
	t.Helper()
	plan := strings.Replace(planFile, "  title: Invite flow\n", "  title: Invite flow\n  to: "+to+"\n", 1)
	return `{"file":` + opstest.Quote(writePlan(t, plan)) + `}`
}

// TestMissionApplyFolderTarget (BUG-T8, later 568): a plan's `to:
// <store>/<folder>` for a folder the store lacks is E_NEEDS_CONFIRM
// naming the existing folders with nothing written (no folder, no
// mission); a yes creates the folder and the mission and its tasks land
// there. In a client repo whose org store is attached at its linked folder
// under the plain name "agency", `to: agency/client1` is that same
// attached store (the alias), not a second target.
func TestMissionApplyFolderTarget(t *testing.T) {
	t.Run("new folder", func(t *testing.T) {
		dir := applyStore(t)
		h := opstest.Bare(t, func(o *wire.Options) {
			o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
		})
		yaml := "stores:\n  - name: northwind\n    url: file://" + filepath.ToSlash(dir) + "\n"
		if err := os.MkdirAll(filepath.Join(h.Repo, ".lore"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.Repo, filepath.FromSlash(attach.FileName)), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		h.SeedProject()
		in := applyIn(t, "northwind/globex")
		_, err := h.Run(mission.OpApply, in)
		if !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "existing: client1") {
			t.Fatalf("missing folder: %v", err)
		}
		if _, serr := os.Stat(filepath.Join(dir, "globex")); !os.IsNotExist(serr) {
			t.Fatalf("a refused apply made the folder: %v", serr)
		}
		if js := h.JSON(h.Must(mission.OpList, `{"all":true}`)); strings.Contains(js, "Invite flow") {
			t.Fatalf("a refused apply wrote the mission: %s", js)
		}
		var out mission.ApplyOut
		if err = json.Unmarshal([]byte(h.JSON(h.MustYes(mission.OpApply, in))), &out); err != nil {
			t.Fatal(err)
		}
		if out.Store != "northwind/globex" || len(out.Tasks) != 3 {
			t.Fatalf("apply --yes: store %q, %d tasks", out.Store, len(out.Tasks))
		}
		if _, serr := os.Stat(filepath.Join(dir, "globex", "missions", out.MissionID+".json")); serr != nil {
			t.Fatalf("the mission is not in the new folder: %v", serr)
		}
	})
	t.Run("client-linked folder alias", func(t *testing.T) {
		dir := applyStore(t)
		h := opstest.New(t, func(o *wire.Options) {
			o.RepoIsClient = func(context.Context) (bool, error) { return true, nil }
			o.OrgStores = func(context.Context) ([]attach.Store, error) {
				return []attach.Store{{Name: "agency", URL: fileuri.URL(dir, ""), Folder: "client1"}}, nil
			}
		})
		var out mission.ApplyOut
		if err := json.Unmarshal([]byte(h.JSON(h.Must(mission.OpApply, applyIn(t, "agency/client1")))), &out); err != nil {
			t.Fatal(err)
		}
		if out.Store != "agency" {
			t.Fatalf("store %q, want the attached alias agency", out.Store)
		}
		if _, serr := os.Stat(filepath.Join(dir, "client1", "missions", out.MissionID+".json")); serr != nil {
			t.Fatalf("the mission is not in the linked folder: %v", serr)
		}
		if b, _ := os.ReadFile(filepath.Join(h.Repo, filepath.FromSlash(attach.FileName))); strings.Contains(string(b), "agency/client1") {
			t.Fatalf("the alias was recorded as a second target:\n%s", b)
		}
	})
}
