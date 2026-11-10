package setup

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// The fake store's TrashStore upgrade: the same moves as wire, on memory.

func (f *fakeStore) TrashSetup(_ context.Context, name string, at time.Time) (bool, error) {
	for i, s := range f.cfg.Setups {
		if s.Name == name {
			f.cfg.Setups = slices.Delete(f.cfg.Setups, i, i+1)
			f.cfg.Trash = append(f.cfg.Trash, onboard.TrashedSetup{Setup: s, RemovedAt: at.Format(time.RFC3339)})
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) RestoreSetup(_ context.Context, name, as string) (RestoreOut, error) {
	i := onboard.NewestTrashed(f.cfg.Trash, name)
	if i < 0 {
		return RestoreOut{}, errs.New(errs.SetupNotFound, "not in the trash")
	}
	if as == "" {
		as = name
	}
	if _, taken := f.cfg.Setup(as); taken {
		return RestoreOut{}, errs.New(errs.SetupNameTaken, "taken")
	}
	e := f.cfg.Trash[i].Setup
	e.Name = as
	f.cfg.Trash = slices.Delete(f.cfg.Trash, i, i+1)
	f.cfg.Setups = append(f.cfg.Setups, e)
	return RestoreOut{Name: as}, nil
}

func (f *fakeStore) PurgeSetup(_ context.Context, name string, clearIdentity, clearStore bool) error {
	i := onboard.NewestTrashed(f.cfg.Trash, name)
	if i < 0 {
		return errs.New(errs.SetupNotFound, "not in the trash")
	}
	f.cfg.Trash = slices.Delete(f.cfg.Trash, i, i+1)
	if clearStore {
		f.cfg.SharedStore = ""
	}
	return nil
}

func (f *fakeStore) SetDisabled(_ context.Context, name string, disabled bool, at time.Time) (bool, bool, error) {
	for i, s := range f.cfg.Setups {
		if s.Name == name {
			if s.Disabled == disabled {
				return true, false, nil
			}
			f.cfg.Setups[i].Disabled = disabled
			return true, true, nil
		}
	}
	return false, false, nil
}

func (f *fakeStore) MachineIdentity(context.Context) string { return "Ada Example" }

// TestRemoveMovesToTrash: remove needs no confirm (it is restorable), the
// entry lands in the trash, restore brings it back, a second remove of a
// gone name is E_SETUP_NOT_FOUND.
func TestRemoveMovesToTrash(t *testing.T) {
	env := newFakeEnv(onboard.SetupEntry{Name: "old-client", Kind: onboard.KindSolo, Note: "a client from 2025"})
	e := newTestEngine(env)
	out, err := e.Remove(bg, RemoveIn{Name: "old-client"})
	if err != nil || out.Name != "old-client" || out.RemovedAt == "" || len(env.store.cfg.Setups) != 0 || len(env.store.cfg.Trash) != 1 {
		t.Fatalf("remove = %+v, %v", out, err)
	}
	if !strings.Contains(out.Text(), "lore setup trash restore old-client") {
		t.Fatalf("text must say how to restore: %q", out.Text())
	}
	if _, err := e.Remove(bg, RemoveIn{Name: "old-client"}); !errs.Has(err, errs.SetupNotFound) {
		t.Fatalf("second remove err = %v", err)
	}
	list, err := e.TrashList(bg, TrashListIn{})
	if err != nil || len(list.Setups) != 1 || list.Setups[0].Setup.Note != "a client from 2025" {
		t.Fatalf("trash list = %+v, %v", list, err)
	}
	if r, err := e.TrashRestore(bg, RestoreIn{Name: "old-client"}); err != nil || r.Name != "old-client" || len(env.store.cfg.Setups) != 1 {
		t.Fatalf("restore = %+v, %v", r, err)
	}
}

// TestDisableEnable: both are idempotent and name a missing setup.
func TestDisableEnable(t *testing.T) {
	env := newFakeEnv(onboard.SetupEntry{Name: "s", Kind: onboard.KindSolo})
	e := newTestEngine(env)
	for _, step := range []struct {
		run              func(context.Context, ToggleIn) (ToggleOut, error)
		disabled, change bool
	}{{e.Disable, true, true}, {e.Disable, true, false}, {e.Enable, false, true}, {e.Enable, false, false}} {
		out, err := step.run(bg, ToggleIn{Name: "s"})
		if err != nil || out.Disabled != step.disabled || out.Changed != step.change || env.store.cfg.Setups[0].Disabled != step.disabled {
			t.Fatalf("step %+v: %+v %v", step, out, err)
		}
	}
	if _, err := e.Disable(bg, ToggleIn{Name: "nope"}); !errs.Has(err, errs.SetupNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

// TestPurgeNeedsConfirmNeverYes: no --confirm is E_NEEDS_CONFIRM naming
// --confirm and what stays (even with --yes); --dry-run previews; the
// purge with --confirm deletes only the trash entry.
func TestPurgeNeedsConfirmNeverYes(t *testing.T) {
	env := newFakeEnv()
	env.store.cfg.SharedStore = "git@github.com:example/lore-me.git"
	env.store.cfg.Trash = []onboard.TrashedSetup{{Setup: onboard.SetupEntry{Name: "gone", Kind: onboard.KindSolo}, RemovedAt: "2026-11-06T10:00:00Z"}}
	e := newTestEngine(env)
	for _, ctx := range []context.Context{bg, yesCtx} {
		_, err := e.TrashPurge(ctx, PurgeIn{Name: "gone"})
		var ee *errs.Error
		if !errors.As(err, &ee) || ee.Code != errs.NeedsConfirm {
			t.Fatalf("no --confirm: %v", err)
		}
		next := strings.Join(ee.Next.Args, " ")
		if !strings.Contains(next, "--confirm") || strings.Contains(next, "--yes") {
			t.Fatalf("next must name --confirm only: %q", next)
		}
		for _, w := range []string{"identity (Ada Example)", "example/lore-me", "never deletes a repository"} {
			if !strings.Contains(ee.Message, w) {
				t.Fatalf("refusal must list what stays (%q): %s", w, ee.Message)
			}
		}
	}
	pre, err := e.TrashPurge(bg, PurgeIn{Name: "gone", DryRun: true, ClearIdentity: true})
	if err != nil || !pre.DryRun || len(env.store.cfg.Trash) != 1 {
		t.Fatalf("dry run: %+v %v", pre, err)
	}
	if !strings.Contains(strings.Join(pre.Deletes, "\n"), "identity (Ada Example)") || strings.Contains(strings.Join(pre.Stays, "\n"), "Ada Example") {
		t.Fatalf("--clear-identity moves the identity to deletes: %+v", pre)
	}
	if _, err := e.TrashPurge(bg, PurgeIn{Name: "gone", Confirm: true}); err != nil || len(env.store.cfg.Trash) != 0 {
		t.Fatalf("purge: %v (trash %d)", err, len(env.store.cfg.Trash))
	}
	if env.store.cfg.SharedStore == "" {
		t.Fatal("personal.store cleared without being asked")
	}
	if _, err := e.TrashPurge(bg, PurgeIn{Name: "gone", Confirm: true}); !errs.Has(err, errs.SetupNotFound) {
		t.Fatalf("purge of nothing: %v", err)
	}
}

// TestImportFoundBanner: the banner names the solo proposal's identity
// and store, says "existing lore install" only with legacy files, and
// never names an identity no proposal uses.
func TestImportFoundBanner(t *testing.T) {
	src := []ImportSource{{Kind: importKindConfig, Path: "/home/u/.lore/config.yaml", State: importStateFound}}
	solo := ImportProposal{Setup: onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Identity: onboard.SetupIdentity{Name: "Ada"}, PersonalStore: "git@github.com:example/lore-me.git"}}
	member := ImportProposal{Setup: onboard.SetupEntry{Name: "acme", Kind: onboard.KindMember, Org: "acme"}}
	f := importFound(ImportScanOut{Sources: src, Proposals: []ImportProposal{solo}})
	if f.Config != "~/.lore/config.yaml" || f.Identity != "Ada" || len(f.LegacyFiles) != 0 {
		t.Fatalf("found = %+v", f)
	}
	f = importFound(ImportScanOut{Sources: src, Proposals: []ImportProposal{member}})
	if f.Identity != "" || f.PersonalStore != "" {
		t.Fatalf("no solo proposal: nothing to name, got %+v", f)
	}
	legacy := append(slices.Clone(src), ImportSource{Kind: importKindLegacy, Path: "/home/u/.lore/identity.yaml", State: importStateFound},
		ImportSource{Kind: "identity_backup", Path: "/home/u/.lore/identity.toml.migrated", State: importStateFound},
		ImportSource{Kind: importKindLegacy, Path: "/home/u/.lore/identity.toml", State: "missing"})
	f = importFound(ImportScanOut{Sources: legacy, Proposals: []ImportProposal{solo}})
	if len(f.LegacyFiles) != 1 || f.LegacyFiles[0] != "~/.lore/identity.yaml" {
		t.Fatalf("legacy = %+v", f.LegacyFiles)
	}
}

// clockedEnv is a fakeEnv that offers the engine's clock (opclock.Clocker),
// as wire.App does with a test build's LORE_NOW.
type clockedEnv struct {
	fakeEnv
	at time.Time
}

func (c clockedEnv) Now() time.Time { return c.at }

// TestRemoveStampsEngineClock: removed_at is the env's clock, not the wall
// clock, so a fixed LORE_NOW gives the same trash on every run.
func TestRemoveStampsEngineClock(t *testing.T) {
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	env := newFakeEnv(onboard.SetupEntry{Name: "old-client", Kind: onboard.KindSolo})
	e := newEngine(clockedEnv{fakeEnv: env, at: fixed}, fakeCatalog{})
	out, err := e.Remove(bg, RemoveIn{Name: "old-client"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "2026-01-02T02:04:05Z"; out.RemovedAt != want || env.store.cfg.Trash[0].RemovedAt != want {
		t.Fatalf("removed_at = %q / %q, want %q", out.RemovedAt, env.store.cfg.Trash[0].RemovedAt, want)
	}
}
