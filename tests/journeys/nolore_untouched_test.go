//go:build journeys && !windows

package journeys

// nolore_untouched_test.go — a repo with no lore data is never touched
// (FP-7). A store folder links a repo by its remote; a clone of that repo
// that holds no .lore/ sits in the setup's folders. `lore fix --all` used to
// count it as one of its repos and run lore fix in it, which left an empty
// .lore/ behind (an owner found one in ~96 repos that had no notes). Now
// --all lists it as having no lore data, and fix --all, doctor, a read and
// the link's resolution leave its tree byte-identical.

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
