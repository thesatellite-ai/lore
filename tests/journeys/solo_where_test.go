//go:build journeys && !windows

package journeys

// solo_where_test.go — no hidden magic (plain files sweep 3.2/3.5,
// 2026-11-12): Sam sets lore up on a fresh laptop (solo setup, a personal
// store on GitHub, a linked repo with notes in both) and asks lore what it
// keeps where. `lore where` (and `lore files`) must list every path lore
// wrote: every file under ~/.lore, the store clone, and in the repo the
// .lore/ files, lore's blocks in tracked files, .git/config keys, hook
// blocks, .git/info/exclude lines and .git/lore files. Every path it lists
// exists, nothing in the home is unknown, and ~/.lore/README.md explains
// the home.

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// whereEntry is one entry of `lore where --json`.
type whereEntry struct {
	Area      string `json:"area"`
	Path      string `json:"path"`
	Key       string `json:"key"`
	Kind      string `json:"kind"`
	What      string `json:"what"`
	Exists    bool   `json:"exists"`
	Collapsed bool   `json:"collapsed"`
}

// whereOut is the `lore where --json` answer.
type whereOut struct {
	Home    string       `json:"home"`
	Repo    string       `json:"repo"`
	Readme  string       `json:"readme"`
	Entries []whereEntry `json:"entries"`
	Unknown []string     `json:"unknown"`
}

// soloWhere runs `lore <verb> --json` in dir and decodes it.
func soloWhere(t testing.TB, m *Machine, dir, verb string) whereOut {
	t.Helper()
	r := m.Lore(dir, verb, "--json").OK(t)
	var env struct {
		Data whereOut `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil || env.Data.Home == "" {
		var plain whereOut
		if perr := json.Unmarshal([]byte(r.Stdout), &plain); perr != nil || plain.Home == "" {
			t.Fatalf("lore %s --json does not decode: %v\n%s", verb, err, r.Stdout)
		}
		return plain
	}
	return env.Data
}

// covered reports whether p is listed: an entry for p itself, or a
// collapsed folder entry above it.
func (o whereOut) covered(p string) bool {
	for _, e := range o.Entries {
		if e.Kind == "git-config" || e.Kind == "git-ref" {
			continue
		}
		if e.Path == p || (e.Collapsed && strings.HasPrefix(p, e.Path+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

// hasKey reports whether a git config entry for key in cfg is listed.
func (o whereOut) hasKey(cfg, key string) bool {
	for _, e := range o.Entries {
		if e.Kind == "git-config" && e.Path == cfg && e.Key == key {
			return true
		}
	}
	return false
}

// soloFileHas reports whether the file at p contains s.
func soloFileHas(p, s string) bool {
	b, err := os.ReadFile(p)
	return err == nil && strings.Contains(string(b), s)
}

func TestJourneySoloWhere(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	app := w.Remote("github.com/sam-dev/app")
	w.Remote(soloStore)
	var appRepo *teamharness.Clone

	w.Step("day1/setup-and-notes", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		soloSetupStart(t, lap, soloName, soloEmail)
		soloSetupRest(t, lap, "create")
		lap.Lore("", "skill", "install", "--tool", "claude_code").OK(t)
		appRepo = lap.Clone(app, "app")
		lap.LoreIn(appRepo, "init").OK(t)
		// the write names the row file it wrote
		lap.LoreIn(appRepo, "memory", "add", "--body", "app: timestamps are UTC").OK(t).Says(t, "lore: wrote "+filepath.Join(appRepo.Dir, ".lore", "data", "memories"))
		// a personal note's push names the store, the commit and the branch
		lap.LoreIn(appRepo, "memory", "add", "--body", "I like short commit subjects", "--to", "me").OK(t).Says(t, "[me]", "lore: pushed store me: commit ", " to branch main")
		lap.LoreIn(appRepo, "task", "add", "write the release notes").OK(t)
		lap.LoreIn(appRepo, "render").OK(t)
	})

	w.Step("day1/where-lists-everything", func() {
		home := filepath.Join(lap.Home, ".lore")
		// the home's map exists and says what lore where does
		if !soloFileHas(filepath.Join(home, "README.md"), "`lore where`") {
			t.Errorf("~/.lore/README.md is missing or does not point at lore where")
		}
		in := soloWhere(t, lap, appRepo.Dir, "where")
		files := soloWhere(t, lap, appRepo.Dir, "files")
		if len(files.Entries) != len(in.Entries) {
			t.Errorf("lore files lists %d entries, lore where %d", len(files.Entries), len(in.Entries))
		}
		if in.Home != home || in.Repo != appRepo.Dir || in.Readme != filepath.Join(home, "README.md") {
			t.Errorf("where names home %q repo %q readme %q", in.Home, in.Repo, in.Readme)
		}
		if len(in.Unknown) > 0 {
			t.Errorf("lore where finds paths in ~/.lore it cannot name: %v", in.Unknown)
		}
		// every path it lists exists, and every entry says what it is
		for _, e := range in.Entries {
			if _, err := os.Lstat(e.Path); err != nil || !e.Exists {
				t.Errorf("lore where lists %s (%s), which does not exist: %v", e.Path, e.Kind, err)
			}
			if strings.TrimSpace(e.What) == "" {
				t.Errorf("lore where lists %s with no description", e.Path)
			}
		}
		// nothing lore wrote under ~/.lore is missing
		_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !in.covered(p) {
				t.Errorf("lore where does not list %s", p)
			}
			return nil
		})
		// the personal store's clone is listed with its own git folder
		storeListed := false
		for _, e := range in.Entries {
			if e.Area == "store" && e.Collapsed && strings.Contains(e.What, "lore-me") {
				storeListed = true
				if !in.covered(filepath.Join(e.Path, ".git", "lore", "lock")) && !in.covered(filepath.Join(e.Path, ".git", "lore")) {
					t.Errorf("the store clone's .git/lore is not listed")
				}
			}
		}
		if !storeListed {
			t.Errorf("the personal store clone is not listed: %+v", in.Entries)
		}
		// the repo: every file under .lore/, lore's blocks, .git entries
		_ = filepath.WalkDir(filepath.Join(appRepo.Dir, ".lore"), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !in.covered(p) {
				t.Errorf("lore where does not list %s", p)
			}
			return nil
		})
		for _, f := range []struct{ rel, marker string }{
			{".gitignore", "# lore"}, {".gitattributes", "lore-sync"}, {"CLAUDE.md", "lore:"}, {"CLAUDE.local.md", "lore"},
		} {
			p := filepath.Join(appRepo.Dir, f.rel)
			if soloFileHas(p, f.marker) && !in.covered(p) {
				t.Errorf("%s holds lore's block but lore where does not list it", p)
			}
		}
		git := filepath.Join(appRepo.Dir, ".git")
		cfg := filepath.Join(git, "config")
		out, err := exec.Command("git", "-C", appRepo.Dir, "config", "--local", "--name-only", "--get-regexp", `^(merge\.lore|lore\.)`).Output()
		if err != nil {
			t.Errorf("git config: %v", err)
		}
		keys := strings.Fields(string(out))
		if len(keys) == 0 {
			t.Errorf("the repo has no merge.lore config after a write; the wiring did not run")
		}
		for _, k := range keys {
			if !in.hasKey(cfg, k) {
				t.Errorf("lore where does not list .git/config %s", k)
			}
		}
		for _, dir := range []string{filepath.Join(git, "hooks"), filepath.Join(git, "info")} {
			ents, _ := os.ReadDir(dir)
			for _, e := range ents {
				p := filepath.Join(dir, e.Name())
				if !e.IsDir() && (soloFileHas(p, "lore-sync") || soloFileHas(p, "# lore")) && !in.covered(p) {
					t.Errorf("%s holds lore's lines but lore where does not list it", p)
				}
			}
		}
		_ = filepath.WalkDir(filepath.Join(git, "lore"), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !in.covered(p) {
				t.Errorf("lore where does not list %s", p)
			}
			return nil
		})
		// the human form groups by area and names the README
		lap.LoreIn(appRepo, "where").OK(t).Says(t, "lore's home: "+home, "store clones:", "this repo: "+appRepo.Dir, "README.md")
	})
}
