package contextfit

import (
	"slices"
	"testing"
)

// TestFloorAndPathFloor (devco D-815): one shared word picks a row only
// for a short text with no row holding two; a note not scoped to a path's
// folders names every path word, up to two.
func TestFloorAndPathFloor(t *testing.T) {
	five := Words("retry failed invoice emails from the queue")
	if len(five) < 3 || Floor(five, 1) != 2 || Floor(five, 3) != 2 {
		t.Errorf("a %d-word text: floor %d/%d, want 2", len(five), Floor(five, 1), Floor(five, 3))
	}
	two := Words("how is the ledger stored")
	if Floor(two, 1) != 1 || Floor(two, 2) != 2 {
		t.Errorf("%q: floor %d (best 1) %d (best 2), want 1 2", two, Floor(two, 1), Floor(two, 2))
	}
	if got := PathFloor(PathWords("billing/invoice.go")); got != 2 {
		t.Errorf("billing/invoice.go: path floor %d, want 2", got)
	}
	if got := PathFloor(PathWords("src/pay/ledger.go")); got != 1 {
		t.Errorf("src/pay/ledger.go: path floor %d, want 1", got)
	}
	// lore's own rows named in a request are no topic
	if w := Words("see the ledger notes and rules"); !slices.Equal(w, []string{"ledger"}) {
		t.Errorf("words %q, want [ledger]", w)
	}
}

// TestSemanticWords (devco D-815): a three-letter topic word reaches a
// semantic rule's description; a request's verbs and numbers do not.
func TestSemanticWords(t *testing.T) {
	got := SemanticWords("log failed login attempts, add a fix for sql in 10m")
	for _, w := range []string{"log", "sql", "failed", "login", "attempts"} {
		if !slices.Contains(got, w) {
			t.Errorf("%q misses %q", got, w)
		}
	}
	for _, w := range []string{"add", "fix", "for", "10m"} {
		if slices.Contains(got, w) {
			t.Errorf("%q holds %q", got, w)
		}
	}
	if Score("logging, logs, logger, log lines", []string{"log"}) == 0 {
		t.Error(`"log" does not match the logging description`)
	}
}

// TestPathWordsMonorepoFolders (devco D-830): a monorepo's container
// folders name no topic, so a note on the app or package itself meets
// the path floor (D-815) without naming "apps" or "packages".
func TestPathWordsMonorepoFolders(t *testing.T) {
	for p, want := range map[string][]string{
		"apps/app1/main.go":           {"app1"},
		"packages/ledger/index.ts":    {"ledger"},
		"billing/invoice.go":          {"billing", "invoice"},
		"apps/web/src/billing/tax.ts": {"billing"},
	} {
		got := PathWords(p)
		if !slices.Equal(got, want) {
			t.Errorf("PathWords(%s) = %q, want %q", p, got, want)
		}
		if PathFloor(got) > len(want) {
			t.Errorf("%s: path floor %d over its %d words", p, PathFloor(got), len(want))
		}
	}
	if Score("app1 runs locally on port 7400", PathWords("apps/app1/main.go")) < PathFloor(PathWords("apps/app1/main.go")) {
		t.Error("a note on app1 does not meet the floor of apps/app1/main.go")
	}
}
