// upgrade.go — `lore upgrade` (FORMAT_EVOLUTION 8, decision FUPG).
//
//	lore upgrade [--check] [--version vX] [--ignore-pin] [--yes]
//	  resolvedExe ─▶ detectInstall
//	    source ─▶ print how to update a source build; done
//	    other  ─▶ print path + target; done
//	  findProjectMeta(cwd) ─▶ effectivePin ─▶ chooseTarget (CI refusal, pin refusal, 8.4)
//	  --check ─▶ one line; done
//	  same version ─▶ "nothing to do"
//	  downgrade into a project the target cannot write ─▶ confirm / E_NEEDS_CONFIRM (8.9)
//	  brew   ─▶ brew upgrade thesatellite-ai/tap/lore (no --version)
//	  script ─▶ lock ~/.lore/state/upgrade.lock ─▶ fetch archive + checksums.txt
//	            ─▶ verifySHA ─▶ extract ─▶ temp file beside exe ─▶ `version --json` smoke
//	            ─▶ swapBinary ─▶ copySkills
//	  inside a project ─▶ new binary `setup --schema-only` (8.11 part 2)
package main

import (
	"bufio"
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

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/processlock"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
	"golang.org/x/term"
)

// upgradeFlags are the command's flags.
type upgradeFlags struct {
	check, ignorePin, yes bool
	version               string
}

func newUpgradeCommand() *cobra.Command {
	var f upgradeFlags
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Install the newest lore release (or this project's pinned version)",
		Long: `Install the newest lore release, however lore was installed.

Homebrew installs run brew upgrade; script and archive installs download the
release, check its sha256 against checksums.txt and swap the binary atomically.
Inside a project that pins a version (lore_version_pin in .lore/data/_meta.json,
or LORE_VERSION_PIN), the pin is installed instead. In CI a version must be
named. After the swap the local database schema is updated with the new binary.`,
		Example: `  lore upgrade
  lore upgrade --check
  lore upgrade --version v0.1.19`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpgrade(cmd.Context(), cmd.OutOrStdout(), f)
		},
	}
	cmd.Flags().BoolVar(&f.check, "check", false, "say whether a newer release exists; change nothing")
	cmd.Flags().StringVar(&f.version, "version", "", "install exactly this release (also used to downgrade)")
	cmd.Flags().BoolVar(&f.ignorePin, "ignore-pin", false, "install past this project's pinned version")
	cmd.Flags().BoolVar(&f.yes, "yes", false, "confirm a downgrade into a project the older lore could not write")
	return cmd
}

// sourceUpdateHint is how a source build is updated (8.8).
const sourceUpdateHint = "git pull && task lore:build"

func runUpgrade(ctx context.Context, out io.Writer, f upgradeFlags) error {
	exe, err := resolvedExe()
	if err != nil {
		return err
	}
	method := detectInstall(exe, version)
	if method == installSource {
		fmt.Fprintf(out, "This %s (%s) was built from source. Update it with: %s\n", BinaryName, version, sourceUpdateHint)
		return nil
	}
	if method == installBrew && f.version != "" {
		return errcodes.New(errcodes.Code(errs.Unsupported), "--version is not available for a Homebrew install: the tap keeps only the newest formula").
			WithHint("run `" + BinaryName + " upgrade` for the newest release")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	root, meta, inProject, err := findProjectMeta(cwd)
	if err != nil {
		return err
	}
	pin := effectivePin(meta)
	rc := newReleaseClient(upgradeHTTPTimeout)
	newest := func() (string, error) { return rc.latestTag(ctx) }
	target, err := chooseTarget(f.version, pin, f.ignorePin, os.Getenv(envCI) == "true", newest)
	if err != nil {
		return err
	}
	have, want := normVersion(version), normVersion(target)
	if pin.pin != "" && !f.ignorePin && f.version == "" && have == want {
		latest, lerr := rc.latestTag(ctx)
		fmt.Fprintf(out, "%s %s is this project's pinned version (%s)", BinaryName, have, pin.source)
		if lerr == nil && normVersion(latest) != have {
			fmt.Fprintf(out, "; the newest release is %s. Nothing to do.\n", normVersion(latest))
			fmt.Fprintf(out, "hint: %s upgrade --ignore-pin to install %s anyway\n", BinaryName, normVersion(latest))
			return nil
		}
		fmt.Fprintln(out, ". Nothing to do.")
		return nil
	}
	if f.check {
		if have == want || !formatgate.VersionBelow(have, want) {
			fmt.Fprintf(out, "%s %s is the newest release; nothing to do.\n", BinaryName, have)
			return nil
		}
		fmt.Fprintf(out, "%s %s is available (you have %s, installed by %s). Run: %s upgrade\n", BinaryName, want, have, method, BinaryName)
		return nil
	}
	if have == want {
		fmt.Fprintf(out, "%s %s is the newest release; nothing to do.\n", BinaryName, have)
		return nil
	}
	if method == installOther {
		fmt.Fprintf(out, "%s at %s is managed by another tool; changing nothing. It would have installed %s.\n", BinaryName, exe, want)
		return nil
	}
	if proceed, err := confirmDowngrade(out, have, want, filepath.Base(root), meta, inProject, f.yes); err != nil || !proceed {
		return err
	}
	switch method {
	case installBrew:
		fmt.Fprintf(out, "%s %s → %s (installed by Homebrew)\n", BinaryName, have, want)
		if err := runBrewUpgrade(ctx, out); err != nil {
			return err
		}
	default:
		if err := upgradeByScript(ctx, out, rc, exe, have, target); err != nil {
			return err
		}
	}
	if inProject {
		runSchemaOnly(ctx, out, exe, root)
	}
	fmt.Fprintf(out, "Done. Running %s processes keep %s until they exit.\n", BinaryName, have)
	return nil
}

// confirmDowngrade asks before a downgrade into a project the target could
// not write (8.9): a prompt with a terminal, else E_NEEDS_CONFIRM with the
// same command plus --yes (agents override: agents ask the human too).
func confirmDowngrade(out io.Writer, have, want, project string, meta projectMeta, inProject, yes bool) (bool, error) {
	if !formatgate.VersionBelow(want, have) {
		return true, nil
	}
	fmt.Fprintf(out, "%s %s → %s is a DOWNGRADE.\n", BinaryName, have, want)
	why := []string{}
	if inProject {
		why = lockedOut(want, meta)
	}
	if len(why) == 0 || yes {
		return true, nil
	}
	question := fmt.Sprintf("%s would become read-only for this lore: %s. Downgrade anyway?", project, strings.Join(why, "; "))
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, errcodes.New(errcodes.Code(errs.NeedsConfirm), question).
			WithHint("ask the person, then run `" + BinaryName + " upgrade --version " + tagOf(want) + " --yes`")
	}
	fmt.Fprintf(out, "%s [y/N] ", question)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n') // EOF answers no
	if a := strings.ToLower(strings.TrimSpace(line)); a == "y" || a == "yes" {
		return true, nil
	}
	fmt.Fprintln(out, "Nothing changed.")
	return false, nil
}

// runBrewUpgrade hands a Homebrew install to brew and shows its output.
func runBrewUpgrade(ctx context.Context, out io.Writer) error {
	fmt.Fprintf(out, "Running: brew upgrade %s\n", brewFormula)
	c := exec.CommandContext(ctx, "brew", "upgrade", brewFormula)
	c.Stdout, c.Stderr = out, os.Stderr
	if err := c.Run(); err != nil {
		return errcodes.New(errcodes.Internal, "brew upgrade failed").WithCause(err).
			WithHint("run `brew upgrade " + brewFormula + "` yourself to see why")
	}
	return nil
}

// upgradeLockFile is the machine lock of 8.9 under ~/.lore/state.
const upgradeLockFile = "upgrade.lock"

// upgradeLockWait is how long a second upgrade waits for the first.
const upgradeLockWait = 60 * time.Second

// upgradeByScript downloads, checks and swaps the binary itself.
func upgradeByScript(ctx context.Context, out io.Writer, rc releaseClient, exe, have, tag string) error {
	unlock, err := lockUpgrade()
	if err != nil {
		return err
	}
	defer unlock()
	// The first upgrade may have finished while this one waited (8.9).
	if now, verr := runVersion(ctx, exe); verr == nil && normVersion(now) == normVersion(tag) {
		fmt.Fprintf(out, "already %s\n", normVersion(tag))
		return nil
	}
	dir := filepath.Dir(exe)
	sweepStaleTemps(dir)
	if !dirWritable(dir) {
		return errcodes.New(errcodes.Code(errs.UpgradePermission), dir+" is not writable, so "+exe+" cannot be replaced").
			WithHint("run `sudo " + BinaryName + " upgrade`")
	}
	fmt.Fprintf(out, "%s %s → %s (installed by script at %s)\n", BinaryName, have, normVersion(tag), exe)
	name := archiveName(runtime.GOOS, runtime.GOARCH)
	archive, err := rc.fetch(ctx, tag, name)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "  downloaded %s (%.1f MB)\n", name, float64(len(archive))/(1<<20))
	sums, err := rc.fetch(ctx, tag, checksumsFile)
	if err != nil {
		return err
	}
	if err := verifySHA(archive, sums, name); err != nil {
		return err
	}
	fmt.Fprintf(out, "  sha256 ok (%s of %s)\n", checksumsFile, tag)
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
	if err := os.WriteFile(tmp, files.binary, cliFileMode); err != nil {
		return errcodes.New(errcodes.Code(errs.UpgradePermission), "write "+tmp).WithCause(err).
			WithHint("run `sudo " + BinaryName + " upgrade`")
	}
	if err := os.Chmod(tmp, binaryMode(exe)); err != nil {
		_ = os.Remove(tmp) // best effort: never leave a half-installed binary behind
		return errcodes.New(errcodes.Code(errs.UpgradePermission), "make "+tmp+" executable").WithCause(err).
			WithHint("run `sudo " + BinaryName + " upgrade`")
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmp) // never leave a half-installed binary behind
		}
	}()
	got, err := runVersion(ctx, tmp)
	if err != nil || normVersion(got) != normVersion(tag) {
		return errcodes.New(errcodes.Code(errs.UpgradeChecksum),
			fmt.Sprintf("the new binary does not run as %s (got %q)", normVersion(tag), got)).WithCause(err).
			WithHint("nothing changed; run `" + BinaryName + " upgrade` again")
	}
	fmt.Fprintf(out, "  new binary runs: %s %s\n", BinaryName, normVersion(got))
	if err := swapBinary(runtime.GOOS, tmp, exe, osSwapOps); err != nil {
		return errcodes.New(errcodes.Code(errs.UpgradePermission), "replace "+exe).WithCause(err).
			WithHint("run `sudo " + BinaryName + " upgrade`")
	}
	keep = true
	if runtime.GOOS == goosWindows {
		fmt.Fprintf(out, "  replaced %s (old copy %s%s is removed on the next start)\n", filepath.Base(exe), filepath.Base(exe), oldExeSuffix)
	} else {
		fmt.Fprintf(out, "  replaced %s\n", exe)
	}
	if home, herr := os.UserHomeDir(); herr == nil {
		if done, cerr := copySkills(skillDest(home), files.skills); cerr != nil {
			fmt.Fprintf(out, "  agent skill bundle not updated: %v\n", cerr)
		} else if done {
			fmt.Fprintln(out, "  updated the agent skill bundle")
		}
	}
	return nil
}

// lockUpgrade takes ~/.lore/state/upgrade.lock, waiting up to a minute.
func lockUpgrade() (func(), error) {
	home, err := loreHome()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, "state", upgradeLockFile)
	deadline := time.Now().Add(upgradeLockWait)
	for {
		l, err := processlock.Acquire(path)
		switch {
		case err == nil:
			return func() { _ = l.Release() }, nil // the OS drops the flock at exit anyway
		case errors.Is(err, processlock.ErrReadOnly):
			return func() {}, nil
		case !errors.Is(err, processlock.ErrLockHeld):
			return nil, errcodes.New(errcodes.Internal, "lock "+path).WithCause(err)
		}
		if time.Now().After(deadline) {
			return nil, errcodes.New(errcodes.LockHeld, "another lore upgrade has held "+path+" for over "+upgradeLockWait.String())
		}
		time.Sleep(schemaLockPoll)
	}
}

// versionProbeTimeout bounds the new binary's smoke test.
const versionProbeTimeout = 30 * time.Second

// runVersion runs `<bin> version --json` and returns binary_version (the
// stable JSON contract of version.go). A variable so tests can fake it.
var runVersion = func(ctx context.Context, bin string) (string, error) {
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
// next command in the project updates the database itself.
func runSchemaOnly(ctx context.Context, out io.Writer, exe, root string) {
	if _, err := os.Stat(filepath.Join(root, ".lore", "lore.db")); err != nil {
		return
	}
	fmt.Fprintln(out, "Running: "+BinaryName+" setup --schema-only (new binary)")
	c := exec.CommandContext(ctx, exe, "setup", "--schema-only")
	c.Dir, c.Stdout, c.Stderr = root, out, out
	if err := c.Run(); err != nil {
		fmt.Fprintf(out, "  database not updated (%v); the next command in this project updates it\n", err)
	}
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
