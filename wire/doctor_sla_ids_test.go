package wire

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestDoctorSLAKeepsEveryID pins output round 1-sla: the SLA note cut its
// own list of task ids, so `lore doctor --json` (which carries the message
// as is) lost the late tasks past the cut. With many late tasks the one note
// names every one of them; the human renderer is the only place that may
// shorten what it prints.
func TestDoctorSLAKeepsEveryID(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	const n = 14
	var ids []string
	for i := 0; i < n; i++ {
		out, err := run(t, a, opstask.OpAdd, `{"title":"late `+string(rune('a'+i))+`","priority":"urgent"}`, false)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, taskIDOf(t, out))
	}
	if _, err := run(t, a, settings.OpPolicySet, `{"key":"tasks.sla.urgent","value":"1"}`, true); err != nil {
		t.Fatal(err)
	}
	a.opts.Wall = func() time.Time { return time.Now().Add(2 * time.Hour) }
	got, err := a.DoctorSLA(background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Message, "14 open task(s) breached their SLA") {
		t.Fatalf("findings %+v", got)
	}
	for _, id := range ids {
		if !strings.Contains(got[0].Message, id) {
			t.Errorf("SLA note misses %s: %q", id, got[0].Message)
		}
	}
}
