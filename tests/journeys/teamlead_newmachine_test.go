//go:build journeys && !windows

package journeys

// teamlead_newmachine_test.go — an admin's new machine for a team that
// exists already (B1-24, decision D8): Rae made the org on her laptop;
// on her new desk machine she runs the setup page's admin steps only
// (no lore org import, no hand-written config). The team step takes the
// existing store, builds the org entry from its lore-org.yaml (every
// store, own and repos) and the setup is an admin setup. The GitHub step
// is skipped: the stores here are not reached through a GitHub login.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	tnOrg       = "rivet"
	tnStoreSlug = "github.com/rivet/notes"
	tnKBSlug    = "github.com/rivet/kb"
	tnEmail     = "rae@rivet.example"
)

func TestJourneyTeamLeadNewMachine(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	rae := w.Persona("Rae Lind", tnEmail)
	lap := rae.Laptop()
	store, kb := w.Remote(tnStoreSlug), w.Remote(tnKBSlug)

	w.Step("day1/the-org-exists", func() {
		app := lap.Clone(w.Remote("github.com/rivet/app"), "app")
		lap.LoreIn(app, "init", "--name=app").OK(t)
		lap.LoreIn(app, "store", "init", "kb", kb.URL(), "--yes").OK(t)
		lap.LoreIn(app, "store", "init", tnOrg, store.URL(), "--yes").OK(t)
		lap.LoreIn(app, "org", "create", tnOrg, "--store", tnOrg, "--own", "github.com/rivet/*", "--repos", "github.com/clientco/*",
			"--key", "human:"+tnEmail, "--name", rae.Name, "--yes").OK(t)
		lap.LoreIn(app, "sync").OK(t) // C5: the org reaches the store with Rae's sync
		// the team's file lists a second store
		edit := filepath.Join(w.Root, "org-edit")
		w.Git(w.Root, "clone", "-q", store.Dir, edit)
		f := filepath.Join(edit, "org", "lore-org.yaml")
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		two := strings.Replace(string(b), "org_folder: org\n", "org_folder: org\n    - url: "+kb.URL()+"\n", 1)
		if err = os.WriteFile(f, []byte(two), 0o644); err != nil {
			t.Fatal(err)
		}
		w.Git(edit, "-c", "user.name=Rae", "-c", "user.email="+tnEmail, "commit", "-q", "-am", "the kb store")
		w.Git(edit, "push", "-q", "origin", "main")
	})

	w.Step("day30/new-machine-admin-setup-attaches-the-team", func() {
		desk := rae.NewLaptop("desk")
		apply := func(step string, answers map[string]any) {
			t.Helper()
			b, err := json.Marshal(answers)
			if err != nil {
				t.Fatal(err)
			}
			desk.Lore("", "setup", "apply", tnOrg, "--step", step, "--answers", string(b), "--yes").OK(t)
		}
		apply("kind", map[string]any{"kind": "admin", "name": tnOrg})
		apply("identity", map[string]any{"name": rae.Name, "email": tnEmail})
		desk.Lore("", "setup", "apply", tnOrg, "--step", "github", "--answers", "{}").OK(t).Says(t, "skipped")
		apply("personal_store", map[string]any{"personal_store": "none"})
		apply("team", map[string]any{"org": tnOrg, "store_url": store.URL()})

		cfg, err := os.ReadFile(filepath.Join(desk.Home, ".lore", "config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"- url: " + store.URL(), "- url: " + kb.URL(), "github.com/rivet/*", "github.com/clientco/*", "kind: admin"} {
			if !strings.Contains(string(cfg), want) {
				t.Errorf("config.yaml lacks %q:\n%s", want, cfg)
			}
		}
		// a repo of the org is the org's own repo here, as on her laptop
		app := desk.Clone(w.Remote("github.com/rivet/app"), "app")
		desk.LoreIn(app, "whoami").OK(t).Says(t, "own repo")
	})
}
