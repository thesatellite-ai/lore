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
	"github.com/thesatellite-ai/lore/schema"
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

// TestAfterInOrgLinkedFolder is t4b's root cause of teame2e F09/F01: the
// repo is linked to org northwind at folder globex, so the org hook
// attaches that store folder as "northwind"; `--to northwind/globex` names
// the SAME store folder and must reuse that attachment (an alias), never
// attach it a second time, or every row of the folder is found twice and
// `--after <task in globex>` fails with E_AMBIGUOUS_ID (which parseAfter
// used to hide as "no such task"). The repo's own stores.yaml does not
// attach the store (the org link is the only way in).
func TestAfterInOrgLinkedFolder(t *testing.T) {
	h0 := opstest.WithStores(t, []string{"northwind"})
	cfgPath := filepath.Clean(filepath.Join(h0.Repo, ".lore", "stores.yaml"))
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	url := regexp.MustCompile(`url: (\S+)`).FindStringSubmatch(string(b))[1]
	org := func(o *wire.Options) {
		o.OrgStores = func(context.Context) ([]attach.Store, error) {
			return []attach.Store{{Name: "northwind", URL: url, Folder: "globex"}}, nil
		}
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
	}
	h := opstest.New(t, org)
	if err := os.WriteFile(cfgPath, []byte("stores: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h = h.Reopen(org)
	// the first task goes in through each spelling of the same place
	for _, to := range []string{"northwind/globex", "northwind"} {
		up := addID(h, h.MustYes(opstask.OpAdd, `{"title":"up via `+to+`","to":"northwind/globex"}`))
		for name, run := range map[string]*opstest.H{"same process": h, "new process": h.Reopen(org)} {
			for _, target := range []string{"northwind/globex", "northwind"} {
				res, err := run.RunYes(opstask.OpAdd, `{"title":"landing","to":"`+target+`","after":["`+up+`"]}`)
				if err != nil {
					t.Fatalf("%s, --to %s --after %s: %v", name, target, up, err)
				}
				src, r := get(run, schema.TableTasks, addID(run, res))
				if src != "northwind" {
					t.Fatalf("%s: --to %s landed in %q; the org's attachment of that folder is \"northwind\" (one folder, one name)", name, target, src)
				}
				if raw, merr := r.Fields["blocked_by"].MarshalJSON(); merr != nil || !strings.Contains(string(raw), up) {
					t.Fatalf("%s: blocked_by does not name %s: %s %v", name, up, raw, merr)
				}
			}
			if out := run.JSON(run.Must(opstask.OpShow, `{"id":"`+up+`"}`)); !strings.Contains(out, up) {
				t.Fatalf("%s: show %s by id alone: %s", name, up, out)
			}
		}
	}
	if got, err := os.ReadFile(cfgPath); err != nil || strings.Contains(string(got), "northwind") {
		t.Fatalf("the org store's url must never be written into stores.yaml (CFG7): %q %v", got, err)
	}
}
