package link_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestCommitShowMissingCommit (devco Q-9): after git filter-repo dropped a
// linked commit, commit show still printed its linked entities as if the
// commit were in the repo. It now says the commit does not exist here and
// how to move the links (commit_missing in JSON); a commit the repo has
// carries no note.
func TestCommitShowMissingCommit(t *testing.T) {
	h := opstest.New(t)
	t.Chdir(h.Repo) // commit show reads git where it runs, as the CLI does
	seedTask(t, h)
	real := commit(t, h, "feat: real")
	h.Must("link.add", `{"entity":"`+v017Task+`","commit":"`+real+`"}`)
	h.Must("link.add", `{"entity":"`+v017Task+`","commit":"`+v017SHA+`"}`)
	if got := opstest.Text(h.Must("commit.show", `{"id":"`+real+`"}`)); strings.Contains(got, "does not exist") {
		t.Fatalf("a commit the repo has got the missing note: %q", got)
	}
	res := h.Must("commit.show", `{"id":"`+v017SHA+`"}`)
	if got := opstest.Text(res); !strings.Contains(got, "does not exist in this repository") || !strings.Contains(got, "lore link remap --pair "+v017SHA+"=<new-sha>") {
		t.Fatalf("missing commit: %q, want the note and the remap line", got)
	}
	if js := h.JSON(res); !strings.Contains(js, `"commit_missing": true`) && !strings.Contains(js, `"commit_missing":true`) {
		t.Fatalf("JSON lacks commit_missing: %s", js)
	}
}
