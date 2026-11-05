package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestSessionIDRuleSkipsLoreRecords (BUG-T7, AGENT_IDENTITY 9): the
// session-id rule does not fire inside a field schema marks LoreRecord
// (tasks.time_open: open spans only lore writes), and keeps firing on
// free text: memories.body, the notes inside tasks.time_entries, and
// tasks.claim_log (it keeps sessions only as ses_ refs, P3-SEC-LAND-1, so
// a raw tool id there is a leak like anywhere else). Other rules still
// fire inside a LoreRecord field.
func TestSessionIDRuleSkipsLoreRecords(t *testing.T) {
	const sid = "sess-0a1b2c3d4e5f"
	cases := []struct {
		name    string
		table   row.Table
		field   string
		json    bool
		text    string
		refused bool
	}{
		{name: "time_open (LoreRecord)", table: schema.TableTasks, field: "time_open", json: true,
			text: `[{"id":"op1","source":"` + sid + `","actor_id":"act_x","start":"2026-11-05T10:00:00Z"}]`},
		{name: "time_entries note", table: schema.TableTasks, field: "time_entries", json: true, refused: true,
			text: `[{"id":"te1","kind":"work","note":"pasted ` + sid + `","start":"2026-11-05T10:00:00Z","end":"2026-11-05T11:00:00Z","seconds":3600}]`},
		{name: "claim_log", table: schema.TableTasks, field: "claim_log", json: true, refused: true,
			text: `[{"session":"` + sid + `"}]`},
		{name: "memory body", table: schema.TableMemories, field: "body", refused: true, text: "resume with " + sid},
		{name: "aws key inside time_open still fires", table: schema.TableTasks, field: "time_open", json: true, refused: true,
			text: `[{"id":"op1","source":"` + fakeKey + `","start":"2026-11-05T10:00:00Z"}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc, err := scan.NewRegexScanner([]scan.Rule{
				{ID: awsRule, Regex: `AKIA[0-9A-Z]{16}`, Keywords: []string{"akia"}, Source: scan.SourceBuiltin},
				{ID: scan.RuleSessionID, Regex: `sess-[0-9a-f]{12}`, Keywords: []string{"sess-"}, Source: scan.SourceBuiltin},
			})
			if err != nil {
				t.Fatal(err)
			}
			f := setup(t, onDetect(t, policy.OnDetectRefuse), WithScanner(sc))
			var fields map[string]value.Value
			if c.table == schema.TableMemories {
				fields = f.memFields("x")
			} else {
				fields = f.taskFields("t", ids.AnyID{})
			}
			if c.json {
				j, jerr := value.JSONOf([]byte(c.text))
				if jerr != nil {
					t.Fatal(jerr)
				}
				fields[c.field] = j
			} else {
				fields[c.field] = txt(c.text)
			}
			_, err = f.eng.Create(context.Background(), "", c.table, fields)
			if c.refused != errs.Has(err, errs.SecretDetected) {
				t.Fatalf("err = %v, want refused=%v", err, c.refused)
			}
			if !c.refused && err != nil {
				t.Fatal(err)
			}
		})
	}
}
