package memory_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/memory"
)

// TestAtSetsValidAtAndWindowsFilter (P-9, devco D-765): memory add --at
// stores the date the memory is about as valid_at; memory list, memory
// search and lore search keep only the memories whose date (valid_at,
// else when added) falls in --since/--until; memory edit --at moves it.
// Before the fix --at, --since and --until were unknown flags.
func TestAtSetsValidAtAndWindowsFilter(t *testing.T) {
	h := opstest.New(t)
	call := h.ID(h.Must(memory.OpAdd, `{"body":"call with the printer vendor","at":"2025-03-14"}`))
	kick := h.ID(h.Must(memory.OpAdd, `{"body":"kickoff of the vendor garden project"}`))

	var show memory.ShowOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(memory.OpShow, `{"id":"`+call+`"}`))), &show); err != nil {
		t.Fatal(err)
	}
	if got := show.Data.ValidAt.UTC().Format("2006-01-02T15:04:05Z"); got != "2025-03-14T00:00:00Z" {
		t.Fatalf("valid_at = %s, want the --at day", got)
	}
	if txt := opstest.Text(h.Must(memory.OpShow, `{"id":"`+call+`"}`)); !strings.Contains(txt, "  valid-at: 2025-03-14\n") {
		t.Fatalf("show text has no valid-at line:\n%s", txt)
	}

	ids := func(opName, input string) string {
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		raw := h.JSON(h.Must(opName, input))
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("%s %s: %v\n%s", opName, input, err, raw)
		}
		got := make([]string, 0, len(out.Data))
		for _, d := range out.Data {
			got = append(got, d.ID)
		}
		return strings.Join(got, ",")
	}
	march := `"since":"2025-03-01","until":"2025-03-31"`
	for _, tc := range []struct{ op, input, want string }{
		{memory.OpList, `{` + march + `}`, call},
		{memory.OpList, `{"until":"2025-03-14"}`, call}, // a day bound covers the whole day
		{memory.OpList, `{"since":"2025-03-15"}`, kick}, // no --at: the day it was added
		{memory.OpSearch, `{"query":"vendor",` + march + `}`, call},
		{memory.OpSearch, `{"query":"vendor","since":"2025-04-01","until":"2025-12-31"}`, ""},
	} {
		if got := ids(tc.op, tc.input); got != tc.want {
			t.Fatalf("%s %s = [%s], want [%s]", tc.op, tc.input, got, tc.want)
		}
	}
	var global struct {
		Data struct {
			Hits []struct {
				ID string `json:"id"`
			} `json:"hits"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must("search.run", `{"query":"vendor",`+march+`}`))), &global); err != nil {
		t.Fatal(err)
	}
	if len(global.Data.Hits) != 1 || global.Data.Hits[0].ID != call {
		t.Fatalf("lore search --since/--until: %+v, want only %s", global.Data.Hits, call)
	}

	h.Must(memory.OpEdit, `{"id":"`+kick+`","at":"2025-03-20"}`)
	if got := ids(memory.OpList, `{`+march+`}`); got != call+","+kick && got != kick+","+call {
		t.Fatalf("after edit --at: [%s], want both", got)
	}
	if txt := opstest.Text(h.Must(memory.OpAdd, `{"body":"yesterday's standup","at":"yesterday"}`)); !strings.Contains(txt, "at: ") || !strings.Contains(txt, `(from "yesterday")`) {
		t.Fatalf("a phrase --at must say what day it stored:\n%s", txt)
	}
	h.Fails(memory.OpAdd, `{"body":"x","at":"nonsense day"}`, errs.Date)
	h.Fails(memory.OpList, `{"since":"2025-03-31","until":"2025-03-01"}`, errs.InvalidInput)
	h.Fails("search.run", `{"query":"vendor","since":"2025-03-31","until":"2025-03-01"}`, errs.InvalidInput)
}
