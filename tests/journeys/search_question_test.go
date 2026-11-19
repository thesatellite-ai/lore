//go:build journeys && !windows

package journeys

// search_question_test.go — a teammate asks lore a question in plain
// words (devco:D-435, D-436): `lore search "how do we deploy orbit"`
// finds the deploy decision first (before, every word, "how" and "we"
// too, had to match and it printed "(no matches)"); hits of every kind
// are ranked in one list by a real score, each line shows the row's
// title and the text around a match in the body, a resource hit shows its name
// and kind, and a query no row holds whole still lists the closest rows
// and says so.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJourneySearchQuestion(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kai := w.Persona("Kai Moreno", "kai@larkfield.example")
	lap := kai.Laptop()
	repo := lap.Clone(w.Remote("github.com/larkfield/orbit"), "orbit")

	w.Step("day1/notes", func() {
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "decision", "add", "--title", "Orbit deploys with lath", "--body", "Run lath run deploy staging to ship orbit; production needs a release tag.").OK(t)
		lap.LoreIn(repo, "rule", "add", "--body", "Run task dev to start orbit locally; it reads .env.local.").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "The Pinecrest staging server lives in the ops store; ask the lead for access.").OK(t)
		lap.LoreIn(repo, "playbook", "add", "--name", "Local development setup", "--body", "Install the toolchain, copy the example environment file into place, check the ports, then start the server locally with task dev.").OK(t)
		lap.LoreIn(repo, "resource", "kind", "add", "--kind", "host").OK(t)
		lap.LoreIn(repo, "resource", "field", "add", "--kind", "host", "--field", "provider", "--type", "text").OK(t)
		lap.LoreIn(repo, "resource", "add", "--kind", "host", "--name", "pinecrest_staging", "--set", "provider=hetzner").OK(t)
	})

	w.Step("day1/a-question-finds-its-rows", func() {
		out := lap.LoreIn(repo, "search", "--query", "how do we deploy orbit").OK(t).Never(t, "(no matches)").Stdout
		first := firstHitLine(t, out)
		if !strings.HasPrefix(first, "decision") || !strings.Contains(first, "Orbit deploys with lath") {
			t.Errorf("first hit %q: want the deploy decision\n%s", first, out)
		}
		lap.LoreIn(repo, "search", "--query", "where is the Pinecrest staging server").OK(t).Says(t, "The Pinecrest staging server lives")
		lap.LoreIn(repo, "search", "--query", "how do I run orbit locally").OK(t).Says(t, "Run task dev to start orbit locally")
	})

	w.Step("day1/titles-matches-and-resources-show", func() {
		// a word only the body holds, late in it: the title, then the text around the word
		lap.LoreIn(repo, "search", "--query", "ports").OK(t).Says(t, "Local development setup — ", "the ports")
		lap.LoreIn(repo, "search", "--query", "hetzner").OK(t).Says(t, "pinecrest_staging (host) — ")
		res := lap.LoreIn(repo, "search", "--query", "hetzner", "--json").OK(t)
		var env struct {
			Data struct {
				Hits []struct {
					Title        string  `json:"title"`
					ResourceKind string  `json:"resource_kind"`
					Score        float64 `json:"score"`
				} `json:"hits"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil || len(env.Data.Hits) != 1 {
			t.Fatalf("json: %v\n%s", err, res.Stdout)
		}
		if h := env.Data.Hits[0]; h.Title != "pinecrest_staging" || h.ResourceKind != "host" || h.Score < 0.5 {
			t.Errorf("resource hit %+v", h)
		}
	})

	w.Step("day1/no-row-holds-every-word", func() {
		lap.LoreIn(repo, "search", "--query", "deploy pinecrest").OK(t).
			Says(t, "No row holds every word", "Orbit deploys with lath", "Pinecrest staging server").Never(t, "(no matches)")
	})
}

// firstHitLine is the first hit line of a `lore search` text output.
func firstHitLine(t *testing.T, out string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if l != "" && !strings.HasPrefix(l, "Found ") && !strings.HasPrefix(l, "No row ") {
			return l
		}
	}
	t.Fatalf("no hit line in\n%s", out)
	return ""
}
