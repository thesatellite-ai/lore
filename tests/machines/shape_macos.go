//go:build machines && !windows

package machines

// shape_macos.go — a Mac user's machine (owner, 2026-11-10: doctor said
// ".lore/data is gitignored" because Finder's .DS_Store under .lore/data is
// ignored by his global core.excludesFile). The shape is a fresh machine
// plus what macOS leaves behind:
//
//	~/.gitconfig       core.excludesFile = ~/.gitignore_global
//	~/.gitignore_global .DS_Store and ._* (what GitHub's macOS template has)
//	clutter            .DS_Store and ._foo in the home and, in each lore
//	                   repo (MacApp, has-lore, work/app), in its root, .lore,
//	                   .lore/data and every table folder (plus an
//	                   AppleDouble "._<row>.json" next to a real row file)
//	case               the repo is ~/code/MacApp; ~/code/macapp names the
//	                   same folder. On a case-insensitive file system (APFS,
//	                   a Mac running this suite) that is the folder itself;
//	                   on Linux (ext4 is case-sensitive, and making a
//	                   casefold file system needs root) it is a symlink, so
//	                   lore sees a path whose case differs from how the repo
//	                   and the setup folder were registered, resolving to the
//	                   same folder. Machine.MacCaseFold says which one ran.
//
// Every name is a placeholder; the repo is under ~/code so CheckModes walks it.

import (
	"os"
	"path/filepath"
	"strings"
)

// ShapeEnvMacOS is the macOS-realistic shape.
const ShapeEnvMacOS = "env_macos"

// Folders and files of ShapeEnvMacOS.
const (
	// MacRepo is the lore repo as registered (Machine.Repos key, under Home).
	MacRepo = "code/MacApp"
	// MacRepoOtherCase is the same folder spelled in another case (a
	// Machine.Repos key too, so commands run there as well).
	MacRepoOtherCase = "code/macapp"
	// macExcludes is the global excludes file core.excludesFile names.
	macExcludes = ".gitignore_global"
	// macExcludesBody is what it ignores.
	macExcludesBody = ".DS_Store\n._*\n"
	// macJunkBody is what a clutter file holds (Finder's are binary; the
	// content never matters to git's ignore rules).
	macJunkBody = "Bud1 placeholder\n"
)

// macJunk are the clutter files the shape plants: relative to Home ("~/")
// or to the repo.
var (
	macHomeJunk = []string{".DS_Store", "._foo", "code/.DS_Store"}
	macRepoJunk = []string{".DS_Store", "._foo", ".lore/.DS_Store", ".lore/data/.DS_Store", ".lore/data/._foo"}
)

// macTableJunkDSStore is planted in each table folder of .lore/data, with
// an AppleDouble twin ("._<row>.json") of the first row file there.
const macTableJunkDSStore = ".DS_Store"

func init() {
	RegisterShape(ExtraShape{Name: ShapeEnvMacOS, Build: func(b *builder) {
		b.ssh(false)
		b.gh([]string{LoginAlpha}, LoginAlpha)
		b.macGlobalExcludes()
		b.envRepo(MacRepo)
		b.macOtherCase()
		b.macClutter()
	}})
}

// macGlobalExcludes appends core.excludesFile to ~/.gitconfig and writes
// the excludes file.
func (b *builder) macGlobalExcludes() {
	b.t.Helper()
	gc := filepath.Join(b.m.Home, ".gitconfig")
	body, err := os.ReadFile(gc)
	if err != nil {
		b.t.Fatal(err)
	}
	b.file(".gitconfig", string(body)+"[core]\n\texcludesFile = ~/"+macExcludes+"\n", 0o644)
	b.file(macExcludes, macExcludesBody, 0o644)
}

// macOtherCase makes MacRepoOtherCase name MacRepo's folder.
func (b *builder) macOtherCase() {
	b.t.Helper()
	real := b.m.Repos[MacRepo]
	other := filepath.Join(b.m.Home, MacRepoOtherCase)
	if !caseFolds(filepath.Dir(real)) {
		if err := os.Symlink(filepath.Base(real), other); err != nil {
			b.t.Fatal(err)
		}
	} else {
		b.m.MacCaseFold = true
	}
	b.m.Repos[MacRepoOtherCase] = other
}

// caseFolds reports whether dir's file system treats names case-insensitively.
func caseFolds(dir string) bool {
	probe, err := os.CreateTemp(dir, "casefold-probe-")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	defer func() { _ = os.Remove(name) }()
	upper := filepath.Join(dir, strings.ToUpper(filepath.Base(name)))
	_, err = os.Stat(upper)
	return err == nil && upper != name
}

// macClutter plants Finder's files in the home and in every lore repo.
func (b *builder) macClutter() {
	b.t.Helper()
	for _, rel := range macHomeJunk {
		b.file(rel, macJunkBody, 0o644)
	}
	for _, key := range macJunkRepos {
		b.macRepoClutter(b.m.Repos[key])
	}
}

// macJunkRepos are the lore repos the clutter lands in: the shape's own and
// the two every machine has (the sweep's commands run in has-lore).
var macJunkRepos = []string{MacRepo, RepoLore, RepoWorkApp}

// macRepoClutter plants the clutter in one lore repo: its root, .lore,
// .lore/data and every table folder.
func (b *builder) macRepoClutter(repo string) {
	b.t.Helper()
	for _, rel := range macRepoJunk {
		b.file(filepath.Join(repo, rel), macJunkBody, 0o644)
	}
	data := filepath.Join(repo, ".lore", "data")
	tables, err := os.ReadDir(data)
	if err != nil {
		b.t.Fatal(err)
	}
	for _, tb := range tables {
		if !tb.IsDir() {
			continue
		}
		dir := filepath.Join(data, tb.Name())
		b.file(filepath.Join(dir, macTableJunkDSStore), macJunkBody, 0o644)
		rows, _ := os.ReadDir(dir)
		for _, r := range rows {
			if strings.HasSuffix(r.Name(), ".json") {
				b.file(filepath.Join(dir, "._"+r.Name()), macJunkBody, 0o644)
				break
			}
		}
	}
}
