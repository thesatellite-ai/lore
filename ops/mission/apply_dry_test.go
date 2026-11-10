package mission_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
)

// writePlan writes body to a plan file and returns its path.
func writePlan(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMissionApplyDryRun (OV15b, TASK 15.2): --dry-run makes the same
// lookups and writes nothing: before the first apply every row is "would
// add" with an empty id; after it, the mission and tasks "exist" with
// their ids and only a new task in the file is "would add".
func TestMissionApplyDryRun(t *testing.T) {
	h := opstest.New(t)
	path := writePlan(t, planFile)
	in := `{"file":` + opstest.Quote(path) + `,"dry_run":true}`

	dry := h.Must("mission.apply", in)
	txt := opstest.Text(dry)
	for _, w := range []string{"would add mission Invite flow", "would add  backend Backend API", "would add  docs Docs  after backend,ui", "nothing written (--dry-run)"} {
		if !strings.Contains(txt, w) {
			t.Errorf("dry run lacks %q:\n%s", w, txt)
		}
	}
	var out struct {
		DryRun    bool   `json:"dry_run"`
		Created   bool   `json:"created"`
		MissionID string `json:"mission_id"`
		Tasks     []struct {
			Key, ID string
			Created bool
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(h.JSON(dry)), &out); err != nil {
		t.Fatal(err)
	}
	if !out.DryRun || !out.Created || out.MissionID != "" || len(out.Tasks) != 3 || out.Tasks[0].ID != "" || !out.Tasks[0].Created {
		t.Errorf("dry run json %s", h.JSON(dry))
	}
	if got := opstest.Text(h.Must("mission.list", `{"all":true}`)); strings.Contains(got, "Invite flow") {
		t.Fatalf("dry run wrote a mission: %q", got)
	}
	if got := h.JSON(h.Must("task.list", `{"all":true}`)); strings.Contains(got, "Backend API") {
		t.Fatalf("dry run wrote a task: %s", got)
	}

	// After a real apply, a file with one more task: the rest exist.
	h.Must("mission.apply", `{"file":`+opstest.Quote(path)+`}`)
	more := writePlan(t, planFile+"  - key: qa\n    title: QA pass\n    after: [docs]\n")
	txt = opstest.Text(h.Must("mission.apply", `{"file":`+opstest.Quote(more)+`,"dry_run":true}`))
	if !strings.Contains(txt, "mission msn_") || !strings.Contains(txt, " exists") || strings.Count(txt, "exists     tsk_") != 3 || !strings.Contains(txt, "would add  qa QA pass  after docs") {
		t.Errorf("dry run over an applied plan:\n%s", txt)
	}
	if got := h.JSON(h.Must("task.list", `{"all":true}`)); strings.Contains(got, "QA pass") {
		t.Fatal("dry run wrote the new task")
	}
}

// TestMissionApplyStartsAt: mission.starts_at is a calendar day only (a
// file applied again later must mean the same day): YYYY-MM-DD is stored
// as that day, any other form is refused before anything is written.
func TestMissionApplyStartsAt(t *testing.T) {
	h := opstest.New(t)
	res := h.Must("mission.apply", `{"file":`+opstest.Quote(writePlan(t, "mission:\n  title: Starts\n  starts_at: 2026-12-01\ntasks: []\n"))+`}`)
	var out struct {
		MissionID string `json:"mission_id"`
	}
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	mid, err := ids.ParseAny(out.MissionID)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := h.App.Get(t.Context(), engine.Ref{Table: schema.TableMissions, ID: mid})
	if err != nil {
		t.Fatal(err)
	}
	if v := r.Fields["starts_at"]; v == nil || !strings.HasPrefix(v.String(), "2026-12-01") {
		t.Errorf("starts_at = %v", v)
	}
	for _, bad := range []string{"tomorrow", "2026-12-01T10:00:00Z", "12/01/2026", "2026-13-01"} {
		body := "mission:\n  title: Bad " + bad + "\n  starts_at: \"" + bad + "\"\ntasks: []\n"
		h.Fails("mission.apply", `{"file":`+opstest.Quote(writePlan(t, body))+`}`, errs.InvalidInput)
		if strings.Contains(opstest.Text(h.Must("mission.list", `{"all":true}`)), "Bad "+bad) {
			t.Errorf("starts_at %q: refused file wrote a mission", bad)
		}
	}
}
