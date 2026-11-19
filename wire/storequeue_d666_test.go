package wire

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/logic/queue"
	"github.com/thesatellite-ai/lore/logic/urgency"
)

// TestReadLinesAtNamesScopeD666: task next of the store task queue
// (--setup from home, --store anywhere) prints its next steps with the
// same flag, so each works where printed; a claim by id is offered only
// where it reaches that store (devco:D-666).
func TestReadLinesAtNamesScopeD666(t *testing.T) {
	t.Parallel()
	const id = "tsk_01a1efc2ff11702f9b4cceb28ffcb754"
	q := queue.Result{Ready: []queue.Task{{ID: id, Title: "ship"}}, Scores: map[string]urgency.Score{id: {}}}
	got := strings.Join(readLinesAt(coord.Peek{Queue: q}, false, " --push", " --setup acme-admin", true), "\n")
	if !strings.Contains(got, "take it: lore task next --claim --push --setup acme-admin (or lore task claim --id "+id+" --push)") {
		t.Errorf("store queue read = %q, want task next with --push --setup and the claim by id", got)
	}
	got = strings.Join(readLinesAt(coord.Peek{Queue: q}, false, "", " --store acme", false), "\n")
	if !strings.Contains(got, "take it: lore task next --claim --store acme") || strings.Contains(got, "task claim --id") {
		t.Errorf("store queue read in a code repo = %q, want task next --store and no claim by id", got)
	}
	got = strings.Join(readLinesAt(coord.Peek{Holding: id}, false, " --push", " --store acme/web", true), "\n")
	if !strings.Contains(got, "lore task next --claim --push --store acme/web resumes it") {
		t.Errorf("store queue holding = %q, want the resume with --store", got)
	}
}
