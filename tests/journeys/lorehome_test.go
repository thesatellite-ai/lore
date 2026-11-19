//go:build journeys && !windows

package journeys

// lorehome_test.go — LORE_HOME moves the whole lore home (owner,
// 2026-11-14). The owner keeps a released lore as `lore` and tries a
// preview as `lore-dev`, which runs with LORE_HOME=~/.lore-dev; before the
// fix the preview kept its machine state in ~/.lore too and rewrote the
// released build's config.yaml and store clones. Sam does a whole working
// day with LORE_HOME set (solo setup with a personal store, an org with a
// team store on a remote, notes, tasks and rules, sync, render, a commit
// through lore's git hooks, doctor --all, fix --all): ~/.lore is never
// created, and everything lands under LORE_HOME.

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// loreHomeEnv is the variable under test (adapters/lorehome.EnvName).
const loreHomeEnv = "LORE_HOME"

func TestJourneyLoreHomeMovesTheWholeHome(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	moved := filepath.Join(w.Root, "elsewhere", "lore-dev-home")
	lap.SetEnv(loreHomeEnv, moved)
	oldHome := filepath.Join(lap.Home, ".lore")
	_ = w.Remote(soloStore) // the personal store GitHub holds (the setup page creates it there)
	team := w.Remote("github.com/acme-co/lore-team")
	app := jPlainRepo(t, lap, w.Remote("github.com/sam-dev/app"), filepath.Join(lap.Home, "work", "app"))
	billing := jPlainRepo(t, lap, w.Remote("github.com/acme-co/billing"), filepath.Join(lap.Home, "work", "billing"))
	noOldHome := func(when string) {
		t.Helper()
		if _, err := os.Lstat(oldHome); err == nil {
			var made []string
			_ = filepath.WalkDir(oldHome, func(p string, _ fs.DirEntry, _ error) error {
				made = append(made, p)
				return nil
			})
			t.Fatalf("%s: lore wrote under ~/.lore although LORE_HOME=%s:\n%v", when, moved, made)
		}
	}

	w.Step("day1/solo-setup", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, lap, soloName, soloEmail)
		soloApply(t, lap, "github", map[string]any{"github_account": soloLogin, "host_alias": "github.com"})
		soloApply(t, lap, "personal_store", map[string]any{"personal_store": "create", "store_url": soloStoreURL})
		soloApply(t, lap, "tools", map[string]any{"tools": []string{"claude_code"}})
		soloApply(t, lap, "secrets", map[string]any{"backend": "none"})
		soloApply(t, lap, "folders", map[string]any{"folders": []string{filepath.Join(lap.Home, "work") + "/**"}, "default": true})
		soloApply(t, lap, "summary", map[string]any{})
		noOldHome("after setup")
	})

	w.Step("day1/org-with-team-store", func() {
		lap.LoreIn(billing, "store", "init", "--name", "acme", "--url", team.SSHURL()).OK(t)
		lap.LoreIn(billing, "org", "create", "--org", "acme", "--store", team.SSHURL(), "--own", "github.com/acme-co/*", "--key", "human:"+soloEmail, "--name", "Sam", "--yes").OK(t)
		lap.LoreIn(billing, "init").OK(t)
		lap.LoreIn(billing, "memory", "add", "--body", "invoices go out on the 1st", "--to", "acme").OK(t)
		noOldHome("after org create")
	})

	w.Step("day1/writes-sync-render-commit", func() {
		lap.LoreIn(app, "init").OK(t)
		lap.LoreIn(app, "memory", "add", "--body", "app: timestamps are UTC").OK(t)
		lap.LoreIn(app, "memory", "add", "--body", "I like short commit subjects", "--to", "me").OK(t)
		lap.LoreIn(app, "task", "add", "--title", "ship the export").OK(t)
		lap.LoreIn(app, "rule", "add", "--body", "never log a token", "--severity", "must").OK(t)
		lap.LoreIn(app, "render").OK(t)
		lap.LoreIn(app, "sync").OK(t)
		lap.LoreIn(billing, "sync").OK(t)
		// lore's git hooks run inside git: they find the same home
		jWrite(t, filepath.Join(app.Dir, "README.md"), "app\n")
		app.CommitAll("docs: readme")
		noOldHome("after writes, sync, render and a commit")
	})

	w.Step("day1/doctor-and-fix-all", func() {
		lap.LoreIn(app, "doctor", "--all").Says(t, "Lore home: "+moved+" (from LORE_HOME)")
		lap.LoreIn(app, "fix", "--all")
		noOldHome("after doctor --all and fix --all")
	})

	w.Step("day1/where-names-the-moved-home", func() {
		r := lap.LoreIn(app, "where", "--json").OK(t)
		type homeOut struct {
			Home       string `json:"home"`
			HomeSource string `json:"home_source"`
		}
		var env struct {
			Data homeOut `json:"data"`
		}
		got := env.Data
		if err := json.Unmarshal([]byte(r.Stdout), &env); err == nil && env.Data.Home != "" {
			got = env.Data
		} else if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil {
			t.Fatalf("where --json: %v\n%s", err, r.Stdout)
		}
		if got.Home != moved || got.HomeSource != loreHomeEnv {
			t.Fatalf("where says home %q from %q, want %q from LORE_HOME", got.Home, got.HomeSource, moved)
		}
		lap.LoreIn(app, "where").OK(t).Says(t, "lore home: "+moved+" (from LORE_HOME)")
		for _, rel := range []string{"config.yaml", "stores", "cache", "state"} {
			if _, err := os.Stat(filepath.Join(moved, rel)); err != nil {
				t.Errorf("LORE_HOME has no %s: %v", rel, err)
			}
		}
		noOldHome("at the end")
	})
}

// TestJourneyLoreHomeBesideOldLore is story K1 (tests/stories/K1-lore-home.md):
// the owner's machine keeps lore 0.1.17 as plain `lore` and runs the new
// build with LORE_HOME=$HOME/.lore-dev. The old build's ~/.lore stays
// byte-identical through a whole round of the new build, and the old build
// still reads its notes afterwards.
func TestJourneyLoreHomeBesideOldLore(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	owner := w.Persona("Ola Owner", "ola@example.test")
	lap := owner.Laptop()
	old := UseOldLore(t, lap)
	oldRepo := jPlainRepo(t, lap, w.Remote("github.com/ola-dev/legacy"), filepath.Join(lap.Home, "work", "legacy"))
	newRepo := jPlainRepo(t, lap, w.Remote("github.com/ola-dev/fresh"), filepath.Join(lap.Home, "work", "fresh"))
	oldHome := filepath.Join(lap.Home, ".lore")
	devHome := filepath.Join(lap.Home, ".lore-dev")
	var before map[string]string

	w.Step("day1/old-lore-writes", func() {
		old.Old(oldRepo, "init", "--name", "legacy").OK(t)
		old.Old(oldRepo, "task", "add", "Write the export job").OK(t)
		old.Old(oldRepo, "memory", "add", "--body", "staging is reset every monday").OK(t)
		before = treeHashes(t, oldHome)
		if len(before) == 0 {
			t.Fatalf("lore 0.1.17 wrote nothing under %s", oldHome)
		}
	})

	w.Step("day1/new-lore-with-its-own-home", func() {
		lap.SetEnv(loreHomeEnv, devHome)
		lap.LoreIn(newRepo, "init").OK(t)
		lap.LoreIn(newRepo, "memory", "add", "--body", "the preview keeps its own home").OK(t)
		lap.LoreIn(newRepo, "task", "add", "--title", "try the preview").OK(t)
		lap.LoreIn(newRepo, "render").OK(t)
		lap.LoreIn(newRepo, "doctor")
		lap.LoreIn(newRepo, "where").OK(t).Says(t, "lore home: "+devHome+" (from LORE_HOME)")
		lap.SetEnv(loreHomeEnv, "")
		if _, err := os.Stat(filepath.Join(devHome, "README.md")); err != nil {
			t.Errorf("the new build's home %s has no README.md: %v", devHome, err)
		}
		after := treeHashes(t, oldHome)
		for p, h := range after {
			if before[p] != h {
				t.Errorf("the new build changed or added %s in lore 0.1.17's ~/.lore", p)
			}
		}
		for p := range before {
			if _, ok := after[p]; !ok {
				t.Errorf("the new build removed %s from lore 0.1.17's ~/.lore", p)
			}
		}
	})

	w.Step("day2/old-lore-still-reads-its-notes", func() {
		old.Old(oldRepo, "memory", "list").OK(t).Says(t, "staging is reset every monday")
	})
}

// treeHashes maps every file and folder under root (relative path) to its
// mode and content, so a test sees any byte that changed.
func treeHashes(t testing.TB, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		v := info.Mode().String()
		if d.Type().IsRegular() {
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			v += " " + string(b)
		}
		out[rel] = v
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}
