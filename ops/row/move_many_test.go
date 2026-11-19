package row_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/row"
)

// TestMoveFromDryRunAndJSON (devco D-712): `lore move --from repo --to me`
// moves every matching row of the store. --dry-run lists them, asks
// nothing and writes nothing (it printed a write before: the folder's
// project row), and prints the run as a next step with every flag named;
// without --yes the bulk move asks (E_NEEDS_CONFIRM, nothing moved);
// --where table=… narrows it; --json carries from, to and the rows.
func TestMoveFromDryRunAndJSON(t *testing.T) {
	h := opstest.WithStores(t, []string{"me"})
	m1 := h.ID(h.Must("memory.add", `{"body":"first note to move"}`))
	m2 := h.ID(h.Must("memory.add", `{"body":"second note to move"}`))
	rl := h.ID(h.Must("rule.add", `{"body":"a rule that stays"}`))

	dry := h.Must(row.OpMove, `{"from":"repo","to":"me","where":["table=memories"],"dry_run":true}`)
	var out row.MoveOut
	if err := json.Unmarshal([]byte(h.JSON(dry)), &out); err != nil {
		t.Fatal(err)
	}
	if !out.DryRun || out.From != "repo" || out.To != "me" || len(out.Rows) != 2 {
		t.Fatalf("dry run json: %s", h.JSON(dry))
	}
	txt := opstest.Text(dry)
	if !strings.Contains(txt, "would move 2 row(s) from repo to me (nothing written, --dry-run)") ||
		!strings.Contains(txt, "next: lore move --from repo --to me --where 'table=memories' --yes") {
		t.Fatalf("dry run text: %q", txt)
	}
	for _, id := range []string{m1, m2, rl} {
		if got := showJSON(t, h, `{"id":"`+id+`"}`); got.Store != "repo" {
			t.Fatalf("a dry run moved %s to %s", id, got.Store)
		}
	}

	in := `{"from":"repo","to":"me","where":["table=memories"]}`
	h.Fails(row.OpMove, in, errs.NeedsConfirm)
	res := h.MustYes(row.OpMove, in)
	out = row.MoveOut{}
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.DryRun || len(out.Rows) != 2 || out.Rows[0].From != "repo" {
		t.Fatalf("move json: %s", h.JSON(res))
	}
	if !strings.HasPrefix(opstest.Text(res), "✓ moved 2 row(s) from repo to me (same ids; one commit in each store") {
		t.Fatalf("move text: %q", opstest.Text(res))
	}
	for id, want := range map[string]string{m1: "me", m2: "me", rl: "repo"} {
		if got := showJSON(t, h, `{"id":"`+id+`"}`); got.Store != want {
			t.Fatalf("%s is in %s, want %s", id, got.Store, want)
		}
	}
}

// TestMoveFromRefusals (D-712): the two forms do not mix, the bulk flags
// need --from, an unknown --from names the attached stores, and a --where
// field no movable table has is refused naming the fields.
func TestMoveFromRefusals(t *testing.T) {
	h := opstest.WithStores(t, []string{"me"})
	id := h.ID(h.Must("memory.add", `{"body":"stay"}`))
	h.Fails(row.OpMove, `{"id":"`+id+`","from":"repo","to":"me"}`, errs.InvalidInput)
	h.Fails(row.OpMove, `{"id":"`+id+`","to":"me","dry_run":true}`, errs.InvalidInput)
	if msg := h.Fails(row.OpMove, `{"from":"nowhere","to":"me","dry_run":true}`, errs.NotFound); !strings.Contains(msg, "attached: ") {
		t.Fatalf("unknown --from: %s", msg)
	}
	if msg := h.Fails(row.OpMove, `{"from":"repo","to":"me","where":["colour=red"],"dry_run":true}`, errs.InvalidInput); !strings.Contains(msg, "unknown field colour") {
		t.Fatalf("unknown --where field: %s", msg)
	}
	if msg := h.Fails(row.OpMove, `{"from":"me","to":"me","dry_run":true}`, errs.InvalidInput); !strings.Contains(msg, "same store") {
		t.Fatalf("--from = --to: %s", msg)
	}
}

// TestMoveFromListsNearDuplicates (devco D-724): a bulk move into a store
// that already holds a rule of the same name lists the pair as store
// adopt does: both ids, the read of both (in a dry run the moved row is
// read where it still is), the delete that drops one; it never merges
// (both rules stay). A row with nothing like it in --to is not listed.
// Break-verification: without the finder call the dry run and the run
// list nothing.
func TestMoveFromListsNearDuplicates(t *testing.T) {
	h := opstest.WithStores(t, []string{"me"})
	curated := h.ID(h.Must("rule.add", `{"body":"Use portless for dev URLs.","to":"me"}`))
	copied := h.ID(h.Must("rule.add", `{"body":"use portless for dev urls\nso every dev server has a stable name"}`))
	h.Must("rule.add", `{"body":"Run the linter before every push"}`)

	in := `{"from":"repo","to":"me","where":["table=rules"]`
	dry := h.Must(row.OpMove, in+`,"dry_run":true}`)
	var out row.MoveOut
	if err := json.Unmarshal([]byte(h.JSON(dry)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Duplicates) != 1 || out.Duplicates[0].ID != copied || out.Duplicates[0].Existing != curated || out.Duplicates[0].Why != "same title" {
		t.Fatalf("dry run duplicates: %s", h.JSON(dry))
	}
	txt := opstest.Text(dry)
	for _, want := range []string{
		"would leave 1 near-duplicate: rows that repeat a row already in me",
		copied + " ~ " + curated + ": rule",
		"compare: lore show --id " + copied + " --store repo && lore show --id " + curated + " --store me",
		"drop one: lore rule delete --id " + curated + " --yes (the folder's), or the moved one: lore rule delete --id " + copied + " --yes",
	} {
		if !strings.Contains(txt, want) {
			t.Fatalf("dry run text lacks %q:\n%s", want, txt)
		}
	}
	if strings.Count(txt, " ~ ") != 1 {
		t.Fatalf("a row with no near-duplicate is listed:\n%s", txt)
	}

	res := h.MustYes(row.OpMove, in+`}`)
	out = row.MoveOut{}
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Duplicates) != 1 || out.Duplicates[0].ID != copied {
		t.Fatalf("move duplicates: %s", h.JSON(res))
	}
	txt = opstest.Text(res)
	if !strings.Contains(txt, "1 near-duplicate: moved rows that repeat a row already in me (both kept") ||
		!strings.Contains(txt, "compare: lore show --id "+copied+" --store me && lore show --id "+curated+" --store me") {
		t.Fatalf("move text:\n%s", txt)
	}
	for _, id := range []string{curated, copied} {
		if got := showJSON(t, h, `{"id":"`+id+`"}`); got.Store != "me" {
			t.Fatalf("%s is in %s, want me (both kept, nothing merged)", id, got.Store)
		}
	}
}
