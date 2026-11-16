package row_test

// show_everything_test.go — `lore show --id X --json` prints everything
// the row has (devco D-455, D-456): the origin, audit, write and lineage
// blocks without --origin, list fields as JSON arrays, the tags by name
// and the rows that point at it; the origin block names its store and
// says in words why git is "none".

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/ops/row"
)

func TestShowJSONCarriesEverything(t *testing.T) {
	h := opstest.New(t)
	id := h.ID(h.MustYes("task.add", `{"title":"rotate the token"}`))
	raw := h.JSON(h.Must(row.OpShow, `{"id":"`+id+`"}`))
	var out struct {
		Fields map[string]json.RawMessage `json:"fields"`
		Origin *struct {
			Recorded bool `json:"recorded"`
			Store    struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"store"`
			Version string `json:"version"`
		} `json:"origin"`
		Audit *struct {
			CreatedBy struct {
				ID string `json:"id"`
			} `json:"created_by"`
		} `json:"audit"`
		Lineage *struct {
			Moves []row.Move `json:"moves"`
		} `json:"lineage"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	// (the store's name next to its sto_ id is checked on a real store
	// clone: TestJourneyStoreReadsEverything; this repo store has no sto_)
	if out.Origin == nil || !out.Origin.Recorded || out.Origin.Store.ID == "" {
		t.Errorf("show --json without --origin has no origin block: %s", raw)
	}
	if out.Audit == nil || out.Audit.CreatedBy.ID == "" || out.Lineage == nil || out.Lineage.Moves == nil {
		t.Errorf("show --json without --origin has no audit or lineage: %s", raw)
	}
	w, ok := out.Fields["watchers"]
	if !ok || !strings.HasPrefix(strings.TrimSpace(string(w)), "[") {
		t.Errorf("watchers is a list in the row file, shown as %s", w)
	}
	var back row.ShowOut
	if err := json.Unmarshal([]byte(raw), &back); err != nil || !back.Fields["watchers"].IsList {
		t.Errorf("ShowOut does not read its own JSON back (%v): %+v", err, back.Fields["watchers"])
	}
	// the text form stays short without --origin, and gains the block with it
	short := opstest.Text(h.Must(row.OpShow, `{"id":"`+id+`"}`))
	if strings.Contains(short, "origin (written once)") {
		t.Errorf("show without --origin printed the origin block:\n%s", short)
	}
	long := opstest.Text(h.Must(row.OpShow, `{"id":"`+id+`","origin":true}`))
	if !strings.Contains(long, "origin (written once)") || !strings.Contains(long, "watchers: ") {
		t.Errorf("show --origin text:\n%s", long)
	}
}
