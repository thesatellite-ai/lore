package trash

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// labelEnv is an Env with the StoreLabel upgrade: the org store attached
// under the bare org name at its sync folder, as in a linked repo.
type labelEnv struct{ Env }

func (labelEnv) StoreLabel(name store.Source) string {
	if name == "acme" {
		return "acme/sync"
	}
	return string(name)
}

// TestRestoreNamesTheFolderD647: devco D-647, `trash restore` in a repo
// linked to acme/sync printed "(memories, acme)" while the same restore
// from the home folder printed "(memories, acme/sync)": the text names
// each store (row, comment, link) by the Env's label; the JSON keeps the
// attached name; an Env without the upgrade prints the name as it is.
func TestRestoreNamesTheFolderD647(t *testing.T) {
	t.Parallel()
	res := ResultOut{Action: "restored", Count: 1, Rows: []Item{{ID: "mem_1", Table: "memories", Store: "acme"}},
		Comments: []Comment{{ID: "cmt_1", Store: "acme"}}, Links: []Link{{ID: "lnk_1", Table: "knowledge_links", Store: "acme"}}}
	got := ops{env: labelEnv{}}.labeled(res).Text()
	for _, want := range []string{"mem_1 (memories, acme/sync)", "cmt_1 (comments, acme/sync)", "lnk_1 (knowledge_links, acme/sync)"} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "acme)") {
		t.Errorf("a bare store name left in:\n%s", got)
	}
	if res.Rows[0].Store != "acme" {
		t.Errorf("JSON store changed to %q", res.Rows[0].Store)
	}
	if plain := (ops{}).labeled(res).Text(); !strings.Contains(plain, "mem_1 (memories, acme)") {
		t.Errorf("no labeler: %s", plain)
	}
}
