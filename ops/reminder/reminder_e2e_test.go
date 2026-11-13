package reminder_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// TestReminderLifecycle drives add / list / done / delete and compares
// with lore 0.1.17's output for the same commands:
//
//	✓ reminder rmd_… due 2020-01-02
//	! 2020-01-02  past one          (overdue mark "!")
//	    rmd_…
//	  2099-03-04  weekly review (7d)
//	✓ rescheduled to 2099-03-11
//	✓ reminder rmd_… completed
func TestReminderLifecycle(t *testing.T) {
	h := opstest.New(t)
	past := h.Must("reminder.add", `{"message":"past one","due":"2020-01-02"}`)
	pid := h.ID(past)
	if got := opstest.Text(past); got != "✓ reminder "+pid+" due 2020-01-02\n" {
		t.Fatalf("add %q", got)
	}
	weekly := h.ID(h.Must("reminder.add", `{"message":"weekly review","due":"2099-03-04","recurrence":"7d"}`))
	rfc := h.ID(h.Must("reminder.add", `{"message":"timed","due":"2099-05-06T07:08:09Z"}`))

	want := "! 2020-01-02  past one\n    " + pid + "\n" +
		"  2099-03-04  weekly review (7d)\n    " + weekly + "\n" +
		"  2099-05-06  timed\n    " + rfc + "\n"
	list := h.Must("reminder.list", ``)
	if got := opstest.Text(list); got != want {
		t.Fatalf("list\n got %q\nwant %q", got, want)
	}
	// v0.1.17's --json: keys sorted, recurrence only when set, due_at RFC3339.
	var env struct {
		Kind  string
		Count int
		Data  []map[string]string
	}
	if err := json.Unmarshal([]byte(h.JSON(list)), &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "reminder.list" || env.Count != 3 || env.Data[0]["due_at"] != "2020-01-02T00:00:00Z" ||
		env.Data[1]["recurrence"] != "7d" || env.Data[2]["due_at"] != "2099-05-06T07:08:09Z" {
		t.Fatalf("json %s", h.JSON(list))
	}
	if _, ok := env.Data[0]["recurrence"]; ok {
		t.Fatalf("recurrence printed when unset: %s", h.JSON(list))
	}

	// done on a recurring reminder moves it; twice moves it twice.
	for _, next := range []string{"2099-03-11", "2099-03-18"} {
		res := h.Must("reminder.done", `{"id":"`+weekly+`"}`)
		if got := opstest.Text(res); got != "✓ rescheduled to "+next+"\n" || h.Field(res, "rescheduled") != next {
			t.Fatalf("recurring done: %q %s", got, h.JSON(res))
		}
	}
	if got := opstest.Text(h.Must("reminder.done", `{"id":"`+pid+`"}`)); got != "✓ reminder "+pid+" completed\n" {
		t.Fatalf("done %q", got)
	}
	if strings.Contains(opstest.Text(h.Must("reminder.list", ``)), pid) {
		t.Fatal("completed reminder still pending")
	}
	done := h.Must("reminder.list", `{"done":true}`)
	if got := opstest.Text(done); got != "  2020-01-02  past one\n    "+pid+"\n" {
		t.Fatalf("done list %q (a done reminder never gets the overdue mark)", got)
	}
	if !strings.Contains(h.JSON(done), `"done_at":"`) {
		t.Fatalf("done_at missing: %s", h.JSON(done))
	}

	h.Fails("reminder.delete", `{"id":"`+rfc+`"}`, errs.NeedsConfirm)
	if got := opstest.Text(h.MustYes("reminder.delete", `{"id":"`+rfc+`"}`)); got != "✓ "+rfc+" moved to the trash (restore: lore trash restore --target "+rfc+")\n" {
		t.Fatalf("delete %q", got)
	}
	if strings.Contains(opstest.Text(h.Must("reminder.list", ``)), rfc) {
		t.Fatal("deleted reminder listed")
	}
	h.Fails("reminder.done", `{"id":"`+rfc+`"}`, errs.InTrash)
	if got := opstest.Text(h.Must("reminder.list", `{"store":"repo","done":true}`)); !strings.Contains(got, pid) {
		t.Fatalf("--store repo: %q", got)
	}
}

// TestReminderAddRefusals keeps v0.1.17's words for each bad input.
func TestReminderAddRefusals(t *testing.T) {
	h := opstest.New(t)
	cases := []struct {
		in   string
		code errs.Code
		want string
	}{
		{`{"message":"x","due":"2099-03-04","recurrence":"2w"}`, errs.InvalidInput, `bad --recurrence "2w" (want 7d | 30d | 1m | 3m | 6m | 1y)`},
		{`{"message":"x","due":"tomorrow"}`, errs.InvalidInput, "bad --due (want YYYY-MM-DD)"},
		{`{"message":"x","due":"2099-13-40"}`, errs.InvalidInput, "bad --due"},
		{`{"message":"x","due":"   "}`, errs.InvalidInput, `required flag(s) "due" not set`},
		{`{"message":"x"}`, errs.InvalidInput, "due"},
		{`{"message":"   ","due":"2099-01-01"}`, errs.EmptyBody, "message is empty"},
		{`{"message":"x","due":"2099-01-01","created_by":"act_0e45506e64357c94a87e70ef1c6bbba9"}`, errs.InvalidInput, `unknown field "created_by"`}, // no hand-set creator (INTEG-AUDIT-1)
	}
	for _, c := range cases {
		if msg := h.Fails("reminder.add", c.in, c.code); !strings.Contains(msg, c.want) {
			t.Errorf("%s: %s (want %q)", c.in, msg, c.want)
		}
	}
	for _, bad := range []string{"", "rmd_bad", "tsk_01a1a67b1b617dc7acc5f1af36e0e979"} {
		if _, err := h.Run("reminder.done", `{"id":`+opstest.Quote(bad)+`}`); err == nil {
			t.Errorf("done %q accepted", bad)
		}
	}
	// a unicode, multi-word message is kept whole
	msg := "réunion — 会議 ✓"
	id := h.ID(h.Must("reminder.add", `{"message":`+opstest.Quote(msg)+`,"due":"2099-01-01"}`))
	if got := opstest.Text(h.Must("reminder.list", ``)); !strings.Contains(got, msg+"\n    "+id) {
		t.Fatalf("unicode message: %q", got)
	}
}

// TestReminderOnIDDirection is F15 on --on-id (later.md f15b): a value
// holding a core row's id is a pointer, so a repo (client-class without a
// RepoIsClient answer) reminder may not name a row of an internal or the
// personal store, while a personal reminder may name an internal row; a
// value that is not a core id (free text, an off-core table's id, an id
// not loaded here) is accepted as v0.1.17 accepted it.
func TestReminderOnIDDirection(t *testing.T) {
	h := opstest.WithStores(t, []string{"agency", "me"})
	agencyMem := h.ID(h.Must("memory.add", `{"body":"internal note","to":"agency"}`))
	meMem := h.ID(h.Must("memory.add", `{"body":"personal note","to":"me"}`))
	repoTask := h.Seed(schema.TableTasks, nil)

	add := func(onID, to string) error {
		in := `{"message":"m","due":"2099-01-01","on_table":"memories","on_id":` + opstest.Quote(onID)
		if to != "" {
			in += `,"to":` + opstest.Quote(to)
		}
		_, err := h.Run("reminder.add", in+`}`)
		return err
	}
	refused := []struct{ onID, to string }{
		{agencyMem, ""}, {agencyMem, "repo"}, {meMem, ""}, {meMem, "agency"},
	}
	for _, c := range refused {
		if err := add(c.onID, c.to); !errs.Has(err, errs.LeakBlocked) {
			t.Errorf("on %s from %q: %v, want E_LEAK_BLOCKED", c.onID, c.to, err)
		}
	}
	allowed := []struct{ onID, to string }{
		{agencyMem, "agency"}, {agencyMem, "me"}, {meMem, "me"}, {repoTask, ""}, {repoTask, "agency"},
		{"free text, not an id", ""}, {"run_01a1a67b1b617dc7acc5f1af36e0e979", ""},
		{"mem_01a1a67b1b617dc7acc5f1af36e0e000", ""}, // not loaded here
	}
	for _, c := range allowed {
		if err := add(c.onID, c.to); err != nil {
			t.Errorf("on %s from %q: %v, want allowed", c.onID, c.to, err)
		}
	}
	// nothing was written by the refusals: 5 core-id + 3 other allowed adds.
	var env struct{ Count int }
	if err := json.Unmarshal([]byte(h.JSON(h.Must("reminder.list", ``))), &env); err != nil {
		t.Fatal(err)
	}
	if env.Count != len(allowed) {
		t.Fatalf("%d reminders, want %d (a refused add wrote a row)", env.Count, len(allowed))
	}
}
