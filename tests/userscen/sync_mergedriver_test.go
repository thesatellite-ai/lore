//go:build userscen

package userscen

// The per-clone merge driver config (`merge.lore.driver`,
// `merge.lore-keep-ours.driver`) after a lore command, for every
// combination of
//
//   - what the clone already holds: nothing, the bare `lore merge-driver …`,
//     an absolute path to a lore that exists, an absolute path to a lore
//     that was deleted, and a value that is not a lore driver at all;
//   - the command: an ordinary one (`lore check leaks`), `lore init` (in a
//     fresh clone whose config already holds the value) and the repair
//     step `lore doctor --fix`;
//   - the binary running it: the `lore` on PATH, a scratch copy not on PATH
//     (with another lore on PATH), and a binary with no lore on PATH.
//
// The promise (wire/host/gitwire.go driverConfigAction): a missing key is
// written; a lore driver that runs is never replaced by anything; a lore
// driver whose absolute program is gone is repaired by any command; a value
// that is not a lore driver is replaced only by init and doctor --fix.
// What is written is bare `lore` when the running binary is the `lore` on
// PATH, else its absolute path. Regression for a scratch build run once in
// a repo repointing every later merge at a file that was then deleted.

import (
	"os"
	"path/filepath"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Driver config keys and their command lines after the program.
const (
	mdKeyLore     = "merge.lore.driver"
	mdKeyKeepOurs = "merge.lore-keep-ours.driver"
	mdArgsLore    = " merge-driver %O %A %B %P"
	mdArgsKeep    = " merge-driver --keep-ours %O %A %B %P"
	mdSlug        = "github.com/octo-personal/driver"
	mdForeign     = "cat %A"
)

// mdExisting is what the clone's config holds before the command.
type mdExisting int

const (
	mdNone mdExisting = iota
	mdBare
	mdAbsPresent
	mdAbsMissing
	mdNotLore
)

func (e mdExisting) String() string {
	return [...]string{"none", "bare", "abs-present", "abs-missing", "not-lore"}[e]
}

// mdKind is the command run.
type mdKind int

const (
	mdCheck mdKind = iota
	mdInit
	mdRepair
)

func (k mdKind) String() string { return [...]string{"check", "init", "repair"}[k] }

// mdRunner is which binary runs the command and what PATH holds.
type mdRunner int

const (
	mdOnPath  mdRunner = iota // the lore on PATH runs
	mdScratch                 // a copy outside PATH runs; the on-PATH lore is another file
	mdNoPath                  // no lore on PATH at all
)

func (r mdRunner) String() string { return [...]string{"on-path", "scratch", "no-path"}[r] }

func TestMergeDriverConfigMatrix(t *testing.T) {
	for _, runner := range []mdRunner{mdOnPath, mdScratch, mdNoPath} {
		for _, kind := range []mdKind{mdCheck, mdInit, mdRepair} {
			t.Run(runner.String()+"/"+kind.String(), func(t *testing.T) {
				mergeDriverCase(t, runner, kind)
			})
		}
	}
}

// mergeDriverCase runs every existing value for one runner and command.
func mergeDriverCase(t *testing.T, runner mdRunner, kind mdKind) {
	w := newWorld(t)
	a := w.Human("person", "person@example.com")
	withShim := narrowPath(t, filepath.Join(w.Root, "shim"))
	noShim := narrowPath(t)
	a.SetEnv("PATH", withShim)

	// Another lore that exists (abs-present) and one that is gone.
	present := filepath.Join(w.Root, "other", "lore")
	for _, d := range []string{"other", "scratch"} {
		if err := os.MkdirAll(filepath.Join(w.Root, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	th.WriteExecutable(t, present, "#!/bin/sh\nexit 0\n")
	missing := filepath.Join(w.Root, "gone", "lore")
	quoted := func(p string) string {
		if r, rerr := filepath.EvalSymlinks(p); rerr == nil {
			p = r
		}
		return "'" + filepath.ToSlash(p) + "'"
	}
	programs := map[mdExisting]string{mdBare: "lore", mdAbsPresent: quoted(present), mdAbsMissing: "'" + filepath.ToSlash(missing) + "'"}

	// The program the running binary writes.
	origBin := w.Bin
	runBin := w.Bin
	switch runner {
	case mdScratch:
		// with the binary's own extension: Windows runs only lore-scratch.exe
		scratch := filepath.Join(w.Root, "scratch", "lore-scratch"+filepath.Ext(w.Bin))
		body, rerr := os.ReadFile(filepath.Clean(w.Bin))
		if rerr != nil {
			t.Fatal(rerr)
		}
		th.WriteExecutable(t, scratch, string(body))
		runBin = scratch
	case mdNoPath, mdOnPath:
	}
	ours := quoted(runBin)
	if runner == mdOnPath {
		ours = "lore"
	}

	for _, ex := range []mdExisting{mdNone, mdBare, mdAbsPresent, mdAbsMissing, mdNotLore} {
		c := a.Clone(w.Remote(mdSlug), "c-"+ex.String())
		if kind != mdInit {
			c.MustLore("init", "--non-interactive", "--name=site-"+ex.String())
		}
		set := func(key, args string) {
			switch ex {
			case mdNone:
				_ = c.TryGit("config", "--local", "--unset", key) // absent before init is fine
			case mdNotLore:
				c.Git("config", "--local", key, mdForeign)
			case mdBare, mdAbsPresent, mdAbsMissing:
				c.Git("config", "--local", key, programs[ex]+args)
			}
		}
		set(mdKeyLore, mdArgsLore)
		set(mdKeyKeepOurs, mdArgsKeep)

		// Switch to the runner's binary and PATH for the command under test.
		w.Bin = runBin
		if runner == mdNoPath {
			a.SetEnv("PATH", noShim)
		}
		switch kind {
		case mdCheck:
			c.MustLore("check", "leaks")
		case mdInit:
			c.MustLore("init", "--non-interactive", "--name=site-"+ex.String())
		case mdRepair:
			c.MustLore("doctor", "--fix")
		}

		// Expected program: kept, or ours.
		want := ours
		switch ex {
		case mdBare, mdAbsPresent:
			want = programs[ex] // a lore driver that runs is never replaced
		case mdNotLore:
			if kind == mdCheck {
				want = "" // an ordinary command leaves a foreign value
			}
		case mdNone, mdAbsMissing:
		}
		for _, kv := range [][2]string{{mdKeyLore, mdArgsLore}, {mdKeyKeepOurs, mdArgsKeep}} {
			wantVal := want + kv[1]
			if want == "" {
				wantVal = mdForeign
			}
			if got := c.Git("config", "--local", "--get", kv[0]); got != wantVal {
				t.Errorf("existing %s, %s by %s: %s = %q, want %q", ex, kind, runner, kv[0], got, wantVal)
			}
		}
		w.Bin = origBin
		a.SetEnv("PATH", withShim)
	}
}
