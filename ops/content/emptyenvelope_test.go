package content_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// emptyQuery matches nothing in a fresh repo (the same word the v0.1.17
// recording searched for).
const emptyQuery = "zzzqqq"

// TestEmptyEnvelopesMatchV0117 pins the --json bytes of every noun's
// empty list and empty search to v0.1.17's. The expected values were read
// from the real v0.1.17 binary (/home/khanakia/.local/bin/lore, `lore
// <noun> list --json` and `lore <noun> search zzzqqq --json` in a fresh
// scratch repo, compacted with key order kept, 2026-11-04), not from the
// code under test. The rule they show: an empty list prints "data": [],
// an empty search prints "data": null, for every noun. Nouns without a
// search verb in v0.1.17 (external-source, reminder, tag) have list only.
func TestEmptyEnvelopesMatchV0117(t *testing.T) {
	cases := []struct {
		op, input, want string
	}{
		{"architecturenote.list", "", `{"schema_version":1,"kind":"architectureNote.list","data":[]}`},
		{"behaviour.list", "", `{"schema_version":1,"kind":"behaviour.list","data":[]}`},
		{"comment.list", "", `{"schema_version":1,"kind":"comment.list","data":[]}`},
		{"cookbookrecipe.list", "", `{"schema_version":1,"kind":"cookbookRecipe.list","data":[]}`},
		{"decision.list", "", `{"schema_version":1,"kind":"decision.list","data":[]}`},
		{"external_source.list", "", `{"schema_version":1,"kind":"external-source.list","data":[]}`},
		{"handoff.list", "", `{"schema_version":1,"kind":"handoff.list","data":[]}`},
		{"hotfix.list", "", `{"schema_version":1,"kind":"hotfix.list","data":[]}`},
		{"incident.list", "", `{"schema_version":1,"kind":"incident.list","data":[]}`},
		{"memory.list", "", `{"schema_version":1,"kind":"memory.list","data":[]}`},
		{"mission.list", "", `{"schema_version":1,"kind":"mission.list","data":[]}`},
		{"pattern.list", "", `{"schema_version":1,"kind":"pattern.list","data":[]}`},
		{"plan.list", "", `{"schema_version":1,"kind":"plan.list","data":[]}`},
		{"playbook.list", "", `{"schema_version":1,"kind":"playbook.list","data":[]}`},
		{"prompt.list", "", `{"schema_version":1,"kind":"prompt.list","data":[]}`},
		{"reminder.list", "", `{"schema_version":1,"kind":"reminder.list","data":[]}`},
		{"rule.list", "", `{"schema_version":1,"kind":"rule.list","data":[]}`},
		{"snapshot.list", "", `{"schema_version":1,"kind":"snapshot.list","data":[]}`},
		{"suggestion.list", "", `{"schema_version":1,"kind":"suggestion.list","data":[]}`},
		{"tag.list", "", `{"schema_version":1,"kind":"tag.list","data":[]}`},
		{"task.list", "", `{"schema_version":1,"kind":"task.list","data":[]}`},
		{"tasklist.list", "", `{"schema_version":1,"kind":"taskList.list","data":[]}`},
		{"tastepref.list", "", `{"schema_version":1,"kind":"tastePref.list","data":[]}`},
		{"techdoc.list", "", `{"schema_version":1,"kind":"techdoc.list","data":[]}`},
		{"workflow.list", "", `{"schema_version":1,"kind":"workflow.list","data":[]}`},
		{"workspace.list", "", `{"schema_version":1,"kind":"workspace.list","data":[]}`},
	}
	// Every search envelope has the same empty shape; only the kind
	// differs, and v0.1.17 spelled it with the FTS entity name (all
	// lowercase, so tasklist.search although its list says taskList).
	for _, kind := range []string{
		"architecturenote", "behaviour", "comment", "cookbookrecipe", "decision",
		"handoff", "hotfix", "incident", "memory", "mission", "pattern", "plan",
		"playbook", "prompt", "rule", "snapshot", "suggestion", "task", "tasklist",
		"tastepref", "techdoc", "workflow", "workspace",
	} {
		cases = append(cases, struct{ op, input, want string }{
			kind + ".search", `{"query":"` + emptyQuery + `"}`,
			`{"schema_version":1,"kind":"` + kind + `.search","query":"` + emptyQuery + `","count":0,"data":null}`,
		})
	}
	h := opstest.New(t)
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			res, err := h.Run(c.op, c.input)
			if err != nil {
				t.Fatalf("%s: %v", c.op, err)
			}
			if got := h.JSON(res); got != c.want {
				t.Errorf("%s --json\n got %s\nwant %s", c.op, got, c.want)
			}
		})
	}
}
