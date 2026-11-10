package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestRosterRegisterGivesTheFolderItsProjectRow (team trial step 02,
// fx-orgrow): the org folder's first `lore actor register` writes the
// folder's project row under the id its _meta.json names, so a second
// machine registering over the same store (even before it saw the first
// push) writes the same row file, never a second project; a team put
// reuses that row.
func TestRosterRegisterGivesTheFolderItsProjectRow(t *testing.T) {
	t.Parallel()
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	a := rosterApp(t, orgURL, false)
	if _, _, err := a.RosterRegister(ctx, rosterStore, RosterActor{Kind: schema.ActorKindHuman, StableKey: "mailto:ana@example.com", Name: "Ana", Handle: "ana"}); err != nil {
		t.Fatal(err)
	}
	meta := a.DescribeSource(rosterStore).FolderID
	if meta.IsZero() {
		t.Fatal("the org folder has no project id after a register")
	}
	b := rosterApp(t, orgURL, false)
	if _, _, err := b.RosterRegister(ctx, rosterStore, RosterActor{Kind: schema.ActorKindHuman, StableKey: "mailto:bo@example.com", Name: "Bo", Handle: "bo"}); err != nil {
		t.Fatal(err)
	}
	for name, app := range map[string]*App{"first machine": a, "second machine": b} {
		eng, err := app.Engine(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := eng.List(ctx, rosterStore, schema.TableProjects)
		if err != nil || len(rows) != 1 || rows[0].ID != meta.Any() {
			t.Fatalf("%s: org folder projects = %v %v, want one row %s (_meta.json)", name, rows, err, meta)
		}
	}
	if _, _, err := a.RosterTeamPut(ctx, rosterStore, "org", "ops", "Ops", nil); err != nil {
		t.Fatal(err)
	}
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if notes := strayProjectNotes(ctx, st); len(notes) != 0 {
		t.Fatalf("one project row is no stray: %v", notes)
	}
}

// TestRosterProjectPrefersMetaOverStrayRows: a folder an older lore left
// with an extra project row (lower id, so "first row" would pick it)
// resolves to the row _meta.json names, and the open notice names the
// stray row and the one in use.
func TestRosterProjectPrefersMetaOverStrayRows(t *testing.T) {
	t.Parallel()
	ctx := background()
	a := rosterApp(t, folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal), false)
	if _, _, err := a.RosterRegister(ctx, rosterStore, RosterActor{Kind: schema.ActorKindHuman, StableKey: "mailto:ana@example.com", Name: "Ana", Handle: "ana"}); err != nil {
		t.Fatal(err)
	}
	meta := a.DescribeSource(rosterStore).FolderID
	st, err := a.writeState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name, err := value.TextOf("org")
	if err != nil {
		t.Fatal(err)
	}
	stray, err := st.eng.Create(ctx, rosterStore, schema.TableProjects, map[string]value.Value{rosterFieldName: name})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := a.rosterProject(ctx, st, rosterStore, "org")
	if err != nil || ref.String() != meta.String() {
		t.Fatalf("rosterProject = %s %v, want %s (_meta.json), not the stray %s", ref, err, meta, stray.Row.ID)
	}
	notes := strayProjectNotes(ctx, st)
	if len(notes) != 1 || !strings.Contains(notes[0], stray.Row.ID.String()) || !strings.Contains(notes[0], "using "+meta.String()) {
		t.Fatalf("notes = %v, want one naming the stray %s and using %s", notes, stray.Row.ID, meta)
	}
}
