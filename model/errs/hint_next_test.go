package errs

import "testing"

// TestHintFromNext: an error with a next command and no hint prints
// "run <command>" as its hint; a hint given stays; no next, no hint.
func TestHintFromNext(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *Error
		want string
	}{
		{"next only", New(InvalidInput, "x").WithNext("lore", "setup", "list"), "run lore setup list"},
		{"hint wins", New(InvalidInput, "x").WithHint("do this").WithNext("lore", "setup", "list"), "do this"},
		{"neither", New(InvalidInput, "x"), ""},
		{"add-args only", New(InvalidInput, "x").WithAddArgs([]string{"--yes"}), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EnvelopeOf(tc.err).Error.Hint; got != tc.want {
				t.Errorf("hint %q, want %q", got, tc.want)
			}
		})
	}
}
