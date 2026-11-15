package engine_test

import (
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
)

// TestBornBranch: --born-branch values are checked (the flag named in
// the refusal) and BornOn reads the row's provenance (D-392).
func TestBornBranch(t *testing.T) {
	for _, ok := range []string{"feat/x", " feat/x ", "release-1.2", ""} {
		if _, err := engine.ParseBornBranch(ok); err != nil {
			t.Fatalf("ParseBornBranch(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"feat x", "a:b", "a~1", "a^", "a\\b", "a\tb"} {
		_, err := engine.ParseBornBranch(bad)
		if !errs.Has(err, errs.InvalidInput) {
			t.Fatalf("ParseBornBranch(%q) = %v, want E_INVALID_INPUT", bad, err)
		}
	}
	o, err := origin.Build(origin.Facts{Cmd: "task add", Git: origin.Git{State: origin.GitBranch, Branch: "feat/x", Commit: "abc"}})
	if err != nil {
		t.Fatal(err)
	}
	r := row.Row{Origin: o}
	if !engine.BornOn(r, "feat/x") || engine.BornOn(r, "feat/y") || engine.BornOn(r, "") || engine.BornOn(row.Row{}, "feat/x") {
		t.Fatal("BornOn does not follow _o.git.branch")
	}
}
