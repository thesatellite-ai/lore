//go:build journeys && !windows

package journeys

// migrate_unattached_test.go — devco P-9: a store folder lore 0.1.17 made
// from a repo (`--to <org>/<folder> --create`) that the repo does not
// attach. Ana's repo attaches the root of the store "acme"; a 0.1.17
// teammate's folder billing/ (one `_v: 1` memory and its project row)
// arrives with a pull. migrate status names it as its own place with
// `lore migrate run`, doctor warns about it (never "current", never "a
// lore older than 0.1.18"), and the run upgrades it and pushes it, after
// which nothing is pending.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	muMemID = "mem_01a1dc0d2dae7306a6ad9f99e786245d"
	muPrjID = "prj_dada87779c777e1899961f60829dc2d9"
)

// muV1Files are the files a lore 0.1.17 write leaves in a new folder of a
// store (the store layout's <folder>/<table>/<id>.json): its _meta.json,
// the folder's project row and one memory, all `_v: 1`.
var muV1Files = map[string]string{
	"billing/_meta.json": `{"_v": 1, "project_id": "` + muPrjID + `"}`,
	"billing/projects/" + muPrjID + ".json": `{"_table": "projects", "_v": 1, "id": "` + muPrjID + `", "name": "billing",` +
		` "created_at": "2026-11-04T08:09:56.252002659Z", "updated_at": "2026-11-04T08:09:56.252002894Z"}`,
	"billing/memories/" + muMemID + ".json": `{"_table": "memories", "_v": 1, "id": "` + muMemID + `", "project_id": "` + muPrjID + `",` +
		` "kind": "retrieved", "body": "billing deploys on tuesdays", "source_kind": "manual",` +
		` "created_at": "2026-11-04T08:09:56.252002659Z", "updated_at": "2026-11-04T08:09:56.252002894Z"}`,
}

func TestJourneyMigrateUnattachedFolder(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	lap := w.Persona("Ana Ruiz", "ana@example.test").Laptop()
	bare := filepath.Join(w.Root, "srv", "git", "acme-notes.git")
	w.Git(w.Root, "init", "-q", "--bare", "-b", "main", bare)
	app := lap.Clone(w.Remote("github.com/acme-mu/app"), "app")

	w.Step("day1/attach-the-store-root", func() {
		lap.LoreIn(app, "init", "--non-interactive", "--name=app").OK(t)
		lap.LoreIn(app, "store", "init", "acme", bare, "--yes").OK(t)
		lap.LoreIn(app, "memory", "add", "--body", "the api runs on port 8080", "--to", "acme").OK(t)
		lap.LoreIn(app, "sync").OK(t)
	})

	w.Step("day2/a-0.1.17-folder-arrives", func() {
		seed := filepath.Join(w.Root, "seed")
		w.Git(w.Root, "clone", "-q", bare, seed)
		for rel, body := range muV1Files {
			p := filepath.Join(seed, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		w.Git(seed, "add", "-A")
		w.Git(seed, "-c", "user.name=Old Teammate", "-c", "user.email=old@example.test", "commit", "-q", "-m", "lore: write 3 row(s) in billing")
		w.Git(seed, "push", "-q", "origin", "HEAD:main")
		w.AdvanceDays(1)
		lap.LoreIn(app, "sync").OK(t)
		lap.LoreIn(app, "migrate", "status").OK(t).
			Says(t, "acme/billing", "acme/billing (a folder of the store this repo does not attach): memories 1 row, projects 1 row at v1; lore migrate run upgrades them", "--folder billing")
		lap.LoreIn(app, "doctor").
			Says(t, "row_format: row files at _v: 1 in store folders this repo does not attach", "fix: lore migrate run").
			Never(t, "older than 0.1.18")
	})

	w.Step("day2/migrate-run-upgrades-and-pushes-it", func() {
		lap.LoreIn(app, "migrate", "run").OK(t).Says(t, "Upgraded 2 rows", muMemID, muPrjID)
		for _, rel := range []string{"billing/memories/" + muMemID + ".json", "billing/projects/" + muPrjID + ".json"} {
			if got := w.Git(w.Root, "--git-dir", bare, "show", "main:"+rel); !strings.Contains(got, `"_v": 2`) {
				t.Fatalf("the store remote still has %s at v1:\n%s", rel, got)
			}
		}
		lap.LoreIn(app, "migrate", "status").OK(t).Never(t, "pending:", "acme/billing")
		lap.LoreIn(app, "doctor").Never(t, "row_format")
	})
}
