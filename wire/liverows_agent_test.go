package wire

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// The owner's v0.1.17 repo (2026-11-11): `lore migrate run` failed with
// `E_INVALID_INPUT wire: live row _root/missions/msn_….json: hlc:
// "2026-11-03T14:44:58.429Z/0000/agent": actor`. An agent's live edit is
// stamped with the bare writer "agent" (D8), never an act_ id.
const (
	liveAgentMission = "msn_01a1a59b891c70b789144cd3e711fb09"
	liveAgentStamp   = "2026-11-03T14:44:58.429Z/0000/agent"
)

// liveMission is a v0.1.17 live document of a mission.
func liveMission(id, project, title, stamp string) string {
	return `{"_table":"missions","_v":1,"title":"` + title + `","status":"active","created_at":"2026-11-03T14:44:58.400000000Z",` +
		`"id":"` + id + `","project_id":"` + project + `","updated_at":"2026-11-03T14:44:58.429000000Z","_live":{"hlc":"` + stamp + `"}}`
}

// TestMigrateLiveAgentWriter: a live row stamped by the bare "agent"
// writer migrates. Its _hlc keeps the wall time and counter and names the
// stable actor LiveWriterActor("agent"); the run's report line says so;
// a second run changes nothing; and the pending-rows note of every
// command reads such a ref instead of failing.
func TestMigrateLiveAgentWriter(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	project := projectID(t, a)
	path := liveRootFolder + "/" + schema.TableMissions + "/" + liveAgentMission + ".json"
	writeLiveRef(t, repo, liveRefLocal, map[string]string{path: liveMission(liveAgentMission, project, "ship the importer", liveAgentStamp)})

	if note := a.LiveNote(background()); !strings.Contains(note, "1 missions") {
		t.Fatalf("note before the run: %q", note)
	}
	actor, err := LiveWriterActor("agent")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := LiveWriterActor("agent"); again != actor {
		t.Fatalf("the writer's actor is not stable: %s then %s", actor, again)
	}
	out, err := run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatalf("migrate run refused a live row stamped by an agent: %v", err)
	}
	var res opsmigrate.RunOut
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil {
		t.Fatal(jerr)
	}
	want := schema.TableMissions + "/" + liveAgentMission + ` (live hlc writer "agent" recorded as ` + actor.String() + ")"
	if !slices.Contains(res.Rows, want) {
		t.Fatalf("the report does not name the mapping %q:\n%s", want, out)
	}
	f := rowFile(t, repo+"/.lore/data/missions/"+liveAgentMission+".json")
	h, err := hlc.Parse(fieldString(t, f, row.KeyHLC))
	if err != nil {
		t.Fatalf("the migrated row's _hlc: %v", err)
	}
	if h.Actor != actor || h.Wall.Format("2006-01-02T15:04:05.000Z") != "2026-11-03T14:44:58.429Z" || h.Counter != 0 || string(f[row.KeyVersion]) != "2" {
		t.Fatalf("migrated row: %v", f)
	}
	if again, aerr := a.MigrateLive(background(), false); aerr != nil || len(again) != 0 {
		t.Fatalf("second run: %v %v", again, aerr)
	}
}

// TestLiveStamp: the inputs a live block's hlc holds. An act_ writer and
// no writer are the core's own form; a bare name maps to its stable
// actor; a broken wall time or counter is still refused with its reason.
func TestLiveStamp(t *testing.T) {
	t.Parallel()
	agent, err := LiveWriterActor("agent")
	if err != nil {
		t.Fatal(err)
	}
	ci, err := LiveWriterActor("ci-bot")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		in, writer, want, refused string
	}{
		{in: "2026-11-03T14:44:58.429Z/0000/" + otherActor, want: "2026-11-03T14:44:58.429Z/0000/" + otherActor},
		{in: "2026-11-03T14:44:58.429Z/0000/", want: "2026-11-03T14:44:58.429Z/0000/"},
		{in: "", want: ""},
		{in: liveAgentStamp, writer: "agent", want: "2026-11-03T14:44:58.429Z/0000/" + agent.String()},
		{in: "2026-11-03T14:44:58.429Z/0012/ci-bot", writer: "ci-bot", want: "2026-11-03T14:44:58.429Z/0012/" + ci.String()},
		{in: "yesterday/0000/agent", refused: "wall time"},
		{in: "2026-11-03T14:44:58.429Z/x/agent", refused: "counter"},
		{in: "2026-11-03T14:44:58.429Z/0000/act_nothex", refused: "actor"},
	} {
		h, writer, err := LiveStamp(c.in)
		if c.refused != "" {
			if err == nil || !strings.Contains(err.Error(), c.refused) {
				t.Errorf("LiveStamp(%q) = %v, %v; want refused (%s)", c.in, h, err, c.refused)
			}
			continue
		}
		if err != nil || h.String() != c.want || writer != c.writer {
			t.Errorf("LiveStamp(%q) = %q, %q, %v; want %q, %q", c.in, h.String(), writer, err, c.want, c.writer)
		}
	}
	if agent == ci {
		t.Fatal("two writer names map to one actor")
	}
}
