//go:build userscen

package userscen

// campaign_sync_helpers_test.go — shared helpers of the Phase B sync
// campaign (store, sync, live, land, trash, migrate, move, row, commit and
// cache commands driven the way a person runs them). Every helper name
// starts with camSync so it never collides with another area's helpers.
// The scenarios reuse the existing harnesses: newSyncTeam (two clones of
// one fake-GitHub remote), newTrashWorld (a repo plus the `me` and `kb`
// stores), migNew/migPlant (v0.1.17 row files) and restStart (`lore api`).

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// camSyncCommands are the 46 command paths of the sync area
// (the sync area of the command inventory).
var camSyncCommands = [][]string{
	{"cache", "prune"},
	{"commit", "show"},
	{"land", "run"},
	{"live", "compact"}, {"live", "diff"}, {"live", "log"}, {"live", "purge"}, {"live", "restore"}, {"live", "show"},
	{"migrate", "enable"}, {"migrate", "rollback"}, {"migrate", "run"}, {"migrate", "status"}, {"migrate", "unlock"},
	{"move"},
	{"row", "move"}, {"row", "show"},
	{"store", "add"}, {"store", "doctor"}, {"store", "folder", "add"}, {"store", "forget"}, {"store", "init"},
	{"store", "list"}, {"store", "open"}, {"store", "remove"}, {"store", "set-url"}, {"store", "show"}, {"store", "visibility"},
	{"sync", "ci-merge"}, {"sync", "conflicts"}, {"sync", "fix-projects"}, {"sync", "install-action"}, {"sync", "merge-rows"},
	{"sync", "peek"}, {"sync", "promote"}, {"sync", "purge"}, {"sync", "resolve"}, {"sync", "run"}, {"sync", "status"},
	{"sync", "trash", "list"}, {"sync", "trash", "restore"},
	{"trash", "list"}, {"trash", "purge"}, {"trash", "restore"}, {"trash", "show"}, {"trash", "stats"},
}

// camSyncTree maps every file under dir (git's own folder and the SQLite
// caches, camSyncIsCache, left out) to the sha256 of its content, so a dry
// run can be proven to write nothing a person or a teammate reads.
func camSyncTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() || camSyncIsCache(d.Name()) {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		sum := sha256.Sum256(b)
		rel, _ := filepath.Rel(dir, p)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// camSyncSameTree fails naming every file a step added, removed or changed.
func camSyncSameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	var diff []string
	for p, h := range before {
		if after[p] != h {
			diff = append(diff, p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			diff = append(diff, p+" (new)")
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Fatalf("%s changed files: %v", what, diff)
	}
}

// camSyncIsCache reports a file lore keeps for itself and refreshes on
// every read: the SQLite caches (lore-cache.db, the machine and store
// caches, their -wal/-shm/-journal companions) and a store clone's fetch
// stamps and lock file. None is a row a person or a teammate reads, so a
// dry run may touch them.
func camSyncIsCache(name string) bool {
	switch name {
	case "lock", "fetch-stamp", "lore-fetched-at":
		return true
	}
	for _, suf := range []string{".db", ".db-wal", ".db-shm", ".db-journal"} {
		if strings.HasSuffix(name, suf) {
			return true
		}
	}
	return false
}

// camSyncHint is the hint line of a human-mode refusal ("" when none).
func camSyncHint(r th.Result) string {
	for _, l := range strings.Split(r.Out(), "\n") {
		if s := strings.TrimSpace(l); strings.HasPrefix(s, "hint:") {
			return strings.TrimSpace(strings.TrimPrefix(s, "hint:"))
		}
	}
	return ""
}

// camSyncRefusal is one bad-input case: the args, the code and exit it must
// answer, and a phrase of the message.
type camSyncRefusal struct {
	args []string
	code string
	exit int
	says string
}

// camSyncRefuses checks a refusal in both output modes: human mode prints
// `[code]`, the message phrase and a non-empty hint; --json prints one
// error envelope with the same code and exit and a hint.
func camSyncRefuses(t *testing.T, c *th.Clone, tc camSyncRefusal) {
	t.Helper()
	r := c.Lore(tc.args...)
	r.Fails(t, tc.exit, "["+tc.code+"]", tc.says)
	if camSyncHint(r) == "" {
		t.Errorf("lore %s: a refusal names the next step (hint:), got:\n%s", strings.Join(tc.args, " "), r.Out())
	}
	e := trashErr(t, c, tc.code, tc.exit, tc.args...)
	if e.Error.Hint == "" {
		t.Errorf("lore %s --json: the error envelope carries a hint: %+v", strings.Join(tc.args, " "), e.Error)
	}
}

// camSyncPeople are the two teammates of the two-clone scenarios.
func camSyncPair(t *testing.T) (*syncTeam, *th.Clone, *th.Clone) {
	t.Helper()
	st := newSyncTeam(t, 2)
	return st, st.clones[0], st.clones[1]
}
