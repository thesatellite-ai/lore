//go:build journeys && !windows

package journeys

// searchindex_test.go — search and ask follow the row files (devco D-920,
// hunt H-178). Sam edits a memory, then undoes the edit in git (`git
// checkout -- .lore/data`, as a branch switch or a reset does). The file
// holds the older version again; before the fix the cache kept the newer
// copy (it never takes an older one back) and moved its cursor past the
// file, so search and ask answered with the edited text no file held
// while `lore show --id` showed the file, and doctor said nothing.

import (
	"strings"
	"testing"
)

func TestJourneySearchFollowsARowFileThatWentBack(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	app := w.Remote("github.com/sam-dev/index-app")
	repo := lap.Clone(app, "index-app")
	var id string

	w.Step("day1/memory-committed-then-edited", func() {
		repo.WriteFile("README.md", "# index-app\n")
		repo.CommitAll("first")
		lap.LoreIn(repo, "init").OK(t)
		r := lap.LoreIn(repo, "memory", "add", "--to", "repo", "--body", "The invoice queue drains every ten minutes.", "--json").OK(t)
		i := strings.Index(r.Stdout, `"id": "mem_`)
		if i < 0 {
			t.Fatalf("memory add printed no id:\n%s", r.Stdout)
		}
		id = r.Stdout[i+len(`"id": "`) : i+len(`"id": "`)+36]
		repo.CommitAll("lore: memory")
		lap.LoreIn(repo, "memory", "edit", "--id", id, "--body", "The invoice queue drains every hourly batch.").OK(t)
		lap.LoreIn(repo, "search", "--query", "hourly").OK(t).Says(t, id)
	})

	w.Step("day1/edit-undone-in-git-search-and-ask-follow-the-file", func() {
		repo.Git("checkout", "--", ".lore/data")
		lap.LoreIn(repo, "show", "--id", id).OK(t).Says(t, "every ten minutes").Never(t, "hourly")
		lap.LoreIn(repo, "search", "--query", "minutes").OK(t).Says(t, id, "every ten minutes").Never(t, "hourly")
		lap.LoreIn(repo, "search", "--query", "hourly").OK(t).Never(t, id)
		lap.LoreIn(repo, "ask", "--query", "how often does the invoice queue drain").OK(t).Says(t, "every ten minutes").Never(t, "hourly")
		lap.LoreIn(repo, "doctor").Never(t, "search_index")
	})
}
