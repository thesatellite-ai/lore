package app

import (
	"strings"
	"testing"
)

// TestBareCallHintsNamedForm pins G-13: lore land run, lore resource delete
// and lore task reassign with nothing named refuse with exit 20 and a hint
// line that gives the named form (U-4: --id <id>, --assign "@handle"), not
// a sentence about an unnamed row id or @handle. Their usage shows the
// value as optional ([--id <id>]), so the userscen tree walk of bare
// required leaves no longer reaches them; this test does.
func TestBareCallHintsNamedForm(t *testing.T) {
	w := newCLIWorld(t).person()
	repo := w.initRepo(t)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"land", "run"}, []string{"name a row with --id", "hint: lore land run --id <id>"}},
		{[]string{"resource", "delete"}, []string{"name the resource with --id", "hint: lore resource delete --id <name or res_ id>"}},
		{[]string{"task", "reassign", "--id", "tsk_01a1d4d2aa887eb2ab289c9947151bb3"}, []string{"--assign is required", `hint: lore task reassign --id <id> --assign "@handle"`}},
	} {
		code, stdout, stderr := w.lore(t, repo, tc.args...)
		if code != 20 || stdout != "" {
			t.Errorf("lore %s: exit %d, stdout %q; want 20 and nothing on stdout (stderr %s)", strings.Join(tc.args, " "), code, stdout, stderr)
		}
		for _, s := range tc.want {
			if !strings.Contains(stderr, s) {
				t.Errorf("lore %s: stderr lacks %q:\n%s", strings.Join(tc.args, " "), s, stderr)
			}
		}
	}
}
