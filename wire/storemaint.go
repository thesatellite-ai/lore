package wire

// storemaint.go — lore keeps its store clones packed (store growth SG-2,
// devco:D-996; the pass itself is adapters/backend/git/maintain.go).
//
//	any command ─► (after its output) StoreAutoMaintain: store.maintenance
//	               auto and a clone whose pass is due ─► one detached
//	               `lore store maintain --all --due` child; the command
//	               never waits for it
//	store.maintain ─► MaintainClones ─► git.MaintainClone per clone
//	lore doctor ─► store_maintenance: a clone over 2x its packed size, or
//	               whose last pass never finished, with the command
//
// Settings: `store: {maintenance: auto|off, maintenance_every: 24h}` in
// ~/.lore/config.yaml: like store.lock_timeout, how this machine keeps its
// own clones is about this machine, not a team rule.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/logic/yamlerr"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// Store maintenance settings.
const (
	// MaintAuto and MaintOff are the values of store.maintenance.
	MaintAuto = "auto"
	MaintOff  = "off"
	// maintChildEnv marks the background child, so it never starts another.
	maintChildEnv = "LORE_STORE_MAINT_CHILD"
	// maxMaintEvery caps store.maintenance_every (a typo like 2400h).
	maxMaintEvery = 30 * 24 * time.Hour
)

// maintDoc is the part of config.yaml this file reads.
type maintDoc struct {
	Store struct {
		Maintenance      string `yaml:"maintenance"`
		MaintenanceEvery string `yaml:"maintenance_every"`
	} `yaml:"store"`
}

// maintSettings are store.maintenance and store.maintenance_every.
type maintSettings struct {
	on    bool
	every time.Duration
}

// readMaintSettings reads both settings from <home>/config.yaml (defaults:
// auto, 24h). A value lore cannot use fails closed with E_CONFIG_INVALID
// (the automatic pass then stays off until it is fixed).
func readMaintSettings(home string) (maintSettings, error) {
	s := maintSettings{on: true, every: git.DefaultMaintEvery}
	b, err := readConfigRaw(home)
	if err != nil || b == nil {
		return s, err
	}
	var doc maintDoc
	if err = yamlerr.Decode(b, &doc, yamlerr.Lenient); err != nil {
		return s, configYAMLErr(err)
	}
	switch strings.TrimSpace(doc.Store.Maintenance) {
	case "", MaintAuto:
	case MaintOff:
		s.on = false
	default:
		return s, errs.New(errs.ConfigInvalid, lorehome.Shown()+"/"+ConfigFile+": store.maintenance "+doc.Store.Maintenance+" is not auto or off")
	}
	if v := strings.TrimSpace(doc.Store.MaintenanceEvery); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil || d < git.MinMaintEvery || d > maxMaintEvery {
			return s, errs.New(errs.ConfigInvalid, lorehome.Shown()+"/"+ConfigFile+": store.maintenance_every "+v+" is not a duration between 1h and 720h (e.g. 24h)")
		}
		s.every = d
	}
	return s, nil
}

// maintClones are the clones a pass works on: whole clones lore made
// (a person's own clone, `lore store use`, is theirs: never packed unless
// named).
func maintClones(home string) []git.CloneFound {
	found, err := git.ScanClones(home)
	if err != nil {
		return nil
	}
	var out []git.CloneFound
	for _, f := range found {
		if f.State == git.CloneWhole && !f.Own {
			out = append(out, f)
		}
	}
	return out
}

// StoreAutoMaintainSkips reports commands after which the automatic pass
// never starts: help and completion print only, the merge driver runs
// inside git.
func StoreAutoMaintainSkips(first string) bool {
	switch first {
	case "", "help", "completion", "__complete", "__completeNoDesc", "merge-driver":
		return true
	}
	return false
}

// StoreAutoMaintain starts one detached `lore store maintain --all --due`
// when store.maintenance is auto and a clone's pass is due. It only reads
// two stamp files per clone, never waits, and never fails the command. A
// dry run, a home lore cannot write, the child itself and a test binary
// start nothing.
func (a *App) StoreAutoMaintain(ctx context.Context) {
	home := a.opts.Home
	if home == "" || dryRunProcess() || os.Getenv(maintChildEnv) != "" || ctx.Err() != nil || !maintCheckNow(time.Now()) || !homeWritable(home) {
		return
	}
	s, err := readMaintSettings(home)
	if err != nil || !s.on {
		return
	}
	now := a.wall() // the app clock (LORE_NOW in a test build), as the stamps a new clone gets
	if len(dueClones(home, s.every, now)) == 0 {
		return
	}
	exe, err := os.Executable()
	if err != nil || isTestBinary(exe) {
		return
	}
	cmd := exec.Command(exe, "store", "maintain", "--all", "--due") // #nosec G204 -- lore's own binary, fixed arguments
	cmd.Env = append(os.Environ(), maintChildEnv+"=1")
	cmd.Dir = home
	maintChildDetach(cmd)
	if cmd.Start() == nil {
		_ = cmd.Process.Release() // never waited for: it outlives this command
	}
}

// maintCheckEvery spaces this process's looks for a due clone: the local
// read server runs every served read through the same end-of-command
// path, and a read there must not pay for a scan of the stores folder each
// time (the pass is daily; a minute late changes nothing).
const maintCheckEvery = time.Minute

// lastMaintCheck is when this process last looked (unix nanoseconds).
var lastMaintCheck atomic.Int64

// maintCheckNow reports whether this process may look for a due clone at
// now (the wall clock, never the test clock: it spaces real calls), and
// records the look.
func maintCheckNow(now time.Time) bool {
	last := lastMaintCheck.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < maintCheckEvery {
		return false
	}
	return lastMaintCheck.CompareAndSwap(last, now.UnixNano())
}

// smallClone is the .git size under which a due clone with nothing to
// pack is only stamped: a commit-graph pays off on a long history, and a
// child process for a clone of a few commits is more work than it saves.
const smallClone = 4 << 20

// dueClones are the clones whose pass is due and has work to do. A due
// clone with no repack to do and a small .git (one made by an earlier
// lore, a test's throwaway store) gets its stamp here instead, so no
// background process starts for it.
func dueClones(home string, every time.Duration, now time.Time) []git.CloneFound {
	var out []git.CloneFound
	for _, c := range maintClones(home) {
		if !git.MaintenanceDue(c.Paths.Dir, every, now) {
			continue
		}
		if size, err := git.SizeOf(c.Paths.Dir); err == nil && !size.NeedsRepack() && size.GitBytes < smallClone {
			git.MarkMaintained(c.Paths.Dir, now)
			continue
		}
		out = append(out, c)
	}
	return out
}

// isTestBinary reports a `go test` binary (pkg.test): running it with
// lore's arguments would run the tests again.
func isTestBinary(exe string) bool {
	base := strings.TrimSuffix(filepath.Base(exe), ".exe")
	return strings.HasSuffix(base, ".test")
}

// KnowsClone implements opsstores.Maintainer.
func (a *App) KnowsClone(_ context.Context, name string) bool {
	found, err := git.ScanClones(a.opts.Home)
	if err != nil {
		return false
	}
	for _, f := range found {
		if cloneMatches(f, name) {
			return true
		}
	}
	return false
}

// cloneMatches reports whether name names clone f: its folder under the
// stores folder, its path, or its origin url in any spelling.
func cloneMatches(f git.CloneFound, name string) bool {
	if name == f.Rel || filepath.Clean(name) == filepath.Clean(f.Paths.Dir) {
		return true
	}
	k, ok := storeKey(name)
	if !ok || f.Origin == "" {
		return false
	}
	ko, ok := storeKey(f.Origin)
	return ok && k == ko
}

// MaintainClones implements opsstores.Maintainer.
func (a *App) MaintainClones(ctx context.Context, in opsstores.MaintainIn) (opsstores.MaintainOut, error) {
	home := a.opts.Home
	s, err := readMaintSettings(home)
	if err != nil {
		return opsstores.MaintainOut{}, err
	}
	var targets []git.CloneFound
	if in.Store != "" {
		found, serr := git.ScanClones(home)
		if serr != nil {
			return opsstores.MaintainOut{}, errs.FileIO("list the store clones", filepath.Join(home, git.StoresDir), serr)
		}
		for _, f := range found {
			if f.State == git.CloneWhole && cloneMatches(f, in.Store) {
				targets = append(targets, f)
			}
		}
		if len(targets) == 0 {
			return opsstores.MaintainOut{}, errs.New(errs.NotFound, "store maintain: this machine has no clone of "+in.Store).
				WithHint("lore store show lists the clones; lore store maintain --all packs every one")
		}
	} else {
		if in.Due && !s.on {
			return opsstores.MaintainOut{}, nil // store.maintenance: off
		}
		targets = maintClones(home)
	}
	out := opsstores.MaintainOut{Clones: []opsstores.MaintainView{}}
	// clones `lore store reclone` replaced, kept a week (devco:D-1001)
	_, _ = git.SweepOldClones(home, a.wall())
	for _, f := range targets {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		res, merr := git.MaintainClone(ctx, f.Paths.Dir, git.MaintOptions{Every: s.every, Force: !in.Due, Gentle: in.Due, Now: a.wall()})
		if in.Due && res.Skipped == git.MaintSkipNotDue {
			continue // the background form lists only what it did
		}
		v := opsstores.MaintainView{Store: f.Rel, URL: f.Origin, Clone: f.Paths.Dir, Skipped: res.Skipped,
			Before: maintSize(res.Before), After: maintSize(res.After), Repacked: res.Repacked,
			CommitGraph: res.CommitGraph, Refs: res.Refs, TookMS: res.Took.Milliseconds()}
		if merr != nil {
			v.Error = errs.EnvelopeOf(merr).Error.Message
		}
		out.Clones = append(out.Clones, v)
	}
	return out, nil
}

func maintSize(s git.CloneSize) opsstores.MaintainSize {
	return opsstores.MaintainSize{GitBytes: s.GitBytes, Packs: s.Packs, PackBytes: s.PackBytes, Loose: s.Loose, LooseBytes: s.LooseBytes}
}

// DoctorCheckStoreMaintenance names the doctor check.
const DoctorCheckStoreMaintenance DoctorCheck = "store_maintenance"

// maintOverPacked is how much bigger than its packs a clone's .git may be
// before doctor names the pass (loose objects, many packs).
const maintOverPacked = 2

// DoctorStoreMaintenance notes each clone whose .git is over twice its
// packed size with more loose objects or packs than a pass leaves, or
// whose last pass started and never finished: its size, loose objects,
// packs and last pass, and the command that runs the pass now (a note:
// lore runs it by itself, nothing is broken). A packed clone prints
// nothing.
func (a *App) DoctorStoreMaintenance(context.Context) ([]DoctorFinding, error) {
	s, err := readMaintSettings(a.opts.Home)
	if err != nil {
		return nil, nil // `lore config check` reports a bad setting
	}
	now := a.wall() // the app clock (LORE_NOW in a test build), as the stamps a new clone gets
	var out []DoctorFinding
	for _, c := range maintClones(a.opts.Home) {
		size, serr := git.SizeOf(c.Paths.Dir)
		if serr != nil {
			continue
		}
		over := size.NeedsRepack() && size.GitBytes > maintOverPacked*size.PackBytes
		stuck := git.MaintenanceDue(c.Paths.Dir, s.every, now) && maintStarted(c.Paths.Dir, now)
		if !over && !stuck {
			continue
		}
		last := "never"
		if t, ok := git.LastMaintenance(c.Paths.Dir); ok {
			last = t.Local().Format("2006-01-02 15:04")
		}
		when := "lore runs it by itself after a command once its last pass is older than store.maintenance_every (" + shortDuration(s.every) + ")"
		if !s.on {
			when = "store.maintenance is off, so it waits for you"
		}
		out = append(out, DoctorFinding{Check: DoctorCheckStoreMaintenance, Level: DoctorNote,
			Message: "store clone " + c.Rel + ": .git " + opsstores.HumanBytes(size.GitBytes) + ", " + strconv.Itoa(size.Packs) + " pack(s) " + opsstores.HumanBytes(size.PackBytes) +
				", " + strconv.Itoa(size.Loose) + " loose object(s) " + opsstores.HumanBytes(size.LooseBytes) + "; last maintenance " + last + "; " + when,
			Fix: "lore store maintain --store " + c.Rel})
	}
	return out, nil
}

// maintStarted reports a pass that started over an hour ago and left no
// newer finish stamp (a crash or a git error).
func maintStarted(dir string, now time.Time) bool {
	tried, ok := git.MaintenanceTried(dir)
	if !ok || now.Sub(tried) < time.Hour {
		return false
	}
	last, done := git.LastMaintenance(dir)
	return !done || last.Before(tried)
}
