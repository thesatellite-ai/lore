package migrate

import (
	"strings"
	"testing"
)

// TestRunOutText pins the human form of `lore migrate run` (56e7199e,
// FORMAT_EVOLUTION 4 example): a count line first (nothing / 1 row / N
// rows, "Would upgrade" on a dry run), each row then each settings step
// indented on its own line, then the journal lines unindented, a dry run
// ending "Nothing written (dry run).", and never a "rows:" or "steps:"
// line (the generic dump it replaced printed them empty).
func TestRunOutText(t *testing.T) {
	cases := []struct {
		name string
		out  RunOut
		want string
	}{
		{"nothing", RunOut{}, "Nothing to migrate: every row is already in the current format.\n"},
		{"nothing, dry run", RunOut{DryRun: true}, "Nothing to migrate: every row is already in the current format.\nNothing written (dry run).\n"},
		{"nothing with journal", RunOut{Journal: []string{"team lock: none taken", "team lock released"}},
			"Nothing to migrate: every row is already in the current format.\nteam lock: none taken\nteam lock released\n"},
		{"one row", RunOut{Count: 1, Rows: []string{"memories/mem_1"}}, "Upgraded 1 row\n  memories/mem_1\n"},
		{"one row, dry run", RunOut{DryRun: true, Count: 1, Rows: []string{"memories/mem_1"}}, "Would upgrade 1 row\n  memories/mem_1\nNothing written (dry run).\n"},
		{"many, order rows steps journal", RunOut{Count: 3, Rows: []string{"tasks/tsk_1", "tasks/tsk_2"}, Steps: []string{"tags_snake_case: 1 tag"}, Journal: []string{"backup: refs/lore-backup/migrate/x", "undo: lore migrate rollback"}, BackupRef: "refs/lore-backup/migrate/x"},
			"Upgraded 3 rows\n  tasks/tsk_1\n  tasks/tsk_2\n  tags_snake_case: 1 tag\nbackup: refs/lore-backup/migrate/x\nundo: lore migrate rollback\n"},
		// a settings-only run counts its rows through Steps: still a count line, not "nothing"
		{"steps only", RunOut{Count: 2, Steps: []string{"personal_flat: 2 rows"}}, "Upgraded 2 rows\n  personal_flat: 2 rows\n"},
		// a step that changed settings but moved no rows is not "nothing"
		{"step, count 0", RunOut{Steps: []string{"identity: moved aside"}}, "Upgraded 0 rows\n  identity: moved aside\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.out.Text()
			if got != tc.want {
				t.Fatalf("Text() =\n%q\nwant\n%q", got, tc.want)
			}
			for _, bad := range []string{"rows:", "steps:", "count:"} {
				if strings.Contains(got, bad) {
					t.Fatalf("Text() carries a %q line: %q", bad, got)
				}
			}
		})
	}
}
