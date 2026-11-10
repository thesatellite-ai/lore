package trash_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/trash"
	"github.com/thesatellite-ai/lore/schema"
)

// listOf runs trash.list with input and decodes it.
func listOf(t *testing.T, h *opstest.H, input string) trash.ListOut {
	t.Helper()
	var out trash.ListOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(trash.OpList, input))), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// trashed adds a memory with body and deletes it (to the trash) with
// reason, returning its id.
func trashed(t *testing.T, h *opstest.H, body, reason string) string {
	t.Helper()
	id := h.ID(h.Must(memory.OpAdd, `{"body":"`+body+`"}`))
	in := `{"id":"` + id + `"`
	if reason != "" {
		in += `,"reason":"` + reason + `"`
	}
	h.MustYes(memory.OpDelete, in+`}`)
	return id
}

// inTrash reports whether id is listed by trash.list.
func inTrash(t *testing.T, h *opstest.H, id string) bool {
	t.Helper()
	for _, it := range listOf(t, h, ``).Data {
		if it.ID == id {
			return true
		}
	}
	return false
}

// liveGet reads a memory through the engine: nil for a live row, the
// read error, or E_PURGED for a purged row's tombstone.
func liveGet(h *opstest.H, id string) error {
	x, err := ids.ParseAny(id)
	if err != nil {
		return err
	}
	_, r, err := h.App.Get(context.Background(), engine.Ref{Table: schema.TableMemories, ID: x})
	if err == nil && r.Deleted {
		return errs.New(errs.Purged, "tombstone")
	}
	return err
}

// TestTrashListFilters: trash.list over real trashed rows: every row is
// listed newest first; --type (singular or plural), --to, --search (id,
// title, reason; any case), --reason, --batch narrow it; an unknown
// store or type lists nothing; --limit/--page cut with the 1-based
// position, a negative value is E_INVALID_INPUT; a bad --since is refused.
func TestTrashListFilters(t *testing.T) {
	h := opstest.New(t)
	a := trashed(t, h, "alpha deploy note", "superseded by runbook")
	b := trashed(t, h, "beta cache note", "")
	c := trashed(t, h, "gamma ünïcode note", "duplicate")
	all := listOf(t, h, ``)
	if all.Count != 3 || all.Total != 3 {
		t.Fatalf("list: %+v", all)
	}
	got := map[string]trash.Item{}
	for _, it := range all.Data {
		got[it.ID] = it
		if it.Table != schema.TableMemories || it.Store != "repo" || it.Batch == "" || it.DeletedAt == "" || it.DeletedBy == "" {
			t.Errorf("item %+v", it)
		}
	}
	for _, id := range []string{a, b, c} {
		if _, ok := got[id]; !ok {
			t.Fatalf("%s not listed: %+v", id, all.Data)
		}
	}
	for _, f := range []struct {
		in   string
		want []string
	}{
		{`{"type":"memories"}`, []string{a, b, c}},
		{`{"type":"memory"}`, []string{a, b, c}},
		{`{"type":"tasks"}`, nil},
		{`{"to":"repo"}`, []string{a, b, c}},
		{`{"search":"BETA"}`, []string{b}},
		{`{"search":"ÜNÏCODE"}`, []string{c}}, // unicode case folding
		{`{"search":"runbook"}`, []string{a}},
		{`{"search":"` + c[len(c)-8:] + `"}`, []string{c}},
		{`{"reason":"DUPLICATE"}`, []string{c}},
		{`{"batch":"` + got[a].Batch + `"}`, []string{a}},
		{`{"search":"nothing like this"}`, nil},
	} {
		out := listOf(t, h, f.in)
		ids := []string{}
		for _, it := range out.Data {
			ids = append(ids, it.ID)
		}
		if len(ids) != len(f.want) {
			t.Errorf("list %s = %v, want %v", f.in, ids, f.want)
			continue
		}
		for _, w := range f.want {
			if !strings.Contains(strings.Join(ids, ","), w) {
				t.Errorf("list %s = %v, missing %s", f.in, ids, w)
			}
		}
	}
	p2 := listOf(t, h, `{"limit":2,"page":2}`)
	if p2.Count != 1 || p2.Total != 3 || p2.From != 3 {
		t.Fatalf("page 2 of 2: %+v", p2)
	}
	p1 := listOf(t, h, `{"limit":2}`)
	if p1.Count != 2 || p1.From != 1 {
		t.Fatalf("page 1: %+v", p1)
	}
	if far := listOf(t, h, `{"limit":2,"page":9}`); far.Count != 0 || far.Total != 3 {
		t.Fatalf("past the last page: %+v", far)
	}
	h.Fails(trash.OpList, `{"limit":-1}`, errs.InvalidInput)
	h.Fails(trash.OpList, `{"page":-1}`, errs.InvalidInput)
	h.Fails(trash.OpList, `{"since":"yesterday-ish"}`, errs.InvalidInput)
	if s := listOf(t, h, `{"since":"1d"}`); s.Count != 3 {
		t.Fatalf("--since 1d: %+v", s)
	}
}

// TestTrashPurgeGuards (DQ2, destructive path): purge needs exactly one
// mode; a target without --confirm is E_NEEDS_CONFIRM (exit 26) naming
// --confirm and purges nothing; --yes alone never purges; --dry-run lists
// and writes nothing; --confirm purges (the row reads E_PURGED after,
// leaves the trash); --older-than and --archived-before follow the same
// gate, a bad duration is E_INVALID_INPUT.
func TestTrashPurgeGuards(t *testing.T) {
	h := opstest.New(t)
	id := trashed(t, h, "purge me", "")
	keep := trashed(t, h, "keep me", "")

	h.Fails(trash.OpPurge, ``, errs.InvalidInput)
	h.Fails(trash.OpPurge, `{"target":"`+id+`","expired":true}`, errs.InvalidInput)
	h.Fails(trash.OpPurge, `{"older_than":"30d","expired":true}`, errs.InvalidInput)

	for _, yes := range []bool{false, true} {
		run := h.Run
		if yes {
			run = h.RunYes
		}
		_, err := run(trash.OpPurge, `{"target":"`+id+`"}`)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.NeedsConfirm || e.Def().Exit != 26 || e.Next == nil || !strings.Contains(strings.Join(e.Next.Args, " "), "--confirm") {
			t.Fatalf("purge without --confirm (yes=%v): %v", yes, err)
		}
	}
	if !inTrash(t, h, id) {
		t.Fatal("an unconfirmed purge touched the row")
	}

	dry := h.Must(trash.OpPurge, `{"target":"`+id+`","dry_run":true}`)
	if !strings.Contains(h.JSON(dry), id) || !strings.Contains(h.JSON(dry), `"dry_run":true`) {
		t.Fatalf("dry run: %s", h.JSON(dry))
	}
	if !inTrash(t, h, id) {
		t.Fatal("a dry run purged the row")
	}

	h.Must(trash.OpPurge, `{"target":"`+id+`","confirm":true}`)
	if err := liveGet(h, id); !errs.Has(err, errs.Purged) && !errs.Has(err, errs.NotFound) {
		t.Fatalf("after purge: %v, want E_PURGED", err)
	}
	if l := listOf(t, h, ``); l.Count != 1 || l.Data[0].ID != keep {
		t.Fatalf("trash after purge: %+v", l)
	}
	h.Fails(trash.OpRestore, `{"target":"`+id+`"}`, errs.Purged)

	h.Fails(trash.OpPurge, `{"older_than":"0d"}`, errs.NeedsConfirm)
	h.Fails(trash.OpPurge, `{"older_than":"soon","confirm":true}`, errs.InvalidInput)
	h.Fails(trash.OpPurge, `{"expired":true}`, errs.NeedsConfirm)
	h.Fails(trash.OpPurge, `{"archived_before":"90d"}`, errs.NeedsConfirm)
	if l := listOf(t, h, ``); l.Count != 1 {
		t.Fatalf("unconfirmed bulk purges removed rows: %+v", l)
	}
	if d := h.Must(trash.OpPurge, `{"older_than":"30d","dry_run":true}`); strings.Contains(h.JSON(d), keep) {
		t.Fatalf("--older-than 30d dry run lists a row deleted now: %s", h.JSON(d))
	}
}

// TestTrashRestore (DELETION 3.3, 4.2): restore brings the row back with
// the same id; the home flags exclude each other (E_INVALID_INPUT) and
// take an id (E_INVALID_ID); a row not in the trash is E_NOT_FOUND.
func TestTrashRestore(t *testing.T) {
	h := opstest.New(t)
	id := trashed(t, h, "bring me back", "")
	h.Fails(trash.OpRestore, `{"target":"`+id+`","parent":"tsk_01a1a5aca7f57659be7320c5f4b70011","no_parent":true}`, errs.InvalidInput)
	h.Fails(trash.OpRestore, `{"target":"`+id+`","tasklist":"tl_01a1a5aca7f57659be7320c5f4b70011","no_tasklist":true}`, errs.InvalidInput)
	h.Fails(trash.OpRestore, `{"target":"`+id+`","parent":"not-an-id"}`, errs.InvalidID)
	if !inTrash(t, h, id) {
		t.Fatal("a refused restore changed the row")
	}
	res := h.Must(trash.OpRestore, `{"target":"`+id+`"}`)
	if !strings.Contains(h.JSON(res), id) {
		t.Fatalf("restore: %s", h.JSON(res))
	}
	if err := liveGet(h, id); err != nil {
		t.Fatalf("restored row: %v", err)
	}
	if l := listOf(t, h, ``); l.Count != 0 {
		t.Fatalf("trash after restore: %+v", l)
	}
	h.Fails(trash.OpRestore, `{"target":"`+id+`"}`, errs.NotFound)
	h.Fails(trash.OpShow, `{"id":"`+id+`"}`, errs.NotFound)
}

// TestTrashShowAndStats: show prints the trashed row's fields; stats
// counts rows per table, store and deleter.
func TestTrashShowAndStats(t *testing.T) {
	h := opstest.New(t)
	id := trashed(t, h, "shown body", "why not")
	trashed(t, h, "second", "")
	var s trash.ShowOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(trash.OpShow, `{"id":"`+id+`"}`))), &s); err != nil {
		t.Fatal(err)
	}
	if s.Data.ID != id || s.Data.Reason != "why not" || s.Fields["body"] != "shown body" {
		t.Fatalf("show: %+v", s)
	}
	var st trash.StatsOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(trash.OpStats, ``))), &st); err != nil {
		t.Fatal(err)
	}
	if st.Rows != 2 || len(st.ByTable) != 1 || st.ByTable[0].Name != schema.TableMemories || st.ByTable[0].Count != 2 ||
		len(st.ByStore) != 1 || st.ByStore[0].Name != "repo" || len(st.ByDeleter) != 1 {
		t.Fatalf("stats: %+v", st)
	}
}
