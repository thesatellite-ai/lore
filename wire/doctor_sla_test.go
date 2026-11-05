package wire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestDoctorSLA (TASK 18.4, later 470): with no SLA policy doctor says
// nothing; with tasks.sla.urgent set, an open urgent task past the limit
// is one note naming it (never a warning), a done one is not listed.
func TestDoctorSLA(t *testing.T) {
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	late, err := run(t, a, opstask.OpAdd, `{"title":"late","priority":"urgent"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	lateID := taskIDOf(t, late)
	done, err := run(t, a, opstask.OpAdd, `{"title":"done","priority":"urgent"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run(t, a, opstask.OpDone, `{"id":"`+taskIDOf(t, done)+`","no_lesson":true}`, true); err != nil {
		t.Fatal(err)
	}
	if got, derr := a.DoctorSLA(background()); derr != nil || len(got) != 0 {
		t.Fatalf("no SLA policy: %v %v", got, derr)
	}
	if _, err = run(t, a, settings.OpPolicySet, `{"key":"tasks.sla.urgent","value":"1"}`, true); err != nil {
		t.Fatal(err)
	}
	a.opts.Wall = func() time.Time { return time.Now().Add(2 * time.Hour) }
	got, err := a.DoctorSLA(background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Level != DoctorNote || got[0].Check != DoctorCheckSLA ||
		!strings.Contains(got[0].Message, "1 open task(s) breached their SLA: "+lateID) {
		t.Fatalf("findings %+v", got)
	}
}

// taskIDOf is task.add's id (the task add envelope: data.id).
func taskIDOf(t *testing.T, out string) string {
	t.Helper()
	var w opstask.AddOut
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatal(err)
	}
	return w.Data.ID
}
