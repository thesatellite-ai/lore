package wire

// setupstore.go — setup.Store on *App (ONBOARDING_DESIGN 2.1 L7 row, 3.1,
// 3.4): the setups[] entries live in ~/.lore/config.yaml (config_setups.go
// does the node-tree edits), unfinished answers in the machine store's
// setup_progress table (MachineLocal, never synced), and the repos lore ran
// in in known_repos (knownrepos.go).
//
//	Config / PutSetup / RemoveSetup / PinRepo ─▶ config_setups.go helpers
//	Progress / PutProgress / DeleteProgress  ─▶ engine, store.SourceMachine, setup_progress
//	KnownRepos / TouchRepo                    ─▶ knownrepos.go
//	Active                                    ─▶ 3.3 resolution (App.ActiveSetup, setupactive.go)

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// setupStore is the App's setup.Store. It holds no state of its own: every
// call reads config.yaml or machine.db afresh, so two processes (the page
// and a CLI) never disagree through a cache.
type setupStore struct{ a *App }

var (
	_ opssetup.Store           = setupStore{}
	_ opssetup.GitHubValidator = setupStore{}
	_ opssetup.GitHubRecorder  = setupStore{}
)

// RecordGitHub implements opssetup.GitHubRecorder: on an ssh block (route
// ssh or empty, an account given) an empty route is recorded as ssh and an
// empty key_fingerprint as the route key's fingerprint (the same
// routeFingerprint the github step's apply records; "" when the key cannot
// be read, so the field stays empty and the next save tries again). A
// recorded fingerprint is kept; an https block records nothing about ssh.
func (s setupStore) RecordGitHub(ctx context.Context, g onboard.SetupGitHub) onboard.SetupGitHub {
	if g.IsHTTPS() || strings.TrimSpace(g.User) == "" {
		return g
	}
	if g.Route == "" {
		g.Route = onboard.RouteSSH
	}
	if g.KeyFingerprint == "" {
		g.KeyFingerprint = setupApplier(s).routeFingerprint(ctx, g)
	}
	return g
}

// ValidateGitHub implements opssetup.GitHubValidator for the given (non
// empty) fields: the probe.Route validator every git and ssh call of the
// setup's route runs (routeFor, the setup prober), so setup edit refuses
// what those would drop or refuse (a host alias or key path ssh could
// read as an option, a GitHub login the gh credential helper would not
// take); and the key file must exist, as github.ssh_key wants it (the
// file itself or its .pub, ~ expanded against the user's home).
func (setupStore) ValidateGitHub(_ context.Context, g onboard.SetupGitHub) error {
	alias := strings.TrimSpace(g.HostAlias)
	if alias == probe.GitHubHost {
		alias = ""
	}
	key := strings.TrimSpace(g.SSHKey)
	if err := (probe.Route{HostAlias: alias, SSHKey: key, HTTPSUser: strings.TrimSpace(g.User)}).Validate(); err != nil {
		return err
	}
	if key == "" {
		return nil
	}
	path := onboard.ExpandHome(key, userHome(SetupQuery{}))
	for _, p := range []string{path, path + sshPubSuffix} {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return nil
		}
	}
	return errs.New(errs.InvalidInput, "no key file at "+path).
		WithHint("give the path of an existing key file (private key or its .pub), or --no-ssh-key to use the alias's own key")
}

// sshPubSuffix is a public key file's suffix next to its private key.
const sshPubSuffix = ".pub"

// Config implements opssetup.Store: the onboarding keys of config.yaml,
// validated (a setup op acting on an invalid list would act on a guess).
func (s setupStore) Config(context.Context) (onboard.Config, error) {
	return ValidateSetupsConfig(s.a.opts.Home)
}

// PutSetup implements opssetup.Store.
func (s setupStore) PutSetup(_ context.Context, e onboard.SetupEntry) (bool, error) {
	return PutSetup(s.a.opts.Home, e)
}

// PatchSetup implements opssetup.SetupPatcher: only the keys the edit
// changed are written (PatchSetup).
func (s setupStore) PatchSetup(_ context.Context, before, after onboard.SetupEntry) error {
	return PatchSetup(s.a.opts.Home, before, after)
}

// RemoveSetup implements opssetup.Store: the entry, its repo_setups pins
// and its setup_progress rows (ONB 3.4: "lore setup remove deletes them").
// Progress goes first so a failed config write never leaves orphan rows
// for a setup that still exists only half.
func (s setupStore) RemoveSetup(ctx context.Context, name string) (bool, error) {
	if err := s.DeleteProgress(ctx, name); err != nil {
		return false, err
	}
	return RemoveSetup(s.a.opts.Home, name)
}

// PinRepo implements opssetup.Store: repo_setups[<this repo id>] = name
// (`lore setup use --here`, 3.3 step 3 "repo pin").
func (s setupStore) PinRepo(ctx context.Context, name string) (string, error) {
	repoID, ok := s.a.RepoIdentity(ctx)
	if !ok {
		// A git repo lore was never set up in has no repo id yet: say
		// that, not "run it inside a git repo" (it is one).
		if top, err := s.a.knownRepoTop(ctx); err == nil && top != "" {
			return "", errs.New(errs.InvalidInput, "setup use --here: this git repo has no lore project yet, so it has no repo id to pin ("+top+"/.lore/data/_meta.json is missing)").
				WithHint("run `lore init` here first, then `lore setup use --name " + name + " --here`")
		}
		repoID = ""
	}
	if err := SetRepoSetup(s.a.opts.Home, repoID, name); err != nil {
		return "", err
	}
	return repoID, nil
}

// Active implements opssetup.Store: the 3.3 resolution (setupactive.go).
func (s setupStore) Active(ctx context.Context, flag string) (opssetup.ActiveSetup, error) {
	return s.a.ActiveSetup(ctx, flag)
}

// progressRows lists the live setup_progress rows of setup ("" = all).
func (s setupStore) progressRows(ctx context.Context, setup string) ([]row.Row, error) {
	m, err := s.a.machineApp(ctx)
	if err != nil {
		return nil, err
	}
	eng, err := m.rowEngine(ctx) // List reads the machine store's rows directly: no cache refresh
	if err != nil {
		return nil, err
	}
	rows, err := eng.List(ctx, store.SourceMachine, schema.TableSetupProgress)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(rows, func(r row.Row) bool {
		return r.Deleted || (setup != "" && fieldText(r, schema.FieldProgressSetup) != setup)
	}), nil
}

// Progress implements opssetup.Store: the setup's rows in catalog step
// order (an unknown step id sorts last, by name).
func (s setupStore) Progress(ctx context.Context, setup string) ([]onboard.Progress, error) {
	rows, err := s.progressRows(ctx, setup)
	if err != nil {
		return nil, err
	}
	out := make([]onboard.Progress, 0, len(rows))
	for _, r := range rows {
		p, perr := progressOfRow(r)
		if perr != nil {
			return nil, perr
		}
		out = append(out, p)
	}
	order := onboard.StepIDs()
	rank := func(id onboard.StepID) int {
		if i := slices.Index(order, id); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortFunc(out, func(x, y onboard.Progress) int {
		if d := rank(x.Step) - rank(y.Step); d != 0 {
			return d
		}
		return strings.Compare(string(x.Step), string(y.Step))
	})
	return out, nil
}

// progressOfRow decodes one setup_progress row.
func progressOfRow(r row.Row) (onboard.Progress, error) {
	p := onboard.Progress{Setup: fieldText(r, schema.FieldProgressSetup), Step: onboard.StepID(fieldText(r, schema.FieldProgressStep)),
		Status: onboard.StepStatus(fieldText(r, schema.FieldProgressStatus)), UpdatedAt: r.UpdatedAt, Answers: onboard.Answers{}}
	if raw := fieldRawJSON(r, schema.FieldProgressAnswers); raw != nil {
		if err := json.Unmarshal(raw, &p.Answers); err != nil {
			return p, errs.Wrap(errs.Internal, "setup_progress "+p.Setup+"/"+string(p.Step)+": answers are not valid JSON", err).
				WithHint("lore setup remove --name " + p.Setup + " clears the saved progress")
		}
	}
	if raw := fieldRawJSON(r, schema.FieldProgressChecks); raw != nil {
		if err := json.Unmarshal(raw, &p.Checks); err != nil {
			return p, errs.Wrap(errs.Internal, "setup_progress "+p.Setup+"/"+string(p.Step)+": checks are not valid JSON", err).
				WithHint("lore setup remove --name " + p.Setup + " clears the saved progress")
		}
	}
	return p, nil
}

// fieldRawJSON is a json field's bytes whatever form it decoded as (JSON,
// the Text a row file holds before a schema-aware pass, or a List); nil when absent.
func fieldRawJSON(r row.Row, name string) []byte {
	switch v := r.Fields[name].(type) {
	case value.JSON:
		if v.IsZero() {
			return nil
		}
		return v.Raw()
	case value.Text:
		if v.String() == "" {
			return nil
		}
		return []byte(v.String())
	}
	// A JSON array may come back from the cache as a List: its JSON form
	// is the array again.
	raw, _ := value.JSONForm(r.Fields[name])
	return raw
}

// PutProgress implements opssetup.Store: one upsert keyed by (setup,
// step), the table's natural key. Bookkeeping, never part of a command's
// bulk action (lore batch undo must not take it back), as for known_repos.
func (s setupStore) PutProgress(ctx context.Context, p onboard.Progress) error {
	if p.Setup == "" || p.Step == "" {
		return errs.New(errs.InvalidInput, "setup progress needs a setup name and a step").WithHint("lore setup plan lists the steps")
	}
	ctx = engine.WithBatch(s.a.withNamedSetup(ctx, p.Setup), engine.Batch{})
	answers, err := json.Marshal(p.Answers)
	if err != nil {
		return errs.Wrap(errs.Internal, "encode setup progress answers", err)
	}
	checks := p.Checks
	if checks == nil {
		checks = []onboard.CheckResult{}
	}
	checksRaw, err := json.Marshal(checks)
	if err != nil {
		return errs.Wrap(errs.Internal, "encode setup progress checks", err)
	}
	fields := map[string]value.Value{}
	for k, raw := range map[string][]byte{schema.FieldProgressAnswers: answers, schema.FieldProgressChecks: checksRaw} {
		j, jerr := value.JSONOf(raw)
		if jerr != nil {
			return errs.Wrap(errs.Internal, "setup progress field "+k, jerr)
		}
		fields[k] = j
	}
	if err = setTexts(fields, map[string]string{schema.FieldProgressStatus: string(p.Status)}); err != nil {
		return err
	}
	rows, err := s.progressRows(ctx, p.Setup)
	if err != nil {
		return err
	}
	m, err := s.a.machineApp(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(rows, func(r row.Row) bool { return fieldText(r, schema.FieldProgressStep) == string(p.Step) })
	if i >= 0 {
		_, err = m.Update(ctx, engine.Ref{Store: store.SourceMachine, Table: schema.TableSetupProgress, ID: rows[i].ID}, fields)
		return err
	}
	if err = setTexts(fields, map[string]string{schema.FieldProgressSetup: p.Setup, schema.FieldProgressStep: string(p.Step)}); err != nil {
		return err
	}
	_, err = m.Create(ctx, store.SourceMachine, schema.TableSetupProgress, fields)
	return err
}

// DeleteProgress implements opssetup.Store: every row of setup (the
// summary step of a finished setup, setup.remove, and the old name of a
// setup.rename). The rows go for good, as PurgeProgress purges them: a
// step's progress is machine-local bookkeeping that nothing restores
// (setup trash restore brings back the config entry, never its steps),
// so a soft delete only filled `lore trash` with one setup_progress row
// per step of every setup a person ever finished (journey solo/day1:
// eight rows in the trash of someone who deleted nothing) and kept the
// answers in machine.db. Decided 2026-11-09, owner may change.
func (s setupStore) DeleteProgress(ctx context.Context, setup string) error {
	if setup == "" {
		return errs.New(errs.InvalidInput, "delete setup progress: no setup named").WithHint("pass the setup name")
	}
	ctx = engine.WithBatch(s.a.withNamedSetup(ctx, setup), engine.Batch{})
	rows, err := s.progressRows(ctx, setup)
	if err != nil {
		return err
	}
	m, err := s.a.machineApp(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err = m.purgeRow(ctx, engine.Ref{Store: store.SourceMachine, Table: schema.TableSetupProgress, ID: r.ID}); err != nil {
			return err
		}
	}
	return nil
}

// PurgeProgress deletes every setup_progress row of setup for good (the
// setup trash purge): the live rows and the ones already in the row trash
// (a finished step's rows go there, answers and all). Each is purged
// through the engine, so machine.db keeps only an id-only tombstone plus
// the id in machine_purged: no setup name, step or answer stays on disk,
// and `lore trash` no longer lists them. A soft delete (DeleteProgress)
// would keep the whole document in machine.db.
func (s setupStore) PurgeProgress(ctx context.Context, setup string) error {
	if setup == "" {
		return errs.New(errs.InvalidInput, "purge setup progress: no setup named").WithHint("pass the setup name")
	}
	ctx = engine.WithBatch(s.a.withNamedSetup(ctx, setup), engine.Batch{})
	live, err := s.progressRows(ctx, setup)
	if err != nil {
		return err
	}
	m, err := s.a.machineApp(ctx)
	if err != nil {
		return err
	}
	eng, err := m.Engine(ctx)
	if err != nil {
		return err
	}
	trashed, err := eng.Trashed(ctx)
	if err != nil {
		return err
	}
	var targets []row.Row
	targets = append(targets, live...)
	for _, t := range trashed {
		if t.Store == store.SourceMachine && t.Row.Table == schema.TableSetupProgress && fieldText(t.Row, schema.FieldProgressSetup) == setup {
			targets = append(targets, t.Row)
		}
	}
	for _, r := range targets {
		if err = m.purgeRow(ctx, engine.Ref{Store: store.SourceMachine, Table: schema.TableSetupProgress, ID: r.ID}); err != nil {
			return err
		}
	}
	return nil
}

// purgeRow is Delete's purge twin: the engine's Purge after the same
// write gate and actor row.
func (a *App) purgeRow(ctx context.Context, ref engine.Ref) error {
	st, err := a.writeStateFor(ctx, ref.Store)
	if err != nil {
		return err
	}
	if aerr := a.ensureActorFor(ctx, st, ref); aerr != nil {
		return aerr
	}
	_, err = st.eng.Purge(ctx, ref)
	a.wroteTo(ref.Store, err)
	return err
}

// KnownRepos implements opssetup.Store (Q11): the clones lore ran in
// whose folder is still there. A folder renamed, moved or deleted since
// is left out: doctor --all checked it anyway and reported its missing
// git config as "git config user.email is no email" under the old name
// (journey solo/month1/folder-renamed); the clone's new folder gets its
// own row the next time lore runs in it. The row itself stays (machine
// bookkeeping; a folder back in place is listed again).
func (s setupStore) KnownRepos(ctx context.Context) ([]onboard.KnownRepo, error) {
	all, err := s.a.KnownRepos(ctx)
	if err != nil {
		return nil, err
	}
	out := all[:0:0]
	for _, r := range all {
		if _, serr := os.Stat(r.Root); serr != nil && errors.Is(serr, fs.ErrNotExist) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// TouchRepo implements opssetup.Store: records r (root resolved like
// every 3.3 path compare); a zero LastSeen means now.
func (s setupStore) TouchRepo(ctx context.Context, r onboard.KnownRepo) error {
	if r.LastSeen.IsZero() {
		r.LastSeen = s.a.opts.Wall()
	}
	r.Root = resolvedPath(r.Root)
	return s.a.TouchRepo(ctx, r)
}
