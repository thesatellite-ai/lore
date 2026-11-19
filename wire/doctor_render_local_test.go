package wire

import (
	"strings"
	"testing"

	opsrender "github.com/thesatellite-ai/lore/ops/render"
)

// TestRenderedCopyLocalMissingIsANote: a private render file (or a tool's
// untracked import file) missing on this machine is a note, since lore
// writes it on its next render; the same file stale or edited, and a
// missing repo copy, stay warnings (B3-25).
func TestRenderedCopyLocalMissingIsANote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		f    opsrender.CopyFinding
		want DoctorLevel
	}{
		{"local missing", opsrender.CopyFinding{Kind: opsrender.CopyMissing, Local: true}, DoctorNote},
		{"local stale", opsrender.CopyFinding{Kind: opsrender.CopyStale, Local: true}, DoctorWarning},
		{"local edited", opsrender.CopyFinding{Kind: opsrender.CopyEdited, Local: true}, DoctorWarning},
		{"repo copy missing", opsrender.CopyFinding{Kind: opsrender.CopyMissing}, DoctorWarning},
	}
	for _, c := range cases {
		if got := renderedCopyFinding(c.f); got.Level != c.want || got.Check != DoctorCheckRenderedCopy {
			t.Errorf("%s: %+v, want level %s", c.name, got, c.want)
		}
	}
}

// TestGeneratedPointerIsANote (devco L-13): a tool file another tool
// generates is left alone by render on purpose, so doctor notes it with
// the steps that wire the pointer instead of staying DEGRADED forever; a
// plain missing pointer stays a warning.
func TestGeneratedPointerIsANote(t *testing.T) {
	t.Parallel()
	gen := renderedCopyFinding(opsrender.CopyFinding{Kind: opsrender.PointerGenerated, Fix: "lore render --add-target codex"})
	if gen.Level != DoctorNote || gen.Check != DoctorCheckToolPointer {
		t.Errorf("generated pointer file: %+v, want a tool_pointer note", gen)
	}
	if miss := renderedCopyFinding(opsrender.CopyFinding{Kind: opsrender.PointerMissing}); miss.Level != DoctorWarning {
		t.Errorf("missing pointer: %+v, want a warning", miss)
	}
}

// TestStoreOfflineNote (devco L-10, L-11): an offline store's line names
// the remote and git's reason, and a store that sent its commits in the
// same run never says its local changes were kept.
func TestStoreOfflineNote(t *testing.T) {
	t.Parallel()
	const url = "ssh://git.example.test/acme/team-store.git"
	const stderr = "ssh: connect to host git.example.test port 22: Connection timed out\nfatal: Could not read from remote repository.\n"
	got := storeOfflineNote(0, url, stderr)
	if got != " (offline: "+url+" did not answer: ssh: connect to host git.example.test port 22: Connection timed out; local changes kept, sent on the next sync)" {
		t.Errorf("offline, nothing sent: %q", got)
	}
	sent := storeOfflineNote(1, url, stderr)
	if strings.Contains(sent, "local changes kept") || !strings.Contains(sent, "sent; then "+url+" did not answer") {
		t.Errorf("sent then offline: %q", sent)
	}
}
