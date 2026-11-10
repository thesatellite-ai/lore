//go:build userscen

package userscen

// Shared fixtures of the trash area (TestTrash*): one person in their own
// repo with three stores attached (the repo's own .lore/data, the personal
// folder store `me`, and a team git store `kb` on the fake GitHub), plus
// small readers for the trash, a row's fields, search and the bytes on
// disk. Every helper name starts with "trash" so the area files never
// collide with another area's package-level names.

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Store names of the trash matrix: the repo's own rows, the personal
// folder store and a team git store.
const (
	trashStoreRepo = "repo"
	trashStoreMe   = "me"
	trashStoreKB   = "kb"
	// trashKBSlug is the team store's bare repo on the fake GitHub.
	trashKBSlug = "github.com/octo-personal/notes"
	// trashAppSlug is the person's own code repo.
	trashAppSlug = "github.com/octo-personal/app"
)

// trashStores is the store dimension of the matrix (backend git: repo and
// kb; backend folder: me).
var trashStores = []string{trashStoreRepo, trashStoreMe, trashStoreKB}

// trashWorld is one person's setup for a trash scenario.
type trashWorld struct {
	w   *th.World
	a   *th.Actor
	c   *th.Clone
	app *th.Remote
	kb  *th.Remote
	// tasklists holds one tasklist id per store (tasks need one).
	tasklists map[string]string
	// me is the person's own act_ id (handoffs name it).
	me string
}

// trashLive is the person's live config: the default live kinds (tasks,
// missions, handoffs, comments go live and land on main) with every fetch
// fresh. The matrix used to run with those kinds off because a land once
// dropped rows from live before the checkout had them (bugs.md, "rows
// vanish ... lands them on main"); since the read-through of carried rows
// (5776ae6e, TestSyncLand_TagCarriedByLiveCommentsStaysReadable) it runs
// the way a person's repo does.
func trashLive() *th.Live { return th.FreshFetch() }

// newTrashWorld initialises lore in the person's own repo and attaches
// the `me` and `kb` stores. withStores false skips the two stores (the
// flows that only need the repo).
func newTrashWorld(t *testing.T, withStores bool) *trashWorld {
	t.Helper()
	w := newWorld(t)
	a := w.Human("person", "person@example.com")
	a.WriteConfig(th.Config{Live: trashLive()})
	tw := &trashWorld{w: w, a: a, app: w.Remote(trashAppSlug), tasklists: map[string]string{}}
	tw.c = a.Clone(tw.app, "app")
	tw.c.MustLore("init", "--non-interactive", "--name=app")
	stores := []string{trashStoreRepo}
	if withStores {
		tw.kb = w.Remote(trashKBSlug)
		tw.c.MustLore("store", "init", trashStoreMe)
		tw.c.MustLore("store", "init", trashStoreKB, tw.kb.URL())
		stores = trashStores
	}
	for _, s := range stores {
		tw.tasklists[s] = trashID(t, tw.c.MustLore("tasklist", "add", "--title", "Inbox "+s, "--body", "default list", "--to", s, "--json"))
	}
	// The usual first step after lore init: commit .lore and push main.
	tw.c.CommitAll("lore")
	tw.c.AdminPush("origin", "main")
	return tw
}

// trashID is the "id" of a write's --json output (top level or under data).
func trashID(t *testing.T, r th.Result) string {
	t.Helper()
	v := th.DecodeJSON[struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}](t, r)
	if v.ID != "" {
		return v.ID
	}
	if v.Data.ID == "" {
		t.Fatalf("no id in the output: %s", r)
	}
	return v.Data.ID
}

// trashItem is one row of `lore trash list --json` (ops/trash Item).
type trashItem struct {
	ID        string `json:"id"`
	Table     string `json:"table"`
	Store     string `json:"store"`
	Title     string `json:"title"`
	DeletedAt string `json:"deleted_at"`
	DeletedBy string `json:"deleted_by"`
	Reason    string `json:"reason"`
	Batch     string `json:"batch"`
	Dependent bool   `json:"dependent"`
}

// trashListOut is `lore trash list --json`.
type trashListOut struct {
	Data       []trashItem `json:"data"`
	Count      int         `json:"count"`
	Dependents int         `json:"dependents"`
	Total      int         `json:"total"`
	From       int         `json:"from"`
}

// trashResultOut is `lore trash restore|purge --json`.
type trashResultOut struct {
	Action string      `json:"action"`
	Batch  string      `json:"batch"`
	Rows   []trashItem `json:"rows"`
	Count  int         `json:"count"`
	DryRun bool        `json:"dry_run"`
}

// trashErrOut is the --json error envelope every command prints.
type trashErrOut struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Exit    int    `json:"exit"`
		Class   string `json:"class"`
	} `json:"error"`
}

// trashList is `lore trash list --json <flags…>` decoded.
func trashList(t *testing.T, c *th.Clone, flags ...string) trashListOut {
	t.Helper()
	return th.DecodeJSON[trashListOut](t, c.MustLore(append([]string{"trash", "list", "--json"}, flags...)...))
}

// trashByID indexes a listing by id.
func trashByID(l trashListOut) map[string]trashItem {
	out := map[string]trashItem{}
	for _, it := range l.Data {
		out[it.ID] = it
	}
	return out
}

// trashErr runs a command that must fail with --json and returns its
// error envelope, checking the exit code matches the envelope's.
func trashErr(t *testing.T, c *th.Clone, wantCode string, wantExit int, args ...string) trashErrOut {
	t.Helper()
	r := c.Lore(append(args, "--json")...)
	if r.Code != wantExit {
		t.Fatalf("%v: want exit %d (%s): %s", args, wantExit, wantCode, r)
	}
	var e trashErrOut
	if err := json.Unmarshal([]byte(r.Stdout), &e); err != nil {
		t.Fatalf("%v: the error must be one JSON envelope on stdout: %v\n%s", args, err, r)
	}
	if e.Error.Code != wantCode || e.Error.Exit != wantExit {
		t.Fatalf("%v: want %s/%d, got %+v", args, wantCode, wantExit, e.Error)
	}
	if e.Error.Message == "" {
		t.Fatalf("%v: the error names what went wrong: %+v", args, e.Error)
	}
	return e
}

// trashShowOut is `lore show <id> --json`: the row's stored fields.
type trashShowOut struct {
	ID     string            `json:"id"`
	Table  string            `json:"table"`
	Store  string            `json:"store"`
	Fields map[string]string `json:"fields"`
}

// trashShow is `lore show <id> --json`.
func trashShow(t *testing.T, c *th.Clone, id string) trashShowOut {
	t.Helper()
	return th.DecodeJSON[trashShowOut](t, c.MustLore("show", id, "--json"))
}

// trashSearchIDs is the ids `lore search <q> --json` finds.
func trashSearchIDs(t *testing.T, c *th.Clone, q string) []string {
	t.Helper()
	v := th.DecodeJSON[struct {
		Data struct {
			Hits []struct {
				ID string `json:"id"`
			} `json:"hits"`
		} `json:"data"`
	}](t, c.MustLore("search", q, "--json"))
	var out []string
	for _, h := range v.Data.Hits {
		out = append(out, h.ID)
	}
	return out
}

// trashFilesHolding lists the files under dir whose bytes contain needle
// (the git dir excluded: history is not "the files").
func trashFilesHolding(t *testing.T, dir, needle string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".db") || strings.HasSuffix(p, ".db-wal") || strings.HasSuffix(p, ".db-shm") {
			return nil // the SQLite cache keeps freed pages; it is checked through lore sql
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), needle) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// trashTipsHolding lists the refs of a bare remote whose tip tree holds
// needle in any file (what a fresh clone of that ref would get).
func trashTipsHolding(t *testing.T, w *th.World, r *th.Remote, needle string) []string {
	t.Helper()
	var out []string
	for _, ref := range r.Refs("refs/") {
		if g := w.TryGit(r.Dir, "grep", "-l", "-F", needle, ref); g.Code == 0 && strings.TrimSpace(g.Stdout) != "" {
			out = append(out, ref)
		}
	}
	return out
}

// trashContains reports whether ids holds id.
func trashContains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// trashAssertPurgedShow checks `lore show <id>` of a purged row: it
// refuses with E_PURGED naming the id and prints no content (INTEG-TRASH-4;
// it used to print the tombstone as an empty row, exit 0).
func trashAssertPurgedShow(t *testing.T, c *th.Clone, id string) {
	t.Helper()
	e := trashErr(t, c, "E_PURGED", th.ExitRefused, "show", id)
	if !strings.Contains(e.Error.Message, id) {
		t.Errorf("show of the purged %s names it: %+v", id, e.Error)
	}
}
