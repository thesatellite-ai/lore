//go:build stories && !windows

package stories

// WU_h182archive_test.go — bug hunt H-182 (devco D-924, owner's 440-row
// import item 4): `lore cookbookrecipe archive` did not exist while rules,
// memories and playbooks archived, so a recipe an import replaced could
// only be deleted. Every searchable row kind now archives and unarchives
// (wire TestEveryRowKindHasTheLifecycleVerbs holds the verb set), and an
// archived row leaves the kind's list, its search and `lore search`.

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// h182Folder is the org store folder the story's repo is linked to (made up).
const h182Folder = "recipes"

// h182RecipeID finds a cookbook recipe id in an add line.
var h182RecipeID = regexp.MustCompile(`cbr_[0-9a-f]{16,}`)

func TestStoryWUH182ArchiveEveryKind(t *testing.T) {
	t.Parallel()
	s := &Story{World: journeys.NewWorld(t), ID: "WU"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", h182Folder).OK(t)
	m.Lore(pc.Dir, "org", "link", "--org", wOrg, "--project", h182Folder, "--own", "--yes").OK(t)
	const title = "Rotate the release signing key"

	s.Step("D-924/cookbookrecipe-archive-hides-and-unarchive-restores", func() {
		id := h182RecipeID.FindString(m.LoreIn(pc, "cookbookrecipe", "add", "--title", title, "--body", "steps to rotate the signing key", "--yes").OK(t).Out())
		if id == "" {
			t.Fatal("devco:D-924: cookbookrecipe add printed no cbr_ id")
		}
		m.LoreIn(pc, "cookbookrecipe", "archive", "--id", id).OK(t).Says(t, id+" archived")
		m.LoreIn(pc, "cookbookrecipe", "list").OK(t).Never(t, title)
		m.LoreIn(pc, "cookbookrecipe", "list", "--archived").OK(t).Says(t, title)
		m.LoreIn(pc, "cookbookrecipe", "search", "--query", "signing").OK(t).Never(t, id)
		m.LoreIn(pc, "search", "--query", "signing").OK(t).Never(t, id)
		m.LoreIn(pc, "search", "--query", "signing", "--include-archived").OK(t).Says(t, id)
		m.LoreIn(pc, "cookbookrecipe", "show", "--id", id, "--json").OK(t).Says(t, `"archived_at"`)
		m.LoreIn(pc, "cookbookrecipe", "unarchive", "--id", id).OK(t).Says(t, id+" unarchived")
		m.LoreIn(pc, "cookbookrecipe", "list").OK(t).Says(t, title)
		m.LoreIn(pc, "search", "--query", "signing").OK(t).Says(t, id)
	})

	s.Step("D-924/every-kind-archives-as-a-batch", func() {
		for _, k := range []string{"architecturenote", "behaviour", "cookbookrecipe", "incident", "suggestion", "tastepref", "workflow", "workspace", "techdoc"} {
			m.LoreIn(pc, k, "archive", "--where", "created_at<2000-01-01", "--dry-run").OK(t).Never(t, "unknown command", "unknown field").Says(t, "nothing archived")
		}
	})

	s.Step("D-924/techdoc-edits-in-place", func() {
		out := m.LoreIn(pc, "techdoc", "add", "--name", "Widget API", "--base-url", "https://docs.example.test/v1", "--yes").OK(t).Out()
		id := regexp.MustCompile(`tdc_[0-9a-f]{16,}`).FindString(out)
		if id == "" {
			t.Fatal("devco:D-924: techdoc add printed no tdc_ id")
		}
		m.LoreIn(pc, "techdoc", "edit", "--id", id, "--base-url", "https://docs.example.test/v2").OK(t).Says(t, id+" updated")
		m.LoreIn(pc, "techdoc", "show", "--id", id).OK(t).Says(t, "https://docs.example.test/v2").Never(t, "/v1")
	})
}
