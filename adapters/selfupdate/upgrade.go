// Package selfupdate replaces the running lore binary with another release
// (layer L3, `lore upgrade`, FORMAT_EVOLUTION 8, decision FUPG). It is the
// mechanism only: the operation upgrade.run (ops/selfupgrade) supplies the
// running version, the lore home, the working folder and the caller's
// confirmation, and prints what Run writes.
//
//	Updater.Run(flags)
//	  resolvedExe ─▶ detectInstall
//	    source ─▶ print how to update a source build; done
//	    other  ─▶ print path + target; done
//	  findProjectMeta(Dir) ─▶ effectivePin ─▶ chooseTarget (CI refusal, pin refusal, 8.4)
//	  --check ─▶ one line; done
//	  same version ─▶ "nothing to do"
//	  downgrade into a project the target cannot write ─▶ E_NEEDS_CONFIRM unless Confirmed (8.9)
//	  brew   ─▶ brew upgrade thesatellite-ai/tap/lore (no --version)
//	  script ─▶ lock <home>/state/upgrade.lock ─▶ fetch archive + checksums.txt
//	            ─▶ verifySHA ─▶ extract ─▶ temp file beside exe ─▶ `version --json` smoke
//	            ─▶ swapBinary ─▶ copySkills
//	  inside a project ─▶ new binary `setup --schema-only` (8.11 part 2)
package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/execfile"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
)

// BinaryName is the command name in every line and hint, and the name of
// the binary inside a release archive.
const BinaryName = "lore"

// File modes of what an upgrade writes besides the binary (the skill
// bundle, the temp file before it gets the running binary's mode): owner
// only, like every other file lore writes for one user.
const (
	cliFileMode fs.FileMode = 0o600
	cliDirMode  fs.FileMode = 0o750
)

// Flags are the choices of one upgrade (CLI_REFERENCE 3.5).
type Flags struct {
	// Check only reports whether a newer release exists.
	Check bool
	// Version installs exactly this release (also a downgrade).
	Version string
	// IgnorePin installs past the project pin.
	IgnorePin bool
}

// Updater is one upgrade's context. There is no package state: the caller
// names everything (no globals, the composition root supplies them).
type Updater struct {
	// Version is the running binary's version (ldflag-stamped).
	Version string
	// Home is the lore home (~/.lore); the machine lock lives under it.
	Home string
	// Dir is where the project pin is looked up (the caller's folder).
	Dir string
	// UserHome is the person's home folder, for the agent skill bundle
	// (~/.claude/skills/lore); "" skips the bundle.
	UserHome string
	// Confirmed is the caller's yes: it allows a downgrade into a project
	// the older binary could not write (8.9).
	Confirmed bool
	// ReleaseBase is the repository page releases hang off; "" means
	// DefaultReleaseBase. Tests point it at a local server.
	ReleaseBase string
	// VersionProbe returns the binary_version of the binary at a path (the
	// new binary's smoke test, and the "already installed" check after
	// waiting for the lock); nil means ProbeVersion. Tests fake it.
	VersionProbe func(ctx context.Context, bin string) (string, error)
}

// probe is the version probe this upgrade uses.
func (u Updater) probe(ctx context.Context, bin string) (string, error) {
	if u.VersionProbe != nil {
		return u.VersionProbe(ctx, bin)
	}
	return ProbeVersion(ctx, bin)
}

// sourceUpdateHint is how a source build is updated (8.8).
const sourceUpdateHint = "git pull && task lore:build"

// Run upgrades (or, with Check, only reports), writing its lines to out.
func (u Updater) Run(ctx context.Context, out io.Writer, f Flags) error {
	exe, err := resolvedExe()
	if err != nil {
		return err
	}
	method := detectInstall(exe, u.Version)
	if method == installSource {
		_, werr := fmt.Fprintf(out, "This %s (%s) was built from source. Update it with: %s\n", BinaryName, u.Version, sourceUpdateHint)
		return werr
	}
	if method == installBrew && f.Version != "" {
		return errs.New(errs.Unsupported, "--version is not available for a Homebrew install: the tap keeps only the newest formula").
			WithHint("run `" + BinaryName + " upgrade` for the newest release")
	}
	root, meta, inProject, err := findProjectMeta(u.Dir)
	if err != nil {
		return err
	}
	pin := effectivePin(meta)
	rc := newReleaseClient(u.ReleaseBase, upgradeHTTPTimeout, u.Version)
	newest := func() (string, error) { return rc.latestTag(ctx) }
	target, err := chooseTarget(f.Version, pin, f.IgnorePin, os.Getenv(envCI) == "true", newest)
	if err != nil {
		return err
	}
	have, want := normVersion(u.Version), normVersion(target)
	if pin.pin != "" && !f.IgnorePin && f.Version == "" && have == want {
		return pinnedLine(ctx, out, rc, have, pin)
	}
	if f.Check {
		if have == want || !formatgate.VersionBelow(have, want) {
			_, werr := fmt.Fprintf(out, "%s %s is the newest release; nothing to do.\n", BinaryName, have)
			return werr
		}
		_, werr := fmt.Fprintf(out, "%s %s is available (you have %s, installed by %s). Run: %s upgrade\n", BinaryName, want, have, method, BinaryName)
		return werr
	}
	if have == want {
		_, werr := fmt.Fprintf(out, "%s %s is the newest release; nothing to do.\n", BinaryName, have)
		return werr
	}
	if method == installOther {
		_, werr := fmt.Fprintf(out, "%s at %s is managed by another tool; changing nothing. It would have installed %s.\n", BinaryName, exe, want)
		return werr
	}
	if err := u.confirmDowngrade(out, have, want, filepath.Base(root), meta, inProject); err != nil {
		return err
	}
	switch method {
	case installBrew:
		if _, werr := fmt.Fprintf(out, "%s %s → %s (installed by Homebrew)\n", BinaryName, have, want); werr != nil {
			return werr
		}
		if err := runBrewUpgrade(ctx, out); err != nil {
			return err
		}
	default:
		if err := u.upgradeByScript(ctx, out, rc, exe, have, target); err != nil {
			return err
		}
	}
	if inProject {
		if err := runSchemaOnly(ctx, out, exe, root); err != nil {
			return err
		}
	}
	_, werr := fmt.Fprintf(out, "Done. Running %s processes keep %s until they exit.\n", BinaryName, have)
	return werr
}

// pinnedLine reports that the running binary is the project pin, naming a
// newer release when one exists.
func pinnedLine(ctx context.Context, out io.Writer, rc releaseClient, have string, pin pinChoice) error {
	latest, lerr := rc.latestTag(ctx)
	if _, werr := fmt.Fprintf(out, "%s %s is this project's pinned version (%s)", BinaryName, have, pin.source); werr != nil {
		return werr
	}
	if lerr == nil && normVersion(latest) != have {
		_, werr := fmt.Fprintf(out, "; the newest release is %s. Nothing to do.\nhint: %s upgrade --ignore-pin to install %s anyway\n",
			normVersion(latest), BinaryName, normVersion(latest))
		return werr
	}
	_, werr := fmt.Fprintln(out, ". Nothing to do.")
	return werr
}

// confirmDowngrade refuses, unless the caller confirmed, a downgrade into a
// project the target could not write (8.9). The refusal is E_NEEDS_CONFIRM
// with the question; the surface asks it with a terminal and runs again
// confirmed, and without one names the same command plus --yes (agents
// ask the person too).
func (u Updater) confirmDowngrade(out io.Writer, have, want, project string, meta projectMeta, inProject bool) error {
	if !formatgate.VersionBelow(want, have) {
		return nil
	}
	if _, werr := fmt.Fprintf(out, "%s %s → %s is a DOWNGRADE.\n", BinaryName, have, want); werr != nil {
		return werr
	}
	var why []string
	if inProject {
		why = lockedOut(want, meta)
	}
	if len(why) == 0 || u.Confirmed {
		return nil
	}
	question := fmt.Sprintf("%s would become read-only for %s %s: %s. Downgrade anyway?", project, BinaryName, want, strings.Join(why, "; "))
	return errs.New(errs.NeedsConfirm, question).WithQuestion(question).
		WithHint("ask the person, then run `"+BinaryName+" upgrade --version "+tagOf(want)+" --yes`").
		WithNext(BinaryName, "upgrade", "--version", tagOf(want), "--yes")
}

// runBrewUpgrade hands a Homebrew install to brew and shows its output.
func runBrewUpgrade(ctx context.Context, out io.Writer) error {
	if _, werr := fmt.Fprintf(out, "Running: brew upgrade %s\n", brewFormula); werr != nil {
		return werr
	}
	c := exec.CommandContext(ctx, "brew", "upgrade", brewFormula)
	c.Stdout, c.Stderr = out, os.Stderr
	if err := c.Run(); err != nil {
		return errs.Wrap(errs.Internal, "brew upgrade failed", err).
			WithHint("run `brew upgrade " + brewFormula + "` yourself to see why")
	}
	return nil
}

// upgradeLockFile is the machine lock of 8.9 under ~/.lore/state.
const upgradeLockFile = "upgrade.lock"

// lockPoll is how often a waiting upgrade retries the lock.
const lockPoll = 25 * time.Millisecond

// upgradeLockWait is how long a second upgrade waits for the first.
const upgradeLockWait = 60 * time.Second

// upgradeByScript downloads, checks and swaps the binary itself.
func (u Updater) upgradeByScript(ctx context.Context, out io.Writer, rc releaseClient, exe, have, tag string) error {
	unlock, err := u.lockUpgrade()
	if err != nil {
		return err
	}
	defer unlock()
	// The first upgrade may have finished while this one waited (8.9).
	if now, verr := u.probe(ctx, exe); verr == nil && normVersion(now) == normVersion(tag) {
		if _, werr := fmt.Fprintf(out, "already %s\n", normVersion(tag)); werr != nil {
			return werr
		}
		return nil
	}
	dir := filepath.Dir(exe)
	sweepStaleTemps(dir)
	if !dirWritable(dir) {
		return errs.New(errs.UpgradePermission, dir+" is not writable, so "+exe+" cannot be replaced").
			WithHint("run `sudo " + BinaryName + " upgrade`")
	}
	if _, werr := fmt.Fprintf(out, "%s %s → %s (installed by script at %s)\n", BinaryName, have, normVersion(tag), exe); werr != nil {
		return werr
	}
	name := archiveName(runtime.GOOS, runtime.GOARCH)
	archive, err := rc.fetch(ctx, tag, name)
	if err != nil {
		return err
	}
	if _, werr := fmt.Fprintf(out, "  downloaded %s (%.1f MB)\n", name, float64(len(archive))/(1<<20)); werr != nil {
		return werr
	}
	sums, err := rc.fetch(ctx, tag, checksumsFile)
	if err != nil {
		return err
	}
	if verifyErr := verifySHA(archive, sums, name); verifyErr != nil {
		return verifyErr
	}
	if _, werr := fmt.Fprintf(out, "  sha256 ok (%s of %s)\n", checksumsFile, tag); werr != nil {
		return werr
	}
	files, err := extract(archive, runtime.GOOS)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, newBinaryPrefix+strconv.Itoa(os.Getpid()))
	if runtime.GOOS == goosWindows {
		tmp += ".exe"
	}
	// Written owner-only first, then given the running binary's mode, so the
	// new binary is never readable or runnable by more users than the old one.
	// Under execfile.Do: the new binary is run right below (u.probe), and a
	// child of a concurrent fork still holding the write descriptor would make
	// that run fail with "text file busy".
	if writeFileErr := execfile.Do(func() error { return os.WriteFile(tmp, files.binary, cliFileMode) }); writeFileErr != nil {
		return errs.Wrap(errs.UpgradePermission, "write "+tmp, writeFileErr).
			WithHint("run `sudo " + BinaryName + " upgrade`")
	}
	if chmodErr := os.Chmod(tmp, binaryMode(exe)); chmodErr != nil {
		_ = os.Remove(tmp) // best effort: never leave a half-installed binary behind
		return errs.Wrap(errs.UpgradePermission, "make "+tmp+" executable", chmodErr).
			WithHint("run `sudo " + BinaryName + " upgrade`")
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmp) // never leave a half-installed binary behind
		}
	}()
	got, err := u.probe(ctx, tmp)
	if err != nil || normVersion(got) != normVersion(tag) {
		return errs.Wrap(errs.UpgradeChecksum,
			fmt.Sprintf("the new binary does not run as %s (got %q)", normVersion(tag), got), err).
			WithHint("nothing changed; run `" + BinaryName + " upgrade` again")
	}
	if _, werr := fmt.Fprintf(out, "  new binary runs: %s %s\n", BinaryName, normVersion(got)); werr != nil {
		return werr
	}
	if err := swapBinary(runtime.GOOS, tmp, exe, osSwapOps); err != nil {
		return errs.Wrap(errs.UpgradePermission, "replace "+exe, err).
			WithHint("run `sudo " + BinaryName + " upgrade`")
	}
	keep = true
	if runtime.GOOS == goosWindows {
		if _, werr := fmt.Fprintf(out, "  replaced %s (old copy %s%s is removed on the next start)\n", filepath.Base(exe), filepath.Base(exe), oldExeSuffix); werr != nil {
			return werr
		}
	} else {
		if _, werr := fmt.Fprintf(out, "  replaced %s\n", exe); werr != nil {
			return werr
		}
	}
	if u.UserHome != "" {
		// ONB 4.7.1: the copied SKILL.md carries the new binary's version
		// line, not the archive's `lore_version: dev`.
		skillFiles := map[string][]byte{}
		for rel, b := range files.skills {
			if rel == skillEntryFile {
				b = toolprofile.StampSkillVersion(b, normVersion(tag))
			}
			skillFiles[rel] = b
		}
		if done, cerr := copySkills(skillDest(u.UserHome), skillFiles); cerr != nil {
			if _, werr := fmt.Fprintf(out, "  agent skill bundle not updated: %v\n", cerr); werr != nil {
				return werr
			}
		} else if done {
			if _, werr := fmt.Fprintln(out, "  updated the agent skill bundle"); werr != nil {
				return werr
			}
		}
		// One hint line when any installed skill (another tool's folder, or a
		// copy that failed) is older than the new binary (ONB 4.7.1).
		if hint := staleSkillHint(u.UserHome, normVersion(tag)); hint != "" {
			if _, werr := fmt.Fprintln(out, "  "+hint); werr != nil {
				return werr
			}
		}
	}
	return nil
}

// skillEntryFile is the skill bundle's entry page, the one with the
// lore_version line (skills.EntryFile; not imported to keep the release
// archive the only bundle this package sees).
const skillEntryFile = "SKILL.md"

// staleSkillHint is toolprofile.UpgradeSkillHint when any built-in tool's
// installed SKILL.md under home is older than version (no version line, or
// a lower release), else "". Unreadable or absent folders are not stale.
func staleSkillHint(home, version string) string {
	reg, err := toolprofile.New(nil)
	if err != nil {
		return ""
	}
	for _, p := range reg.SkillProfiles() {
		b, rerr := os.ReadFile(filepath.Join(p.SkillPath(home), skillEntryFile))
		if rerr == nil && toolprofile.SkillOlder(b, version) {
			return toolprofile.UpgradeSkillHint(version)
		}
	}
	return ""
}

// lockUpgrade takes <home>/state/upgrade.lock, waiting up to a minute.
func (u Updater) lockUpgrade() (func(), error) {
	path := filepath.Join(u.Home, "state", upgradeLockFile)
	deadline := time.Now().Add(upgradeLockWait)
	for {
		l, err := acquireLock(path)
		switch {
		case err == nil:
			return func() { _ = l.release() }, nil // the OS drops the flock at exit anyway
		case errors.Is(err, errReadOnly):
			return func() {}, nil
		case !errors.Is(err, errLockHeld):
			return nil, errs.Wrap(errs.Internal, "lock "+path, err)
		}
		if time.Now().After(deadline) {
			return nil, errs.New(errs.LockHeld, "another lore upgrade has held "+path+" for over "+upgradeLockWait.String())
		}
		time.Sleep(lockPoll)
	}
}

// versionProbeTimeout bounds the new binary's smoke test.
const versionProbeTimeout = 30 * time.Second

// ProbeVersion runs `<bin> version --json` and returns binary_version (the
// stable JSON contract of the version command); Updater's default probe.
func ProbeVersion(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	b, err := exec.CommandContext(ctx, bin, "version", "--json").Output()
	if err != nil {
		return "", err
	}
	var v struct {
		BinaryVersion string `json:"binary_version"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return "", err
	}
	return v.BinaryVersion, nil
}

// runSchemaOnly runs the NEW binary's `setup --schema-only` in the project
// (8.11 part 2). A failure is reported, not fatal: the swap is done, and the
// next command in the project updates the database itself. Only a failed
// write to out is returned.
func runSchemaOnly(ctx context.Context, out io.Writer, exe, root string) error {
	if _, err := os.Stat(filepath.Join(root, ".lore", "lore.db")); err != nil {
		return nil
	}
	if _, err := fmt.Fprintln(out, "Running: "+BinaryName+" setup --schema-only (new binary)"); err != nil {
		return err
	}
	c := exec.CommandContext(ctx, exe, "setup", "--schema-only")
	c.Dir, c.Stdout, c.Stderr = root, out, out
	if err := c.Run(); err != nil {
		_, werr := fmt.Fprintf(out, "  database not updated (%v); the next command in this project updates it\n", err)
		return werr
	}
	return nil
}

// defaultBinaryMode is the mode of a fresh binary when the running one cannot
// be read: rwxr-xr-x, the mode install.sh and the release archives use, since
// a binary on a shared PATH must be runnable by every user.
const defaultBinaryMode fs.FileMode = 0o755

// binaryMode returns the permission bits the upgraded binary gets: the
// running binary's own, so an upgrade never widens or narrows who can run
// lore, or defaultBinaryMode when exe cannot be read or is not executable.
func binaryMode(exe string) fs.FileMode {
	info, err := os.Stat(exe)
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		return defaultBinaryMode
	}
	return info.Mode().Perm()
}
