package wheredel_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/mission"
	"github.com/thesatellite-ai/lore/ops/plan"
	"github.com/thesatellite-ai/lore/ops/row"
	"github.com/thesatellite-ai/lore/ops/rule"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/ops/tasklist"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// whereNoun is one noun with the bulk delete --where form: add makes a
// row whose filter field holds word; field is that field.
type whereNoun struct {
	name     string
	add, del string
	field    string
	addIn    func(word string) string
	// alwaysAsks: the op asks before running even a dry run (memory
	// keeps v0.1.17's up-front question, FA-LTASK-2 in ops/memory).
	alwaysAsks bool
}

var whereNouns = []whereNoun{
	{"tasklist", tasklist.OpAdd, tasklist.OpDelete, "title", func(w string) string { return `{"title":"` + w + `","body":"b"}` }, false},
	{"mission", mission.OpAdd, mission.OpDelete, "title", func(w string) string { return `{"title":"` + w + `"}` }, false},
	{"plan", plan.OpAdd, plan.OpDelete, "title", func(w string) string { return `{"title":"` + w + `","body":"b"}` }, false},
	{"memory", memory.OpAdd, memory.OpDelete, "body", func(w string) string { return `{"body":"` + w + `"}` }, true},
	{"rule", rule.OpAdd, rule.OpDelete, "body", func(w string) string { return `{"body":"` + w + `","severity":"should"}` }, false},
}

var batchID = regexp.MustCompile(`bat_[0-9a-f]{32}`)

// live reports whether row id is readable (not in the trash).
func live(h *opstest.H, id string) bool {
	_, err := h.Run(row.OpShow, `{"id":"`+id+`"}`)
	return err == nil
}

// TestDeleteWhereMatrix (later 18, DELETION 3.2 / 2.8, FA-LTASK-2): per
// noun, --dry-run names the matches and writes nothing; without --yes the
// delete is E_NEEDS_CONFIRM and writes nothing; an id with --where, an
// unknown key, a metadata term, an empty filter and --dry-run without
// --where are E_INVALID_INPUT; a match of nothing deletes nothing; the
// confirmed delete trashes exactly the matches in one batch, and restoring
// that batch brings every one back.
func TestDeleteWhereMatrix(t *testing.T) {
	for _, n := range whereNouns {
		t.Run(n.name, func(t *testing.T) {
			h := opstest.New(t)
			a := h.ID(h.MustYes(n.add, n.addIn("sunset alpha")))
			b := h.ID(h.MustYes(n.add, n.addIn("sunset beta")))
			keep := h.ID(h.MustYes(n.add, n.addIn("keep gamma")))
			where := `"where":["` + n.field + `~=sunset"]`

			dryRun := h.Must
			if n.alwaysAsks {
				h.Fails(n.del, `{`+where+`,"dry_run":true}`, errs.NeedsConfirm)
				dryRun = h.MustYes
			}
			dry := opstest.Text(dryRun(n.del, `{`+where+`,"dry_run":true}`))
			if !strings.HasPrefix(dry, "would delete 2 "+n.name+"(s):") || !strings.Contains(dry, a) || !strings.Contains(dry, b) || strings.Contains(dry, keep) {
				t.Fatalf("dry run: %q", dry)
			}
			h.Fails(n.del, `{`+where+`}`, errs.NeedsConfirm)
			for _, id := range []string{a, b, keep} {
				if !live(h, id) {
					t.Fatalf("a dry run or an unconfirmed delete trashed %s", id)
				}
			}

			for _, in := range []string{
				`{"id":"` + a + `",` + where + `}`,
				`{"where":["nosuchkey=x"]}`,
				`{"where":[""]}`,
				`{"dry_run":true}`,
				`{}`,
			} {
				if _, err := h.RunYes(n.del, in); !errs.Has(err, errs.InvalidInput) {
					t.Fatalf("%s %s: err %v, want E_INVALID_INPUT", n.del, in, err)
				}
			}
			// A metadata term is refused as such (not as an unknown key),
			// even for a field name the noun has.
			if _, err := h.RunYes(n.del, `{"where":["meta.`+n.field+`=x"]}`); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "metadata terms are not taken here") {
				t.Fatalf("meta term: %v", err)
			}
			if got := opstest.Text(h.MustYes(n.del, `{"where":["`+n.field+`=nomatch"]}`)); got != "no "+n.name+" matches --where; nothing deleted\n" {
				t.Fatalf("no match: %q", got)
			}

			out := opstest.Text(h.MustYes(n.del, `{`+where+`}`))
			bat := batchID.FindString(out)
			if bat == "" || !strings.Contains(out, "2 "+n.name+"(s) (--where)") {
				t.Fatalf("confirmed delete: %q", out)
			}
			if live(h, a) || live(h, b) || !live(h, keep) {
				t.Fatalf("after the delete: a %v b %v keep %v", live(h, a), live(h, b), live(h, keep))
			}
			h.MustYes(trash.OpRestore, `{"target":"`+bat+`"}`)
			if !live(h, a) || !live(h, b) {
				t.Fatalf("restoring %s did not bring both back", bat)
			}
		})
	}
}

// claimTeam is a work.Team whose board holds one active claim.
type claimTeam struct{ held string }

func (c claimTeam) ResolveHandle(context.Context, string, bool) (opstask.Assignee, error) {
	return opstask.Assignee{}, errs.New(errs.NotFound, "no roster in this test")
}

func (c claimTeam) Claims(context.Context) (opstask.ClaimBoard, error) { return c, nil }

func (c claimTeam) Me() string { return "act_me" }

func (c claimTeam) Claim(id string) (string, bool, bool) {
	if id == c.held {
		return "act_other", true, true
	}
	return "", false, false
}

func (c claimTeam) State(opstask.View) string { return "" }

// TestDeleteWhereFencesClaimedTasks (later 18, FA-TASK-7): a tasklist
// delete --where takes the list's tasks with it, and a task someone holds
// is fenced in the same batch (its claim_epoch rises, the output says so);
// the unclaimed task is not.
func TestDeleteWhereFencesClaimedTasks(t *testing.T) {
	h := opstest.New(t)
	tl := h.ID(h.MustYes(tasklist.OpAdd, `{"title":"sunset list","body":"b"}`))
	held := h.ID(h.MustYes(opstask.OpAdd, `{"title":"held","tasklist":"`+tl+`"}`))
	free := h.ID(h.MustYes(opstask.OpAdd, `{"title":"free","tasklist":"`+tl+`"}`))
	ctx := opstask.WithTeam(context.Background(), claimTeam{held: held})
	e, ok := h.App.Registry().Lookup(tasklist.OpDelete)
	if !ok {
		t.Fatal("no tasklist.delete")
	}
	res, err := h.App.Run(ctx, e, op.JSONDecoder{Body: []byte(`{"where":["title~=sunset"]}`)}, op.Caller{Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	out := opstest.Text(res)
	if !strings.Contains(out, "fenced 1 claimed task(s)") || live(h, held) || live(h, free) {
		t.Fatalf("delete: %q (held live %v, free live %v)", out, live(h, held), live(h, free))
	}
	epoch := func(id string) string {
		js := h.JSON(h.Must(trash.OpShow, `{"id":"`+id+`"}`))
		i := strings.Index(js, `"claim_epoch":`)
		if i < 0 {
			return ""
		}
		return strings.SplitN(js[i+len(`"claim_epoch":`):], ",", 2)[0]
	}
	if e := epoch(held); e == "" || e == `"0"` || e == "0" {
		t.Fatalf("the held task was not fenced: claim_epoch %s", e)
	}
	if e := epoch(free); e != "" && e != `"0"` && e != "0" {
		t.Fatalf("the free task was fenced: claim_epoch %s", e)
	}
}
