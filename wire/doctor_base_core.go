package wire

// doctor_base_core.go — the base section of `lore doctor` for a binary
// without lore.db (cmd/lore): the [identity] section and the PATH check of
// v0.1.17's runDoctor (saas/cmd/cli/doctor.go steps 3 and 4), which the
// old binary's doctorHost supplies. cmd/lore hands it to the app as
// Options.Context.Doctor (the live hook wraps it, live_doctor.go), so the
// report no longer shows an empty identity:
//
//	CoreDoctorBase ─▶ identity: the chain every write uses; ephemeral or
//	               │  unresolvable ─▶ warning, degraded
//	               └▶ PATH: every "lore" executable on $PATH; more than one
//	                  ─▶ warning, degraded
//
// There is no lore.db to check, so DBOK stays true and the WAL size 0.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// doctor base words (v0.1.17's).
const (
	doctorEphemeralWarning = "identity is ephemeral; future sessions won't link"
	doctorPathWarning      = "multiple lore binaries on PATH: %s"
	doctorIdentityWarning  = "identity: cannot resolve: %v"
	// pathEnv is the search path variable.
	pathEnv = "PATH"
	// windowsGOOS and windowsExe: on Windows the binary is lore.exe.
	windowsGOOS = "windows"
	windowsExe  = ".exe"
	// execBits are the permission bits of an executable file.
	execBits fs.FileMode = 0o111
)

// CoreDoctorBase is the doctor base of a binary without lore.db
// (opsdoctor.Host.Base; fix has nothing to repair here).
func (a *App) CoreDoctorBase(ctx context.Context) opsdoctor.Base {
	b := opsdoctor.Base{Status: opsdoctor.StatusHealthy, DBOK: true}
	warn := func(msg string) {
		b.Warnings = append(b.Warnings, msg)
		if b.Status == opsdoctor.StatusHealthy {
			b.Status = opsdoctor.StatusDegraded
		}
	}
	if r, err := resolveIdentity(ctx, a.opts); err != nil {
		warn(fmt.Sprintf(doctorIdentityWarning, err))
	} else {
		b.Identity = opsdoctor.Identity{Resolved: r.StableKey, Source: string(r.Step), Stable: r.Stable()}
		if !r.Stable() {
			warn(doctorEphemeralWarning)
		}
	}
	if found := lorePathBinaries(os.Getenv(pathEnv)); len(found) > 1 {
		b.PathConflicts = found
		warn(fmt.Sprintf(doctorPathWarning, strings.Join(found, ", ")))
	}
	return b
}

// lorePathBinaries lists every lore executable in the folders of path, in
// PATH order, each once (v0.1.17 ran `which -a lore`; this reads the
// folders itself, so it needs no external program).
func lorePathBinaries(path string) []string {
	name := doctorBinaryName
	if runtime.GOOS == windowsGOOS {
		name += windowsExe
	}
	seen := map[string]bool{}
	var out []string
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		p := filepath.Clean(filepath.Join(dir, name))
		if seen[p] {
			continue
		}
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != windowsGOOS && info.Mode().Perm()&execBits == 0) {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
