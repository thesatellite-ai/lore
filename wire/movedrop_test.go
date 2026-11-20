package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/ids"
	opscomment "github.com/thesatellite-ai/lore/ops/comment"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestMoveOutOfLinkedRepoLeavesNothing guards devco D-731: after its notes
// moved out, a linked repo kept a tombstone per moved row plus the rows'
// comments. Now a move out of a linked repo removes the repo copy outright
// once the store holds the row, the comment travels with its row (same id)
// and the removal is recorded as moved out for the pre-commit hook; an
// unlinked repo keeps today's tombstone.
func TestMoveOutOfLinkedRepoLeavesNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		linked bool
	}{{"linked", true}, {"unlinked", false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			url := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
			repo := gitRepo(t)
			a := openWith(t, repo, Options{
				OrgStores: func(context.Context) ([]attach.Store, error) {
					if !tc.linked { // attached by name, no org link naming a folder for this repo
						return []attach.Store{{Name: "team", URL: url}}, nil
					}
					return []attach.Store{{Name: "team", URL: url, Folder: "sync"}}, nil
				},
				RepoLinkTarget: func(context.Context) string {
					if tc.linked {
						return "team/sync"
					}
					return ""
				},
			})
			withProject(t, a)
			mem := idOf(t, mustRun(t, a, opsmemory.OpAdd, `{"body":"the cache warms at boot","to":"repo"}`))
			com := idOf(t, mustRun(t, a, opscomment.OpAdd, `{"on_id":"`+mem+`","body":"seen in prod","to":"repo"}`))
			if _, err := run(t, a, opsmemory.OpMove, `{"id":"`+mem+`","to":"team"}`, true); err != nil {
				t.Fatalf("move: %v", err)
			}
			memFile := filepath.Join(repo, ".lore", "data", "memories", mem+".json")
			comFile := filepath.Join(repo, ".lore", "data", "comments", com+".json")
			_, memErr := os.Stat(memFile)
			_, comErr := os.Stat(comFile)
			if tc.linked {
				if memErr == nil || comErr == nil {
					t.Fatalf("devco:D-731: the linked repo still holds the moved row (%v) or its comment (%v)", memErr == nil, comErr == nil)
				}
				eng, err := a.Engine(background())
				if err != nil {
					t.Fatal(err)
				}
				for _, id := range []string{mem, com} {
					aid, perr := ids.ParseAny(id)
					if perr != nil {
						t.Fatal(perr)
					}
					src, r, err := eng.Get(background(), engine.Ref{ID: aid})
					if err != nil || r.Deleted || string(src) != "team" {
						t.Fatalf("devco:D-731: %s reads from %q (deleted %v, err %v), want team", id, src, r.Deleted, err)
					}
				}
				if got := MovedOut(background(), repo); len(got) == 0 {
					t.Fatalf("devco:D-731: no moved-out record: the pre-commit would refuse the removal")
				}
				return
			}
			if memErr != nil {
				t.Fatalf("an unlinked repo lost its tombstone: %v", memErr)
			}
		})
	}
}
