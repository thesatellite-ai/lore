package wire

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/machine"
	"github.com/thesatellite-ai/lore/adapters/procwalk"
	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// StoreIdentity is one attached store's permanent identity as the private
// render (STORE_SERVER_SPEC 7.3, CFG6) names its file:
// ~/.lore/render/<repo_id>/<ID>.md. Several attached sources (an org
// store's two folders) share one ID.
type StoreIdentity struct {
	// Name is the attached source name (engine Stores() spelling).
	Name store.Source
	// ID is the sto_ id from lore-store.yaml; "" for a v0.1.17 marker or
	// a backend without one.
	ID string
	// Folder is the attached folder ("" = the store root).
	Folder string
	// Shareable is true when the marker reads visibility: shareable
	// (absent = internal, fail closed).
	Shareable bool
}

// StoreIdentities lists every attached store except store.SourceRepo, in
// the engine's Stores() order.
func (a *App) StoreIdentities(ctx context.Context) ([]StoreIdentity, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	var out []StoreIdentity
	for _, name := range st.eng.Stores() {
		if name == store.SourceRepo {
			continue
		}
		si := StoreIdentity{Name: name, Folder: st.folders[name]}
		if m, ok := concrete(st.byName[name]).(markerOf); ok {
			mk := m.Marker()
			if !mk.ID.IsZero() {
				si.ID = mk.ID.String()
			}
			si.Shareable = mk.Visibility == rowfs.VisibilityShareable
		}
		out = append(out, si)
	}
	return out, nil
}

// ---- agent detection (AGENT_IDENTITY_DESIGN 3, R11 B+) ----

// DetectAgent is the real detection the identity chain runs: the
// allowlisted environment (os.Getenv is only called with names from the
// agentdetect table, 6.3), whether stdin is a terminal (OQ11), the
// ancestor walk (adapters/procwalk) and the package.json reader for the
// tool version (OQ12). Plugin detectors (PQ1) are not wired yet.
// In loretest builds LORE_NO_DETECT=1 makes it see nothing (cl-gate).
func DetectAgent() (agentdetect.Result, error) {
	return detectAgent(func() agentdetect.Inputs {
		return agentdetect.Inputs{Getenv: os.Getenv, TTY: stdinTTY(), Procs: procwalk.New(), Files: machine.OSFiles{}}
	})
}

// detectAgent is DetectAgent over the given inputs: the build's detection
// switch first (identity.DetectionOff, honoured only in loretest builds),
// then agentdetect.Detect. The inputs are built only when detection runs.
// A seam so the switch can be tested with a fixed environment, terminal
// and ancestor walk instead of whatever shell runs the test.
func detectAgent(inputs func() agentdetect.Inputs) (agentdetect.Result, error) {
	if identity.DetectionOff() {
		return agentdetect.Result{}, nil
	}
	return agentdetect.Detect(inputs())
}

// stdinTTY reports whether stdin is a terminal (StdinIsTerminal, P3-TTY-1).
func stdinTTY() bool { return StdinIsTerminal() }

// Who is the App's resolved identity (zero when Options named the actor).
func (a *App) Who(ctx context.Context) (identity.Resolved, error) {
	st, err := a.state(ctx)
	if err != nil {
		return identity.Resolved{}, err
	}
	return st.who, nil
}

// CallerAgent is the agent name the CLI puts on op.Caller.Agent so
// agents.person_only also fires on the CLI (8.2): the stable key of an
// agent identity (detected or LORE_ACTOR=agent:…), "" for a person, the
// CI actor, or an App whose actor was set by Options.
func (a *App) CallerAgent(ctx context.Context) string {
	who, err := a.Who(ctx)
	if err != nil {
		return ""
	}
	return who.Agent
}

// ---- machine profile (AGENT_IDENTITY_DESIGN 6.8, CFG11) ----

// ThisMachine reads this machine's id (the ~/.lore/machine.json cache,
// CFG11) and its profile now, as machines-row fields (ops/infra.Machines).
func (a *App) ThisMachine(context.Context) (opsinfra.ThisMachine, error) {
	id, src, err := machine.Load(a.opts.Home, os.Getenv)
	if err != nil {
		return opsinfra.ThisMachine{}, err
	}
	rowID, err := identity.Deterministic(ids.PrefixMachine, id.String())
	if err != nil {
		return opsinfra.ThisMachine{}, err
	}
	p := machine.Collect(os.Getenv, machine.OSFiles{})
	fields := map[string]value.Value{}
	put := func(k, s string) error {
		if s == "" {
			return nil
		}
		t, terr := value.TextOf(s)
		if terr != nil {
			return errs.Wrap(errs.InvalidInput, "wire: machine profile field "+k, terr)
		}
		fields[k] = t
		return nil
	}
	putJSON := func(k string, b []byte, merr error) error {
		if merr != nil {
			return errs.Wrap(errs.Internal, "wire: encode machine profile field "+k, merr)
		}
		if string(b) == "{}" {
			return nil
		}
		j, jerr := value.JSONOf(b)
		if jerr != nil {
			return errs.Wrap(errs.Internal, "wire: machine profile field "+k, jerr)
		}
		fields[k] = j
		return nil
	}
	for _, kv := range [][2]string{{schema.FieldMachineID, id.String()}, {"id_source", string(src)}, {"hostname", p.Hostname},
		{"arch", p.Arch}, {"os_user", p.OSUser}, {"install_method", string(machine.ThisInstall(a.opts.Version))}} {
		if err := put(kv[0], kv[1]); err != nil {
			return opsinfra.ThisMachine{}, err
		}
	}
	osB, e1 := json.Marshal(p.OS)
	kerB, e2 := json.Marshal(p.Kernel)
	libB, e3 := json.Marshal(p.Libc)
	cpuB, e4 := json.Marshal(p.CPU)
	rtB, e5 := json.Marshal(p.Runtime)
	if err := errors.Join(putJSON("os", osB, e1), putJSON("kernel", kerB, e2), putJSON("libc", libB, e3), putJSON("cpu", cpuB, e4), putJSON("runtime", rtB, e5)); err != nil {
		return opsinfra.ThisMachine{}, err
	}
	if p.RAMMB > 0 {
		n, nerr := value.NumberOf(strconv.FormatInt(p.RAMMB, 10))
		if nerr != nil {
			return opsinfra.ThisMachine{}, errs.Wrap(errs.Internal, "wire: machine ram_mb", nerr)
		}
		fields["ram_mb"] = n
	}
	return opsinfra.ThisMachine{RowID: rowID, ID: id, Fields: fields}, nil
}

// MachineTarget is where this machine's profile is written: to when it
// names an attached own store, else the first attached store that is
// neither the code repo, client-readable nor read-only (OV59). Machine data never goes into
// a code repo (Q-R3-9) nor a client repo (MACHINE PROFILE).
func (a *App) MachineTarget(ctx context.Context, to string) (store.Source, error) {
	st, err := a.state(ctx)
	if err != nil {
		return "", err
	}
	refuse := func(why string) (store.Source, error) {
		return "", errs.New(errs.Refused, "machine profiles live in our own stores, never in a code or client repo: "+why).
			WithHint("attach an org or personal store (lore store add), or pass --to <store>")
	}
	if to != "" {
		name := store.Source(to)
		if !slices.Contains(st.eng.Stores(), name) {
			return "", errs.New(errs.NotFound, "store "+to+" is not attached")
		}
		if name == store.SourceRepo || st.eng.IsClient(name) {
			return refuse("store " + to + " is a repo or client-readable")
		}
		if st.eng.ReadOnly(name) {
			return "", errs.Wrap(errs.StoreReadOnly, "store "+to+" is attached read-only", store.ErrReadOnly).
				WithHint("pass --to an own store that is not read_only")
		}
		return name, nil
	}
	// OV59: the automatic pick skips a read-only own store (a write there
	// is E_STORE_READ_ONLY) and takes the next own store.
	for _, name := range st.eng.Stores() {
		if name != store.SourceRepo && !st.eng.IsClient(name) && !st.eng.ReadOnly(name) {
			return name, nil
		}
	}
	return refuse("no writable own store is attached")
}

var _ opsinfra.Machines = (*App)(nil)

// MachineVersions is the team_versions read (OQ7) over every attached
// store's cache, folded per machine: the newest lore version each full
// machine id saved with and when (ops/infra.MachineVersions, the LORE and
// LAST WRITE columns of `lore machines`). Code-repo saves carry no machine
// id (Q-R3-9) and are skipped.
func (a *App) MachineVersions(ctx context.Context) (map[string]opsinfra.MachineVersion, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]opsinfra.MachineVersion{}
	for _, c := range cacheFiles(st.caches) {
		tvs, terr := c.TeamVersions(ctx)
		if terr != nil {
			return nil, terr
		}
		for _, tv := range tvs {
			if tv.Machine == "" {
				continue
			}
			// RFC 3339 UTC text sorts by time: the newest save wins.
			if cur, ok := out[tv.Machine]; !ok || tv.LastSeen > cur.LastWrite {
				out[tv.Machine] = opsinfra.MachineVersion{Version: tv.Version, LastWrite: tv.LastSeen}
			}
		}
	}
	return out, nil
}

// RefreshMachineProfile writes this machine's profile into its own store
// when a field changed (OV3 MACH-AUTO: with the next ordinary write, not
// only through `lore whoami --machine-label`). wire's Run calls it once
// after a successful write command; a failure is a notice, never the
// command's error.
func (a *App) RefreshMachineProfile(ctx context.Context) (bool, error) {
	return opsinfra.RefreshMachineProfile(ctx, a, a)
}

// afterWrite runs after a successful operation (wire Run): a write
// command refreshes this machine's profile in its own store (MACH-AUTO).
// machine.label already wrote it; a failure is only a notice.
func (a *App) afterWrite(ctx context.Context, e op.Entry) {
	if e.Spec().Access != op.AccessWrite || e.Spec().Name == opsinfra.OpMachineLabel || opscoord.SignalsOnly(e.Spec().Name) || migrateBookkeeping(e.Spec().Name) {
		return
	}
	_, err := a.RefreshMachineProfile(ctx)
	switch {
	case err == nil, errs.Has(err, errs.StoreReadOnly):
		// A read-only own store simply holds no profile from here: not
		// news on every write, so no notice.
	case a.opts.Notice != nil:
		a.opts.Notice("machine profile not refreshed: " + err.Error())
	}
}

// migrateBookkeeping reports the migrate operations, which carry no
// MACH-AUTO profile write (BUGFIX-07): a run rewrites the format of rows
// already there (a profile written after it lands outside the run's
// journal, so the rollback lists it as kept and keeps _meta.json raised),
// a rollback that just put a store back must not leave new actor and
// machine rows in it (`lore migrate status` then counted them as if the
// rollback had added rows), enable writes only _meta.json, and unlock
// only removes a lock file.
func migrateBookkeeping(name string) bool {
	switch name {
	case opsmigrate.OpRun, opsmigrate.OpEnable, opsmigrate.OpRollback, opsmigrate.OpUnlock:
		return true
	}
	return false
}

var _ opsinfra.MachineVersions = (*App)(nil)

// cacheFiles is every distinct open cache file of the router (a combined
// cache is shared by many sources: listed once), in source order.
func cacheFiles(c *caches) []*sqlite.Cache {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[*sqlite.Cache]bool{}
	var out []*sqlite.Cache
	add := func(x *sqlite.Cache) {
		if x != nil && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	for _, name := range c.order {
		add(c.by[name])
	}
	add(c.combined)
	return out
}
