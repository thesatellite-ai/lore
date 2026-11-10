package task_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// ghIssue is the github export's object as a gh script reads it.
type ghIssue struct {
	LoreID      string   `json:"lore_id"`
	Parent      string   `json:"parent"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	State       string   `json:"state"`
	StateReason string   `json:"state_reason"`
	Labels      []string `json:"labels"`
	Milestone   string   `json:"milestone"`
	Due         string   `json:"due"`
}

// exportGitHub runs task export --format github with extra input and
// decodes the issue list; every object must decode with no other key.
func exportGitHub(t *testing.T, h *opstest.H, extra string) map[string]ghIssue {
	t.Helper()
	var out opstask.ExportOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpExport, `{"format":"github"`+extra+`}`))), &out); err != nil {
		t.Fatal(err)
	}
	if out.Format != opstask.FormatGitHub {
		t.Fatalf("format %q", out.Format)
	}
	dec := json.NewDecoder(strings.NewReader(out.Content))
	dec.DisallowUnknownFields()
	var list []ghIssue
	if err := dec.Decode(&list); err != nil {
		t.Fatalf("github export is not the issue shape: %v\n%s", err, out.Content)
	}
	m := map[string]ghIssue{}
	for _, is := range list {
		m[is.LoreID] = is
	}
	if len(m) != len(list) || out.Count != len(list) {
		t.Fatalf("count %d, %d issues, %d distinct", out.Count, len(list), len(m))
	}
	return m
}

// TestExportGitHub (TASK 19.2, G-TASK-2): one issue per task with the
// state from the status (done closed/completed, cancelled
// closed/not_planned, every other status open), priority and status
// labels, proposed as a label, the mission's title as milestone, the due
// day; --with-checklist appends the checklist as a task list (an empty
// body gets only the list, a task without items keeps its body);
// --with-children adds sub-tasks flat with their parent; internal fields
// never appear, --internal included.
func TestExportGitHub(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	msn := addID(h, h.Must("mission.add", `{"title":"Release 1"}`))
	open := add(h, "open one", `"body":"the body","priority":"high","mission":"`+msn+`","due":"2026-12-01"`)
	done := add(h, "done one", `"priority":"low"`)
	h.Must(opstask.OpDone, `{"id":"`+done+`","no_lesson":true}`)
	gone := add(h, "cancelled one", "")
	h.Must(opstask.OpCancel, `{"id":"`+gone+`"}`)
	idea := add(h, "an idea", `"commitment":"proposed"`)
	started := add(h, "started", "")
	h.Must(opstask.OpStart, `{"id":"`+started+`"}`)
	h.Must(opstask.OpChecklistAdd, `{"id":"`+open+`","text":"step a","items":["step b"]}`)
	h.Must(opstask.OpChecklistTick, `{"id":"`+open+`","item":"2"}`)
	bare := add(h, "no body", "")
	h.Must(opstask.OpChecklistAdd, `{"id":"`+bare+`","text":"only item"}`)
	kid := add(h, "child", `"parent":"`+open+`"`)
	h.Must(opstask.OpTimeLog, `{"id":"`+open+`","duration":"1h","at":"2026-11-02"}`)

	all := exportGitHub(t, h, `,"with_checklist":true,"internal":true`)
	cases := []struct {
		id, state, reason, milestone, due string
		labels                            []string
	}{
		{open, "open", "", "Release 1", "2026-12-01", []string{"priority:high", "status:todo"}},
		{done, "closed", "completed", "", "", []string{"priority:low", "status:done"}},
		{gone, "closed", "not_planned", "", "", []string{"priority:medium", "status:cancelled"}},
		{idea, "open", "", "", "", []string{"priority:medium", "status:todo", "proposed"}},
		{started, "open", "", "", "", []string{"priority:medium", "status:in_progress"}},
	}
	for _, c := range cases {
		is, ok := all[c.id]
		if !ok {
			t.Fatalf("%s missing from the export", c.id)
		}
		if is.State != c.state || is.StateReason != c.reason || is.Milestone != c.milestone || is.Due != c.due || !slices.Equal(is.Labels, c.labels) {
			t.Errorf("%s: %+v; want state %s/%s milestone %q due %q labels %v", c.id, is, c.state, c.reason, c.milestone, c.due, c.labels)
		}
	}
	if b := all[open].Body; b != "the body\n\n- [ ] step a\n- [x] step b\n" {
		t.Errorf("body with checklist: %q", b)
	}
	if b := all[bare].Body; b != "- [ ] only item\n" {
		t.Errorf("empty body with checklist: %q", b)
	}
	if b := all[done].Body; b != "" {
		t.Errorf("no body, no checklist: %q", b)
	}
	if kidIssue, ok := all[kid]; !ok || kidIssue.Parent != open || kidIssue.Milestone != "Release 1" {
		t.Errorf("child: %+v (ok %v)", kidIssue, ok)
	}

	// without --with-checklist the body is the task's own
	if b := exportGitHub(t, h, "")[open].Body; b != "the body" {
		t.Errorf("body without --with-checklist: %q", b)
	}
	// --where narrows; --with-children adds the sub-task flat
	only := exportGitHub(t, h, `,"where":["title~=open"]`)
	if len(only) != 1 {
		t.Fatalf("--where: %d issues", len(only))
	}
	withKids := exportGitHub(t, h, `,"where":["title~=open"],"with_children":true`)
	if len(withKids) != 2 || withKids[kid].Parent != open {
		t.Fatalf("--with-children: %+v", withKids)
	}
	// no internal or sensitive field, even with --internal
	var out opstask.ExportOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpExport, `{"format":"github","internal":true}`))), &out); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"time_entries", "claim_epoch", "attempts", "assignees", "watchers"} {
		if strings.Contains(out.Content, f) {
			t.Errorf("github export carries %s", f)
		}
	}
}
