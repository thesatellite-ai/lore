package wire

import (
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
