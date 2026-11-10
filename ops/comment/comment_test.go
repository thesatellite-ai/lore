package comment_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// TestCommentLifecycle: add / list / search / delete with v0.1.17's human
// lines (checked against lore 0.1.17 output: "✓ comment <id> on
// <table>/<id>", list "<id> on <table>/<id>  <excerpt>", excerpt 60 →
// 57 + "...", search "<id> [<table>/<id>] <body>", "(no matches)").
func TestCommentLifecycle(t *testing.T) {
	h := opstest.New(t)
	task := h.Seed(schema.TableTasks, nil)
	add := h.Must("comment.add", `{"on_table":"tasks","on_id":"`+task+`","body":"looks good to me"}`)
	id := h.ID(add)
	// v0.1.17 printed "%s comment %s on %s/%s %s\n" with route.Label empty
	// for a repo comment, so the line ends in a space (checked against
	// ~/.local/bin/lore 0.1.17, 2026-11-04).
	if got, want := opstest.Text(add), "✓ comment "+id+" on tasks/"+task+" \n"; got != want {
		t.Fatalf("add %q, want %q", got, want)
	}
	long := strings.Repeat("x", 70)
	h.Must("comment.add", `{"on_table":"tasks","on_id":"`+task+`","body":"`+long+`"}`)
	uni := strings.Repeat("ü", 40) // 80 bytes: cut on a character boundary
	h.Must("comment.add", `{"on_table":"tasks","on_id":"`+task+`","body":"`+uni+`"}`)

	list := opstest.IDPattern(opstest.Text(h.Must("comment.list", `{"on_id":"`+task+`"}`)))
	for _, line := range []string{
		"cmt_ID on tasks/tsk_ID  looks good to me\n",
		"cmt_ID on tasks/tsk_ID  " + strings.Repeat("x", 57) + "...\n",
		"cmt_ID on tasks/tsk_ID  " + strings.Repeat("ü", 28) + "...\n",
	} {
		if !strings.Contains(list, line) {
			t.Errorf("list lacks %q:\n%s", line, list)
		}
	}
	if got := opstest.Text(h.Must("comment.list", `{"on_table":"memories"}`)); got != "(no comments)\n" {
		t.Fatalf("filtered list %q", got)
	}

	search := h.Must("comment.search", `{"query":"good"}`)
	if got, want := opstest.Text(search), id+" [tasks/"+task+"] looks good to me\n"; got != want {
		t.Fatalf("search %q, want %q", got, want)
	}
	var env struct {
		Kind  string `json:"kind"`
		Query string `json:"query"`
		Count int    `json:"count"`
		Data  []struct {
			ID  string `json:"id"`
			Row struct {
				Body        string `json:"body"`
				EntityID    string `json:"entity_id"`
				EntityTable string `json:"entity_table"`
			} `json:"row"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(search)), &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "comment.search" || env.Query != "good" || env.Count != 1 || env.Data[0].Row.EntityTable != "tasks" || env.Data[0].Row.EntityID != task {
		t.Fatalf("search json %s", h.JSON(search))
	}
	if got := opstest.Text(h.Must("comment.search", `{"query":"zzz"}`)); got != "(no matches)\n" {
		t.Fatalf("no match %q", got)
	}
	if !strings.Contains(h.JSON(h.Must("comment.search", `{"query":"zzz"}`)), `"count":0`) {
		t.Fatal("search count 0 not printed (v0.1.17 always printed it)")
	}

	h.Fails("comment.delete", `{"id":"`+id+`"}`, errs.NeedsConfirm)
	if got := opstest.Text(h.MustYes("comment.delete", `{"id":"`+id+`"}`)); got != "✓ "+id+" moved to the trash (restore: lore trash restore "+id+")\n" {
		t.Fatalf("delete %q", got)
	}
	if strings.Contains(opstest.Text(h.Must("comment.list", ``)), id) {
		t.Fatal("deleted comment still listed")
	}
	if got := opstest.Text(h.Must("comment.search", `{"query":"good"}`)); got != "(no matches)\n" {
		t.Fatalf("deleted comment still found: %q", got)
	}
}

// TestCommentTargets: a table on the core fails closed on a missing,
// deleted or wrong-table target (v0.1.17 accepted a missing task id, which
// left a comment pointing at nothing); a table not on the core is accepted
// as v0.1.17 accepted it; malformed table names and ids are refused.
// runs is on the core since P3-LOCAL-1 (a machine-local table, held in the
// machine store): a missing run fails closed, and a live run is an
// internal-class row, so F15 refuses it from a client repo and allows it
// from an internal one. bench_runs stands for a table not on the core: it
// is DROPPED (p3/dropped.md), so it never gets a TableDef.
func TestCommentTargets(t *testing.T) {
	h := opstest.New(t)
	task := h.Seed(schema.TableTasks, nil)
	mem := h.Seed(schema.TableMemories, nil)
	cases := []struct {
		table, id string
		code      errs.Code
	}{
		{"tasks", "tsk_01a1a67b1b617dc7acc5f1af36e0e000", errs.NotFound},
		{"tasks", mem, errs.InvalidID},
		{"tasks", "garbage", errs.InvalidID},
		{"tasks", "", errs.InvalidInput}, // required field
		{"Not A Table", task, errs.InvalidInput},
		{"", task, errs.InvalidInput},
		{"memory", mem, errs.InvalidInput}, // misspelt core table (INTEG-KNOWLEDGE-1)
		{"nonsense", task, errs.InvalidInput},
		{"bench_runs", "run_01a1a67b1b617dc7acc5f1af36e0e979", errs.InvalidInput}, // run_ names the core runs table
	}
	for _, c := range cases {
		h.Fails("comment.add", `{"on_table":`+opstest.Quote(c.table)+`,"on_id":`+opstest.Quote(c.id)+`,"body":"hi"}`, c.code)
	}
	missing := "run_01a1a67b1b617dc7acc5f1af36e0e979"
	h.Fails("comment.add", `{"on_table":"runs","on_id":"`+missing+`","body":"hi"}`, errs.NotFound)
	run := h.Seed(schema.TableRuns, nil)
	h.Fails("comment.add", `{"on_table":"runs","on_id":"`+run+`","body":"hi"}`, errs.LeakBlocked)
	internal := opstest.New(t, func(o *wire.Options) {
		o.RepoIsClient = func(context.Context) (bool, error) { return false, nil }
	})
	irun := internal.Seed(schema.TableRuns, nil)
	if got := opstest.Text(internal.Must("comment.add", `{"on_table":"runs","on_id":"`+irun+`","body":"hi"}`)); !strings.HasSuffix(got, " on runs/"+irun+" \n") {
		t.Fatalf("run target from an internal repo: %q", got)
	}
	// A non-core table's id carries a prefix no core table owns (a run_ id
	// names the core runs table, so bench_runs with it is a misspelling).
	bench := "bnr_01a1a67b1b617dc7acc5f1af36e0e979"
	if got := opstest.Text(h.Must("comment.add", `{"on_table":"bench_runs","on_id":"`+bench+`","body":"hi"}`)); !strings.HasSuffix(got, " on bench_runs/"+bench+" \n") {
		t.Fatalf("non-core target: %q", got)
	}
	h.MustYes("task.delete", `{"id":"`+task+`"}`)
	if msg := h.Fails("comment.add", `{"on_table":"tasks","on_id":"`+task+`","body":"hi"}`, errs.InTrash); !strings.Contains(msg, "in the trash") {
		t.Fatalf("deleted target: %s", msg)
	}
}

// TestCommentInputRefusals: body empty / blank, search query empty, limit
// out of range, an unknown --column; body is the only FTS column.
func TestCommentInputRefusals(t *testing.T) {
	h := opstest.New(t)
	task := h.Seed(schema.TableTasks, nil)
	h.Fails("comment.add", `{"on_table":"tasks","on_id":"`+task+`","body":""}`, errs.InvalidInput)
	h.Fails("comment.add", `{"on_table":"tasks","on_id":"`+task+`","body":"  \n\t"}`, errs.EmptyBody)
	h.Fails("comment.search", `{"query":"  "}`, errs.InvalidInput)
	for _, lim := range []string{"-1", "1001"} {
		h.Fails("comment.search", `{"query":"x","limit":`+lim+`}`, errs.InvalidInput)
	}
	h.Fails("comment.search", `{"query":"x","columns":["title"]}`, errs.InvalidInput)
	h.Must("comment.add", `{"on_table":"tasks","on_id":"`+task+`","body":"alpha beta"}`)
	h.Must("comment.add", `{"on_table":"tasks","on_id":"`+task+`","body":"alpha gamma"}`)
	var env struct{ Count int }
	if err := json.Unmarshal([]byte(h.JSON(h.Must("comment.search", `{"query":"alpha","limit":1,"columns":["body"]}`))), &env); err != nil {
		t.Fatal(err)
	}
	if env.Count != 1 {
		t.Fatalf("limit 1 gave %d", env.Count)
	}
}
