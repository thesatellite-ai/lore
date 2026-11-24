//go:build stories && !windows

package stories

// O_ask_partname_test.go — devco:D-949 (bug hunt H-208, public issue
// #157) on the owner-shaped machine (story O): one personal store keeps
// the code repos of a product as resources (kind code_repo, closed sets
// for project and status) in one folder and the notes about the product
// in another. A question that says the product word ("widget", a part of
// the repo names widget_api, widget_web, widget_legacy) was answered with
// the three repo cards and no note, and the cards printed the closed
// values as val_ ids. Now the notes that answer lead, the cards follow,
// and they print the values' names. A question naming a resource in full
// that no note names is answered by the notes holding its other words,
// never by the resource card alone (owner addendum).

import (
	"strings"
	"testing"
)

func TestStoryOAskPartOfName(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	repos, notes := "me/code", "me/"+psFolder
	s.Step("D-949/repos-and-notes", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", "code").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", psFolder).OK(t)
		ada.Lore(ada.Home, "resource", "kind", "add", "--kind", "code_repo", "--to", repos, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "field", "add", "--kind", "code_repo", "--field", "project", "--type", "text", "--values", "gizmo,widget,tools", "--to", repos, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "field", "add", "--kind", "code_repo", "--field", "purpose", "--type", "text", "--to", repos, "--yes").OK(t)
		ada.Lore(ada.Home, "resource", "field", "add", "--kind", "code_repo", "--field", "status", "--type", "text", "--values", "active,dormant,archived", "--to", repos, "--yes").OK(t)
		for _, r := range [][3]string{
			{"widget_api", "The widget backend: API, import workers, public URL and admin", "active"},
			{"widget_web", "The widget admin frontend", "active"},
			{"widget_legacy", "Old widget system, reference only", "archived"},
		} {
			ada.Lore(ada.Home, "resource", "add", "--kind", "code_repo", "--name", r[0], "--set", "project=widget", "--set", "purpose="+r[1], "--set", "status="+r[2], "--to", repos, "--yes").OK(t)
		}
		ada.Lore(ada.Home, "decision", "add", "--title", "Widget import runs as a two-step workflow", "--body", "Widget import: fetch the bulk file in step one, parse and ingest it in step two.", "--to", notes, "--yes").OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "The public URL of a widget feed is minted by the server; clients never build it.", "--to", notes, "--yes").OK(t)
		// a resource named in full that no note names (owner addendum)
		ada.Lore(ada.Home, "resource", "add", "--kind", "code_repo", "--name", "gizmo", "--set", "project=gizmo", "--set", "purpose=Batch jobs", "--set", "status=active", "--to", repos, "--yes").OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "The nightly export writes a bulk file per tenant.", "--to", notes, "--yes").OK(t)
	})
	s.Step("D-949/notes-lead-cards-print-names", func() {
		for q, want := range map[string]string{
			"how does the widget import work":              "Widget import runs as a two-step workflow",
			"how is the public URL of a widget feed built": "minted by the server",
			"how does the gizmo nightly export work":       "writes a bulk file per tenant",
		} {
			r := ada.Lore(ada.Home, "ask", "--store", "me", "--query", q)
			r.OK(t)
			out := r.Out()
			at, card := strings.Index(out, want), strings.Index(out, "code_repo ")
			if at < 0 || card >= 0 && card < at {
				t.Errorf("[O D-949] `lore ask --store me --query %q` does not lead with the note saying %q:\n%s", q, want, r)
			}
			// a card the question names whole (gizmo) prints its fields;
			// one it only touches (widget of widget_*) prints one line
			// with its purpose and link (devco D-963): never a val_ id
			if strings.Contains(out, "val_") || card >= 0 && strings.Contains(q, "gizmo") && !strings.Contains(out, "status active") {
				t.Errorf("[O D-949] `lore ask --store me --query %q` prints a value id, not the value's name:\n%s", q, r)
			}
		}
	})
}
