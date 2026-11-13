//go:build journeys && !windows

package journeys

// nolore_untouched_test.go — a repo with no lore data is never touched
// (FP-7). A store folder links a repo by its remote; a clone of that repo
// that holds no .lore/ sits in the setup's folders. `lore fix --all` used to
// count it as one of its repos and run lore fix in it, which left an empty
// .lore/ behind (an owner found one in ~96 repos that had no notes). Now
// --all lists it as having no lore data, and fix --all, doctor, a read and
// the link's resolution leave its tree byte-identical.
//
// S-2 widens it to the standing rule: EVERY registry operation that is not
// init, setup or link, run in such a linked repo, leaves it byte-identical
// (internal/untouchedwalk; app's TestUntouchedRepoEveryCommand walks a repo
// no link names).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/internal/untouchedwalk"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/wire"
)

const (
	nlSlug   = "github.com/acme-co/quiet"
	nlFolder = "quiet"
	nlNote   = "the quiet service has no lore files of its own"
)

func TestJourneyNoLoreRepoUntouched(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	remote := w.Remote(nlSlug)

	w.Step("day1/a-store-folder-links-a-repo-with-no-lore-files", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", nlFolder).OK(t)
		// the link is made from a clone outside the setup's folders, which
		// is then removed: the clone --all finds was never touched by lore
		hub := jPlainRepo(t, lee, remote, filepath.Join(lee.Home, "elsewhere", "quiet-hub"))
		lee.LoreIn(hub, "org", "link", tlOrg, "--project", nlFolder, "--yes").OK(t)
		lee.LoreIn(hub, "memory", "add", "--body", nlNote).OK(t).Says(t, nlFolder)
		if err := os.RemoveAll(hub.Dir); err != nil {
			t.Fatal(err)
		}
	})

	w.Step("day2/fix-all-doctor-and-reads-leave-the-repo-alone", func() {
		w.AdvanceDays(1)
		quiet := lee.Clone(remote, "quiet")
		lee.MoveClone(t, quiet, filepath.Join(parent, "quiet"))
		before := nlTreeHash(t, quiet.Dir)

		r := lee.Lore(parent, "fix", "--all").OK(t)
		nlSame(t, "lore fix --all", quiet.Dir, before)
		r.Says(t, "skipped "+quiet.Dir+": no lore data").Never(t, "== "+quiet.Dir)

		lee.Lore(parent, "fix", "--all", "--dry-run").OK(t)
		nlSame(t, "lore fix --all --dry-run", quiet.Dir, before)

		lee.LoreIn(quiet, "doctor")
		nlSame(t, "lore doctor", quiet.Dir, before)

		// a read in the repo resolves its link: the store folder's note
		lee.LoreIn(quiet, "memory", "list").OK(t).Says(t, nlNote)
		nlSame(t, "lore memory list", quiet.Dir, before)

		lee.LoreIn(quiet, "whoami")
		nlSame(t, "lore whoami", quiet.Dir, before)

		lee.LoreIn(quiet, "fix", "--dry-run").Fails(t, 19, "no lore data in "+quiet.Dir)
		nlSame(t, "lore fix --dry-run", quiet.Dir, before)

		lee.LoreIn(quiet, "fix").Fails(t, 19, "no lore data in "+quiet.Dir, "lore init")
		nlSame(t, "lore fix", quiet.Dir, before)
	})

	w.Step("day2/a-linked-repo-outside-the-setup-folders", func() {
		// the store links it, but no setup's folders hold it, so no org
		// resolves for it there: lore fix used to set it up as a project
		other := filepath.Join(lee.Home, "other")
		out := lee.Clone(remote, "quiet-out")
		lee.MoveClone(t, out, filepath.Join(other, "quiet"))
		before := nlTreeHash(t, out.Dir)

		r := lee.Lore(parent, "fix", "--all", other).OK(t)
		nlSame(t, "lore fix --all <folder>", out.Dir, before)
		r.Says(t, "skipped "+out.Dir+": no lore data").Never(t, "== "+out.Dir)

		lee.LoreIn(out, "doctor")
		lee.LoreIn(out, "memory", "list")
		lee.LoreIn(out, "fix")
		nlSame(t, "lore doctor, memory list and fix", out.Dir, before)
	})

	w.Step("day3/every-command-leaves-the-linked-repo-alone", func() {
		// S-2: every registry operation but init, setup and link, each in
		// its own copy of the linked clone (in the setup's folders, so the
		// link resolves), nlWalkers at a time on lee's machine
		quiet := filepath.Join(parent, "quiet")
		var mu sync.Mutex
		var failed []string
		sem := make(chan struct{}, nlWalkers)
		var wg sync.WaitGroup
		walked := 0
		for i, e := range nlRegistry(t) {
			path := op.CLIPath(e.Spec().Name)
			if !untouchedwalk.Walked(path, e) {
				continue
			}
			walked++
			args := untouchedwalk.Args(path, e)
			dir := filepath.Join(parent, fmt.Sprintf("quiet-walk-%03d", i))
			if out, err := exec.Command("cp", "-a", quiet, dir).CombinedOutput(); err != nil {
				t.Fatalf("copy the linked clone: %v %s", err, out)
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				before, err := untouchedwalk.Tree(dir)
				if err == nil {
					r := lee.Actor.Lore(dir, args...)
					var after map[string]string
					if after, err = untouchedwalk.Tree(dir); err == nil {
						if diff := untouchedwalk.Diff(before, after); diff != "" {
							err = fmt.Errorf("changed the linked repo:\n%s\nstderr:\n%s", diff, r.Stderr)
						}
					}
				}
				if err != nil {
					mu.Lock()
					failed = append(failed, "lore "+strings.Join(args, " ")+": "+err.Error())
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if walked < nlMinWalked {
			t.Fatalf("the walk ran only %d operations: the registry walk is broken", walked)
		}
		if len(failed) > 0 {
			slices.Sort(failed)
			t.Fatalf("%d of %d commands changed a repo only a store link names:\n%s", len(failed), walked, strings.Join(failed, "\n\n"))
		}
	})
}

// nlWalkers is how many commands the day-3 walk runs at once.
const nlWalkers = 8

// nlMinWalked keeps the day-3 walk from passing by finding nothing.
const nlMinWalked = 300

// nlRegistry is every operation of the lore registry (wire's, as the
// binary builds it).
func nlRegistry(t testing.TB) []op.Entry {
	t.Helper()
	wd, home := t.TempDir(), t.TempDir()
	base, _ := wire.StandaloneOptionsOrg(wd, home, wd, "dev")
	a, err := wire.New(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a.Registry().Entries()
}

// nlSame fails when dir's tree is no longer the one hashed before cmd ran.
func nlSame(t testing.TB, cmd, dir string, before map[string]string) {
	t.Helper()
	after := nlTreeHash(t, dir)
	var diff []string
	for p, h := range after {
		if before[p] != h {
			diff = append(diff, "changed or new: "+p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			diff = append(diff, "gone: "+p)
		}
	}
	if len(diff) > 0 {
		slices.Sort(diff)
		t.Fatalf("%s changed a repo with no lore data (%s):\n%s", cmd, dir, strings.Join(diff, "\n"))
	}
}

// nlGitOwn are the parts of .git that git itself rewrites on a status or
// fetch; the rest of .git (config, hooks, info) is lore's to leave alone.
var nlGitOwn = map[string]bool{".git/objects": true, ".git/refs": true, ".git/logs": true}

// nlTreeHash is every folder and file under dir with its mode and content
// hash; in .git only the folders and .git/config (see nlGitOwn).
func nlTreeHash(t testing.TB, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() && nlGitOwn[filepath.ToSlash(rel)] {
			return filepath.SkipDir
		}
		if filepath.Dir(rel) == ".git" && !d.IsDir() && rel != filepath.Join(".git", "config") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		h := fi.Mode().String()
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			h += " " + hex.EncodeToString(sum[:])
		}
		out[rel] = h
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
