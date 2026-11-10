//go:build machines && !windows

package machines

// shape_env.go — machines whose ENVIRONMENT is odd, not their lore files:
// a home path with non-ASCII letters and spaces, spaces in every path lore
// touches, no ~/.lore at all (or one that is a file, or read-only), two lore
// binaries on PATH, and an unset or missing TMPDIR with XDG_CONFIG_HOME
// pointing elsewhere. Each is a fresh machine (git, one key, one gh login)
// plus the one odd thing; env_test.go runs a user's commands on each.

import (
	"os"
	"path/filepath"
)

// Env shape names.
const (
	// ShapeEnvNonASCIIHome: the home folder is "Zoë Ünicode" and the repo
	// sits in "~/My Projects/app one".
	ShapeEnvNonASCIIHome = "env_nonascii_home"
	// ShapeEnvSpaces: spaces in the home, the repo folders and the store.
	ShapeEnvSpaces = "env_spaces"
	// ShapeEnvNoLoreHome: git, gh and ssh set up, no ~/.lore at all.
	ShapeEnvNoLoreHome = "env_no_lore_home"
	// ShapeEnvLoreHomeFile: ~/.lore is a regular file, not a folder.
	ShapeEnvLoreHomeFile = "env_lore_home_file"
	// ShapeEnvLoreHomeReadOnly: ~/.lore is a folder of mode 0500.
	ShapeEnvLoreHomeReadOnly = "env_lore_home_ro"
	// ShapeEnvTwoLores: an old lore (a script answering 0.1.17) comes first
	// on PATH, the real binary second.
	ShapeEnvTwoLores = "env_two_lores"
	// ShapeEnvTmpdirUnset: TMPDIR is empty and XDG_CONFIG_HOME points at a
	// folder outside home.
	ShapeEnvTmpdirUnset = "env_tmpdir_unset"
	// ShapeEnvTmpdirMissing: TMPDIR names a folder that does not exist,
	// XDG_CONFIG_HOME as above.
	ShapeEnvTmpdirMissing = "env_tmpdir_missing"
)

// Folder and file names the env shapes use.
const (
	// EnvRepo is the spaced repo folder under home (a Repos key too).
	EnvRepo = "My Projects/app one"
	// EnvSpacedRepo is the second spaced repo of env_spaces.
	EnvSpacedRepo = "my repos/app two"
	// EnvStoreDir is the spaced folder holding env_spaces' stores: an empty
	// folder EnvFolderStore (a folder store lore creates there) and a bare
	// git repository EnvBareStore (a file:// url to it is refused).
	EnvStoreDir    = "store folder"
	EnvFolderStore = "me"
	EnvBareStore   = "me.git"
	// OldLoreVersion is what the shadowing fake lore prints.
	OldLoreVersion = "0.1.17"
	// oldLoreDir and realLoreDir are the PATH folders of env_two_lores,
	// under the machine dir; xdgDir is the XDG_CONFIG_HOME of the tmpdir
	// shapes; missingTmp the TMPDIR that is never made.
	oldLoreDir  = "old lore bin"
	realLoreDir = "realbin"
	xdgDir      = "xdg config"
	missingTmp  = "no-such-tmp"
)

// loreHomeReadOnly is the mode of env_lore_home_ro's ~/.lore.
const loreHomeReadOnly os.FileMode = 0o500

// oldLoreScript answers like lore 0.1.17 and records every call, so a test
// sees whether a hook or a directive ran the old binary. Like the release it
// stands for, it knows its own commands only: anything 0.1.18 added
// (migrate, sync hook, land, check leaks, ...) fails as cobra fails an
// unknown command, --help included, so the hooks' probes see an old lore.
const oldLoreScript = `#!/bin/sh
echo "$0 $*" >> "$FAKE_W/old-lore-calls"
case "$1" in
version|--version) echo "lore ` + OldLoreVersion + `" ;;
init|render|memory|rule|doctor|help) echo "lore ` + OldLoreVersion + `: $*" ;;
*) echo "Error: unknown command \"$1\" for \"lore\"" >&2; exit 1 ;;
esac
exit 0
`

func init() {
	fresh := func(b *builder) {
		b.ssh(false)
		b.gh([]string{LoginAlpha}, LoginAlpha)
	}
	RegisterShape(ExtraShape{Name: ShapeEnvNonASCIIHome, HomeName: "Zoë Ünicode", Build: func(b *builder) {
		fresh(b)
		b.envRepo(EnvRepo)
	}})
	RegisterShape(ExtraShape{Name: ShapeEnvSpaces, HomeName: "home with spaces", Build: func(b *builder) {
		fresh(b)
		b.envRepo(EnvRepo)
		b.envRepo(EnvSpacedRepo)
		b.dir(filepath.Join(b.m.Home, EnvStoreDir, EnvFolderStore), 0o755)
		b.run(b.m.Home, "/usr/bin/git", "init", "-q", "--bare", filepath.Join(b.m.Home, EnvStoreDir, EnvBareStore))
	}})
	RegisterShape(ExtraShape{Name: ShapeEnvNoLoreHome, Build: func(b *builder) {
		fresh(b)
		b.envRepo(EnvRepo)
	}})
	RegisterShape(ExtraShape{Name: ShapeEnvLoreHomeFile, Build: func(b *builder) {
		fresh(b)
		b.envRepo(EnvRepo)
		b.file(".lore", "not a folder\n", 0o644)
	}})
	RegisterShape(ExtraShape{Name: ShapeEnvLoreHomeReadOnly, Build: func(b *builder) {
		fresh(b)
		b.envRepo(EnvRepo)
		b.dir(filepath.Join(b.m.Home, ".lore"), loreHomeReadOnly)
		// The test's cleanup removes the machine dir; give it back write.
		lore := filepath.Join(b.m.Home, ".lore")
		b.t.Cleanup(func() { _ = os.Chmod(lore, 0o700) })
	}})
	RegisterShape(ExtraShape{Name: ShapeEnvTwoLores, Build: func(b *builder) {
		fresh(b)
		b.envRepo(EnvRepo)
		old := filepath.Join(b.m.Dir, oldLoreDir)
		b.dir(old, 0o755)
		b.file(filepath.Join(old, "lore"), oldLoreScript, 0o755)
		real := filepath.Join(b.m.Dir, realLoreDir)
		b.dir(real, 0o755)
		if err := os.Symlink(b.m.bin, filepath.Join(real, "lore")); err != nil {
			b.t.Fatal(err)
		}
		b.m.PathPrefix = []string{old, real}
	}})
	tmpdir := func(tmp string) func(b *builder) {
		return func(b *builder) {
			fresh(b)
			b.envRepo(EnvRepo)
			xdg := filepath.Join(b.m.Dir, xdgDir)
			b.dir(xdg, 0o755)
			b.m.ExtraEnv = append(b.m.ExtraEnv, "TMPDIR="+tmp, "XDG_CONFIG_HOME="+xdg)
		}
	}
	RegisterShape(ExtraShape{Name: ShapeEnvTmpdirUnset, Build: func(b *builder) { tmpdir("")(b) }})
	RegisterShape(ExtraShape{Name: ShapeEnvTmpdirMissing, Build: func(b *builder) {
		tmpdir(filepath.Join(b.m.Dir, missingTmp))(b)
	}})
}

// envRepo makes ~/<rel> a git repo where lore init ran, and records it in
// Repos (so CheckModes walks it). It runs before the shape decides ~/.lore:
// the ~/.lore init made is removed again.
func (b *builder) envRepo(rel string) {
	b.t.Helper()
	p := filepath.Join(b.m.Home, rel)
	b.dir(p, 0o755)
	b.run(p, "/usr/bin/git", "init", "-q")
	b.lore(p, "init", "--name", envRepoName(rel))
	if err := os.RemoveAll(filepath.Join(b.m.Home, ".lore")); err != nil {
		b.t.Fatal(err)
	}
	b.m.Repos[rel] = p
}

// envRepoName is the project name lore init gets for a spaced folder: its
// base name with spaces as dashes ("app one" → "app-one").
func envRepoName(rel string) string {
	out := []rune(filepath.Base(rel))
	for i, r := range out {
		if r == ' ' {
			out[i] = '-'
		}
	}
	return string(out)
}
