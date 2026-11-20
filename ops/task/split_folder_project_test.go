package task_test

// split_folder_project_test.go — devco:D-777: a task added with --to
// <org>/<folder> belongs to the folder's project; its sub-tasks (task
// split, task add --parent) resolved "this repo's project" or the
// parent's prj_ id among the repo store's projects only, so splitting it
// answered E_NOT_FOUND "no project" and add --parent E_PARENT_PROJECT.

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/wire"
)

var projectIDRe = regexp.MustCompile(`"project_id":"(prj_[0-9a-f]+)"`)

// TestSubTasksOfAFolderTaskJoinItsProject: the parent is in a store folder
// the repo does not attach; split and add --parent both write sub-tasks in
// the parent's (the folder's) project, in the parent's folder.
func TestSubTasksOfAFolderTaskJoinItsProject(t *testing.T) {
	t.Parallel()
	h0 := opstest.WithStores(t, []string{"northwind"})
	b, err := os.ReadFile(filepath.Join(h0.Repo, ".lore", "stores.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	url := regexp.MustCompile(`url: (\S+)`).FindStringSubmatch(string(b))[1]
	org := func(o *wire.Options) {
		o.OrgStores = func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "northwind", URL: url, Folder: "acme"}}, nil
		}
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
	}
	h := opstest.New(t, org)
	parent := addID(h, h.MustYes(opstask.OpAdd, `{"title":"invoices export","to":"northwind/globex"}`))
	show := func(id string) string {
		t.Helper()
		return h.JSON(h.Must(opstask.OpShow, `{"id":"`+id+`"}`))
	}
	pm := projectIDRe.FindStringSubmatch(show(parent))
	if pm == nil {
		t.Fatalf("the parent names a project: %s", show(parent))
	}
	res, err := h.RunYes(opstask.OpSplit, `{"id":"`+parent+`","titles":["export query","download button"]}`)
	if err != nil {
		t.Fatalf("split of a task in a store folder: %v", err)
	}
	kids := regexp.MustCompile(`tsk_[0-9a-f]+`).FindAllString(h.JSON(res), -1)
	res, err = h.RunYes(opstask.OpAdd, `{"title":"one more","parent":"`+parent+`"}`)
	if err != nil {
		t.Fatalf("add --parent of a task in a store folder: %v", err)
	}
	kids = append(kids, addID(h, res))
	seen := 0
	for _, k := range kids {
		if k == parent {
			continue
		}
		s := show(k)
		if !strings.Contains(s, `"parent_id":"`+parent+`"`) || !strings.Contains(s, `"project_id":"`+pm[1]+`"`) {
			t.Fatalf("sub-task %s is under %s in project %s: %s", k, parent, pm[1], s)
		}
		seen++
	}
	if seen != 3 {
		t.Fatalf("three sub-tasks, got %d: %v", seen, kids)
	}
}
