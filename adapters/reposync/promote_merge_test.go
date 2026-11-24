package reposync

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// memDoc is a canonical memories row file: id, body, the write's HLC and
// the extra fields (archived_at, meta) a test edits.
func memDoc(t *testing.T, id, body, hlc string, extra map[string]any) string {
	t.Helper()
	d := map[string]any{
		"_hlc": hlc + "/0001/act_019a0000000070008000000000000004", "_table": "memories", "_v": 2,
		"id": id, "body": body, "kind": "retrieved", "source_kind": "manual", "trust_score": 0.5,
		"created_at": "2026-01-01T00:00:00.000000000Z", "updated_at": hlc,
	}
	for k, v := range extra {
		d[k] = v
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// TestPromoteMergesOverNewerTarget (H-207, #155 class, devco:D-948): a
// repo note edited on a feature branch, then archived (or given metadata,
// or a newer body) on main after the branch was cut. lore sync promote
// --to main merges field by field against git merge-base(HEAD, main):
// the branch's wording lands and main's archive and metadata stay; a body
// both changed refuses with E_LAND_CONFLICT naming the row and field and
// moves nothing; --take branch / --take main settle only that field.
// Before the fix the checkout's whole file was committed over main's.
func TestPromoteMergesOverNewerTarget(t *testing.T) {
	ctx := context.Background()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	m := merge.Driver{Registry: reg}
	top := gitInitRepo(t)
	git := func(args ...string) string {
		t.Helper()
		return gitT(t, top, args...)
	}
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	const a, c = "mem_019a0000000070008000000000000001", "mem_019a0000000070008000000000000002"
	pa, pc := ".lore/data/memories/"+a+".json", ".lore/data/memories/"+c+".json"
	meta := map[string]any{"meta": map[string]any{"mky_019a0000000070008000000000000003": map[string]any{"name": "team_owner", "scope": "repo", "v": "billing"}}}
	writeRepoFile(t, top, ".lore/data/_meta.json", `{"_v":2}`)
	writeRepoFile(t, top, pa, memDoc(t, a, "pelican invoices export nightly", "2026-01-01T00:00:00.000Z", nil))
	writeRepoFile(t, top, pc, memDoc(t, c, "heron receipts export hourly", "2026-01-01T00:00:00.000Z", nil))
	git("add", ".")
	git("commit", "-qm", "base")
	git("checkout", "-q", "-b", "feature/x")
	writeRepoFile(t, top, pa, memDoc(t, a, "pelican invoices export nightly (branch)", "2026-01-02T00:00:00.000Z", nil))
	writeRepoFile(t, top, pc, memDoc(t, c, "heron receipts export hourly (branch)", "2026-01-02T00:00:00.000Z", nil))
	git("commit", "-qam", "feature")
	git("checkout", "-q", "main")
	writeRepoFile(t, top, pa, memDoc(t, a, "pelican invoices export nightly", "2026-01-03T00:00:00.000Z", map[string]any{"archived_at": "2026-01-03T00:00:00.000000000Z"}))
	writeRepoFile(t, top, pc, memDoc(t, c, "heron receipts export every 30 minutes", "2026-01-03T00:00:00.000Z", meta))
	git("commit", "-qam", "main edits")
	git("checkout", "-q", "feature/x")
	field := func(p, k string) string {
		t.Helper()
		var d map[string]any
		if err := json.Unmarshal([]byte(git("show", "main:"+p)), &d); err != nil {
			t.Fatal(err)
		}
		if v, ok := d[k]; ok {
			b, _ := json.Marshal(v)
			return string(b)
		}
		return "-"
	}
	now := time.Unix(1_700_000_000, 0)

	res, err := Promote(ctx, PromoteOptions{Dir: top, IDs: []string{a}, Branch: "main", Now: now, Merger: m})
	if err != nil || res.Commit == "" || len(res.Merged) != 1 {
		t.Fatalf("promote of a row only the branch reworded: %+v %v", res, err)
	}
	if got := field(pa, "body"); got != `"pelican invoices export nightly (branch)"` {
		t.Errorf("the branch's wording must land, main has %s", got)
	}
	if got := field(pa, "archived_at"); got == "-" {
		t.Error("main's archive was overwritten by the branch's older copy (#155 class)")
	}

	before := git("rev-parse", "main")
	_, err = Promote(ctx, PromoteOptions{Dir: top, IDs: []string{c}, Branch: "main", Now: now, Merger: m})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.LandConflict || !strings.Contains(e.Message, c+" (body)") || !strings.Contains(e.Hint, "--ids "+c+" --to main --take main") {
		t.Fatalf("a body both changed must refuse with E_LAND_CONFLICT naming row + field and a runnable hint: %v", err)
	}
	if git("rev-parse", "main") != before {
		t.Fatal("a refused promote moved main")
	}

	main := false
	res, err = Promote(ctx, PromoteOptions{Dir: top, IDs: []string{c}, Branch: "main", Now: now, Merger: m, Take: &main})
	if err != nil || len(res.Merged) != 0 {
		t.Fatalf("--take main: %+v %v", res, err)
	}
	if got := field(pc, "body"); got != `"heron receipts export every 30 minutes"` || !strings.Contains(field(pc, "meta"), "billing") {
		t.Errorf("--take main must keep main's body and metadata, main has %s", got)
	}
	branch := true
	res, err = Promote(ctx, PromoteOptions{Dir: top, IDs: []string{c}, Branch: "main", Now: now, Merger: m, Take: &branch})
	if err != nil || res.Commit == "" || len(res.Taken) != 1 {
		t.Fatalf("--take branch: %+v %v", res, err)
	}
	if got := field(pc, "body"); got != `"heron receipts export hourly (branch)"` {
		t.Errorf("--take branch must keep the branch's body, main has %s", got)
	}
	if got := field(pc, "meta"); !strings.Contains(got, "billing") {
		t.Errorf("--take branch settles only the clashing body; main's metadata must stay, got %s", got)
	}
	if st := git("status", "--porcelain"); st != "" {
		t.Fatalf("promote dirtied the checkout: %s", st)
	}
}
