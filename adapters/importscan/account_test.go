package importscan

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// tombstone is the trash.purged entry the real purge writes
// (onboard.PurgedOf) for a setup, as config.yaml YAML under "purged:".
func tombstone(t *testing.T, s onboard.SetupEntry, at string) string {
	t.Helper()
	b, err := yaml.Marshal([]onboard.PurgedSetup{onboard.PurgedOf(onboard.TrashedSetup{Setup: s}, "", "", at)})
	if err != nil {
		t.Fatal(err)
	}
	return "  purged:\n" + indent(string(b), "    ")
}

func indent(s, pre string) string {
	var out []string
	for l := range strings.SplitSeq(strings.TrimRight(s, "\n"), "\n") {
		out = append(out, pre+l)
	}
	return strings.Join(out, "\n") + "\n"
}

// Issue 6: which setup covers a GitHub account Home would suggest a setup
// for — account (by github.user, by a solo setup's handle, another login)
// × the setup's state (none, live, disabled, trashed, purged, trashed and
// purged, live and purged, a pre-github_sha tombstone by name). A live
// setup wins over a trashed one, a trashed one over a purged one.
func TestAccountCoverMatrix(t *testing.T) {
	const at = "2026-11-08T01:02:03Z"
	byUser := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, GitHub: onboard.SetupGitHub{User: "Octo-Alice"}}
	byHandle := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Identity: onboard.SetupIdentity{Handle: "octo-alice"}}
	member := onboard.SetupEntry{Name: "acme", Kind: onboard.KindMember, Org: "acme", GitHub: onboard.SetupGitHub{User: "octo-alice"}}
	other := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, GitHub: onboard.SetupGitHub{User: "octo-bob"}}
	live := "setups:\n  - name: work\n    kind: solo\n    github:\n      user: octo-alice\n"
	disabled := "setups:\n  - name: work\n    kind: solo\n    disabled: true\n    github:\n      user: octo-alice\n"
	trashed := "trash:\n  setups:\n    - name: old\n      kind: solo\n      github:\n        user: octo-alice\n      removed_at: \"2026-11-06T10:00:00Z\"\n"
	purgedLines := func(s onboard.SetupEntry) string { return "trash:\n" + tombstone(t, s, at) }
	const wayBack = "; Home does not suggest a setup for it again (Suggest again, or: lore setup suggest-again --login octo-alice)"
	cases := []struct {
		name, config, setupName string
		state                   AccountState
		setup, line             string
	}{
		{name: "no setup", config: "identity:\n  key: human:alice\n"},
		{name: "live by github.user", config: live, state: AccountLive, setup: "work", line: "work works as GitHub account octo-alice"},
		{name: "disabled", config: disabled, state: AccountLive, setup: "work", line: "work (disabled) works as GitHub account octo-alice; enable it with: lore setup enable --name work"},
		{name: "trashed", config: trashed, state: AccountTrashed, setup: "old", line: "old (in the trash) works as GitHub account octo-alice; restore it with: lore setup trash restore --name old"},
		{name: "purged by github.user (case-insensitive)", config: purgedLines(byUser), state: AccountPurged, setup: "personal",
			line: "personal (deleted for good) used GitHub account octo-alice" + wayBack},
		{name: "purged solo by handle", config: purgedLines(byHandle), state: AccountPurged, setup: "personal",
			line: "personal (deleted for good) used GitHub account octo-alice" + wayBack},
		{name: "purged member", config: purgedLines(member), state: AccountPurged, setup: "acme",
			line: "acme (deleted for good) used GitHub account octo-alice" + wayBack},
		{name: "purged for another login", config: purgedLines(other), setupName: "work"},
		{name: "trashed and purged: the trash wins", config: trashed + tombstone(t, byUser, at), state: AccountTrashed, setup: "old",
			line: "old (in the trash) works as GitHub account octo-alice; restore it with: lore setup trash restore --name old"},
		{name: "live and purged: live wins", config: live + "trash:\n" + tombstone(t, byUser, at), state: AccountLive, setup: "work", line: "work works as GitHub account octo-alice"},
		{name: "pre-0.1.18 tombstone, same name", config: "trash:\n  purged:\n    - name: personal\n      kind: solo\n      purged_at: \"" + at + "\"\n", setupName: "personal",
			state: AccountPurged, setup: "personal",
			line: "personal (deleted for good) was named like the setup for GitHub account octo-alice; Home does not suggest a setup for it again (Suggest again, or: lore setup suggest-again --login octo-alice --setup personal)"},
		{name: "pre-0.1.18 tombstone, other name", config: "trash:\n  purged:\n    - name: personal\n      kind: solo\n      purged_at: \"" + at + "\"\n", setupName: "work"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			write(t, filepath.Join(w.lore, ConfigFile), c.config)
			setupName := c.setupName
			if setupName == "" {
				setupName = "personal"
			}
			got, ok := ReadAccountCovers(w.lore).For("octo-alice", setupName)
			if c.state == "" {
				if ok {
					t.Fatalf("covered by %+v, want nothing", got)
				}
				return
			}
			if !ok || got.State != c.state || got.Setup != c.setup || got.Line != c.line {
				t.Fatalf("got %+v (ok %v)\nwant state %s setup %s line %q", got, ok, c.state, c.setup, c.line)
			}
			if c.state == AccountPurged && got.PurgedAt != at {
				t.Fatalf("purged_at %q, want %q", got.PurgedAt, at)
			}
		})
	}
}

// The newest of two tombstones for one login is the cover, so a "Suggest
// again" recorded for the older purge does not hide a newer one.
func TestAccountCoverNewestPurge(t *testing.T) {
	w := newWorld(t)
	s := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, GitHub: onboard.SetupGitHub{User: "octo-alice"}}
	s2 := s
	s2.Name = "personal-2"
	b, err := yaml.Marshal([]onboard.PurgedSetup{
		onboard.PurgedOf(onboard.TrashedSetup{Setup: s}, "", "", "2026-11-01T00:00:00Z"),
		onboard.PurgedOf(onboard.TrashedSetup{Setup: s2}, "", "", "2026-11-08T00:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(w.lore, ConfigFile), "trash:\n  purged:\n"+indent(string(b), "    "))
	got, ok := ReadAccountCovers(w.lore).For("octo-alice", "personal")
	if !ok || got.Setup != "personal-2" || got.PurgedAt != "2026-11-08T00:00:00Z" {
		t.Fatalf("got %+v, want the newer purge personal-2", got)
	}
}

// A missing config.yaml covers nothing and does not fail.
func TestAccountCoverNoConfig(t *testing.T) {
	if got, ok := ReadAccountCovers(newWorld(t).lore).For("octo-alice", "personal"); ok {
		t.Fatalf("covered by %+v without a config.yaml", got)
	}
	if _, ok := (AccountCovers{}).For("", ""); ok {
		t.Fatal("an empty login is covered")
	}
}
