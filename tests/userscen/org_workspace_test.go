//go:build userscen

package userscen

// org_workspace_test.go — `lore workspace` add · list · show · edit ·
// search · delete through the real binary (docs/knowledge/other-row-kinds.md
// Step 2): what a person does with a folder label for a stream of work.

import (
	"net/http"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// orgWorkspace is one workspace of `lore workspace show|list --json`.
type orgWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Body string `json:"body"`
}

// TestOrgWorkspaceCRUD: add two, list both, show one, edit only the fields
// given, search by a word of the body, delete one to the trash (gone from
// list, show and search; back with trash restore); refusals: no title,
// unknown id, malformed id, another kind's id, an edit with nothing to
// change; REST answers the same list.
func TestOrgWorkspaceCRUD(t *testing.T) {
	rw := newRestWorld(t)
	c := rw.repo
	redesign := restAddID(t, rw, "workspace", "add", "--title", "Website redesign", "--body", "Everything for the new look of the site.")
	billing := restAddID(t, rw, "workspace", "add", "--title", "Billing cleanup", "--body", "Invoices and refunds.")

	type list struct {
		Count int            `json:"count"`
		Data  []orgWorkspace `json:"data"`
	}
	l := orgJSON[list](t, c.MustLore("workspace", "list", "--json"))
	if l.Count != 2 || len(l.Data) != 2 {
		t.Fatalf("two workspaces: %+v", l)
	}
	c.MustLore("workspace", "list").Says(t, redesign, billing)
	show := func(id string) orgWorkspace {
		t.Helper()
		return orgJSON[struct {
			Data orgWorkspace `json:"data"`
		}](t, c.MustLore("workspace", "show", id, "--json")).Data
	}
	if w := show(redesign); w.ID != redesign || w.Name != "Website redesign" || w.Body != "Everything for the new look of the site." {
		t.Fatalf("show: %+v", w)
	}

	// edit changes only what is given
	c.MustLore("workspace", "edit", redesign, "--name", "Site redesign")
	if w := show(redesign); w.Name != "Site redesign" || w.Body != "Everything for the new look of the site." {
		t.Fatalf("edit --name keeps the body: %+v", w)
	}
	c.MustLore("workspace", "edit", redesign, "--body", "New look, new fonts.")
	if w := show(redesign); w.Name != "Site redesign" || w.Body != "New look, new fonts." {
		t.Fatalf("edit --body keeps the name: %+v", w)
	}

	c.MustLore("workspace", "search", "refunds").Says(t, billing, "Billing cleanup")
	c.MustLore("workspace", "search", "fonts").Says(t, redesign)
	c.MustLore("workspace", "search", "nothingmatches").Says(t, "(no matches)")

	bad := []struct {
		name string
		args []string
		say  string
	}{
		{"add without a title", []string{"workspace", "add", "--body", "no title"}, "E_INVALID_INPUT"},
		{"show of an unknown id", []string{"workspace", "show", "wsp_0190f0f0f0f07000800000000000dead"}, "E_NOT_FOUND"},
		{"show of a malformed id", []string{"workspace", "show", "not-an-id"}, "E_INVALID_ID"},
		{"show of another kind's id", []string{"workspace", "show", "mem_0190f0f0f0f07000800000000000dead"}, "is not a workspace id (wsp_…)"},
		{"edit with nothing to change", []string{"workspace", "edit", "wsp_0190f0f0f0f07000800000000000dead"}, "give at least one of --name (or --title), --body"},
	}
	for _, b := range bad {
		t.Run(b.name, func(t *testing.T) {
			if r := c.Lore(b.args...); r.Code == 0 {
				t.Fatalf("%v: want a refusal, got %s", b.args, r.Out())
			} else {
				r.Says(t, b.say)
			}
		})
	}

	// REST answers the same list document.
	srv := restStart(t, c)
	cli := c.MustLore("workspace", "list", "--json")
	if rep := srv.get(t, "/v1/workspaces"); rep.status != http.StatusOK || !restJSONEqual([]byte(cli.Stdout), []byte(rep.body)) {
		t.Fatalf("REST list differs: %d\nCLI:  %.400s\nREST: %.400s", rep.status, cli.Stdout, rep.body)
	}

	// delete to the trash and back
	c.MustLore("workspace", "delete", billing, "--yes")
	if l = orgJSON[list](t, c.MustLore("workspace", "list", "--json")); l.Count != 1 || l.Data[0].ID != redesign {
		t.Fatalf("deleted workspace left the list: %+v", l)
	}
	c.Lore("workspace", "show", billing).Fails(t, th.ExitRefused, "E_IN_TRASH")
	c.MustLore("workspace", "search", "refunds").Says(t, "(no matches)")
	c.MustLore("trash", "restore", billing, "--yes")
	if w := show(billing); w.Name != "Billing cleanup" {
		t.Fatalf("restored: %+v", w)
	}
}
