package wire

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// lifeConfig: a machine identity and personal.store, two setups (one
// pinned, one carrying a key lore does not read) and a GitHub-shaped store.
const lifeConfig = `# keep this comment
identity: {key: "human:ada@example.test", name: Ada Example}
personal: {store: "git@github.com:example/lore-me.git"}
orgs:
  - name: acme
    identity: {key: "human:ada@acme.example"}
setups:
  - name: solo-a
    kind: solo
    default: true
    folders: [~/code/a/**]
    colour: green   # unknown to lore
  - name: acme
    kind: member
    org: acme
    folders: [~/code/acme/**]
repo_setups:
  rep_one: solo-a
  rep_two: acme
`

var lifeNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

// TestOldConfigLoadsUnchanged: a config without note, disabled or trash
// loads with them all zero, and a write of something else leaves it so.
func TestOldConfigLoadsUnchanged(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, lifeConfig)
	c, err := ValidateSetupsConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Trash) != 0 || c.Setups[0].Disabled || c.Setups[0].Note != "" || len(c.Enabled()) != 2 {
		t.Fatalf("old config: %+v", c)
	}
	if err := SetRepoSetup(home, "rep_three", "acme"); err != nil {
		t.Fatal(err)
	}
	got := readSetupsFile(t, home)
	for _, absent := range []string{"disabled", "trash", "note", "removed_at"} {
		if strings.Contains(got, absent) {
			t.Fatalf("an unrelated write added %q:\n%s", absent, got)
		}
	}
}

// TestDisableNoteRoundTrip: disable/enable and a note survive a re-read,
// keep unknown keys, and enable removes both disabled keys.
func TestDisableNoteRoundTrip(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, lifeConfig)
	found, changed, err := SetSetupDisabled(home, "solo-a", true, lifeNow)
	if err != nil || !found || !changed {
		t.Fatalf("disable: %v %v %v", found, changed, err)
	}
	if _, changed, _ = SetSetupDisabled(home, "solo-a", true, lifeNow); changed {
		t.Fatal("disabling twice changed something")
	}
	if found, _, _ = SetSetupDisabled(home, "nope", true, lifeNow); found {
		t.Fatal("found a setup that does not exist")
	}
	c, err := ValidateSetupsConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := c.Setup("solo-a")
	if !e.Disabled || e.DisabledAt != "2026-11-06T12:00:00Z" {
		t.Fatalf("after disable: %+v", e)
	}
	e.Note = "my own projects: why this exists"
	if _, err := PutSetup(home, e); err != nil {
		t.Fatal(err)
	}
	raw := readSetupsFile(t, home)
	for _, want := range []string{"colour: green", "disabled: true", "note: 'my own projects: why this exists'", "# keep this comment"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("missing %q:\n%s", want, raw)
		}
	}
	if _, _, err := SetSetupDisabled(home, "solo-a", false, lifeNow); err != nil {
		t.Fatal(err)
	}
	raw = readSetupsFile(t, home)
	if strings.Contains(raw, "disabled") {
		t.Fatalf("enable left a disabled key:\n%s", raw)
	}
	c, _ = ValidateSetupsConfig(home)
	if e, _ = c.Setup("solo-a"); e.Note != "my own projects: why this exists" || e.Disabled {
		t.Fatalf("after enable: %+v", e)
	}
}

// TestMatchingSkipsDisabled: a disabled setup is never chosen by folder,
// repo pin, org or default; --setup / LORE_SETUP naming it is refused.
func TestMatchingSkipsDisabled(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, lifeConfig)
	for _, n := range []string{"solo-a", "acme"} {
		if _, _, err := SetSetupDisabled(home, n, true, lifeNow); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := SetSetupDisabled(home, "acme", false, lifeNow); err != nil {
		t.Fatal(err)
	}
	q := func(dir, repoID, flag string, org string) SetupQuery {
		return SetupQuery{Home: home, Dir: dir, Flag: flag, UserHome: "/home/ada",
			Getenv:   func(string) string { return "" },
			TopLevel: func(context.Context, string) string { return dir },
			RepoID:   func(string) string { return repoID },
			Resolve:  func(string) (string, error) { return "", errors.New("no links") },
			OrgOf:    func(context.Context) (string, error) { return org, nil }}
	}
	cases := []struct {
		name               string
		dir, repo, flag, o string
		want, why          string
		code               errs.Code
	}{
		{name: "folder of the disabled setup: no match, no default", dir: "/home/ada/code/a/x", why: opssetup.WhyNone},
		{name: "pin to the disabled setup is skipped", dir: "/srv/x", repo: "rep_one", why: opssetup.WhyNone},
		{name: "enabled setup still matches", dir: "/home/ada/code/acme/x", want: "acme", why: opssetup.WhyFolder},
		{name: "org still reaches the enabled one", dir: "/srv/x", o: "acme", want: "acme", why: opssetup.WhyOrg},
		{name: "--setup naming a disabled setup", dir: "/srv/x", flag: "solo-a", code: errs.SetupNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ResolveSetup(context.Background(), q(tc.dir, tc.repo, tc.flag, tc.o))
			if tc.code != "" {
				if errs.CodeOf(err) != tc.code || !strings.Contains(err.Error(), "disabled") {
					t.Fatalf("want %s naming disabled, got %v", tc.code, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Active.Name != tc.want || res.Active.Why != tc.why {
				t.Fatalf("got %s/%s, want %s/%s (%+v)", res.Active.Name, res.Active.Why, tc.want, tc.why, res.Steps)
			}
		})
	}
	// Everything disabled: lore behaves as without setups.
	if _, _, err := SetSetupDisabled(home, "acme", true, lifeNow); err != nil {
		t.Fatal(err)
	}
	res, err := ResolveSetup(context.Background(), q("/home/ada/code/acme/x", "rep_two", "", "acme"))
	if err != nil || res.Active.Why != opssetup.WhyNone {
		t.Fatalf("all disabled: %+v %v", res.Active, err)
	}
}

// TestTrashRestorePurge: remove moves the entry (unknown keys, pins) to
// the trash; restore brings the newest back with its pins; purge deletes
// it and, only when asked, the identity and personal.store. No repo is
// ever named for deletion and orgs[] stays.
func TestTrashRestorePurge(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, lifeConfig)
	found, err := TrashSetup(home, "solo-a", lifeNow)
	if err != nil || !found {
		t.Fatalf("trash: %v %v", found, err)
	}
	if found, _ = TrashSetup(home, "solo-a", lifeNow); found {
		t.Fatal("trashed twice")
	}
	c, err := ValidateSetupsConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, live := c.Setup("solo-a"); live || len(c.Trash) != 1 {
		t.Fatalf("after trash: %+v", c)
	}
	tr := c.Trash[0]
	if tr.RemovedAt != "2026-11-06T12:00:00Z" || len(tr.RepoPins) != 1 || tr.RepoPins[0] != "rep_one" || !tr.Setup.Default {
		t.Fatalf("trashed entry: %+v", tr)
	}
	if _, pinned := c.RepoSetups["rep_one"]; pinned {
		t.Fatal("the pin stayed on a trashed setup")
	}
	if raw := readSetupsFile(t, home); !strings.Contains(raw, "colour: green") {
		t.Fatalf("unknown key lost in the trash:\n%s", raw)
	}

	// Restore onto a taken name is refused; --as restores beside it.
	if _, err := PutSetup(home, onboard.SetupEntry{Name: "solo-a", Kind: onboard.KindSolo, Default: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreSetup(home, "solo-a", ""); errs.CodeOf(err) != errs.SetupNameTaken {
		t.Fatalf("restore onto a taken name: %v", err)
	}
	r, err := RestoreSetup(home, "solo-a", "solo-old")
	if err != nil {
		t.Fatal(err)
	}
	if r.Setup.Name != "solo-old" || !r.DefaultCleared || r.Setup.Default || len(r.Pins) != 1 {
		t.Fatalf("restore --as: %+v", r)
	}
	c, _ = ValidateSetupsConfig(home)
	if c.RepoSetups["rep_one"] != "solo-old" || len(c.Trash) != 0 {
		t.Fatalf("after restore: pins %v trash %d", c.RepoSetups, len(c.Trash))
	}
	if raw := readSetupsFile(t, home); strings.Contains(raw, "trash") || strings.Contains(raw, "removed_at") {
		t.Fatalf("an empty trash leaves no keys:\n%s", raw)
	}
	if _, err := RestoreSetup(home, "solo-a", ""); errs.CodeOf(err) != errs.SetupNotFound {
		t.Fatalf("restore from an empty trash: %v", err)
	}

	// Purge: keeps the identity and personal.store unless asked.
	if _, err := TrashSetup(home, "solo-old", lifeNow); err != nil {
		t.Fatal(err)
	}
	if _, err := TrashSetup(home, "acme", lifeNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	p, err := PurgeSetup(home, "solo-old", PurgeOptions{})
	if err != nil || p.IdentityCleared || p.StoreCleared {
		t.Fatalf("purge: %+v %v", p, err)
	}
	raw := readSetupsFile(t, home)
	for _, want := range []string{"Ada Example", "lore-me.git", "name: acme", "removed_at"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("purge removed %q:\n%s", want, raw)
		}
	}
	if c, err := ReadSetupsConfig(home); err != nil || onboard.NewestTrashed(c.Trash, "solo-old") >= 0 {
		t.Fatalf("purged entry still in trash.setups (%v):\n%s", err, raw)
	}
	if !strings.Contains(raw, "purged:\n    - name: solo-old") {
		t.Fatalf("no trash.purged tombstone for solo-old:\n%s", raw)
	}
	p, err = PurgeSetup(home, "acme", PurgeOptions{ClearIdentity: true, ClearPersonalStore: true})
	// SOURCE_OF_TRUTH row 1: the first write already moved the top-level
	// identity: into solo-a (the default solo setup, which had none), so
	// there is no top-level block left to clear.
	if err != nil || p.IdentityCleared || !p.StoreCleared {
		t.Fatalf("purge with clears: %+v %v", p, err)
	}
	raw = readSetupsFile(t, home)
	if strings.HasPrefix(raw, "identity:") || strings.Contains(raw, "\nidentity:") || strings.Contains(raw, "lore-me.git") || strings.Contains(raw, "removed_at") {
		t.Fatalf("clears not applied:\n%s", raw)
	}
	// The purged setup was the last one of team acme: its orgs[] entry goes
	// with it, so the name and the team are free again (owner decision
	// 2026-11-08; before, orgs[] stayed and a re-add was refused).
	if !p.OrgCleared || strings.Contains(raw, "orgs:") || strings.Contains(raw, "human:ada@acme.example") {
		t.Fatalf("the last setup of acme was purged; its orgs[] entry must go (cleared %v):\n%s", p.OrgCleared, raw)
	}
	if _, err := PurgeSetup(home, "acme", PurgeOptions{}); errs.CodeOf(err) != errs.SetupNotFound {
		t.Fatalf("purge of nothing: %v", err)
	}
}

// TestTrashNewestOfName: two trashed entries of one name; restore and
// purge act on the newest.
func TestTrashNewestOfName(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, lifeConfig)
	if _, err := TrashSetup(home, "acme", lifeNow); err != nil {
		t.Fatal(err)
	}
	if _, err := PutSetup(home, onboard.SetupEntry{Name: "acme", Kind: onboard.KindMember, Org: "acme", Note: "second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := TrashSetup(home, "acme", lifeNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	r, err := RestoreSetup(home, "acme", "")
	if err != nil || r.Setup.Note != "second" {
		t.Fatalf("restore picked %+v %v", r.Setup, err)
	}
	c, _ := ValidateSetupsConfig(home)
	if len(c.Trash) != 1 || c.Trash[0].RemovedAt != "2026-11-06T12:00:00Z" {
		t.Fatalf("the older entry must stay: %+v", c.Trash)
	}
}

// TestImportDismissed: a dismissed key is remembered in the state folder.
func TestImportDismissed(t *testing.T) {
	t.Parallel()
	a := &App{opts: Options{Home: t.TempDir()}}
	ctx := context.Background()
	if a.ImportDismissed(ctx, "0123456789abcdef") {
		t.Fatal("nothing dismissed yet")
	}
	for range 2 {
		if err := a.DismissImport(ctx, "0123456789abcdef"); err != nil {
			t.Fatal(err)
		}
	}
	if !a.ImportDismissed(ctx, "0123456789abcdef") || a.ImportDismissed(ctx, "fedcba9876543210") {
		t.Fatal("dismiss not remembered by key")
	}
	if n := len(a.readImportDismissed().Keys); n != 1 {
		t.Fatalf("dismissing twice stored %d keys", n)
	}
}
