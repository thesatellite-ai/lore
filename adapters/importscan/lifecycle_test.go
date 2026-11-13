package importscan

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTrashedAndDisabledCover: a setup the person disabled or moved to the
// trash still covers its proposal (owner decision 2026-11-06: the way back
// is enable / trash restore, never a new import), and the Covered line
// says which and how to get it back.
func TestTrashedAndDisabledCover(t *testing.T) {
	cases := []struct {
		name, body string
		hidden     []string
		covered    string
	}{
		{"trashed solo hides personal",
			"trash:\n  setups:\n    - name: personal\n      kind: solo\n      removed_at: \"2026-11-06T10:00:00Z\"\n",
			[]string{"personal", "personal-2"}, "personal (in the trash) already uses the same personal store; restore it with: lore setup trash restore --name personal"},
		{"disabled solo hides personal",
			"setups:\n  - name: mine\n    kind: solo\n    disabled: true\n",
			[]string{"personal"}, "mine (disabled) already uses the same personal store; enable it with: lore setup enable --name mine"},
		{"trashed member hides its org",
			"trash:\n  setups:\n    - name: work\n      kind: member\n      org: acme\n      removed_at: \"2026-11-06T10:00:00Z\"\n",
			[]string{"acme"}, "work (in the trash) already uses org acme; restore it with: lore setup trash restore --name work"},
		{"disabled member hides its org",
			"setups:\n  - name: work\n    kind: member\n    org: acme\n    disabled: true\n",
			[]string{"acme"}, "work (disabled) already uses org acme; enable it with: lore setup enable --name work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			write(t, filepath.Join(w.lore, ConfigFile), configFull+tc.body)
			res := scan(t, w)
			for _, h := range tc.hidden {
				if _, ok := proposal(res, h); ok {
					t.Errorf("%s proposed: %+v", h, res.Proposals)
				}
			}
			if !strings.Contains(strings.Join(res.Covered, "\n"), tc.covered) {
				t.Errorf("covered %v, want a line with %q", res.Covered, tc.covered)
			}
		})
	}
}

// TestTrashedDefaultDoesNotBlockDefault: a trashed default no longer
// counts as the default, so the solo proposal may take it.
func TestTrashedDefaultDoesNotBlockDefault(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.lore, ConfigFile), configFull+
		"trash:\n  setups:\n    - name: work\n      kind: member\n      org: acme\n      default: true\n      removed_at: \"2026-11-06T10:00:00Z\"\n")
	p, ok := proposal(scan(t, w), "personal")
	if !ok || !p.Entry.Default {
		t.Fatalf("personal must be proposed as the default: %+v", p)
	}
}
