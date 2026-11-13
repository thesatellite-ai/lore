package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestRowScreenNamesActors: the row screen's updated line names the
// writer and an actor-id field keeps the id with the name after it (bug
// hunt D-1: "updated … by act_…" while lists printed names); without a
// namer the ids print as stored.
func TestRowScreenNamesActors(t *testing.T) {
	who, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	r := memRow(t, "hello", "s3cr3t")
	r.UpdatedAt, r.UpdatedBy = time.Date(2026, 11, 2, 10, 1, 0, 0, time.UTC), who
	r.Fields["created_by_actor_id"], _ = value.TextOf(who.String())
	src := fakeSource{rows: map[row.Table][]row.Row{"memories": {r}}}
	name := func(id string) string {
		if id == who.String() {
			return "Nina Newcomer"
		}
		return id
	}
	out, err := run(t, src, Options{Kind: "memory", ActorName: name}, "1", "q")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "updated: 2026-11-02 10:01 by Nina Newcomer\n") || !strings.Contains(out, "created_by_actor_id: "+who.String()+" (Nina Newcomer)\n") {
		t.Fatalf("row screen:\n%s", out)
	}
	out, err = run(t, src, Options{Kind: "memory"}, "1", "q")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "by "+who.String()+"\n") {
		t.Fatalf("without a namer the id prints:\n%s", out)
	}
}
