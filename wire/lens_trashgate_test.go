package wire

// `lore sql` fills its trash table from the row files of every store,
// which reads every row: only a query that names the table pays it (devco
// D-790: `select 1` on a store with a 5,000-row folder took seconds).
// queryNamesTrash must see every spelling SQLite accepts for the name.

import "testing"

func TestQueryNamesTrash(t *testing.T) {
	t.Parallel()
	for q, want := range map[string]bool{
		`select 1`:                                  false,
		`SELECT id FROM memories_all`:               false,
		`select id from trash`:                      true,
		`SELECT * FROM TRASH`:                       true,
		`select * from "trash"`:                     true,
		"select * from `Trash`":                     true,
		`select * from [trash] where id = 'x'`:      true,
		`with t as (select * from trash) select 1`:  true,
		`select count(*) from main.memories, trash`: true,
	} {
		if got := queryNamesTrash(q); got != want {
			t.Errorf("queryNamesTrash(%q) = %v, want %v", q, got, want)
		}
	}
}
