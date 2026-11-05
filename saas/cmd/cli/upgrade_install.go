// upgrade_install.go — how lore was installed, the project pin, and the
// atomic swap (FORMAT_EVOLUTION 8.1, 8.3, 8.4).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"saas/pkg/aicoder/errcodes"

	"github.com/thesatellite-ai/lore/adapters/machine"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
)

// installMethod is the closed set of ways lore can be installed (8.1). The
// one detection lives in adapters/machine (also the machine profile's
// install_method, OV28 "one shared install-method detection"); these names
// keep the upgrade code readable.
type installMethod machine.InstallMethod

const (
	installBrew   = installMethod(machine.InstallHomebrew)
	installScript = installMethod(machine.InstallScript)
	installSource = installMethod(machine.InstallSource)
	// installOther is a version manager's folder: lore changes nothing.
	installOther = installMethod(machine.InstallOther)
)

// String is how `lore upgrade` names the method to a person ("installed by
// Homebrew"); the profile keeps the lowercase stored value.
func (m installMethod) String() string {
	if m == installBrew {
		return "Homebrew"
	}
	return string(m)
}

// brewFormula is the formula .goreleaser.yml publishes under brews.
const brewFormula = "thesatellite-ai/tap/" + BinaryName

// detectInstall is machine.DetectInstall. A non-writable script install
// stays "script": the swap then answers E_UPGRADE_PERMISSION with the sudo
// hint (8.3), which says more than "changes nothing" (DECISION UPG-RO).
func detectInstall(exe, ver string) installMethod {
	return installMethod(machine.DetectInstall(exe, ver))
}

// resolvedExe is os.Executable with symlinks resolved.
func resolvedExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "find this lore binary").WithCause(err)
	}
	if r, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = r
	}
	return exe, nil
}

// projectMeta is the part of .lore/data/_meta.json `lore upgrade` reads.
// lore_version_pin is an additive key every lore keeps (rule 1, 8.4).
type projectMeta struct {
	Pin            string   `json:"lore_version_pin"`
	MinLoreVersion string   `json:"min_lore_version"`
	Features       []string `json:"features"`
}

// metaRelPath is the repo _meta.json under a project root.
var metaRelPath = filepath.Join(".lore", "data", "_meta.json")

// findProjectMeta walks up from dir to the nearest project with a
// _meta.json. ok is false outside a project (or with no _meta.json yet).
func findProjectMeta(dir string) (root string, m projectMeta, ok bool, err error) {
	for d := dir; ; {
		b, rerr := os.ReadFile(filepath.Clean(filepath.Join(d, metaRelPath)))
		switch {
		case rerr == nil:
			if jerr := json.Unmarshal(b, &m); jerr != nil {
				return d, m, false, errcodes.New(errcodes.Code(errs.OldLore), metaRelPath+" cannot be read").WithCause(jerr)
			}
			return d, m, true, nil
		case !errors.Is(rerr, fs.ErrNotExist):
			return d, m, false, errcodes.New(errcodes.Internal, "read "+metaRelPath).WithCause(rerr)
		}
		up := filepath.Dir(d)
		if up == d {
			return "", m, false, nil
		}
		d = up
	}
}

// envVersionPin is the CI pin; it overrides the team pin (8.4).
const envVersionPin = "LORE_VERSION_PIN"

// envCI ("CI", set to true by GitHub Actions and most CI systems) is
// declared in stores_redact.go.

// pinChoice is what the pins say about the target.
type pinChoice struct {
	// pin is the effective pin ("" when none); source names where it came from.
	pin, source string
}

// effectivePin: LORE_VERSION_PIN, else the team pin.
func effectivePin(m projectMeta) pinChoice {
	if v := strings.TrimSpace(os.Getenv(envVersionPin)); v != "" {
		return pinChoice{pin: v, source: envVersionPin}
	}
	if m.Pin != "" {
		return pinChoice{pin: m.Pin, source: "lore_version_pin in " + filepath.ToSlash(metaRelPath)}
	}
	return pinChoice{}
}

// chooseTarget applies 8.4: an explicit --version wins unless it fights the
// pin without --ignore-pin; otherwise the pin, otherwise the newest (looked
// up lazily, only when needed). CI without a pin or --version is refused.
func chooseTarget(want string, pin pinChoice, ignorePin, inCI bool, newest func() (string, error)) (string, error) {
	switch {
	case want != "" && pin.pin != "" && !ignorePin && normVersion(want) != normVersion(pin.pin):
		return "", errcodes.New(errcodes.Code(errs.UpgradePinned),
			fmt.Sprintf("this project pins lore %s (%s); refusing --version %s", normVersion(pin.pin), pin.source, normVersion(want))).
			WithHint("pass --ignore-pin to install " + normVersion(want) + " anyway")
	case want != "":
		return tagOf(want), nil
	case pin.pin != "" && !ignorePin:
		return tagOf(pin.pin), nil
	case inCI:
		return "", errcodes.New(errcodes.Code(errs.UpgradePinned),
			"CI must name its lore version: set "+envVersionPin+", add lore_version_pin to "+filepath.ToSlash(metaRelPath)+", or pass --version.")
	}
	return newest()
}

// normVersion drops a leading v ("v0.1.20" → "0.1.20").
func normVersion(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

// tagOf is the release tag of a version ("0.1.20" → "v0.1.20").
func tagOf(v string) string { return "v" + normVersion(v) }

// lockedOut lists why target could not write in this project: below its
// floor, or older than a declared feature's Since (rule 2). Empty: fine.
func lockedOut(target string, m projectMeta) []string {
	var why []string
	if m.MinLoreVersion != "" && formatgate.VersionBelow(target, m.MinLoreVersion) {
		why = append(why, "it needs "+normVersion(m.MinLoreVersion)+" or later")
	}
	for _, f := range m.Features {
		if k, ok := formatgate.Lookup(f); ok && formatgate.VersionBelow(target, k.Since) {
			why = append(why, "feature "+f+" needs "+k.Since)
		}
	}
	return why
}

// swapOps are the filesystem calls of the swap, a struct so the Windows
// rollback can be tested on any OS against a failing fake.
type swapOps struct {
	rename func(oldpath, newpath string) error
	remove func(name string) error
}

var osSwapOps = swapOps{rename: os.Rename, remove: os.Remove}

// oldExeSuffix is the Windows parking name of the replaced binary.
const oldExeSuffix = ".old"

// swapBinary puts newPath in place of exe (8.3). Unix: one atomic rename.
// Windows: a running .exe cannot be overwritten but can be renamed, so
// exe → exe.old, new → exe, and exe.old back if the second rename fails.
func swapBinary(goos, newPath, exe string, ops swapOps) error {
	if goos != goosWindows {
		return ops.rename(newPath, exe)
	}
	old := exe + oldExeSuffix
	_ = ops.remove(old) // a leftover from an earlier upgrade; still running is fine
	if err := ops.rename(exe, old); err != nil {
		return err
	}
	if err := ops.rename(newPath, exe); err != nil {
		if rerr := ops.rename(old, exe); rerr != nil {
			return fmt.Errorf("%w (and putting the old binary back failed: %w; it is at %s)", err, rerr, old)
		}
		return err
	}
	return nil
}

// removeOldWindowsBinary deletes a leftover lore.exe.old; the failure while
// an old process still runs it is ignored (8.3). Call at every start on
// Windows.
func removeOldWindowsBinary() {
	exe, err := resolvedExe()
	if err != nil {
		return
	}
	_ = os.Remove(exe + oldExeSuffix) // still in use by an old process: next start retries
}

// newBinaryPrefix names the temporary files beside the binary; they carry
// the pid, so two upgrades never collide (8.9).
const newBinaryPrefix = BinaryName + ".new-"

// staleTempAge is when an interrupted upgrade's temp file is swept.
const staleTempAge = time.Hour

// sweepStaleTemps deletes lore.new-<pid> files over an hour old in dir.
func sweepStaleTemps(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), newBinaryPrefix) {
			continue
		}
		if info, ierr := e.Info(); ierr == nil && time.Since(info.ModTime()) > staleTempAge {
			_ = os.Remove(filepath.Join(dir, e.Name())) // best effort; the next upgrade retries
		}
	}
}

// dirWritable probes dir by creating and removing a temp file.
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, newBinaryPrefix+"probe-")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()       // empty probe
	_ = os.Remove(name) // probe only
	return true
}

// skillDest is where install.sh puts the agent skill bundle.
func skillDest(home string) string { return filepath.Join(home, ".claude", "skills", BinaryName) }

// copySkills copies the archive's skills/ over dest when dest exists
// (install.sh does the same; a machine without the bundle is left alone).
func copySkills(dest string, files map[string][]byte) (bool, error) {
	if len(files) == 0 {
		return false, nil
	}
	if st, err := os.Stat(dest); err != nil || !st.IsDir() {
		return false, nil
	}
	for rel, b := range files {
		p := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), cliDirMode); err != nil {
			return false, err
		}
		if err := os.WriteFile(p, b, cliFileMode); err != nil {
			return false, err
		}
	}
	return true, nil
}
