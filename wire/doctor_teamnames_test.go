package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestTeamVersionsNameTheWriter (CLI_REFERENCE 3.7): on cmd/lore the Team
// versions notes name the writer by its actors row (handle, else display
// name), never the raw act_ id, and the save by its date; a save with no
// recorded writer is named as such, not left blank.
func TestTeamVersionsNameTheWriter(t *testing.T) {
	repo := gitRepo(t)
	home := t.TempDir()
	a := openWith(t, repo, Options{Home: home, Version: "0.1.18", OrgCommands: &standaloneOrg{home: home}})
	withProject(t, a)
	histAddMemory(t, a, "", "stamped")
	got, err := a.DoctorTeamVersions(background())
	if err != nil || len(got) < 2 {
		t.Fatalf("team versions: %+v %v", got, err)
	}
	for _, f := range got[1:] {
		line := f.Message
		if strings.Contains(line, "act_") {
			t.Errorf("raw actor id in %q", line)
		}
		if strings.HasPrefix(line, "   on ") {
			t.Errorf("blank writer in %q", line)
		}
		_, date, ok := strings.Cut(line, "(last save ")
		if !ok || len(strings.TrimSuffix(date, ")")) != len("2006-01-02") {
			t.Errorf("save time is not a date in %q", line)
		}
	}
	if saveDate("2026-11-01T23:59:59.123456789Z") != "2026-11-01" || saveDate("not a time") != "not a time" {
		t.Error("saveDate")
	}
}

// TestTeamActorNamerMatrix: the Team versions writer name over the shapes
// an actors row has: handle wins over display name, display name when no
// handle, the id as stamped when the roster has no row (or the id is not
// an actor id), '(no writer recorded)' for an empty writer; a person seen
// twice is looked up once and named the same.
func TestTeamActorNamerMatrix(t *testing.T) {
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	ctx := background()
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(kv map[string]string) string {
		t.Helper()
		id, ierr := ids.New[ids.Actor]()
		if ierr != nil {
			t.Fatal(ierr)
		}
		if _, cerr := eng.CreateWithID(ctx, store.SourceRepo, schema.TableActors, id.Any(), textValues(t, kv)); cerr != nil {
			t.Fatal(cerr)
		}
		return id.String()
	}
	mira := mk(map[string]string{"kind": schema.ActorKindHuman, "display_name": "Mira Rao", "handle": "mira"})
	tomas := mk(map[string]string{"kind": schema.ActorKindHuman, "display_name": "Tomas Ek"})
	unknown, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	name := a.teamActorNamer(ctx)
	for in, want := range map[string]string{
		mira:              "mira",
		tomas:             "Tomas Ek",
		unknown.String():  unknown.String(),
		"not-an-actor-id": "not-an-actor-id",
		"":                unknownWriter,
	} {
		if got := name(in); got != want {
			t.Errorf("name(%q) = %q, want %q", in, got, want)
		}
	}
	if name(mira) != "mira" {
		t.Error("a second lookup of the same writer differs")
	}
}
