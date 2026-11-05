package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
)

// TestMigrateKeepsLegacyIdentityAside pins OWNERQ-8: `lore migrate run`
// copies a v0.1.17 ~/.lore/identity.yaml into identity: of config.yaml (or
// keeps an identity: already set) and moves the old file aside as
// identity.yaml.migrated with its bytes unchanged; it never deletes it.
// A dry run moves nothing, and a second run has no identity step.
func TestMigrateKeepsLegacyIdentityAside(t *testing.T) {
	const legacyDoc = "stable_key: human:old@x.io\ndisplay_name: Old\n"
	for _, preset := range []bool{false, true} {
		name := "no identity in config.yaml"
		if preset {
			name = "config.yaml already sets identity"
		}
		t.Run(name, func(t *testing.T) {
			ctx := background()
			a := newTestApp(t, gitRepo(t))
			home := a.opts.Home
			if preset {
				if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte("identity:\n  key: human:new@x.io\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			legacy := filepath.Join(home, identity.LegacyFile)
			if err := os.WriteFile(legacy, []byte(legacyDoc), 0o600); err != nil {
				t.Fatal(err)
			}
			dry, err := a.MigrateSettings(ctx, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, serr := os.Stat(legacy); serr != nil {
				t.Fatalf("a dry run must leave identity.yaml in place: %v", serr)
			}
			got, err := a.MigrateSettings(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, steps := range [][]string{dry.Steps, got.Steps} {
				if !strings.Contains(strings.Join(steps, "\n"), identity.LegacyFile+LegacyMigratedSuffix) {
					t.Fatalf("the identity step must name the kept file: %v", steps)
				}
			}
			if _, serr := os.Stat(legacy); !os.IsNotExist(serr) {
				t.Fatal("identity.yaml is still in place after the run")
			}
			kept, err := os.ReadFile(filepath.Clean(legacy + LegacyMigratedSuffix))
			if err != nil || string(kept) != legacyDoc {
				t.Fatalf("identity.yaml.migrated = %q, %v", kept, err)
			}
			mc, err := readMachineConfig(home)
			if err != nil {
				t.Fatal(err)
			}
			want := "human:old@x.io"
			if preset {
				want = "human:new@x.io"
			}
			if mc.Identity.Key != want {
				t.Fatalf("identity = %q, want %q", mc.Identity.Key, want)
			}
			again, err := a.MigrateSettings(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.Join(again.Steps, "\n"), "identity:") {
				t.Fatalf("a second run must have no identity step: %v", again.Steps)
			}
		})
	}
}
