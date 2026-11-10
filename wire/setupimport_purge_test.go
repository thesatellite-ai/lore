package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/importscan"
	"github.com/thesatellite-ai/lore/logic/onboard"
)

// purgeConfig is config.yaml as the identity.yaml → config.yaml migration
// leaves it, plus the solo setup "personal" and a member setup of acme.
const purgeConfig = `identity: {key: "human:pat@example.test", name: Pat Example}
personal: {store: "git@github.com:example/lore-me.git"}
orgs:
  - name: acme
    identity: {key: "human:pat@acme.example"}
    stores:
      - url: git@github.com:acme/lore-store.git
setups:
  - name: personal
    kind: solo
    default: true
    folders: [~/code/**]
  - name: work
    kind: member
    org: acme
    folders: [~/work/**]
`

// The *.migrated backups the migration kept: the same person as config.yaml.
const (
	purgeYAMLBackup = "display_name: Pat Example\nstable_key: human:pat@example.test\n"
	purgeTOMLBackup = "display_name = \"Pat Example\"\nstable_key = \"human:pat@example.test\"\n"
)

// TestPurgedSetupNeverReoffered reproduces the owner's report (2026-11-07):
// a migrated install, the setup personal moved to the trash and deleted for
// good, and Home offering "import 1 setup (personal)" from
// identity.yaml.migrated. After a purge no discovery source (config.yaml's
// identity and store, the *.migrated backups, a scanned repo's stores.yaml
// naming the old store) may propose the deleted setup again, with or
// without the purge's clears; a different person's old file still is.
func TestPurgedSetupNeverReoffered(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		purge []string // setups to trash and purge, in order
		opts  PurgeOptions
		// extra writes files under the lore home before the scan.
		extra map[string]string
		// want are the proposals expected afterwards; hidden must not appear.
		want, hidden []string
		covered      []string
	}{
		{name: "owner's case: purge keeps identity and store", purge: []string{"personal"},
			hidden: []string{"personal", "personal-2"}, covered: []string{"personal (deleted for good)"}},
		{name: "purge with clears, the store comes back from a repo", purge: []string{"personal"},
			opts:   PurgeOptions{ClearIdentity: true, ClearPersonalStore: true},
			hidden: []string{"personal", "personal-2"}, covered: []string{"personal (deleted for good)"}},
		// Purging the member setup also removes its orgs[] entry (no other
		// setup names acme; owner decision 2026-11-08), so nothing is left to
		// offer or to explain.
		{name: "member purged: its org is not offered", purge: []string{"work"},
			hidden: []string{"acme", "work"}},
		{name: "both purged", purge: []string{"personal", "work"}, opts: PurgeOptions{ClearIdentity: true},
			hidden: []string{"personal", "acme"}},
		{name: "control: another person's old identity file is still offered", purge: []string{"personal"},
			opts:  PurgeOptions{ClearIdentity: true, ClearPersonalStore: true},
			extra: map[string]string{importscan.IdentityYAML: "display_name: Sam Other\nstable_key: human:sam@example.test\n"},
			want:  []string{"personal"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := writeSetupsConfig(t, purgeConfig)
			user := t.TempDir()
			writeAt(t, filepath.Join(home, importscan.IdentityYAML+importscan.MigratedSuffix), purgeYAMLBackup)
			writeAt(t, filepath.Join(home, importscan.IdentityTOML+importscan.MigratedSuffix), purgeTOMLBackup)
			for n, b := range tc.extra {
				writeAt(t, filepath.Join(home, n), b)
			}
			// A lore repo whose stores.yaml names the old personal store.
			repoRoot := filepath.Join(user, "code", "notes")
			writeAt(t, filepath.Join(repoRoot, ".lore", "stores.yaml"), "stores:\n  - name: me\n    url: git@github.com:example/lore-me.git\n")

			for _, n := range tc.purge {
				if _, err := TrashSetup(home, n, lifeNow); err != nil {
					t.Fatal(err)
				}
				if _, err := PurgeSetup(home, n, PurgeOptions{ClearIdentity: tc.opts.ClearIdentity, ClearPersonalStore: tc.opts.ClearPersonalStore, At: lifeNow}); err != nil {
					t.Fatal(err)
				}
			}
			raw := readSetupsFile(t, home)
			if strings.Contains(raw, "lore-me.git") == tc.opts.ClearPersonalStore {
				t.Fatalf("personal.store clear=%v not honoured (a tombstone must not keep the url):\n%s", tc.opts.ClearPersonalStore, raw)
			}
			if tc.opts.ClearIdentity && strings.Contains(raw, "human:pat@example.test") {
				t.Fatalf("a cleared identity key is still in the file:\n%s", raw)
			}
			if u := configUnknownKeys([]byte(raw)); len(u) > 0 {
				t.Fatalf("lore's own trash keys read as unknown: %v", u)
			}

			res, err := importscan.Scan(context.Background(), importscan.Options{LoreHome: home, Home: user, Folders: []string{user}, TempDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, p := range res.Proposals {
				got[p.Entry.Name] = true
			}
			for _, h := range tc.hidden {
				if got[h] {
					t.Errorf("deleted setup offered again as %q: proposals %v, covered %v", h, proposalNames(res), res.Covered)
				}
			}
			for _, w := range tc.want {
				if !got[w] {
					t.Errorf("%q not proposed: %v", w, proposalNames(res))
				}
			}
			for _, c := range tc.covered {
				if !strings.Contains(strings.Join(res.Covered, "\n"), c) {
					t.Errorf("covered %v, want a line with %q", res.Covered, c)
				}
			}
			for _, s := range res.Sources {
				if strings.HasSuffix(s.Path, importscan.MigratedSuffix) && (s.Kind != importscan.KindIdentityBackup || s.State != importscan.StateFound) {
					t.Errorf("a .migrated backup read as %s/%s: it must be listed as a backup only", s.Kind, s.State)
				}
				if s.Kind == importscan.KindIdentityOld && s.State == importscan.StateFound && len(tc.extra) == 0 {
					t.Errorf("an old identity file counted as found (the banner would say existing install): %s", s.Path)
				}
			}
		})
	}
}

// TestPurgeTombstoneOnce: purging a setup of the same name, kind and
// fingerprints again replaces its trash.purged entry rather than adding one.
func TestPurgeTombstoneOnce(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, purgeConfig)
	for range 2 {
		if _, err := TrashSetup(home, "personal", lifeNow); err != nil {
			t.Fatal(err)
		}
		if _, err := PurgeSetup(home, "personal", PurgeOptions{At: lifeNow}); err != nil {
			t.Fatal(err)
		}
		if _, err := PutSetup(home, onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Folders: []string{"~/code/**"}}); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(readSetupsFile(t, home), "identity_sha"); n != 1 {
		t.Fatalf("trash.purged has %d entries for one setup:\n%s", n, readSetupsFile(t, home))
	}
}

func writeAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func proposalNames(res importscan.Result) []string {
	out := make([]string, 0, len(res.Proposals))
	for _, p := range res.Proposals {
		out = append(out, p.Entry.Name)
	}
	return out
}
