package task_test

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

// TestRefsInOrgFolderTarget is t4b's F09 sequence at the op level: in an
// internal repo whose org store is attached by the org hook (not
// stores.yaml), `task add --to northwind/globex --mission <m> --repo
// frontend --after <task>` with the mission and the earlier task in the
// same store folder resolves every ref, in the same process and in a new
// one (rows read back from the files); a ref into the repo store from the
// folder is allowed (F15: internal → client is fine).
func TestRefsInOrgFolderTarget(t *testing.T) {
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
	up := addID(h, h.MustYes(opstask.OpAdd, `{"title":"up","to":"northwind/globex"}`))
	m := addID(h, h.MustYes("mission.add", `{"title":"m","to":"northwind/globex"}`))
	repoTask := add(h, "in the repo", "")
	for name, run := range map[string]*opstest.H{"same process": h, "new process": h.Reopen(org)} {
		for _, after := range []string{up, repoTask} {
			res, err := run.RunYes(opstask.OpAdd, `{"title":"landing","to":"northwind/globex","after":["`+after+`"],"mission":"`+m+`","repo":"frontend"}`)
			if err != nil {
				t.Fatalf("%s, after %s: %v", name, after, err)
			}
			show := run.JSON(run.Must(opstask.OpShow, `{"id":"`+addID(run, res)+`"}`))
			if !strings.Contains(show, `"mission_id":"`+m+`"`) {
				t.Fatalf("%s: mission not set: %s", name, show)
			}
		}
	}
}
