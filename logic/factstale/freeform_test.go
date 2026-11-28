package factstale_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/factstale"
)

// TestSplitEvidenceReadsFreeFormLists (bug hunt H-298, devco:D-1033): a
// team wrote fact_evidence as one text value with semicolons, line-range
// lists after commas, a parenthesised note and a leading word; the old
// comma split read most of it as one path git never knew, so no row was
// ever stale. Each file is its own item now; line pieces and notes are
// dropped.
func TestSplitEvidenceReadsFreeFormLists(t *testing.T) {
	got := factstale.SplitEvidence([]string{"acme/billing/refund.go:12-20,40-44; billing/tax.go:7 (origin/dev 1a2b3c4); rates.go:3,9\nEvidence: billing/fees.go:5.", "3F9C2AB"})
	want := []string{"acme/billing/refund.go:12-20", "billing/tax.go:7", "rates.go:3", "billing/fees.go:5", "3F9C2AB"}
	if !slices.Equal(got, want) {
		t.Fatalf("SplitEvidence = %q, want %q", got, want)
	}
	ev, bad := factstale.ParseEvidence(got)
	if len(bad) != 0 || len(ev) != 5 || ev[2].Path != "rates.go" || ev[2].Near != "billing" || ev[0].Near != "" {
		t.Fatalf("ParseEvidence = %+v, bad %v", ev, bad)
	}
}

// TestCheckerReadsEvidenceWrittenFromElsewhere (bug hunt H-298): with a
// view of the work tree, a path written with the repo's folder in front
// (acme/billing/refund.go) and a bare file after a path in a folder
// (rates.go after billing/tax.go) are checked as the files the repo holds,
// so a change after the verified day flags the row; a path the repo holds
// under no reading, while another item is here, is listed in Unfound (the
// check could not run for it), never taken as fresh; a claim none of whose
// paths is here is about another repo's code: Unreachable, nothing listed.
// Without the view (no WithFiles) the old reading stays.
func TestCheckerReadsEvidenceWrittenFromElsewhere(t *testing.T) {
	r := newRepo(t)
	r.commit("2026-11-01", map[string]string{"billing/refund.go": "a", "billing/tax.go": "a", "billing/rates.go": "a"})
	r.commit("2026-11-10", map[string]string{"billing/refund.go": "b", "billing/rates.go": "b"})
	cl := claim(t, "2026-11-05", "acme/billing/refund.go:12-20,40; billing/tax.go:7 (origin/dev 1a2b3c4); rates.go:3; docs/gone.md:1")
	exists := func(p string) bool {
		_, err := os.Lstat(filepath.Join(r.dir, filepath.FromSlash(p)))
		return err == nil
	}
	ctx := context.Background()
	for _, prime := range []bool{false, true} {
		chk := r.checker().WithFiles(exists)
		if prime {
			chk.Prime(ctx, []factstale.Claim{cl})
		}
		v := chk.Check(ctx, cl)
		var paths []string
		for _, c := range v.Changed {
			paths = append(paths, c.Path)
		}
		if !v.Stale || !slices.Equal(paths, []string{"billing/refund.go", "billing/rates.go"}) {
			t.Fatalf("prime=%v: verdict %+v, want stale on billing/refund.go and billing/rates.go", prime, v)
		}
		if want := "evidence could not be checked (not in this repo: docs/gone.md)"; v.Unchecked() != want || v.Unreachable {
			t.Fatalf("prime=%v: Unchecked() = %q, want %q", prime, v.Unchecked(), want)
		}
	}
	other := claim(t, "2026-11-05", "web/app.ts:3; web/util.ts:9")
	if v := r.checker().WithFiles(exists).Check(ctx, other); v.Stale || !v.Unreachable || len(v.Unfound) != 0 || v.Unchecked() != "" {
		t.Fatalf("a claim about another repo's code: %+v, want Unreachable and nothing to print", v)
	}
	if v := r.checker().Check(ctx, cl); v.Stale || len(v.Unfound) != 0 {
		t.Fatalf("without a work tree view: %+v, want the paths asked as written (not stale, nothing listed)", v)
	}
}
