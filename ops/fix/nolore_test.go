package fix

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestAllCountsReposWithoutLoreData (K-10): fix --all said "2 repo(s)"
// and nothing about the eight repos with no lore data in its folders.
// The summary counts them (and points at --verbose); with --verbose each
// gets its `skipped <repo>: no lore data …` line (docs/operations/
// upgrade.md); JSON always names them. --verbose without --all is refused.
func TestAllCountsReposWithoutLoreData(t *testing.T) {
	ctx := context.Background()
	noLore := []string{"/w/jeoga", "/w/lath"}
	out, err := Run(ctx, &fakeEnv{roots: []string{"/w/a"}, noLore: noLore}, RunIn{All: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.NoLore, ",") != strings.Join(noLore, ",") {
		t.Fatalf("no_lore %v, want %v", out.NoLore, noLore)
	}
	text := out.Text()
	if !strings.Contains(text, "; 2 repo(s) without lore data left as they are (--verbose lists them)") || strings.Contains(text, "skipped /w/lath") {
		t.Fatalf("text without --verbose:\n%s", text)
	}
	out, err = Run(ctx, &fakeEnv{roots: []string{"/w/a"}, noLore: noLore}, RunIn{All: true, DryRun: true, Verbose: true})
	if err != nil {
		t.Fatal(err)
	}
	text = out.Text()
	for _, r := range noLore {
		if !strings.Contains(text, NoLoreLine(r)+"\n") {
			t.Fatalf("--verbose text lacks %q:\n%s", NoLoreLine(r), text)
		}
	}
	if !strings.HasPrefix(NoLoreLine("/w/lath"), "skipped /w/lath: no lore data") {
		t.Fatalf("line %q is not the documented shape", NoLoreLine("/w/lath"))
	}
	if strings.Contains(text, "(--verbose lists them)") {
		t.Fatalf("--verbose text still points at --verbose:\n%s", text)
	}
	if _, err = Run(ctx, &fakeEnv{}, RunIn{Verbose: true}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("--verbose without --all: %v, want E_INVALID_INPUT", err)
	}
}
