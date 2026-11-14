package wire

import (
	"strings"
	"testing"

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestPushModeLineNamesWhatTheStoreWaitsFor (I-1): a store whose push
// waits on an open sync conflict names the commands that settle it, never
// "`lore sync` pushes them" (sync refuses until the conflict is settled);
// a store with nothing to settle keeps the plain line.
func TestPushModeLineNamesWhatTheStoreWaitsFor(t *testing.T) {
	t.Parallel()
	v := opsstores.PushView{Store: "me", Summary: "push manual, compact off", Unpushed: 2}
	if got := pushModeLine(v, ""); !strings.HasSuffix(got, "(2 local commit(s) waiting; `lore sync` pushes them)") {
		t.Errorf("no conflict: %q", got)
	}
	got := pushModeLine(v, storeConflictPushNote(1))
	if strings.Contains(got, "; `lore sync` pushes them") || !strings.Contains(got, "held by 1 open sync conflict(s)") || !strings.Contains(got, "lore sync resolve --id <n> --take kept|other") {
		t.Errorf("with a conflict: %q", got)
	}
	if line := storeConflictDoctorLine("acme", 2); !strings.Contains(line, "store acme: 2 open sync conflict(s)") || !strings.Contains(line, "lore sync conflicts") {
		t.Errorf("doctor line: %q", line)
	}
}
