package importscan

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// Coordinator finding (polish e2e, 2026-11-07): with every setup in the
// trash, Home's "already covered" line read "nothing to import, your
// setups already cover it: personal (setup work in the trash (lore setup
// trash restore work) has the same identity)" — "your setups" while no
// setup is active, and parentheses inside parentheses. Each Covered line
// names the covering setup, its state once, why, and the way back; the
// banner says nothing about "your setups".
func TestRegress_CoveredLineNamesStateOnce(t *testing.T) {
	const other = "git@github-personal:someone/else.git"
	match := map[string]string{
		// identity key equal to config.yaml's, another store
		"identity": "personal_store: " + other + "\n{i}identity:\n{i}  key: human:pat\n",
		// another identity, config.yaml's personal.store
		"store": "personal_store: git@github-personal:octo-personal/notes.git\n{i}identity:\n{i}  key: human:other@example.test\n",
	}
	why := map[string]string{"identity": "already has the same identity", "store": "already uses the same personal store"}
	states := []struct {
		name, prefix, indent, extra, want string
	}{
		{"active", "setups:\n  - name: work\n    kind: solo\n    ", "    ", "", "work {why}"},
		{"disabled", "setups:\n  - name: work\n    kind: solo\n    disabled: true\n    ", "    ", "", "work (disabled) {why}; enable it with: lore setup enable work"},
		{"trashed", "trash:\n  setups:\n    - name: work\n      kind: solo\n      ", "      ", "      removed_at: \"2026-11-06T10:00:00Z\"\n", "work (in the trash) {why}; restore it with: lore setup trash restore work"},
	}
	for _, st := range states {
		for m, body := range match {
			t.Run(st.name+" same "+m, func(t *testing.T) {
				w := newWorld(t)
				write(t, filepath.Join(w.lore, ConfigFile), configFull+st.prefix+strings.ReplaceAll(body, "{i}", st.indent)+st.extra)
				res := scan(t, w)
				for _, p := range res.Proposals {
					if p.Entry.Kind == onboard.KindSolo {
						t.Fatalf("solo proposed next to a covering setup: %+v", p.Entry)
					}
				}
				want := strings.ReplaceAll(st.want, "{why}", why[m])
				var solo string
				for _, c := range res.Covered {
					if strings.HasPrefix(c, "work ") {
						solo = c
					}
				}
				if solo != want {
					t.Fatalf("covered %q, want %q", res.Covered, want)
				}
				banner := onboard.ImportBanner(onboard.BannerFacts{Legacy: true, LegacyFiles: []string{"~/.lore/identity.toml"}, Covered: []string{solo}})
				if strings.Contains(banner, "your setups") || strings.Contains(banner, "((") || strings.Count(banner, "(") > 2 {
					t.Fatalf("banner %q: says your setups or nests parentheses", banner)
				}
				if wantB := "Found an existing lore install (~/.lore/identity.toml): nothing to import — " + want + "."; banner != wantB {
					t.Fatalf("banner\n %q\nwant\n %q", banner, wantB)
				}
			})
		}
	}
}
