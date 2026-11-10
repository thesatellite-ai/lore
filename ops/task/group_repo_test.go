package task_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestRepoInTargetStore: `task add --to <store> --repo <name>` resolves the
// repo in the target store's project, creating its repos row once (v0.1.17
// found-or-created it there); the same name twice gives the same repo; in
// the repo store the repo must exist (E_NOT_FOUND, nothing created); a
// search --repo <name> finds the store's task by that store's repos row
// without creating anything, and a name no store has is E_NOT_FOUND.
func TestRepoInTargetStore(t *testing.T) {
	t.Parallel()
	h := opstest.WithStores(t, []string{"agency"})
	repoOf := func(id string) string {
		t.Helper()
		var out struct {
			Data struct {
				RepoID string `json:"repo_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpShow, `{"id":"`+id+`"}`))), &out); err != nil {
			t.Fatal(err)
		}
		return out.Data.RepoID
	}
	first := add(h, "backend work", `"to":"agency","repo":"backend"`)
	second := add(h, "more backend", `"to":"agency","repo":"backend"`)
	r1, r2 := repoOf(first), repoOf(second)
	if r1 == "" || r1 != r2 {
		t.Fatalf("repo ids %q / %q: want one repo created once", r1, r2)
	}
	h.Fails(opstask.OpAdd, `{"title":"x","repo":"backend"}`, errs.NotFound)
	h.Fails(opstask.OpAdd, `{"title":"x","to":"repo","repo":"backend"}`, errs.NotFound)

	ctx := context.Background()
	search := func(in string) []string {
		t.Helper()
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(mustCtx(h, ctx, opstask.OpSearch, in))), &out); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, d := range out.Data {
			got = append(got, d.ID)
		}
		return sortedCopy(got)
	}
	if got, want := search(`{"query":"backend","repo":"backend","no_inherit":true}`), sortedCopy([]string{first, second}); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("search --repo backend: %v, want %v", got, want)
	}
	// a name no attached store has is still refused (v0.1.17)
	h.Fails(opstask.OpSearch, `{"query":"backend","repo":"nowhere"}`, errs.NotFound)
}
