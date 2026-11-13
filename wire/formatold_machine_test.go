package wire_test

// formatold_machine_test.go — fmt1: in a project on the lore 0.1.17 format
// this lore writes nothing into the project, but a write whose row lives
// outside it (this machine's session row, setup progress, render history)
// is never refused for the project's format. The session row's update made
// by a process's first audited write used to go through App.Update with a
// ref that named no store, so it was gated as a repo write and refused:
// every `lore setup check|apply` step and every render history row in such
// a project failed with "the row … is stored but its audit record failed".

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// fomToV017 turns every row file of h's project and its _meta.json into
// what lore 0.1.17 wrote (`"_v": 1`, no format key).
func fomToV017(t *testing.T, h *opstest.H) {
	t.Helper()
	data := filepath.Join(h.Repo, rowfs.LoreDir, rowfs.DataDir)
	err := filepath.WalkDir(data, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || filepath.Ext(p) != ".json" || filepath.Base(p) == rowfs.MetaFileName {
			return werr
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		var o map[string]json.RawMessage
		if jerr := json.Unmarshal(b, &o); jerr != nil {
			return jerr
		}
		delete(o, "_hlc")
		delete(o, "_o")
		o["_v"] = json.RawMessage(`1`)
		nb, merr := json.MarshalIndent(o, "", "  ")
		if merr != nil {
			return merr
		}
		return os.WriteFile(p, append(nb, '\n'), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := `{"_v": 1, "project_id": ` + opstest.Quote(h.Project) + `}` + "\n"
	if err := os.WriteFile(filepath.Join(data, rowfs.MetaFileName), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if !wire.ProjectFormatOld(h.Repo) {
		t.Fatal("the checkout does not read as the 0.1.17 format")
	}
}

func TestMachineRowEditsPassInAnOldFormatProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := opstest.New(t)
	if _, err := h.RunYes("memory.add", `{"body":"the cache warms at boot"}`); err != nil {
		t.Fatal(err)
	}
	fomToV017(t, h)
	h = h.ReopenCold()

	// a repo write is still refused (D13)
	if _, err := h.RunYes("memory.add", `{"body":"refused"}`); !errs.Has(err, errs.FormatOld) {
		t.Fatalf("a repo write in a 0.1.17-format project = %v, want E_FORMAT_OLD", err)
	}
	// the session row: created, then updated by the next audited write
	d := wire.SessionDetails{ActorID: opstest.Actor, Tool: "codex", ToolSessionID: toolSession}
	id, err := h.App.FindOrCreateSession(ctx, h.Project, d)
	if err != nil {
		t.Fatalf("create the session row: %v", err)
	}
	d.Model = "gpt-5"
	if _, err = h.App.FindOrCreateSession(ctx, h.Project, d); err != nil {
		t.Fatalf("update the session row in a 0.1.17-format project: %v", err)
	}
	// a ref that names no store is gated by the store its row is in
	rid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	patch := map[string]value.Value{schema.FieldSessionEndedAt: value.DateTimeFrom(time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))}
	if _, err = h.App.Update(ctx, engine.Ref{Table: schema.TableSessions, ID: rid}, patch); err != nil {
		t.Fatalf("an edit of a machine row by a ref without its store: %v", err)
	}
	if _, err = h.App.Delete(ctx, engine.Ref{Table: schema.TableSessions, ID: rid}); err != nil {
		t.Fatalf("a delete of a machine row by a ref without its store: %v", err)
	}
	if !wire.ProjectFormatOld(h.Repo) {
		t.Fatal("the project left the 0.1.17 format")
	}
}
