package wire

// setuplifecycle.go — the setup lifecycle's wire side (owner decision
// 2026-11-06): setupStore's opssetup.TrashStore upgrade over
// config_setuptrash.go, and App's opssetup.ImportDismisser over a small
// machine-state file.
//
//	setup.disable/enable ─▶ SetDisabled  ─▶ SetSetupDisabled (config.yaml)
//	setup.remove         ─▶ TrashSetup   ─▶ trash.setups (progress rows stay)
//	setup.trash.restore  ─▶ RestoreSetup ─▶ setups[] + pins
//	setup.trash.purge    ─▶ PurgeSetup   ─▶ entry gone + its setup_progress rows
//	setup.import_dismiss ─▶ ~/.lore/state/import-dismissed.json (banner keys)

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

var (
	_ opssetup.TrashStore      = setupStore{}
	_ opssetup.LeftoverClearer = setupStore{}
	_ opssetup.SetupRenamer    = setupStore{}
	_ opssetup.ImportDismisser = (*App)(nil)
)

// SetDisabled implements opssetup.TrashStore.
func (s setupStore) SetDisabled(_ context.Context, name string, disabled bool, at time.Time) (bool, bool, error) {
	return SetSetupDisabled(s.a.opts.Home, name, disabled, at)
}

// TrashSetup implements opssetup.TrashStore. The setup_progress rows stay,
// so a restore resumes an unfinished setup; the purge deletes them.
func (s setupStore) TrashSetup(_ context.Context, name string, at time.Time) (bool, error) {
	return TrashSetup(s.a.opts.Home, name, at)
}

// RestoreSetup implements opssetup.TrashStore.
func (s setupStore) RestoreSetup(_ context.Context, name, as string) (opssetup.RestoreOut, error) {
	r, err := RestoreSetup(s.a.opts.Home, name, as)
	if err != nil {
		return opssetup.RestoreOut{}, err
	}
	return opssetup.RestoreOut{Name: r.Setup.Name, Pins: r.Pins, PinsSkipped: r.Skipped, DefaultCleared: r.DefaultCleared}, nil
}

// PurgeSetup implements opssetup.TrashStore: the config entry first, then
// its progress rows for good (PurgeProgress: nothing of them stays in
// machine.db) — unless a setup of that name exists again (the rows
// are then that setup's).
func (s setupStore) PurgeSetup(ctx context.Context, name string, clearIdentity, clearStore bool) error {
	if _, err := PurgeSetup(s.a.opts.Home, name, PurgeOptions{ClearIdentity: clearIdentity, ClearPersonalStore: clearStore}); err != nil {
		return err
	}
	cfg, err := ReadSetupsConfig(s.a.opts.Home)
	if err != nil {
		return err
	}
	if _, live := cfg.Setup(name); live {
		return nil
	}
	return s.PurgeProgress(ctx, name)
}

// ClearLeftover implements opssetup.LeftoverClearer.
func (s setupStore) ClearLeftover(_ context.Context, k onboard.LeftoverKind) (bool, error) {
	return ClearLeftover(s.a.opts.Home, k)
}

// MachineIdentity implements opssetup.TrashStore: the top-level
// identity's name, else its key ("" when none or unreadable).
func (s setupStore) MachineIdentity(context.Context) string {
	raw, err := readConfigRaw(s.a.opts.Home)
	if err != nil || raw == nil {
		return ""
	}
	var d struct {
		Identity struct {
			Name string `yaml:"name"`
			Key  string `yaml:"key"`
		} `yaml:"identity"`
	}
	if yaml.Unmarshal(raw, &d) != nil {
		return ""
	}
	if n := strings.TrimSpace(d.Identity.Name); n != "" {
		return n
	}
	return strings.TrimSpace(d.Identity.Key)
}

// importDismissFile holds the dismissed import-banner keys under the
// machine state folder (never synced).
const importDismissFile = "import-dismissed.json"

// importDismissMode: owner only, like every state file.
const importDismissMode = 0o600

// importDismissMax bounds the keys kept (oldest dropped first).
const importDismissMax = 50

// importDismissed is the file's shape.
type importDismissed struct {
	Keys []string `json:"keys"`
}

// importDismissPath is the file of this lore home.
func (a *App) importDismissPath() string {
	return filepath.Join(a.opts.Home, localStateDir, importDismissFile)
}

// readImportDismissed reads the file; missing or broken is empty (it only
// hides a banner).
func (a *App) readImportDismissed() importDismissed {
	var d importDismissed
	b, err := os.ReadFile(a.importDismissPath())
	if errors.Is(err, fs.ErrNotExist) || err != nil {
		return d
	}
	_ = json.Unmarshal(b, &d) // a broken file is rewritten on the next dismiss
	return d
}

// DismissImport implements opssetup.ImportDismisser.
func (a *App) DismissImport(_ context.Context, key string) error {
	d := a.readImportDismissed()
	if slices.Contains(d.Keys, key) {
		return nil
	}
	d.Keys = append(d.Keys, key)
	if len(d.Keys) > importDismissMax {
		d.Keys = d.Keys[len(d.Keys)-importDismissMax:]
	}
	b, err := json.Marshal(d)
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode "+importDismissFile, err)
	}
	return writeFileAtomic(a.importDismissPath(), b, importDismissMode)
}

// ImportDismissed implements opssetup.ImportDismisser.
func (a *App) ImportDismissed(_ context.Context, key string) bool {
	return slices.Contains(a.readImportDismissed().Keys, key)
}

// RenameSetup implements opssetup.SetupRenamer: config.yaml only (the
// op moves the setup_progress rows through the Store).
func (s setupStore) RenameSetup(_ context.Context, from, to string) (bool, error) {
	return RenameSetup(s.a.opts.Home, from, to)
}
