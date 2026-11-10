package wire

// setuphealth.go — the last doctor results per setup, kept on disk for
// setup.list's Health cell (ONB 5.4 Home, 6.1).
//
//	doctor run (page or CLI) ─▶ RecordHealth ─▶ ~/.lore/cache/doctor-health.json
//	setup.list (Home)        ─▶ SetupHealth  ◀─ the same file
//
// The doctor check cache (machine.db, doctor_cache.go) is shared by every
// lore process but expires after 60 s, so on its own a minute after the
// last Doctor run Home would fall back to older step results. The file is
// derived data like every other file under cache/: deleting it only makes
// Home show the step results until the next doctor run.
//
// Each setup's results carry a fingerprint of its setups[] entry as it was
// when doctor ran (polish item 2, realmac #10). A setup saved since then
// (another account, store, folder …) or never checked has no health: Home
// says "not checked yet" instead of showing results about an entry that no
// longer exists. Only the note and the disabled flag are left out of the
// fingerprint: neither changes what a check looks at.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// healthFileName is the health file under the cache folder.
const healthFileName = "doctor-health.json"

// healthFileMode: owner only, as every cache file (details can name paths).
const healthFileMode = 0o600

// healthFile is the file's shape: setup name → its last doctor results,
// and setup name → the entry fingerprint those results were made for
// (entryFingerprint; absent in files written before 2026-11-07, which
// therefore read as not checked).
type healthFile struct {
	Setups map[string][]onboard.CheckResult `json:"setups"`
	Saved  map[string]string                `json:"saved,omitempty"`
}

// entryFingerprint identifies what a doctor run checked of entry: a hash
// of the entry without its note and disabled state. "" when found is
// false (the setup is not in config.yaml).
func entryFingerprint(e onboard.SetupEntry, found bool) string {
	if !found {
		return ""
	}
	e.Note, e.Disabled, e.DisabledAt = "", false, ""
	b, err := json.Marshal(e)
	if err != nil { // a SetupEntry is plain strings and lists: never fails
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// currentFingerprint is setup name's fingerprint in config.yaml now ("" when
// absent or unreadable).
func (a *App) currentFingerprint(name string) string {
	cfg, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return ""
	}
	e, ok := cfg.Setup(name)
	return entryFingerprint(e, ok)
}

// healthPath is the health file of this lore home.
func (a *App) healthPath() string { return filepath.Join(a.opts.Home, cacheDir, healthFileName) }

// readHealth reads the health file; a missing file is an empty one.
func (a *App) readHealth() (healthFile, error) {
	hf := healthFile{Setups: map[string][]onboard.CheckResult{}, Saved: map[string]string{}}
	b, err := os.ReadFile(a.healthPath())
	if errors.Is(err, fs.ErrNotExist) {
		return hf, nil
	}
	if err != nil {
		return hf, errs.FileIO("read", a.healthPath(), err)
	}
	if err := json.Unmarshal(b, &hf); err != nil {
		return healthFile{Setups: map[string][]onboard.CheckResult{}, Saved: map[string]string{}}, errs.Wrap(errs.Internal, "wire: "+healthFileName+" is not valid JSON", err)
	}
	if hf.Setups == nil {
		hf.Setups = map[string][]onboard.CheckResult{}
	}
	if hf.Saved == nil {
		hf.Saved = map[string]string{}
	}
	return hf, nil
}

// RecordHealth implements opsdoctor.HealthSink: setup's results replace
// its previous ones in the health file (written atomically), with the
// fingerprint of the entry they were made for. A file that does not decode
// is replaced: it only ever holds derived results.
func (a *App) RecordHealth(_ context.Context, setup string, results []onboard.CheckResult) error {
	if setup == "" {
		return nil
	}
	a.healthMu.Lock()
	defer a.healthMu.Unlock()
	hf, _ := a.readHealth() // a broken file is rewritten whole
	hf.Setups[setup] = append([]onboard.CheckResult{}, results...)
	hf.Saved[setup] = a.currentFingerprint(setup)
	b, err := json.Marshal(hf)
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode "+healthFileName, err)
	}
	return writeFileAtomic(a.healthPath(), b, healthFileMode)
}

// SetupHealth implements opssetup.HealthSource: the last doctor results
// recorded for setup name by any lore process on this machine, only while
// the setup's entry is still the one they were made for. Nothing else
// stands in (no per-process cache, no older run): Home shows "not checked
// yet" until doctor runs for the entry as it is now.
func (a *App) SetupHealth(_ context.Context, name string) ([]onboard.CheckResult, bool) {
	hf, err := a.readHealth()
	if err != nil {
		return nil, false
	}
	rs, ok := hf.Setups[name]
	if !ok || len(rs) == 0 {
		return nil, false
	}
	fp, recorded := hf.Saved[name]
	if !recorded || fp != a.currentFingerprint(name) {
		return nil, false
	}
	return rs, true
}
