package wire

// batch_session_test.go — G-ENGINE-3 (6a7955e4): this machine's session
// row is bookkeeping, never part of the user's bulk batch, so `lore batch
// undo` still works after later commands rewrote the session row.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	opsbatch "github.com/thesatellite-ai/lore/ops/batch"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// sessionApp opens the release binary's App (session rows on the machine
// store, audit chain) for repo with home.
func sessionApp(t *testing.T, repo, home string) *App {
	t.Helper()
	o := StandaloneOptions(repo, home, repo, "test")
	o.FlagActor = testEmail
	a, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// TestBatchUndoAfterLaterCommands: a bulk delete run as its process's
// first write (so the session row is written during the batch) lists only
// the memories it deleted (no ses_ row), and `batch undo` restores them
// after a later command in the same process and in a new process (which
// rewrites the session row).
func TestBatchUndoAfterLaterCommands(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	t.Setenv(identity.EnvSession, "batch-session-test") // a session applies (a tool or CI would do the same)
	for _, later := range []string{"same process", "new process"} {
		t.Run(later, func(t *testing.T) {
			repo, home := gitRepo(t), t.TempDir()
			a := sessionApp(t, repo, home)
			withProject(t, a)
			var made []string
			for _, body := range []string{"undo one zz", "undo two zz"} {
				out, err := run(t, a, opsmemory.OpAdd, `{"body":"`+body+`"}`, true)
				if err != nil {
					t.Fatal(err)
				}
				made = append(made, idOf(t, out))
			}
			// the delete is its process's first write, so that process's
			// session row is written while the batch runs (as on the CLI)
			d := sessionApp(t, repo, home)
			out, err := run(t, d, opsmemory.OpDelete, `{"where":["body~=zz"]}`, true)
			if err != nil {
				t.Fatalf("bulk delete: %v", err)
			}
			var del struct {
				ID string `json:"id"`
			}
			if err = json.Unmarshal([]byte(out), &del); err != nil || !strings.HasPrefix(del.ID, "bat_") {
				t.Fatalf("no batch id in %s (%v)", out, err)
			}
			bat := del.ID
			shown, err := run(t, d, opsbatch.OpShow, `{"id":"`+bat+`","all":true}`, false)
			if err != nil {
				t.Fatal(err)
			}
			var sh opsbatch.ShowOut
			if err = json.Unmarshal([]byte(shown), &sh); err != nil {
				t.Fatal(err)
			}
			if ss, serr := a.Sessions(background(), 0); serr != nil || len(ss) == 0 {
				t.Fatalf("no session row on this machine (%v): the test would prove nothing", serr)
			}
			if len(sh.RowList) != 2 {
				t.Fatalf("batch rows = %+v, want the 2 memories only", sh.RowList)
			}
			for _, r := range sh.RowList {
				if r.Table != schema.TableMemories {
					t.Fatalf("batch lists a %s row: %+v", r.Table, r)
				}
			}
			b := d
			if later == "new process" {
				b = sessionApp(t, repo, home)
			}
			if _, err = run(t, b, opsmemory.OpAdd, `{"body":"a later note"}`, true); err != nil {
				t.Fatal(err)
			}
			undone, err := run(t, b, opsbatch.OpUndo, `{"id":"`+bat+`"}`, true)
			if err != nil {
				t.Fatalf("undo after a later command: %v", err)
			}
			for _, id := range made {
				if !strings.Contains(undone, id) {
					t.Fatalf("undo did not restore %s: %s", id, undone)
				}
			}
		})
	}
}
