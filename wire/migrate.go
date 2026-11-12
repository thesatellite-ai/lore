package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/locktrace"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsplan "github.com/thesatellite-ai/lore/ops/plan"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"

	"gopkg.in/yaml.v3"
)

var _ opsmigrate.Rewriter = (*App)(nil)

// MigratedTables implements opsmigrate.Rewriter: the core-owned tables.
func (a *App) MigratedTables() []row.Table { return CoreTables() }

// CoreTables is THE list of tables whose nouns run on the new core (their
// operations are in Registry). migrate.run rewrites exactly these; the
// v0.1.17 binary's lsync coreOwned set must name the same tables.
func CoreTables() []row.Table {
	return []row.Table{
		schema.TableMemories,
		schema.TablePatterns, schema.TablePlaybooks, schema.TablePrompts,
		schema.TableRules, schema.TableDecisions, schema.TableHotfixes,
		schema.TableTags, schema.TableComments, schema.TableCommitLinks,
		schema.TableKinds, schema.TableKindFields, schema.TableResources,
		schema.TableVars, schema.TablePolicies, schema.TableMetaKeys,
		schema.TableMetaValues, schema.TableSecretRules, schema.TableSecretAllows,
		schema.TableTasks, schema.TableTasklists, schema.TableMissions, schema.TablePlans,
		schema.TableReminders, schema.TableKnowledgeRefs, schema.TableRuleVerifierRefs,
		schema.TableArchitectureNotes, schema.TableBehaviours, schema.TableCookbookRecipes, schema.TableIncidents,
		schema.TableSuggestions, schema.TableTastePrefs, schema.TableWorkflows, schema.TableWorkspaces,
		schema.TableHandoffs, schema.TableSnapshots, schema.TableTechDocs, schema.TableExternalSources,
		// Core-only tables added overnight (lsync owned set lists them too).
		schema.TableMachines, schema.TableTaskTemplates, schema.TableTaskViews, schema.TableScopes,
	}
}

// MigrateLockFile is the migration lock, relative to the repo top
// (git-ignored .lore/state). It is a local lock: two `lore migrate run` on
// one machine never interleave. The team-wide lock on the signals ref of
// FORMAT_EVOLUTION 8.10 arrives with the signals noun.
const MigrateLockFile = ".lore/state/migrate.lock"

// lockFileMode is the lock file's mode (it names a process id only).
const lockFileMode = 0o600

// LockMigration implements opsmigrate.Rewriter: create the lock file
// exclusively, recording this process (migrate_lock.go). A lock whose
// holder ran on this machine and is gone is taken over with one notice
// line; a running holder, or another machine's, is E_MIGRATE_LOCKED at
// once, naming it.
func (a *App) LockMigration(ctx context.Context) (func() error, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(top, filepath.FromSlash(MigrateLockFile))
	if err := acquireMigrateLock(ctx, path, a.opts.Notice); err != nil {
		return nil, err
	}
	locktrace.Lock(locktrace.Taken, path)
	return func() error {
		locktrace.Lock(locktrace.Released, path)
		return os.Remove(path)
	}, nil
}

// WriteGate implements opsmigrate.Rewriter: the engine's format gate on
// every attached store, so `lore migrate run` refuses E_OLD_LORE where
// every other write does (INTEG-MIGREST-1).
//
// A lock held by a running (or another machine's) migration answers
// first, before opening the stores, which syncs them (owner item 46: a
// held lock took minutes to report).
func (a *App) WriteGate(ctx context.Context) error {
	if top, err := a.repoTop(ctx); err == nil {
		if err := migrateLockBlocked(top); err != nil {
			return err
		}
	}
	st, err := a.state(ctx)
	if err != nil {
		return err
	}
	for _, name := range st.eng.Stores() {
		if err := st.eng.FormatGate(ctx, name); err != nil {
			return err // unprefixed: the same message as every other refused write
		}
	}
	return nil
}

// MigrateSettings implements opsmigrate.Rewriter: Mode B, the personal
// store flatten, CFG9 identity, the v1 store markers of every attached
// store, the org and the entity_tags links. Rows counts every row those
// steps write or move (opsmigrate.Settings), so `count:` never says 0
// for a run that moved rows.
func (a *App) MigrateSettings(ctx context.Context, dryRun bool) (opsmigrate.Settings, error) {
	steps, rows, err := a.migrateSettings(ctx, dryRun)
	return opsmigrate.Settings{Steps: steps, Rows: rows}, err
}

// migrateSettings is MigrateSettings returning its steps and row count.
func (a *App) migrateSettings(ctx context.Context, dryRun bool) ([]string, int, error) {
	steps, rows, err := a.MigrateModeB(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	// the checkout's own v0.1.17 lore.db (migrate_repodb.go): before the
	// live rows and the v1 rewrite, which then see its rows in .lore/data
	db, n, err := a.migrateRepoDB(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, db...), rows+n
	personal, n, err := a.migrateLocalPersonal(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, personal...), rows+n
	// rows a newer lore moved to another store whose old copy is still
	// here (migrate_moved.go): before every step that looks an id up
	moved, n, err := a.migrateMovedLeftovers(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, moved...), rows+n
	legacy, ok, err := identity.ReadLegacy(a.opts.Home)
	if err != nil {
		return nil, 0, err
	}
	if ok {
		mc, cerr := readMachineConfig(a.opts.Home)
		if cerr != nil {
			return nil, 0, cerr
		}
		// OWNERQ-8: the old file is moved aside as identity.yaml.migrated,
		// never deleted (never delete user config, as Q-MODEB and
		// `lore identity set|unset`); the step line names where it went.
		aside := "; old file kept as ~/.lore/" + identity.LegacyFile + LegacyMigratedSuffix
		step := "identity: ~/.lore/" + identity.LegacyFile + " moved into ~/.lore/" + ConfigFile + aside
		if mc.Identity.Key != "" {
			step = "identity: " + ConfigFile + " already sets identity: (kept)" + aside
		}
		steps = append(steps, step)
		if !dryRun {
			if mc.Identity.Key == "" {
				if err = setTopIdentity(a.opts.Home, legacy); err != nil {
					return nil, 0, err
				}
			}
			if _, err = retireLegacyIdentity(a.opts.Home); err != nil {
				return nil, 0, err
			}
		}
	}
	list, err := a.ListStores(ctx)
	if err != nil {
		return nil, 0, err
	}
	seen := map[string]bool{}
	for _, e := range list {
		step, serr := a.migrateStoreMarker(ctx, e, seen, dryRun)
		if serr != nil {
			return nil, 0, serr
		}
		if step != "" {
			steps = append(steps, step)
		}
	}
	orgSteps, n, err := a.migrateOrg(ctx, dryRun, seen)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, orgSteps...), rows+n
	tagSteps, n, err := a.migrateTags(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, tagSteps...), rows+n
	snakeSteps, n, err := a.migrateTagsSnakeCase(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, snakeSteps...), rows+n
	dbSteps, n, err := a.migrateDBSettings(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, dbSteps...), rows+n
	urlSteps, n, err := a.migrateURLCredentials(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, urlSteps...), rows+n
	auditSteps, n, err := a.migrateAuditFields(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, auditSteps...), rows+n
	periodSteps, n, err := a.migrateActorPeriods(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, periodSteps...), rows+n
	piiSteps, n, err := a.migratePIIPatterns(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, piiSteps...), rows+n
	// edits 0.1.17 kept only in lore.db beside migrated files (v017edits.go)
	editSteps, n, err := a.migrateOldDBEdits(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, editSteps...), rows+n
	// --supersedes links lore 0.1.16 and older wrote on the new row
	// (hostbase_doctor_supersede.go)
	supSteps, n, err := a.migrateSupersedes(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, supSteps...), rows+n
	// actors whose stable key the org roster holds under another id
	// (actor_merge.go)
	actorSteps, n, err := a.migrateActorKeys(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, actorSteps...), rows+n
	// P3-LOCAL-3: the checkout's v0.1.17 machine-local rows, copied once
	machineSteps, n, err := a.migrateMachineLocal(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	steps, rows = append(steps, machineSteps...), rows+n
	// actor copies stale under the 0.1.18 identity (migrate_actorcopies.go)
	copySteps, n, err := a.migrateActorCopies(ctx, dryRun)
	if err != nil {
		return nil, 0, err
	}
	return append(steps, copySteps...), rows + n, nil
}

// StepPIIPatterns names the pii_patterns step (3.3, SETTINGS ARE DATA).
const StepPIIPatterns = "pii_patterns"

// piiPatternsFile is v0.1.17's per-user pattern file under the lore home,
// one regex per line, # comments.
const piiPatternsFile = "pii-patterns.txt"

// piiRulePrefix + the file line number is the rule id each line becomes,
// v0.1.17's own naming (scrub.go LoadUserPatterns), so old audit lines
// still read.
const piiRulePrefix = "user:"

// migratePIIPatterns converts ~/.lore/pii-patterns.txt into personal
// secret_rules rows (3.3): each pattern line becomes rule "user:<line>"
// with state on in the personal store (so it applies to this person's own
// captures only, BATCH 8), and the file is kept as pii-patterns.txt
// .migrated (Q-MODEB: never delete user config). A pattern that does not
// compile fails closed (E_CONFIG_INVALID naming the line, never the
// pattern); with no personal store attached the file is left in place and
// the step says so (the config.yaml fallback is not built). Idempotent:
// a rule id the personal store already holds is skipped.
func (a *App) migratePIIPatterns(ctx context.Context, dryRun bool) ([]string, int, error) {
	path := filepath.Join(a.opts.Home, piiPatternsFile)
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, errs.Wrap(errs.ConfigInvalid, "migrate: read ~/.lore/"+piiPatternsFile, err)
	}
	type pat struct{ id, re string }
	var pats []pat
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, cerr := regexp.Compile(line); cerr != nil {
			return nil, 0, errs.New(errs.ConfigInvalid, "migrate: ~/.lore/"+piiPatternsFile+" line "+strconv.Itoa(i+1)+" is not a valid pattern").
				WithHint("fix or remove that line, then run lore migrate run again")
		}
		pats = append(pats, pat{id: piiRulePrefix + strconv.Itoa(i+1), re: line})
	}
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	personal := store.Source(opsstores.PersonalName)
	b, ok := st.byName[personal]
	if !ok {
		return []string{StepPIIPatterns + ": ~/.lore/" + piiPatternsFile + " kept: no personal store is attached to receive its " + strconv.Itoa(len(pats)) + " rule(s)"}, 0, nil
	}
	have := map[string]bool{}
	rows, _, err := b.Read(ctx, "")
	if err != nil {
		return nil, 0, err
	}
	for _, sr := range rows {
		if sr.Table != row.Table(schema.TableSecretRules) || sr.Deleted {
			continue
		}
		if r, derr := sr.Decode(); derr == nil {
			if t, isText := r.Fields["rule_id"].(value.Text); isText {
				have[t.String()] = true
			}
		}
	}
	steps := []string{StepPIIPatterns + ": ~/.lore/" + piiPatternsFile + " → " + strconv.Itoa(len(pats)) + " personal secret rule(s); file kept as " + piiPatternsFile + migratedSuffix}
	written := 0
	for _, p := range pats {
		if have[p.id] {
			continue
		}
		steps = append(steps, StepPIIPatterns+": rule "+p.id+" (state on, personal store)")
		if dryRun {
			continue
		}
		fields := map[string]value.Value{}
		for k, v := range map[string]string{"rule_id": p.id, "regex": p.re, "state": schema.SecretRuleOn, "description": "converted from ~/.lore/" + piiPatternsFile} {
			t, terr := value.TextOf(v)
			if terr != nil {
				return nil, 0, terr
			}
			fields[k] = t
		}
		if _, cerr := st.eng.Create(ctx, personal, schema.TableSecretRules, fields); cerr != nil {
			return nil, 0, errs.Prefix("migrate: personal secret rule "+p.id, cerr)
		}
		written++
	}
	if !dryRun {
		if err = os.Rename(path, path+migratedSuffix); err != nil {
			return nil, 0, errs.Wrap(errs.Internal, "migrate: keep ~/.lore/"+piiPatternsFile+" as "+piiPatternsFile+migratedSuffix, err)
		}
	}
	return steps, written, nil
}

// StepURLCredentials names the url_credentials step (FORMAT_EVOLUTION
// step 7, SECRETS_AND_REFERENCES_DESIGN 14.5).
const StepURLCredentials = "url_credentials"

// migrateURLCredentials is the url_credentials step over the synced rows
// of every attached store (14.5, D24): each live row whose URL-named text
// field (repourl.URLFieldName) still holds a credential is rewritten
// through the engine's Update, which strips it (engine stripURLs) with a
// fresh HLC and an audit event, so peers merge the clean value. A step
// line names the store, row and field only, never the value, and says to
// rotate, because git history may keep the old value. Idempotent: a clean
// row is not touched. A value lore cannot read is reported, not written.
//
// Not covered here (reported by the agent): ~/.lore/config.yaml store URLs
// and the bare clones' remotes (attach refuses a password there already),
// and url-typed values (value.URLOf strips them on read).
func (a *App) migrateURLCredentials(ctx context.Context, dryRun bool) ([]string, int, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	var steps []string
	written := 0
	names := slices.Sorted(maps.Keys(st.byName))
	for _, name := range names {
		rows, _, rerr := st.byName[name].Read(ctx, "")
		if rerr != nil {
			return nil, 0, rerr
		}
		for _, sr := range rows {
			if sr.Deleted {
				continue
			}
			r, derr := sr.Decode()
			if derr != nil {
				continue // a row the format step reports
			}
			patch := map[string]value.Value{}
			for _, f := range slices.Sorted(maps.Keys(r.Fields)) {
				t, ok := r.Fields[f].(value.Text)
				if !ok || !repourl.URLFieldName(f) || t.String() == "" {
					continue
				}
				clean, removed, serr := repourl.Strip(t.String())
				where := StepURLCredentials + ": store " + string(name) + " " + sr.Table.String() + "/" + sr.ID.String() + " field " + f
				if errors.Is(serr, repourl.ErrUnreadable) {
					steps = append(steps, where+": cannot be read as a URL and may hold a credential; fix it by hand, then rotate the credential")
					continue
				}
				if serr != nil || clean == t.String() {
					continue
				}
				ct, terr := value.TextOf(clean)
				if terr != nil {
					return nil, 0, terr
				}
				patch[f] = ct
				if removed.Warn() {
					steps = append(steps, where+": a credential was removed (stored "+clean+"); this value may be in git history, rotate the credential")
				} else {
					steps = append(steps, where+": a user name was removed (stored "+clean+")")
				}
			}
			if len(patch) == 0 || dryRun {
				continue
			}
			if _, uerr := st.eng.Update(ctx, engine.Ref{Store: name, Table: sr.Table, ID: sr.ID}, patch); uerr != nil {
				if errs.Has(uerr, errs.StoreReadOnly) {
					// the next write of the row by someone who can write it strips it (engine stripURLs)
					steps = append(steps, StepURLCredentials+": store "+string(name)+" is read-only here: "+sr.ID.String()+" not rewritten")
					continue
				}
				return nil, 0, uerr
			}
			written++
		}
	}
	return steps, written, nil
}

// rosterTables are the org roster's tables (LORE_TEAM_SPEC F18): not in
// CoreTables (the v0.1.17 binary still owns the repo's own actors), but
// rewritten as `_v: 2` in the org's stores, where the core keeps the
// roster once the store is 0.1.18 (wire roster.go).
var rosterTables = []row.Table{schema.TableActors, schema.TableTeams}

// orgDefaultsFile is <org folder>/lore-org.yaml (<org folder>/.lore/ in v0.1.17 stores,
// package OrgDefaultsFile), whose `defaults` the org_defaults_to_policy
// step moves into policies rows (CFG8).
const orgDefaultsFile = orgcfg.OrgFile

// migratedSuffix ends the untouched copy of a config file a migration step
// rewrote (owner rule Q-MODEB: never delete user config).
const migratedSuffix = ".migrated"

// StepOrgDefaultsToPolicy names the step that moves lore-org.yaml
// `defaults.client_repos` into a policy.KeyClientRoutes row of the org
// folder (spec 12, CFG8).
const StepOrgDefaultsToPolicy = "org_defaults_to_policy"

// orgDefaultsKey is the lore-org.yaml mapping key the step removes.
const orgDefaultsKey = "defaults"

// orgDefaultsToPolicy is the org_defaults_to_policy step for one attached
// org folder s (dir is its store's local folder): lore-org.yaml's
// defaults.client_repos becomes one routing.client_repos policies row in
// that folder, written through the engine (scanned, stamped, audited,
// committed); the file keeps its structure without `defaults`, and its
// old text is kept beside it as lore-org.yaml.migrated. A folder that
// already holds the policy row only gets the file rewrite (run twice =
// no second row). The routing reads the row from then on
// (policy.Set.ClientRoutes). The int is the policy rows written (would be,
// under dryRun): 1, or 0 when the folder already holds the row.
func (a *App) orgDefaultsToPolicy(ctx context.Context, st *state, s attach.Store, dir string, dryRun bool) ([]string, int, error) {
	rel := ""
	// The folder comes from the org's link, so every file access goes
	// through an os.Root on the store clone: a folder such as ../x can
	// never read or write outside it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, 0, errs.Wrap(errs.Internal, "migrate: open org store clone "+string(s.Name), err)
	}
	defer func() { _ = root.Close() }() // read-only handle on a directory: nothing to flush
	var b []byte
	for _, c := range orgcfg.OrgFilePaths(s.Folder) {
		if b, err = root.ReadFile(filepath.FromSlash(c)); err == nil {
			rel = filepath.FromSlash(c)
			break
		}
	}
	if rel == "" {
		return nil, 0, nil // no lore-org.yaml in this folder (or a folder outside the clone): nothing to move
	}
	var p orgDefaultsProbe
	if yaml.Unmarshal(b, &p) != nil || len(p.Defaults.ClientRepos) == 0 {
		return nil, 0, nil
	}
	items := make([]string, 0, len(p.Defaults.ClientRepos))
	for _, k := range slices.Sorted(maps.Keys(p.Defaults.ClientRepos)) {
		items = append(items, policy.ClientRouteItem(k, p.Defaults.ClientRepos[k]))
	}
	step := StepOrgDefaultsToPolicy + ": org store " + string(s.Name) + ": " + orgDefaultsFile + " defaults.client_repos → policy " +
		string(policy.KeyClientRoutes) + " = " + strings.Join(items, ",") + " (file kept as " + orgcfg.OrgFileKept + ")"
	has := false
	if _, attached := st.byName[s.Name]; attached {
		rows, lerr := st.eng.List(ctx, s.Name, schema.TablePolicies)
		if lerr != nil {
			return nil, 0, lerr
		}
		has = slices.ContainsFunc(rows, func(r row.Row) bool {
			k, ok := r.Fields[policyFieldKey].(value.Text)
			return !r.Deleted && ok && k.String() == string(policy.KeyClientRoutes)
		})
	} else if !dryRun {
		return []string{StepOrgDefaultsToPolicy + ": org store " + string(s.Name) + " is not attached; " + orgDefaultsFile + " left as it is"}, 0, nil
	}
	written := 1
	if has {
		written = 0 // the row is there already: only the file is rewritten
	}
	if dryRun {
		return []string{step}, written, nil
	}
	if !has {
		fields, ferr := clientRoutesPolicy(items, s.Folder)
		if ferr != nil {
			return nil, 0, ferr
		}
		if _, err = st.eng.Create(ctx, s.Name, schema.TablePolicies, fields); err != nil {
			return nil, 0, errs.Prefix("migrate: write the routing policy into "+string(s.Name), err)
		}
	}
	var doc yaml.Node
	if err = yaml.Unmarshal(b, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, 0, errs.New(errs.ConfigInvalid, "migrate: "+orgDefaultsFile+" of "+string(s.Name)+" is not a mapping")
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == orgDefaultsKey {
			m.Content = slices.Delete(m.Content, i, i+2)
			break
		}
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, 0, errs.Wrap(errs.Internal, "migrate: encode "+orgDefaultsFile, err)
	}
	// the kept copy's name is orgcfg's, so the store check knows it
	kept := strings.TrimSuffix(rel, orgDefaultsFile) + orgcfg.OrgFileKept
	if err = root.WriteFile(kept, b, orgFileMode); err != nil {
		return nil, 0, errs.Wrap(errs.Internal, "migrate: keep "+orgcfg.OrgFileKept, err)
	}
	if err = root.WriteFile(rel, out, orgFileMode); err != nil {
		return nil, 0, errs.Wrap(errs.Internal, "migrate: rewrite "+orgDefaultsFile, err)
	}
	if !strings.HasPrefix(s.URL, folder.Scheme+"://") {
		slash := filepath.ToSlash(rel)
		// FLAG4: the migration commit is signed by the lore identity only when
		// git has none for this clone; an existing git identity wins.
		if err = gitCommitFilesAs(ctx, dir, a.opts.Wall(), storeIdentityFunc(a.opts), migrateOrgSubject, slash, filepath.ToSlash(kept)); err != nil {
			return nil, 0, err
		}
	}
	return []string{step}, written, nil
}

// Policies row fields the migration writes (schema.Builtin policies).
const (
	policyFieldKey   = "key"
	policyFieldValue = "value"
	policyFieldScope = "scope"
)

// orgFileMode is the mode of the lore-org.yaml files the step writes
// (store files are owner-only, spec 6.1).
const orgFileMode = 0o600

// migrateOrgSubject is the commit subject of the org file rewrite.
const migrateOrgSubject = "lore: migrate org defaults to policies"

// orgDefaultsProbe is the part of lore-org.yaml migrateOrg looks at.
type orgDefaultsProbe struct {
	Defaults struct {
		ClientRepos map[string]string `yaml:"client_repos"`
	} `yaml:"defaults"`
}

// migrateOrg converts the v0.1.17 org this repo resolves to (the stores
// Options.OrgStores brings): each org store's v1 lore-store.yaml becomes v2
// (a permanent sto_ id, visibility internal: CFG3, CFG4, step store_marker),
// and the roster rows (actors, teams) of every org store folder become
// `_v: 2`, which moves the roster onto the core. The links need no
// conversion: they stay in each folder's _meta.json (CONFIG_GUIDE), which
// the core leaves untouched. Each org folder's lore-org.yaml
// defaults.client_repos becomes a routing.client_repos policy row (step
// org_defaults_to_policy, orgDefaultsToPolicy; the original file is kept as
// lore-org.yaml.migrated). seen lists store folders whose marker was
// already handled. Commits are pushed by the run's Finish (or wait for `lore sync` on --no-push). The
// int counts the rows written: roster rows rewritten and policy rows.
func (a *App) migrateOrg(ctx context.Context, dryRun bool, seen map[string]bool) ([]string, int, error) {
	org := a.orgStoresHere(ctx)
	if len(org) == 0 {
		return nil, 0, nil
	}
	var steps []string
	rows := 0
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	if !dryRun {
		// Convert what the team has, not a stale copy: bring each org
		// store clone up to date first (this also pushes waiting commits,
		// so it is skipped by --dry-run, which changes nothing).
		for _, s := range org {
			if _, ok := st.byName[s.Name]; !ok {
				continue
			}
			if _, err := st.eng.Sync(ctx, s.Name); err != nil {
				return nil, 0, errs.Prefix("migrate: sync org store "+string(s.Name)+" first", err)
			}
		}
	}
	for _, s := range org {
		got, n, oerr := a.migrateOrgStore(ctx, st, s, seen, dryRun)
		if oerr != nil {
			return nil, 0, oerr
		}
		steps, rows = append(steps, got...), rows+n
	}
	for _, s := range org {
		b, ok := st.byName[s.Name]
		if !ok {
			continue
		}
		for _, t := range rosterTables {
			got, err := rewriteStore(ctx, b, t, dryRun)
			if err != nil {
				return nil, 0, errs.Prefix("migrate: org store "+string(s.Name), err)
			}
			if len(got) > 0 {
				steps = append(steps, fmt.Sprintf("org store %s: %d %s row(s) _v: 1 → _v: 2", s.Name, len(got), t))
				rows += len(got)
			}
		}
		if !dryRun {
			if _, err := st.eng.Refresh(ctx, s.Name); err != nil {
				return nil, 0, err
			}
		}
	}
	// No "run lore sync" line here: the run's last lines say per store
	// whether its commit was pushed or waits (Finish), so a step line
	// asking for a push contradicted a run that had pushed.
	return steps, rows, nil
}

// entityTagsTable is v0.1.17's join table of tags (dbent entity_tag.go).
const entityTagsTable = "entity_tags"

// v1TagLink is the part of a v0.1.17 entity_tags row file migrateTags reads
// (the v1 document is flat: columns at the top level).
type v1TagLink struct {
	EntityTable string `json:"entity_table"`
	EntityID    string `json:"entity_id"`
	TagID       string `json:"tag_id"`
}

// v1TagLinkOf decodes one v0.1.17 entity_tags row file (ok=false when it
// is not one).
func v1TagLinkOf(doc []byte) (opsmemory.TagLink, bool) {
	var l v1TagLink
	if err := json.Unmarshal(doc, &l); err != nil {
		return opsmemory.TagLink{}, false
	}
	return opsmemory.TagLink{EntityTable: l.EntityTable, EntityID: l.EntityID, TagID: l.TagID}, true
}

// migrateTags turns the repo store's v0.1.17 entity_tags rows into the
// tag_ids of every row they tag (O11), for every table whose definition
// carries tag_ids (one pass, not per noun), written through the engine
// (refs checked: a tag that does not exist is refused, fail closed). The
// entity_tags files stay for the v0.1.17 readers of the other nouns. The
// int is the rows whose tag_ids are set (would be, under dryRun).
func (a *App) migrateTags(ctx context.Context, dryRun bool) ([]string, int, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	reg, err := builtinRegistry()
	if err != nil {
		return nil, 0, err
	}
	rows, _, err := st.backends[0].Read(ctx, "")
	if err != nil {
		return nil, 0, err
	}
	var links []opsmemory.TagLink
	for _, sr := range rows {
		if sr.Table.String() != entityTagsTable || sr.Deleted {
			continue
		}
		var l v1TagLink
		if err := json.Unmarshal(sr.Doc, &l); err != nil {
			return nil, 0, errs.Wrap(errs.InvalidInput, "migrate: entity_tags/"+sr.ID.String()+" is not a v0.1.17 row", err)
		}
		links = append(links, opsmemory.TagLink{EntityTable: l.EntityTable, EntityID: l.EntityID, TagID: l.TagID})
	}
	if len(links) == 0 {
		return nil, 0, nil
	}
	merged, skipped := mergeTagLinks(reg, links, func(t row.Table, id ids.AnyID) []string {
		if _, r, gerr := st.eng.Get(ctx, engine.Ref{Store: store.SourceRepo, Table: t, ID: id}); gerr == nil {
			// any form: a List, or a JSON array as a row file may decode
			return value.ItemStrings(r.Fields[schema.FieldTagIDs])
		}
		return nil
	})
	var steps []string
	for _, l := range skipped {
		steps = append(steps, "tags: skipped entity_tags link "+l.EntityTable+"/"+l.EntityID+" → "+l.TagID+" (not a tagged row id and a tag_ id)")
	}
	keys := slices.SortedFunc(maps.Keys(merged), func(x, y taggedRow) int { return strings.Compare(x.id.String(), y.id.String()) })
	for _, key := range keys {
		m := merged[key]
		steps = append(steps, "tags: "+key.id.String()+".tag_ids = "+strings.Join(m, ","))
		if dryRun {
			continue
		}
		items := make([]value.Value, 0, len(m))
		for _, t := range m {
			tid, err := ids.ParseAny(t)
			if err != nil {
				return nil, 0, err
			}
			r, err := value.RefOf(tid)
			if err != nil {
				return nil, 0, err
			}
			items = append(items, r)
		}
		list, err := value.ListOf(value.KindRef, items...)
		if err != nil {
			return nil, 0, err
		}
		ref := engine.Ref{Store: store.SourceRepo, Table: key.table, ID: key.id}
		if _, err := st.eng.Update(ctx, ref, map[string]value.Value{schema.FieldTagIDs: list}); err != nil {
			return nil, 0, errs.Prefix("migrate: tags of "+key.id.String(), err)
		}
	}
	return steps, len(keys), nil
}

// taggedRow names one row migrateTags writes tag_ids on.
type taggedRow struct {
	table row.Table
	id    ids.AnyID
}

// mergeTagLinks groups v0.1.17 tag links per tagged row: a link counts when
// its entity table is a registered table whose definition has tag_ids, the
// entity id carries that table's prefix and the tag id is a tag_ id; every
// other link is returned in skipped (reported, never dropped silently).
// existing returns the tag ids a row already lists; the result per row is
// sorted and de-duplicated.
func mergeTagLinks(reg *schema.Registry, links []opsmemory.TagLink, existing func(row.Table, ids.AnyID) []string) (map[taggedRow][]string, []opsmemory.TagLink) {
	merged := map[taggedRow][]string{}
	var skipped []opsmemory.TagLink
	for _, l := range links {
		def, ok := reg.Table(row.Table(l.EntityTable))
		if !ok {
			continue // a table the core does not own yet: its v0.1.17 readers keep entity_tags
		}
		if _, has := def.Field(schema.FieldTagIDs); !has {
			continue
		}
		id, err := ids.ParseAny(l.EntityID)
		if err != nil || id.Prefix() != def.Prefix {
			skipped = append(skipped, l)
			continue
		}
		if t, err := ids.ParseAny(l.TagID); err != nil || t.Prefix() != ids.PrefixTag {
			skipped = append(skipped, l)
			continue
		}
		k := taggedRow{table: def.Name, id: id}
		if _, seen := merged[k]; !seen {
			merged[k] = existing(def.Name, id)
		}
		merged[k] = append(merged[k], l.TagID)
	}
	for k, list := range merged {
		slices.Sort(list)
		merged[k] = slices.Compact(list)
	}
	return merged, skipped
}

// RewriteV1 implements opsmigrate.Rewriter on every attached store. Each
// v1 row is re-encoded exactly as model/row upcast it (same fields, same
// audit times, the _hlc derived from updated_at) and written with Expect set
// to that HLC, so a row someone changed meanwhile is refused (E_STALE),
// never overwritten. The rewrite bypasses the engine's Update on purpose:
// Update would stamp a new HLC and updated_at, which is an edit, not a
// format change. The caches are refreshed afterwards; a git store's commit
// reaches its remote on the next `lore sync`.
func (a *App) RewriteV1(ctx context.Context, table row.Table, dryRun bool) ([]ids.AnyID, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	names := st.eng.Stores()
	skip, err := a.flattenedUnderDryRun(dryRun)
	if err != nil {
		return nil, err
	}
	var found []ids.AnyID
	for i, b := range st.backends {
		if skip[names[i]] {
			continue
		}
		got, err := rewriteStore(ctx, b, table, dryRun)
		if err != nil {
			return nil, errs.Prefix("migrate: store "+string(names[i]), err)
		}
		found = append(found, got...)
		if dryRun || len(got) == 0 {
			continue
		}
		if i == 0 {
			a.wrote(nil)
		}
		if _, err := st.eng.Refresh(ctx, names[i]); err != nil {
			return nil, err
		}
	}
	return found, nil
}

// flattenedUnderDryRun names the stores a dry run must not count v1 rows
// in: the v0.1.17 local personal store's `me` attachments, whose rows the
// personal_store_flatten step (run before the rewrites) moves to the
// store root as `_v: 2` and counts there. The real run finds them
// tombstoned by then, so without this a dry run counted each such row
// twice and its count disagreed with the run's. Empty outside a dry run
// and when no v0.1.17 local personal store is configured (a 0.1.18 `me`
// is rewritten like any store).
func (a *App) flattenedUnderDryRun(dryRun bool) (map[store.Source]bool, error) {
	if !dryRun {
		return nil, nil
	}
	mc, err := readMachineConfig(a.opts.Home)
	if err != nil || mc.localPersonalV017(a.opts.Home) == "" {
		return nil, err
	}
	dir := mc.localPersonalV017(a.opts.Home)
	folders, err := personalFoldersV017(dir)
	if err != nil {
		return nil, err
	}
	skip := map[store.Source]bool{}
	for _, name := range a.Stores() {
		if string(name) == attach.ReservedMe || strings.HasPrefix(string(name), attach.ReservedMe+targetSep) {
			skip[name] = len(folders) > 0
		}
	}
	return skip, nil
}

// formatSteps are the per-table field changes a `_v: 1` → `_v: 2` rewrite
// makes beyond the envelope, keyed by table. Each returns the new fields
// (it may change the map it is given).
var formatSteps = map[row.Table]func(map[string]value.Value) map[string]value.Value{
	schema.TablePlans: upcastPlanStatus,
}

// upcastPlanStatus is TSQ9's plans step: an empty status takes the closed
// value of v0.1.17's free-text status_str (ops/plan.UpcastStatus), and
// status_str is dropped. A status_str that cannot be encoded as text is
// left as it is (validation reports it).
func upcastPlanStatus(f map[string]value.Value) map[string]value.Value {
	if cur, ok := f[planStatusField].(value.Text); !ok || cur.String() == "" {
		old := ""
		if t, ok := f[schema.FieldPlanStatusStr].(value.Text); ok {
			old = t.String()
		}
		t, err := value.TextOf(opsplan.UpcastStatus(old))
		if err != nil {
			return f
		}
		f[planStatusField] = t
	}
	delete(f, schema.FieldPlanStatusStr)
	return f
}

// planStatusField is plans.status (schema/builtin_work.go).
const planStatusField = "status"

// rewriteStore rewrites table's v1 rows in one backend.
func rewriteStore(ctx context.Context, b store.Backend, table row.Table, dryRun bool) ([]ids.AnyID, error) {
	got, err := rewriteStoreTables(ctx, b, []row.Table{table}, dryRun)
	return got[table], err
}

// rewriteStoreTables rewrites the v1 rows of every table of tables in one
// backend: one Read and one Write (one commit for a store clone) for all
// of them. The ids are per table in the backend's read order, as a
// rewriteStore per table returns them.
func rewriteStoreTables(ctx context.Context, b store.Backend, tables []row.Table, dryRun bool) (map[row.Table][]ids.AnyID, error) {
	rows, _, err := b.Read(ctx, "")
	if err != nil {
		return nil, err
	}
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	found := map[row.Table][]ids.AnyID{}
	byTable := map[row.Table][]store.Change{}
	for _, sr := range rows {
		if !slices.Contains(tables, sr.Table) {
			continue
		}
		r, err := sr.Decode()
		if err != nil {
			return nil, err
		}
		if r.SourceVersion == row.FormatVersion {
			continue
		}
		found[sr.Table] = append(found[sr.Table], r.ID)
		up, err := store.FromRow(upcastV1(reg, r))
		if err != nil {
			return nil, err
		}
		byTable[sr.Table] = append(byTable[sr.Table], store.Change{Row: up, Expect: sr.HLC})
	}
	var changes []store.Change
	for _, t := range tables { // the batch in table order, as the per-table writes were
		changes = append(changes, byTable[t]...)
	}
	if dryRun || len(changes) == 0 {
		return found, nil
	}
	return found, b.Write(store.WithCommitMessage(ctx, migrateCommitSubject(ctx, len(changes))), changes)
}

// migrateViaKey is the context key of the command a format rewrite runs
// under (withMigrateVia).
type migrateViaKey struct{}

// Commands a format rewrite names in its commit subject.
const (
	migrateViaRun       = "lore migrate run"
	migrateViaFix       = "lore fix"
	migrateViaFirstSave = "lore fix, before the first write"
)

// withMigrateVia returns ctx whose format rewrites name via (the command
// that ran them) in their commit subject.
func withMigrateVia(ctx context.Context, via string) context.Context {
	return context.WithValue(ctx, migrateViaKey{}, via)
}

// migrateCommitSubject is the subject of a store clone's format-upgrade
// commit (item 46: it was the generic `lore: write mem_… (+4831 more)`):
// `lore: migrate 4831 rows to format 2 (lore fix)`.
func migrateCommitSubject(ctx context.Context, n int) string {
	via, _ := ctx.Value(migrateViaKey{}).(string)
	if via == "" {
		via = migrateViaRun
	}
	return fmt.Sprintf("lore: migrate %s to format %d (%s)", plural(n, "row"), row.FormatVersion, via)
}

// upcastV1 is the row a v1 row file becomes when the migration rewrites
// it as `_v: 2`: the table's format step, v0.1.17's "" for "no ref" as
// null (engine/emptyref.go) and the audit_fields defaults (step 9: a v1
// row never had *_by actors). The hand-edit pick-up compares a migrated
// file with it, so the rewrite itself is never taken for a hand edit.
func upcastV1(reg *schema.Registry, r row.Row) row.Row {
	if step, ok := formatSteps[r.Table]; ok {
		r.Fields = step(r.Fields)
	}
	r, _ = engine.NullEmptyRefs(reg, r)
	r, _ = auditfill.Defaults(r)
	return r
}

// RewriteAllV1 implements opsmigrate.AllTablesRewriter: RewriteV1 (or
// RewriteRepoV1 when repoOnly) for every table, reading and writing each
// store once and refreshing its cache once.
func (a *App) RewriteAllV1(ctx context.Context, tables []row.Table, repoOnly, dryRun bool) (map[row.Table][]ids.AnyID, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	names := st.eng.Stores()
	skip := map[store.Source]bool{}
	if !repoOnly {
		if skip, err = a.flattenedUnderDryRun(dryRun); err != nil {
			return nil, err
		}
	}
	found := map[row.Table][]ids.AnyID{}
	for i, b := range st.backends {
		if skip[names[i]] || (repoOnly && i > 0) {
			continue
		}
		got, err := rewriteStoreTables(ctx, b, tables, dryRun)
		if err != nil {
			if repoOnly {
				return nil, err
			}
			return nil, errs.Prefix("migrate: store "+string(names[i]), err)
		}
		n := 0
		for t, list := range got {
			found[t] = append(found[t], list...)
			n += len(list)
		}
		if dryRun || n == 0 {
			continue
		}
		if i == 0 {
			a.wrote(nil)
		}
		if _, err := st.eng.Refresh(ctx, names[i]); err != nil {
			return nil, err
		}
	}
	return found, nil
}

// migrateStoreMarker converts one stores.yaml entry's v1 marker to v2 and
// returns its step line ("" when there is nothing to convert: no marker,
// already v2, or a folder another entry already handled, listed in seen).
// A read_only entry is reported and left alone: only its owner converts it.
func (a *App) migrateStoreMarker(ctx context.Context, e opsstores.Entry, seen map[string]bool, dryRun bool) (string, error) {
	dir, err := a.storeDir(ctx, attach.Store{URL: e.URL, Branch: e.Branch})
	if err != nil {
		return "", errs.Prefix("migrate: store "+e.Name, err)
	}
	m, err := rowfs.ReadMarker(dir)
	if err != nil || m.Version >= rowfs.MarkerFirstStores || seen[dir] {
		return "", nil // no marker (an empty git store) or already v2: nothing to convert
	}
	seen[dir] = true
	if e.ReadOnly {
		return "store " + e.Name + ": " + rowfs.MarkerFile + " is v1 but the store is read_only here; skipped (its owner runs lore migrate run)", nil
	}
	step := "store " + e.Name + ": " + rowfs.MarkerFile + " v1 → v2 (new sto_ id)"
	if dryRun {
		return step, nil
	}
	return step, a.upgradeMarker(ctx, dir, e.URL, m)
}

// upgradeMarker writes m as a v2 marker with a fresh permanent sto_ id into
// dir and, for a git store (any url that is not folder://), commits it in
// the clone; the commit waits there for lore sync.
func (a *App) upgradeMarker(ctx context.Context, dir, rawURL string, m rowfs.Marker) error {
	id, err := ids.New[ids.Store]()
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: new store id", err)
	}
	m.ID = id
	if err = rowfs.WriteMarker(dir, m); err != nil {
		return err
	}
	if strings.HasPrefix(rawURL, folder.Scheme+"://") {
		return nil
	}
	// FLAG4: same signing rule as every store commit (git identity wins).
	return gitCommitFilesAs(ctx, dir, a.opts.Wall(), storeIdentityFunc(a.opts), initSubject, rowfs.MarkerFile)
}

// migrateOrgStore runs the per-store steps of migrateOrg for one org store:
// its folder's lore-org.yaml defaults become a policy row
// (org_defaults_to_policy), then its v1 marker becomes v2 unless seen
// already lists the folder.
func (a *App) migrateOrgStore(ctx context.Context, st *state, s attach.Store, seen map[string]bool, dryRun bool) ([]string, int, error) {
	dir, err := a.storeDir(ctx, s)
	if err != nil {
		return nil, 0, errs.Prefix("migrate: org store "+string(s.Name), err)
	}
	var steps []string
	rows := 0
	if s.Folder != "" {
		if steps, rows, err = a.orgDefaultsToPolicy(ctx, st, s, dir, dryRun); err != nil {
			return nil, 0, err
		}
	}
	m, err := rowfs.ReadMarker(dir)
	if err != nil || m.Version >= rowfs.MarkerFirstStores || seen[dir] {
		return steps, rows, nil
	}
	seen[dir] = true
	steps = append(steps, "org store "+string(s.Name)+": "+rowfs.MarkerFile+" v1 → v2 (new sto_ id, visibility internal); its roster moves to the core")
	if dryRun {
		return steps, rows, nil
	}
	return steps, rows, a.upgradeMarker(ctx, dir, s.URL, m)
}

// clientRoutesPolicy builds the fields of the routing.client_repos policy
// row (key, scope = the org folder, value = a json list of the
// policy.ClientRouteItem strings) that org_defaults_to_policy writes.
func clientRoutesPolicy(items []string, folderScope string) (map[string]value.Value, error) {
	list := make([]value.Value, len(items))
	for i, it := range items {
		t, err := value.TextOf(it)
		if err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "migrate: "+orgDefaultsFile+" holds a default that is not valid text", err)
		}
		list[i] = t
	}
	lv, err := value.ListOf(value.KindText, list...)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: build the routing list", err)
	}
	raw, err := lv.MarshalJSON()
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: encode the routing list", err)
	}
	jv, err := value.JSONOf(raw)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: encode the routing list", err)
	}
	key, kerr := value.TextOf(string(policy.KeyClientRoutes))
	scope, serr := value.TextOf(folderScope)
	if err = errors.Join(kerr, serr); err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: policy key", err)
	}
	return map[string]value.Value{policyFieldKey: key, policyFieldValue: jv, policyFieldScope: scope}, nil
}
