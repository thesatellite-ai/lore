package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/wire"
)

// TestTaskDoneIntoClientRepoWritesNoInternalField is the seam test of
// PC-LEAK-1: `task start` then `task done` through the real stack (wire,
// the op registry, the folder backend) in a client repo leave a row file
// under .lore/data with no time_entries, time_open or claim_log; the same
// commands in an internal repo record them (the strip is the client's
// only, not a lost write).
func TestTaskDoneIntoClientRepoWritesNoInternalField(t *testing.T) {
	internalFields := []string{"time_entries", "time_open", "claim_log", "claim_epoch", "attempts", "approved_hash"}
	for _, client := range []bool{true, false} {
		name := "internal repo"
		if client {
			name = "client repo"
		}
		t.Run(name, func(t *testing.T) {
			h := opstest.New(t, func(o *wire.Options) {
				o.RepoIsClient = func(context.Context) (bool, error) { return client, nil }
			})
			var out struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpAdd, `{"title":"fix rounding"}`))), &out); err != nil || out.Data.ID == "" {
				t.Fatalf("task.add gave no id: %v", err)
			}
			id := out.Data.ID
			h.Must(opstask.OpStart, `{"id":"`+id+`"}`)
			h.Must(opstask.OpDone, `{"id":"`+id+`"}`)
			doc := rowFile(t, h.Repo, id)
			if !strings.Contains(doc, "fix rounding") {
				t.Fatalf("row file is not the task:\n%s", doc)
			}
			if client {
				for _, k := range internalFields {
					if strings.Contains(doc, `"`+k+`"`) {
						t.Errorf("client repo row file holds internal field %s:\n%s", k, doc)
					}
				}
				return
			}
			if !strings.Contains(doc, `"time_entries"`) {
				t.Errorf("internal repo row file lost time_entries (start+done records a span):\n%s", doc)
			}
		})
	}
}

// rowFile returns the text of the one row file under repo/.lore/data whose
// name holds id.
func rowFile(t *testing.T, repo, id string) string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(filepath.Join(repo, ".lore", "data"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.Contains(d.Name(), strings.TrimPrefix(id, "tsk_")) {
			found = append(found, p)
		}
		return nil
	})
	if err != nil || len(found) != 1 {
		t.Fatalf("row file of %s: %v %v", id, found, err)
	}
	b, err := os.ReadFile(found[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
