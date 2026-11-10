package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestLocateHiddenLeakEndToEnd is the F15 leak lookup through the real
// App (p2-oldsync, final smoke): an org admin writes a memory into the
// org store's company folder; a client repo attaches only its linked
// project folder (acme) of the same store. From the client repo:
//
//	task add --ref <that memory>           → E_LEAK_BLOCKED, nothing written
//	task add --ref <that memory> --to agency → allowed (internal → internal)
//	task add --ref <unknown id>            → E_NOT_FOUND
//
// and App.LocateHidden reports the store and folder the row is in.
func TestLocateHiddenLeakEndToEnd(t *testing.T) {
	t.Parallel()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	internal := func(context.Context) (bool, error) { return false, nil }
	client := func(context.Context) (bool, error) { return true, nil }

	admin := openWith(t, gitRepo(t), Options{
		OrgStores: func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "agency", URL: orgURL, Folder: "acme"}, {Name: "agency/org", URL: orgURL, Folder: "org"}}, nil
		},
		RepoIsClient: internal,
	})
	withProject(t, admin)
	out, err := run(t, admin, opsmemory.OpAdd, `{"body":"company only","to":"company"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	hidden := idOf(t, out)
	// the client's linked folder needs its project row before a --to write
	if _, err = run(t, admin, opsmemory.OpAdd, `{"body":"team note","to":"team"}`, true); err != nil {
		t.Fatal(err)
	}

	repo := gitRepo(t)
	c := openWith(t, repo, Options{
		OrgStores: func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "agency", URL: orgURL, Folder: "acme"}}, nil
		},
		RepoIsClient: client,
	})
	withProject(t, c)

	hid, err := ids.ParseAny(hidden)
	if err != nil {
		t.Fatal(err)
	}
	ref := engine.Ref{Table: schema.TableMemories, ID: hid}
	if _, _, gerr := c.Get(background(), ref); !errs.Has(gerr, errs.NotFound) {
		t.Fatalf("the client sees the company row: %v", gerr)
	}
	hit, found, err := c.LocateHidden(background(), ref)
	if err != nil || !found || hit.Store != "agency" || hit.Folder != "org" {
		t.Fatalf("LocateHidden = %+v %v %v, want agency/org", hit, found, err)
	}

	tasksBefore := countRows(t, c, schema.TableTasks)
	_, err = run(t, c, opstask.OpAdd, `{"title":"points up","ref":["`+hidden+`"]}`, true)
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("client --ref at a hidden company row: %v, want E_LEAK_BLOCKED", err)
	}
	if !strings.Contains(err.Error(), "folder org") {
		t.Errorf("refusal does not say where the row is: %v", err)
	}
	if n := countRows(t, c, schema.TableTasks); n != tasksBefore {
		t.Fatalf("a refused task add wrote %d task rows", n-tasksBefore)
	}
	if entries, rerr := os.ReadDir(filepath.Join(repo, ".lore", "data", schema.TableKnowledgeRefs)); rerr == nil && len(entries) > 0 {
		t.Fatalf("a refused task add wrote %d knowledge_refs files", len(entries))
	}

	if _, err = run(t, c, opstask.OpAdd, `{"title":"points up from the org","ref":["`+hidden+`"],"to":"agency"}`, true); err != nil {
		t.Fatalf("--to agency with the same ref: %v", err)
	}

	_, err = run(t, c, opstask.OpAdd, `{"title":"points nowhere","ref":["mem_01a1a5aca7f57659be7320c5f4b700ff"]}`, true)
	if !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown id: %v, want E_NOT_FOUND", err)
	}
}

// countRows counts live rows of table in the repo store.
func countRows(t *testing.T, a *App, table row.Table) int {
	t.Helper()
	rows, err := a.List(background(), "repo", table)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}
