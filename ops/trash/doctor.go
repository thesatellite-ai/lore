package trash

import (
	"context"
	"strconv"
)

// Doctor is the trash part of `lore doctor` (DELETION 3.8, 3.5, build step
// 9): a warning when rows are past trash.retention_days (it names the one
// command that purges them; nothing purges in the background, DQ3) and a
// note with the trash and tombstone counts (tombstones are kept forever,
// DQ4). It reads only.
func Doctor(ctx context.Context, env Env) (warnings, notes []string, err error) {
	st, err := env.TrashStats(ctx)
	if err != nil {
		return nil, nil, err
	}
	if st.Expired > 0 {
		warnings = append(warnings, "trash: "+strconv.Itoa(st.Expired)+" rows past trash.retention_days; purge with lore trash purge --expired --confirm")
	}
	tombs := 0
	for _, n := range st.Tombstones {
		tombs += n
	}
	if st.Rows > 0 || tombs > 0 {
		notes = append(notes, "trash: "+strconv.Itoa(st.Rows)+" rows in the trash, "+strconv.Itoa(tombs)+" tombstones (kept forever); lore trash stats")
	}
	return warnings, notes, nil
}
