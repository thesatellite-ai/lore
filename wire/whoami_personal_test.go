package wire

// whoami_personal_test.go — G-DECIDE-7 (later 562): `lore whoami` prints
// the personal store as one place, `personal  me -> <url or folder>`, or
// `not set`; never the v0.1.17 `agency/, everywhere/` folder split. The
// --json key is personal_store ("" when not set).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/orgresolve"
)

func TestWhoamiPersonalLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		config string // config.yaml ("" = none)
		mkdir  bool   // a v0.1.17 ~/.lore/personal folder exists
		want   string // "" = not set; "<home>" is replaced by the home
	}{
		{"not set", "", false, ""},
		{"git personal store", "personal:\n  store: git@github.com:alice/lore-me\n", false, "git@github.com:alice/lore-me"},
		{"folder personal store", "personal:\n  store: file:///srv/me\n", false, "file:///srv/me"},
		{"v0.1.17 local", "personal:\n  store: local\n", false, "<home>/" + personalDirV017},
		{"v0.1.17 folder on disk, no key", "", true, "<home>/" + personalDirV017},
		{"url wins over a leftover folder", "personal:\n  store: git@github.com:alice/lore-me\n", true, "git@github.com:alice/lore-me"},
		// The setup in effect holds its own store (what the setup page
		// saves): that store, not "not set" (journey solo/day1).
		{"the setup's own store", "setups:\n  - name: solo\n    kind: solo\n    default: true\n    personal_store: git@github.com:alice/lore-me\n", false, "git@github.com:alice/lore-me"},
		{"the setup's store wins over personal.store", "personal:\n  store: file:///srv/old\nsetups:\n  - name: solo\n    kind: solo\n    default: true\n    personal_store: git@github.com:alice/lore-me\n", false, "git@github.com:alice/lore-me"},
		{"inherit reads personal.store", "personal:\n  store: file:///srv/me\nsetups:\n  - name: solo\n    kind: solo\n    default: true\n    personal_store: inherit\n", false, "file:///srv/me"},
		{"none is not set", "personal:\n  store: file:///srv/me\nsetups:\n  - name: solo\n    kind: solo\n    default: true\n    personal_store: none\n", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			if c.config != "" {
				if err := os.WriteFile(configPath(home), []byte(c.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if c.mkdir {
				if err := os.MkdirAll(filepath.Join(home, personalDirV017), 0o750); err != nil {
					t.Fatal(err)
				}
			}
			want := strings.ReplaceAll(c.want, "<home>", home)
			got := whoamiPersonal(context.Background(), home, home)
			if got != want {
				t.Fatalf("whoamiPersonal = %q, want %q", got, want)
			}
			s := &orgcfg.Situation{Config: &orgconf.Config{}, Result: orgresolve.Result{Org: &orgconf.Org{Name: "agency"}, Role: orgresolve.RoleOwn}}
			lines := whoamiLines(s, identity.Resolved{DisplayName: "Alice", StableKey: "human:alice@agency.example"}, nil, false, whoamiRedactor{}, nil, "", got)
			line := ""
			for _, l := range lines {
				if strings.HasPrefix(l, "personal ") {
					line = strings.TrimSpace(strings.TrimPrefix(l, "personal"))
				}
			}
			wantLine := personalNotSet
			if want != "" {
				wantLine = "me -> " + want
			}
			if line != wantLine {
				t.Fatalf("personal line %q, want %q", line, wantLine)
			}
			if all := strings.Join(lines, "\n"); strings.Contains(all, orgconf.PersonalEverywhere+"/") || strings.Contains(all, "agency/,") {
				t.Fatalf("whoami still prints the folder split:\n%s", all)
			}
			if d := whoamiData(s, identity.Resolved{}, whoamiRedactor{}, nil, "", got); d.Personal != want {
				t.Fatalf("personal_store = %q, want %q", d.Personal, want)
			}
		})
	}
}
