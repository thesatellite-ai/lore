//go:build teame2e

package teame2e

// TASK_SYSTEM_DESIGN 6.8 and 6.14 on real git with two people: a sub-task
// tree moved with --with-children from the org store's org/ folder into its
// acme/ project, then from the org store into the linked client repo, keeps every
// id and parent; dev1's clone and the client's own clone see the same tree
// after a pull; a lone child is refused for the client repo, naming its
// parent, and writes nothing there.

import (
	"encoding/json"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teBTreeOf is the ids `lore task show <id> --tree` prints under a parent.
func teBTreeOf(t *testing.T, c *th.Clone, parent string, kids ...string) {
	t.Helper()
	r := c.MustLore("task", "show", parent, "--tree")
	tree := r.Out()
	if i := strings.Index(tree, "tree:"); i >= 0 {
		tree = tree[i:]
	}
	for _, k := range kids {
		if !strings.Contains(tree, k) {
			t.Fatalf("%s's tree lists %s:\n%s", parent, k, r)
		}
	}
}

// teBStoredParent reads parent_id from a task row's JSON text.
func teBStoredParent(t *testing.T, body string) string {
	t.Helper()
	var row struct {
		ParentID string `json:"parent_id"`
	}
	if err := json.Unmarshal([]byte(body), &row); err != nil {
		t.Fatalf("decode task row: %v\n%s", err, body)
	}
	return row.ParentID
}

func TestTeB_SubtreeMoveAcrossStoresTwoClones(t *testing.T) {
	a := newAgency(t, th.Cmd("move"), th.Flag("with-children", "move"), th.Cmd("task", "split"), th.Flag("to", "task", "add"))
	const parentTitle, kid1Title, kid2Title = "teb invoices export", "teb export query", "teb download button"
	p := mustID(t, a.aliceApp.MustLore("task", "add", parentTitle, "--to", orgNorthwind+"/"+folderOrg), "tsk")
	_, kids := splitIDs(t, a.aliceApp, p, kid1Title, kid2Title)
	if a.storeHas(t, folderAcme, p) || !a.storeHas(t, folderOrg, p) {
		t.Fatal("premise: the tree starts in the store's org/ folder")
	}

	// org/ -> acme/ (the project linked to the client repo), whole subtree.
	a.aliceApp.MustLore("move", p, "--to", orgNorthwind, "--with-children", "--yes")
	for _, id := range append([]string{p}, kids...) {
		if !a.storeHas(t, folderAcme, id) || a.storeHas(t, folderOrg, `"title": "`+parentTitle) {
			t.Fatalf("%s is in the store's acme/ project after the move (files: %v)", id, a.store.Files(refMain))
		}
	}
	a.dev1App.MustLore("task", "list").Says(t, parentTitle, kid1Title, kid2Title)
	teBTreeOf(t, a.dev1App, p, kids...)
	a.aliceApp.MustLore("task", "list").Says(t, parentTitle)
	if r := a.aliceApp.MustLore("task", "list", "--json"); strings.Count(r.Stdout, `"id": "`+p+`"`) != 1 {
		t.Fatalf("one home: alice lists the parent once after the move: %s", r)
	}

	// A lone child into the client repo is refused, naming its parent.
	a.aliceApp.Lore("move", kids[0], "--to", "repo", "--yes").Refused(t, p)
	if _, ok := a.clientRepoRow(t, tblTasks, kids[0]); ok {
		t.Fatal("a refused lone child move writes nothing into the client repo")
	}

	// Org store -> client repo, whole subtree; every row keeps id and parent.
	a.aliceApp.MustLore("move", p, "--to", "repo", "--with-children", "--yes")
	for _, k := range kids {
		body, ok := a.clientRepoRow(t, tblTasks, k)
		if !ok {
			t.Fatalf("child %s is in the client repo after --with-children", k)
		}
		if got := teBStoredParent(t, body); got != p {
			t.Fatalf("child %s keeps parent_id %s in the client repo: %q", k, p, got)
		}
	}
	if _, ok := a.clientRepoRow(t, tblTasks, p); !ok {
		t.Fatal("the parent is in the client repo")
	}
	for _, title := range []string{parentTitle, kid1Title, kid2Title} {
		if a.storeHas(t, folderAcme, title) {
			t.Fatalf("the store keeps no live copy of %q (one home)", title)
		}
	}
	// The rows reach the client's remote; dev1 and the client's own clone
	// see the same tree.
	if strings.TrimSpace(a.aliceApp.Status()) != "" {
		a.aliceApp.CommitAll("move the invoices tree to the client")
		a.aliceApp.AdminPush("origin", "main")
	}
	for _, c := range []*th.Clone{a.dev1App, a.acmeApp} {
		c.Git("pull", "-q")
		c.MustLore("task", "list").Says(t, parentTitle, kid1Title, kid2Title)
		teBTreeOf(t, c, p, kids...)
	}
}
