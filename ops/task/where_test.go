package task_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestListWhereSort: task list --where reads the one filter grammar (XQ7)
// and --sort the one sort grammar (XQ10), ANDed with the old flags.
func TestListWhereSort(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	low := add(h, "low one", `"priority":"low","due":"2026-12-05"`)
	high := add(h, "high one", `"priority":"high","due":"2026-11-20"`)
	urgent := add(h, "Urgent café", `"priority":"urgent"`)
	h.Must(opstask.OpStart, `{"id":"`+high+`"}`)
	done := add(h, "done one", `"priority":"medium"`)
	h.Must(opstask.OpDone, `{"id":"`+done+`","no_lesson":true}`)

	ids := func(in string) []string {
		t.Helper()
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpList, in))), &out); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, d := range out.Data {
			got = append(got, d.ID)
		}
		return got
	}
	set := func(s ...string) []string { slices.Sort(s); return s }
	unordered := []struct {
		in   string
		want []string
	}{
		{`{"where":["priority>=high"]}`, set(high, urgent)},
		{`{"where":["status:todo,in_progress priority>=high"]}`, set(high, urgent)},
		{`{"where":["status:todo,in_progress","priority>=high"]}`, set(high, urgent)},
		{`{"where":["status=todo"]}`, set(low, urgent)},
		{`{"where":["status!=todo"]}`, set(high)},
		{`{"where":["title~=CAFÉ"]}`, set(urgent)},
		{`{"where":["title~=one"]}`, set(low, high)},
		{`{"where":["due_at?"]}`, set(low, high)},
		{`{"where":["due_at!?"]}`, set(urgent)},
		{`{"where":["has:due_at"]}`, set(low, high)},
		{`{"where":["due_at<2026-12-01"]}`, set(high)},
		{`{"where":["due_at>=2026-12-05"]}`, set(low)},
		{`{"where":["id=` + urgent + `"]}`, set(urgent)},
		// --where narrows the default set; --all widens the base first.
		{`{"where":["priority=medium"]}`, nil},
		{`{"all":true,"where":["priority=medium"]}`, set(done)},
		{`{"status":"todo","where":["priority>low"]}`, set(urgent)},
	}
	for _, c := range unordered {
		got := ids(c.in)
		slices.Sort(got)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.in, got, c.want)
		}
	}
	ordered := []struct {
		in   string
		want []string
	}{
		{`{"sort":["priority:desc"]}`, []string{urgent, high, low}},
		{`{"sort":["priority"]}`, []string{low, high, urgent}},
		{`{"sort":["priority:asc"]}`, []string{low, high, urgent}},
		{`{"sort":["due_at"]}`, []string{high, low, urgent}}, // undated last
		{`{"sort":["due_at:desc"]}`, []string{low, high, urgent}},
		{`{"sort":["title"]}`, []string{urgent, high, low}}, // byte order: "U" < "h"
		{`{"sort":["status:desc","priority:desc"]}`, []string{high, urgent, low}},
		{`{"where":["priority>=high"],"sort":["priority:desc"]}`, []string{urgent, high}},
	}
	for _, c := range ordered {
		if got := ids(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.in, got, c.want)
		}
	}
	refusals := []struct {
		in   string
		code errs.Code
		msg  string
	}{
		{`{"where":["title>a"]}`, errs.InvalidInput, `">" compares numbers, dates and datetimes; title is text`},
		{`{"where":["priority>=hi"]}`, errs.InvalidInput, `priority "hi" is not one of low, medium, high, urgent`},
		{`{"where":["status=todoo"]}`, errs.InvalidInput, `status "todoo" is not one of`},
		{`{"where":["due_at<soon"]}`, errs.InvalidInput, "want YYYY-MM-DD or RFC 3339"},
		{`{"where":["nope=1"]}`, errs.InvalidInput, "unknown field nope (fields: "},
		{`{"where":["claim_epoch=1"]}`, errs.InvalidInput, "unknown field claim_epoch"},                   // internal fields stay hidden
		{`{"where":["meta.client=acme,beta"]}`, errs.InvalidInput, "a metadata key takes one value here"}, // r3-grammar: meta.<key> is read; OR-ed values have no --meta form
		{`{"where":["todo"]}`, errs.InvalidInput, "no operator"},
		{`{"where":[""]}`, errs.InvalidInput, "empty filter"},
		{`{"sort":["-priority"]}`, errs.InvalidInput, "a leading - is not a sort direction"},
		{`{"sort":["priority:down"]}`, errs.InvalidInput, "the direction is asc or desc"},
		{`{"sort":["nope"]}`, errs.InvalidInput, "unknown key nope"},
	}
	for _, c := range refusals {
		if msg := h.Fails(opstask.OpList, c.in, c.code); !strings.Contains(msg, c.msg) {
			t.Errorf("%s: %q lacks %q", c.in, msg, c.msg)
		}
	}
	// XQ10's hint names the documented form.
	_, err := h.Run(opstask.OpList, `{"sort":["-priority"]}`)
	if hintOf(err) != "use priority:desc" {
		t.Errorf("-key hint = %q", hintOf(err))
	}
}
