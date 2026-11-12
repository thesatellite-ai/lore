package wire

// homereadme.go — ~/.lore/README.md (plain files sweep 3.5, 2026-11-12):
// lore's home explained in plain words, written by lore. It is written
// when it is missing or was written for another layout version, by every
// command that opens the machine store (practically every command, and
// the first one creates the home) and by every config write; its content
// comes from homeLayout, the same table `lore where` walks the folder
// against, so the two never disagree.
//
//	writeHomeReadme(home) ─▶ README.md's first line names homeLayoutVersion?
//	   ─▶ yes: nothing written   ─▶ no: written (temp file, then rename)

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/osrename"
)

// HomeReadmeFile is the map's name in the lore home.
const HomeReadmeFile = "README.md"

// homeLayoutVersion changes whenever homeLayout changes what it says, so
// every home's README is rewritten once by the next lore that runs.
const homeLayoutVersion = "2026-11-12.4"

// homeReadmeStamp starts the README's first line; the version follows.
const homeReadmeStamp = "<!-- written by lore, layout "

// homeReadmeMode: the README describes this machine only.
const homeReadmeMode = 0o600

// homeReadme is the README's text.
func homeReadme() string {
	var b strings.Builder
	b.WriteString(homeReadmeStamp + homeLayoutVersion + "; lore rewrites this file when its layout changes -->\n")
	b.WriteString("# lore's home on this machine\n\n")
	b.WriteString("This folder is where lore keeps what belongs to this machine: its settings, the stores it cloned, caches and small state files. Your notes themselves live in stores (git repositories or folders) and in your code repos' .lore/data folders; nothing here is synced by itself.\n\n")
	b.WriteString("`lore where` lists every file lore keeps on this machine and in the repo you run it in, one line each, with the store and repo each cache file belongs to (`lore where --json` for scripts). It also names anything in this folder lore does not recognise; lore never deletes such a file.\n\n")
	b.WriteString("Store cache files are named by a store id, not by a store name: two repos that call the same store by different names share one cache. A code repo's own cache is named by its folder name and a short hash of its path (cache/repo/<name>-<hash>.db), so two checkouts get two files and a moved or renamed repo folder gets a fresh one. `lore where` shows which store and repo each one serves.\n\n")
	b.WriteString("| path | what it is | if you edit or delete it |\n|---|---|---|\n")
	for _, e := range homeLayout {
		if !e.Readme {
			continue
		}
		p := e.Pattern
		if e.Dir {
			p += "/"
		}
		b.WriteString("| `" + p + "` | " + e.What + " | " + e.ByHand + " |\n")
	}
	b.WriteString("\nIn a store clone (stores/<owner>/<repo>/, or stores/_local/<repo>/ for a git repository on this machine), the store's own rows (actors, machines, …) sit in _store/<table>/<id>.json, every other folder is a project or the org folder holding its rows as <folder>/<table>/<id>.json, and README.md says how to browse and edit them. Lore's own files sit in .git/lore/ (the store lock, its journal, when the store was last fetched and pushed): never in the files you browse, never committed. A store that is a plain folder keeps the same in .lore-state/ at its top.\n\n")
	b.WriteString("In a code repo, lore keeps .lore/ (data/ holds the rows, committed; lock and state/ are local and listed in .gitignore; the repo's cache is under cache/repo/ here, never in the repo), a marked block in .gitignore and .gitattributes, LORE.md and a pointer block in the files your coding agents read (CLAUDE.md, AGENTS.md, ...), merge driver entries in .git/config and marked blocks in .git/hooks. Lore says so the first time it writes any of them; `lore where` lists them.\n")
	return b.String()
}

// writeHomeReadme writes home's README when it is missing or names
// another layout version. Best effort: a home lore cannot write to fails
// in the command's own writes, with their own error.
func writeHomeReadme(home string) {
	if home == "" {
		return
	}
	path := filepath.Join(home, HomeReadmeFile)
	if cur, err := os.ReadFile(filepath.Clean(path)); err == nil && strings.HasPrefix(string(cur), homeReadmeStamp+homeLayoutVersion+";") {
		return
	}
	if os.MkdirAll(home, cacheDirMode) != nil {
		return
	}
	tmp, err := os.CreateTemp(home, ".README.md.tmp-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(homeReadme())
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(name, homeReadmeMode) != nil || osrename.Rename(name, path) != nil {
		_ = os.Remove(name) // best effort: the next command tries again
	}
}
