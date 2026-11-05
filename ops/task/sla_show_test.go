package task_test

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/wire"
)

// TestSLAShownOnRead (TASK 18.4, later 470): with tasks.sla.urgent 10, an
// urgent task is ok after 1h, at risk after 9h (80%), breached after 11h;
// a low task (no SLA) and a done task never get the column. The human list line carries "SLA at risk" /
// "SLA breached", show has the sla: line and the JSON sla state, and
// --where sla:… filters on it.
func TestSLAShownOnRead(t *testing.T) {
	base := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	var off atomic.Int64
	h := opstest.New(t, repoClient(false), func(o *wire.Options) {
		o.Wall = func() time.Time { return base.Add(time.Duration(off.Load())) }
	})
	h.MustYes(settings.OpPolicySet, `{"key":"tasks.sla.urgent","value":"10"}`)
	urgent := add(h, "urgent one", `"priority":"urgent"`)
	low := add(h, "low one", `"priority":"low"`)
	done := add(h, "done one", `"priority":"urgent"`)
	h.Must(opstask.OpDone, `{"id":"`+done+`","no_lesson":true}`)

	lineOf := func(id string) string {
		for _, l := range strings.Split(opstest.Text(h.Must(opstask.OpList, `{"all":true}`)), "\n") {
			if strings.Contains(l, id) {
				return l
			}
		}
		return ""
	}
	showOf := func(id string) (string, string) {
		res := h.Must(opstask.OpShow, `{"id":"`+id+`"}`)
		var out struct {
			Data struct {
				SLA string `json:"sla"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
			t.Fatal(err)
		}
		return out.Data.SLA, opstest.Text(res)
	}
	whereIDs := func(state string) string {
		return h.JSON(h.Must(opstask.OpList, `{"where":["sla:`+state+`"]}`))
	}
	for _, c := range []struct {
		after        time.Duration
		state, words string
	}{
		{time.Hour, "ok", ""},
		{9 * time.Hour, "at_risk", "SLA at risk"},
		{11 * time.Hour, "breached", "SLA breached"},
	} {
		off.Store(int64(c.after))
		l := lineOf(urgent)
		if c.words == "" && strings.Contains(l, "SLA") || c.words != "" && !strings.Contains(l, c.words) {
			t.Errorf("after %v: list line %q, want %q", c.after, l, c.words)
		}
		st, txt := showOf(urgent)
		if st != c.state || !strings.Contains(txt, "sla:") {
			t.Errorf("after %v: show sla %q (want %s), text:\n%s", c.after, st, c.state, txt)
		}
		if js := whereIDs(c.state); !strings.Contains(js, urgent) || strings.Contains(js, low) || strings.Contains(js, done) {
			t.Errorf("after %v: --where sla:%s = %s", c.after, c.state, js)
		}
		for _, id := range []string{low, done} {
			if l := lineOf(id); strings.Contains(l, "SLA") {
				t.Errorf("after %v: %s has an SLA column: %q", c.after, id, l)
			}
		}
	}
	if st, _ := showOf(low); st != "" {
		t.Errorf("low (no SLA) show sla %q", st)
	}
}
