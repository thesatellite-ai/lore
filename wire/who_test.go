package wire

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestWhoFromAuditFile folds the fallback audit file per session: foreign
// rows, torn lines and session-less events are skipped; a session is
// confirmed when any of its writes was; a missing file is no lines.
func TestWhoFromAuditFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	body := `{"action":"create","id":"tsk_1","session_id":"ses_a"}
{"action":"update","id":"tsk_2","session_id":"ses_b"}
not json
{"action":"update","id":"tsk_1"}
{"action":"update","id":"tsk_1","session_id":"ses_c"}
{"action":"update","id":"tsk_1","session_id":"ses_a","confirmed":true}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := whoFromAuditFile(path, "tsk_1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"session ses_a  tool -  model -  confirmed yes", "session ses_c  tool -  model -  confirmed no"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
	if got, err := whoFromAuditFile(filepath.Join(t.TempDir(), "none"), "tsk_1"); err != nil || got != nil {
		t.Fatalf("missing file: %v %v", got, err)
	}
}
