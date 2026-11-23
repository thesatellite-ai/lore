//go:build stories && !windows

package stories

// O_copied_id_test.go — story O, devco D-929 (hunt H-187, a leftover of
// H-178 / #142): on the owner-shaped laptop the repo's notes are copied
// into a store folder the repo attaches with `lore store adopt` (ids kept), then a
// memory is edited. The copy the edit did not reach kept its old text,
// and every list read showed both: a search for a word only the old text
// holds still found it, and `lore ask` answered from the nearest store's
// copy. Now list, search and ask show the newest copy only, and the old
// words find nothing.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoryOCopiedIDShowsTheNewestCopy(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	billing := s.Seed(s.Remote("github.com/"+oOwner+"/billing-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	bc := ada.CloneTo(billing, ada.OrgDir("billing-app"))
	notes := s.Remote("github.com/" + oOwner + "/notes")
	const folder = "invoices"
	var id string

	s.Step("D-929/a-memory-copied-into-a-store-folder", func() {
		ada.Lore(bc.Dir, "init", "--non-interactive", "--name=billing-app").OK(t)
		ada.Lore(bc.Dir, "store", "init", "--name", "notes", "--url", notes.SSHURL(), "--yes").OK(t)
		ada.Lore(bc.Dir, "store", "folder", "add", "--store", "notes", "--folder", folder).OK(t)
		r := ada.Lore(bc.Dir, "memory", "add", "--body", "The invoice queue drains every ten minutes.", "--to", "repo", "--json").OK(t)
		i := strings.Index(r.Stdout, `"id": "mem_`)
		if i < 0 {
			t.Fatalf("memory add printed no id:\n%s", r.Stdout)
		}
		id = r.Stdout[i+len(`"id": "`) : i+len(`"id": "`)+36]
		bc.CommitAll("lore: invoice note")
		ada.Lore(bc.Dir, "search", "--query", "invoice").OK(t).Says(t, id) // the read server starts
		ada.Lore(bc.Dir, "store", "adopt", "--target", "notes/"+folder, "--yes").OK(t)
	})

	s.Step("D-929/edited-then-every-reader-shows-the-edit", func() {
		// devco:D-934: an edit by id now sees both copies and refuses (it
		// must not reach one copy only); the copy an older lore edited is
		// written straight into the repo's row file
		ada.Lore(bc.Dir, "memory", "edit", "--id", id, "--body", "The invoice queue drains every hourly batch.").
			Refused(t, "E_AMBIGUOUS_ID", "notes/"+folder)
		oEditOneCopy(t, filepath.Join(bc.Dir, ".lore", "data", "memories", id+".json"), "The invoice queue drains every hourly batch.")
		bc.CommitAll("edit of one copy by an older lore")
		ada.Lore(bc.Dir, "search", "--query", "invoice").OK(t).Says(t, id, "every hourly batch").Never(t, "ten minutes")
		r := ada.Lore(bc.Dir, "search", "--query", "minutes").OK(t)
		oCheck(t, "D-929/search-old-words", !strings.Contains(r.Out(), id),
			"a word only the stale copy holds still finds it:\n%s", r.Out())
		ada.Lore(bc.Dir, "memory", "list").OK(t).Says(t, "every hourly batch").Never(t, "ten minutes")
		ada.Lore(bc.Dir, "ask", "--query", "how often does the invoice queue drain").OK(t).Says(t, "every hourly batch").Never(t, "ten minutes")
	})

	// devco:D-934 (hunt H-195): the copy the edit did not reach is seen by
	// every command by id, lint and doctor (the folder adopt filled is a
	// folder of a store the repo attaches whole, which only lists read),
	// the ambiguous-id hint keeps the edited copy, and a move that would
	// keep the stale copy over the edit is refused
	s.Step("D-934/by-id-lint-doctor-see-both-copies", func() {
		ada.Lore(bc.Dir, "show", "--id", id).Refused(t, "E_AMBIGUOUS_ID",
			"`lore row move --from notes/"+folder+" --where id="+id+" --to repo --yes` keeps the newest copy, in repo")
		ada.Lore(bc.Dir, "show", "--id", id, "--store", "repo").OK(t).Says(t, "every hourly batch").Never(t, "ten minutes")
		ada.Lore(bc.Dir, "show", "--id", id, "--store", "notes/"+folder).OK(t).Says(t, "ten minutes")
		ada.Lore(bc.Dir, "lint").OK(t).Says(t, "copied_id "+id+" (notes/"+folder+")")
		ada.Lore(bc.Dir, "doctor").Says(t, "copied_id: ")
	})

	s.Step("D-934/a-move-never-keeps-the-stale-copy", func() {
		ada.Lore(bc.Dir, "row", "move", "--from", "repo", "--where", "id="+id, "--to", "notes/"+folder, "--yes").
			Refused(t, "E_REFUSED", "the copy in repo is newer", "nothing moved",
				"lore row move --from notes/"+folder+" --where id="+id+" --to repo --yes")
		ada.Lore(bc.Dir, "show", "--id", id, "--store", "repo").OK(t).Says(t, "every hourly batch")
		ada.Lore(bc.Dir, "row", "move", "--from", "notes/"+folder, "--where", "id="+id, "--to", "repo", "--yes").OK(t)
		ada.Lore(bc.Dir, "show", "--id", id).OK(t).Says(t, "every hourly batch").Never(t, "ten minutes")
		ada.Lore(bc.Dir, "memory", "list").OK(t).Says(t, "every hourly batch").Never(t, "ten minutes")
		ada.Lore(bc.Dir, "lint").OK(t).Never(t, "copied_id "+id)
	})
}

// oEditOneCopy rewrites the row file at path with body at a later version,
// as an older lore's edit of one copy of a row left it (devco:D-934: this
// lore refuses an edit by id while two copies are live).
func oEditOneCopy(t *testing.T, path, body string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	hlcText, _ := doc["_hlc"].(string)
	parts := strings.SplitN(hlcText, "/", 3)
	if len(parts) != 3 {
		t.Fatalf("row file %s: no _hlc to bump (%q)", path, hlcText)
	}
	at := time.Now().UTC().Add(5 * time.Second)
	doc["body"] = body
	doc["_hlc"] = at.Format("2006-01-02T15:04:05.000Z") + "/0000/" + parts[2]
	doc["updated_at"] = at.Format("2006-01-02T15:04:05.000000000Z")
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
