package migrate_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/ops/migrate"
)

// TestStatusTextOmitsEmptyLists (#22c): migrate status prints no empty
// "pending: ", "notes: " or "features: " line, prints each list when it
// has items, and --json keeps every key (an empty list stays []).
func TestStatusTextOmitsEmptyLists(t *testing.T) {
	t.Parallel()
	places := []migrate.PlaceStatus{{Place: "repo", V2: 4, Total: 4}, {Place: "client2", V1: 1, V2: 3, Total: 4}}
	for _, c := range []struct {
		name              string
		features, pending []string
		notes             []string
		journal, minLore  string
		want, absent      []string
	}{
		{name: "all empty (Mode B repo)", features: []string{}, pending: []string{}, notes: []string{},
			want: []string{"writes: 2\n", "lock: free\n"}, absent: []string{"features:", "pending:", "notes:", "journal:", "min_lore_version:"}},
		{name: "features only", features: []string{"hlc", "policy"}, pending: []string{}, notes: []string{}, minLore: "0.1.18",
			want: []string{"features: hlc, policy\n", "min_lore_version: 0.1.18\n"}, absent: []string{"pending:", "notes:"}},
		{name: "pending and notes", features: []string{"hlc"}, pending: []string{"tasks: 3 rows at v1", "notes: 1 row at v1"},
			notes: []string{"a note"}, journal: "last migration 2026-11-02",
			want: []string{"pending: tasks: 3 rows at v1, notes: 1 row at v1\n", "notes: a note\n", "journal: last migration 2026-11-02\n"}},
		{name: "nil lists", want: []string{"writes: 2\n"}, absent: []string{"features:", "pending:", "notes:"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out := migrate.StatusOut{Writes: 2, Places: places, Features: c.features, MinLoreVersion: c.minLore,
				Pending: c.pending, Journal: c.journal, Lock: "free", Notes: c.notes}
			got := out.Text()
			for _, w := range append(c.want, "places:\n  PLACE    V1  V2  NEWER  TOTAL\n  repo     0   4   0      4\n  client2  1   3   0      4\n") {
				if !strings.Contains(got, w) {
					t.Errorf("text lacks %q:\n%s", w, got)
				}
			}
			for _, a := range c.absent {
				if strings.Contains(got, a) {
					t.Errorf("text has %q:\n%s", a, got)
				}
			}
			if strings.Contains(got, ": \n") {
				t.Errorf("an empty line:\n%s", got)
			}
		})
	}
	b, err := json.Marshal(migrate.StatusOut{Writes: 2, Places: []migrate.PlaceStatus{}, Features: []string{}, Pending: []string{}, Lock: "free", Notes: []string{}})
	if err != nil || string(b) != `{"writes":2,"places":[],"features":[],"pending":[],"lock":"free","notes":[]}` {
		t.Errorf("json %s, %v", b, err)
	}
}
