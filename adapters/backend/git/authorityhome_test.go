package git

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// gitFolderFiles lists the files in the clone's .git folder that are not
// git's own bookkeeping of a read (objects, logs, the index, FETCH_HEAD)
// or the store lock lore takes on every open.
func gitFolderFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	gd := filepath.Join(dir, ".git")
	err := filepath.WalkDir(gd, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(gd, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() || strings.HasPrefix(rel, "objects/") || strings.HasPrefix(rel, "logs/") || rel == "index" || rel == "FETCH_HEAD" || strings.HasPrefix(rel, "lore/") {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// TestOwnCloneReplayKeptInLoreHome (devco:D-812): a read through a
// person's own clone (`lore store use <path>`) writes neither the roster
// replay nor the held-rows memo into that clone's git folder: both live
// in the lore home's cache, and a new process still reads the replay
// back (no commit replayed). A Backend made without an open (AdminAt)
// writes nothing into the clone either. Before, every read left
// .git/lore-authority.gob (and lore-held-seen-*) in the person's clone.
func TestOwnCloneReplayKeptInLoreHome(t *testing.T) {
	o := newOrgTeam(t)
	spoof, roster := mustID[ids.Memory](t), mustID[ids.ActorPeriod](t)
	o.h.commit(o.max.email, map[string]string{orgPath("memories", spoof): stampedRow(spoof, o.boss.actor, "2026-01-02T00:00:01.000Z", "says the admin")})
	o.h.commit(o.max.email, map[string]string{orgPath("actor_periods", roster): o.max.periodRowAt("2026-01-03T00:00:00.000Z", "admin", "")})
	home := filepath.Join(t.TempDir(), "home")
	mine := filepath.Join(t.TempDir(), "code-notes")
	gitT(t, filepath.Dir(mine), "clone", "--quiet", o.h.remote, mine)
	before := gitFolderFiles(t, mine)

	b, err := OpenClone(context.Background(), o.h.remote, mine, rowfs.FolderOrg, store.OpenOptions{Home: home}, WithMerger(newestMerger{}), withOwnClone())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	first := authorityNow(t, b)
	if heldReason(t, b, roster) == "" {
		t.Fatal("the member's own admin stint is not held: the fixture lost its point")
	}
	_, cur, err := b.Read(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = b.Read(context.Background(), cur); err != nil { // an incremental read: re-serves the holds once (heldRecheck)
		t.Fatal(err)
	}
	h, err := b.head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	AdminAt(context.Background(), mine, h, o.boss.email)
	if after := gitFolderFiles(t, mine); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("a read wrote into the person's own clone:\nbefore %v\nafter  %v", before, after)
	}

	p := b.authorityPath()
	if rel, err := filepath.Rel(filepath.Join(home, homeCacheDir, AuthorityCacheDir), p); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("the replay of a person's own clone is kept at %q, want under the lore home's cache/%s", p, AuthorityCacheDir)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("the replay was not kept in the lore home: %v", err)
	}
	if memo, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*"+heldSeenFile+"*")); len(memo) == 0 {
		t.Error("the held-rows memo of a person's own clone was not kept in the lore home")
	}
	forgetAuthority(b)
	n := replayCount()
	sameAuthority(t, authorityNow(t, b), first)
	if got := replayCount() - n; got != 0 {
		t.Errorf("a new process at the same HEAD replayed %d commit(s), want 0 (the replay in the lore home)", got)
	}

	// without a known lore home nothing is kept on disk at all
	if got := authorityFileFor(mine, "", true); got != "" {
		t.Errorf("a person's own clone with no lore home keeps its replay at %q, want nowhere", got)
	}
}
