package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/internal/testenv"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// The personal stores of the shapes below (placeholders).
const (
	paMeURL    = "git@github.com:lee-dev/notes-me.git"
	paOtherURL = "git@github.com:lee-dev/notes-extra.git"
)

// paConfig is a machine with a work member setup (the default, folders
// ~/w/**) and a personal solo setup (folders ~/p/**); top is the top-level
// lines (personal.store or none), work and pers the setups' personal_store
// lines.
func paConfig(top, work, pers string) string {
	return top + `identity: {key: "human:lee@example.test", name: Lee}
orgs:
  - name: acme
    identity: {key: "human:lee@acme.example", name: Lee}
    stores:
      - url: git@github.com:acme-co/lore-acme.git
setups:
  - name: work
    kind: member
    org: acme
    default: true
    folders: [~/w/**]
` + work + `  - name: personal
    kind: solo
    folders: [~/p/**]
    identity: {key: "human:lee@example.test", name: Lee}
` + pers
}

// TestPersonalStoreInEveryShape is owner item 9 (reopened): the person's
// personal store is listed by `lore store list` and synced by the
// setup-wide `lore sync` whatever setup covers the folder — from ~, a work
// folder and a personal folder, with LORE_ORG set or not — in every config
// shape: personal.store inherited by both setups; the solo setup naming
// its own store with the work setup inheriting nothing (what the setup
// page writes; the owner's machine); the work setup with personal_store:
// none; and a second personal store. Before, only the active setup's `me`
// was listed, so B, C and D left the store out outside the personal
// folder.
func TestPersonalStoreInEveryShape(t *testing.T) {
	topMe := "personal:\n  store: " + paMeURL + "\n"
	cases := []struct {
		name, top, work, pers string
		want                  []string // every personal store url
	}{
		{"A/top-level-inherited-by-both", topMe, "", "    personal_store: inherit\n", []string{paMeURL}},
		{"B/solo-own-url-work-inherits-unset", "", "", "    personal_store: " + paMeURL + "\n", []string{paMeURL}},
		{"C/top-level-work-none", topMe, "    personal_store: none\n", "", []string{paMeURL}},
		{"D/solo-own-url-work-none", "", "    personal_store: none\n", "    personal_store: " + paMeURL + "\n", []string{paMeURL}},
		{"E/two-personal-stores", "personal:\n  store: " + paOtherURL + "\n", "", "    personal_store: " + paMeURL + "\n", []string{paOtherURL, paMeURL}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			userHome := t.TempDir()
			testenv.SetHome(t, userHome)
			t.Setenv(opssetup.EnvSetup, "")
			home := writeSetupsConfig(t, paConfig(c.top, c.work, c.pers))
			for _, rel := range []string{"", "w/proj", "p/proj"} {
				dir := filepath.Join(userHome, filepath.FromSlash(rel))
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for _, org := range []string{"", "acme"} {
					t.Setenv(orgcfg.EnvOrg, org)
					where := "cwd ~/" + rel + " LORE_ORG=" + org
					a := openWith(t, dir, Options{Home: home})
					list, err := opsstores.ListEntries(background(), a)
					if err != nil {
						t.Fatalf("%s: store list: %v", where, err)
					}
					stores, err := a.setupStores(background())
					if err != nil {
						t.Fatalf("%s: sync's stores: %v", where, err)
					}
					for _, u := range c.want {
						n := 0
						for _, e := range list {
							if e.URL == u {
								n++
								if !e.Personal || e.Source != opsstores.SourcePersonal {
									t.Errorf("%s: %s listed as %+v, want a personal entry", where, u, e)
								}
							}
						}
						if n != 1 {
							t.Errorf("%s: store list holds %s %d times, want once: %+v", where, u, n, list)
						}
						synced := 0
						for _, s := range stores {
							if s.URL == u {
								synced++
							}
						}
						if synced != 1 {
							t.Errorf("%s: the setup-wide sync takes %s %d times, want once: %+v", where, u, synced, stores)
						}
					}
				}
			}
		})
	}
}

// TestOtherPersonalEntriesNames: a personal store that is not the active
// setup's `me` is named by its short name, by its url when that name is
// taken or reserved; a store already listed (by store key, spelling
// aside) is not listed again; a disabled setup's store is no one's.
func TestOtherPersonalEntriesNames(t *testing.T) {
	userHome := t.TempDir()
	testenv.SetHome(t, userHome)
	t.Setenv(opssetup.EnvSetup, "")
	t.Setenv(orgcfg.EnvOrg, "")
	home := writeSetupsConfig(t, `personal:
  store: git@github.com:lee-dev/team.git
setups:
  - name: one
    kind: solo
    folders: [~/one/**]
    personal_store: git@github.com:lee-dev/notes-me.git
  - name: two
    kind: solo
    folders: [~/two/**]
    personal_store: https://github.com/lee-dev/notes-me
  - name: three
    kind: solo
    folders: [~/three/**]
    personal_store: git@github.com:other-dev/acme.git
  - name: off
    kind: solo
    disabled: true
    personal_store: git@github.com:lee-dev/old-notes.git
`)
	a := openWith(t, userHome, Options{Home: home})
	got := []opsstores.Entry{{Name: "acme", URL: "git@github.com:acme-co/lore-acme.git", Source: opsstores.SourceOrg}}
	more, err := a.OtherPersonalEntries(background(), got)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range more {
		names = append(names, e.Name+"="+e.URL)
	}
	want := "git@github.com:lee-dev/team.git=git@github.com:lee-dev/team.git notes-me=git@github.com:lee-dev/notes-me.git git@github.com:other-dev/acme.git=git@github.com:other-dev/acme.git"
	if strings.Join(names, " ") != want {
		t.Fatalf("other personal stores\n got %s\nwant %s", strings.Join(names, " "), want)
	}
	// the active `me` already listed: not again
	got = append(got, opsstores.Entry{Name: opsstores.PersonalName, URL: "ssh://git@github.com/lee-dev/notes-me.git", Personal: true, Source: opsstores.SourcePersonal})
	more, err = a.OtherPersonalEntries(background(), got)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range more {
		if strings.Contains(e.URL, "notes-me") {
			t.Fatalf("me's store listed again as %+v", e)
		}
	}
}
