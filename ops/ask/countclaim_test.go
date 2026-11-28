package ask

// countclaim_test.go — devco D-1035 (bug hunt H-296, team-store
// benchmark set 10 ask f): the docs said ask prints "up to three rows"
// in full while an answer of short rows printed four to six within the
// 8,000-character budget. How many rows an answer prints in full is
// decided by bytes (--max-chars) and capped by --top, never by a fixed
// count: no page, skill or flag text may claim one.

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// rowCountClaim matches a fixed count of rows printed in full: "up to
// three rows", "until 3 rows are printed", "stay past three", "the best
// 3 rows", "four or five rows in full".
var rowCountClaim = regexp.MustCompile(`(?i)\b(up to|until|at most|the best) (three|3) rows\b|\bstay past (three|3)\b|\bfour or five rows\b`)

func TestNoFixedRowCountClaimed(t *testing.T) {
	t.Parallel()
	for _, p := range []string{
		filepath.Join("..", "..", "docs", "knowledge", "search.md"),
		filepath.Join("..", "..", "skills", "COMMANDS.md"),
		filepath.Join("..", "..", "skills", "SKILL.md"),
		filepath.Join("..", "..", "skills", "SKILL-mini.md"),
	} {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, "lore ask") && !strings.Contains(line, "`--max-chars`") {
				continue // only what describes ask's answer
			}
			if m := rowCountClaim.FindString(line); m != "" {
				t.Errorf("%s:%d claims a fixed count of rows in full (%q); bytes decide (--max-chars, at most --top)", p, i+1, m)
			}
		}
	}
	rt := reflect.TypeFor[RunIn]()
	for i := range rt.NumField() {
		if m := rowCountClaim.FindString(rt.Field(i).Tag.Get("desc")); m != "" {
			t.Errorf("ask flag %s claims a fixed count of rows (%q)", rt.Field(i).Name, m)
		}
	}
}
