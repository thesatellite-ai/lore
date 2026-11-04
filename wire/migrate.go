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
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
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
// exclusively; one that exists is E_MIGRATE_LOCKED naming its holder.
func (a *App) LockMigration(ctx context.Context) (func() error, error) {
	top, err := a.repoTop(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(top, filepath.FromSlash(MigrateLockFile))
	if err = os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: create .lore/state", err)
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, lockFileMode)
	if errors.Is(err, fs.ErrExist) {
		holder, _ := os.ReadFile(filepath.Clean(path)) // best effort: the holder is only shown
		return nil, errs.New(errs.MigrateLocked, "migrate: another migration holds "+MigrateLockFile+" (pid "+strings.TrimSpace(string(holder))+")").
			WithHint("wait for it to finish; if no lore process runs, delete " + MigrateLockFile)
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: take the migration lock", err)
	}
	_, werr := f.WriteString(strconv.Itoa(os.Getpid()))
	if err := errors.Join(werr, f.Close()); err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: take the migration lock", errors.Join(err, os.Remove(path)))
	}
	return func() error { return os.Remove(path) }, nil
}

// MigrateSettings implements opsmigrate.Rewriter: CFG9 identity and the
// v1 store markers of every attached store.
func (a *App) MigrateSettings(ctx context.Context, dryRun bool) ([]string, error) {
	steps, err := a.MigrateModeB(ctx, dryRun)
	if err != nil {
		return nil, err
	}
	personal, err := a.migrateLocalPersonal(ctx, dryRun)
	if err != nil {
		return nil, err
	}
	steps = append(steps, personal...)
	legacy, ok, err := identity.ReadLegacy(a.opts.Home)
	if err != nil {
		return nil, err
	}
	if ok {
		mc, cerr := readMachineConfig(a.opts.Home)
		if cerr != nil {
			return nil, cerr
		}
		step := "identity: ~/.lore/" + identity.LegacyFile + " moved into ~/.lore/" + ConfigFile
		if mc.Identity.Key != "" {
			step = "identity: ~/.lore/" + identity.LegacyFile + " removed; " + ConfigFile + " already sets identity: (kept)"
		}
		steps = append(steps, step)
		if !dryRun {
			if mc.Identity.Key == "" {
				if err = setTopIdentity(a.opts.Home, legacy); err != nil {
					return nil, err
				}
			}
			if err = os.Remove(filepath.Join(a.opts.Home, identity.LegacyFile)); err != nil {
				return nil, errs.Wrap(errs.Internal, "wire: remove "+identity.LegacyFile, err)
			}
		}
	}
	list, err := a.ListStores(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range list {
		step, serr := a.migrateStoreMarker(ctx, e, seen, dryRun)
		if serr != nil {
			return nil, serr
		}
		if step != "" {
			steps = append(steps, step)
		}
	}
	orgSteps, err := a.migrateOrg(ctx, dryRun, seen)
	if err != nil {
		return nil, err
	}
	steps = append(steps, orgSteps...)
	tagSteps, err := a.migrateTags(ctx, dryRun)
	if err != nil {
		return nil, err
	}
	return append(steps, tagSteps...), nil
}

// rosterTables are the org roster's tables (LORE_TEAM_SPEC F18): not in
// CoreTables (the v0.1.17 binary still owns the repo's own actors), but
// rewritten as `_v: 2` in the org's stores, where the core keeps the
// roster once the store is 0.1.18 (wire roster.go).
var rosterTables = []row.Table{schema.TableActors, schema.TableTeams}

// orgDefaultsFile is <org folder>/.lore/lore-org.yaml (v0.1.17 stores
// package OrgDefaultsFile), whose `defaults` the org_defaults_to_policy
// step moves into policies rows (CFG8).
const orgDefaultsFile = "lore-org.yaml"

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
// (policy.Set.ClientRoutes).
func (a *App) orgDefaultsToPolicy(ctx context.Context, st *state, s attach.Store, dir string, dryRun bool) ([]string, error) {
	rel := filepath.Join(filepath.FromSlash(s.Folder), rowfs.LoreDir, orgDefaultsFile)
	// The folder comes from the org's link, so every file access goes
	// through an os.Root on the store clone: a folder such as ../x can
	// never read or write outside it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: open org store clone "+string(s.Name), err)
	}
	defer func() { _ = root.Close() }() // read-only handle on a directory: nothing to flush
	b, err := root.ReadFile(rel)
	if err != nil {
		return nil, nil // no lore-org.yaml in this folder (or a folder outside the clone): nothing to move
	}
	var p orgDefaultsProbe
	if yaml.Unmarshal(b, &p) != nil || len(p.Defaults.ClientRepos) == 0 {
		return nil, nil
	}
	items := make([]string, 0, len(p.Defaults.ClientRepos))
	for _, k := range slices.Sorted(maps.Keys(p.Defaults.ClientRepos)) {
		items = append(items, policy.ClientRouteItem(k, p.Defaults.ClientRepos[k]))
	}
	step := StepOrgDefaultsToPolicy + ": org store " + string(s.Name) + ": " + orgDefaultsFile + " defaults.client_repos → policy " +
		string(policy.KeyClientRoutes) + " = " + strings.Join(items, ",") + " (file kept as " + orgDefaultsFile + migratedSuffix + ")"
	if dryRun {
		return []string{step}, nil
	}
	if _, ok := st.byName[s.Name]; !ok {
		return []string{StepOrgDefaultsToPolicy + ": org store " + string(s.Name) + " is not attached; " + orgDefaultsFile + " left as it is"}, nil
	}
	rows, err := st.eng.List(ctx, s.Name, schema.TablePolicies)
	if err != nil {
		return nil, err
	}
	has := slices.ContainsFunc(rows, func(r row.Row) bool {
		k, ok := r.Fields[policyFieldKey].(value.Text)
		return !r.Deleted && ok && k.String() == string(policy.KeyClientRoutes)
	})
	if !has {
		fields, ferr := clientRoutesPolicy(items, s.Folder)
		if ferr != nil {
			return nil, ferr
		}
		if _, err = st.eng.Create(ctx, s.Name, schema.TablePolicies, fields); err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "migrate: write the routing policy into "+string(s.Name), err)
		}
	}
	var doc yaml.Node
	if err = yaml.Unmarshal(b, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errs.New(errs.ConfigInvalid, "migrate: "+orgDefaultsFile+" of "+string(s.Name)+" is not a mapping")
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
		return nil, errs.Wrap(errs.Internal, "migrate: encode "+orgDefaultsFile, err)
	}
	if err = root.WriteFile(rel+migratedSuffix, b, orgFileMode); err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: keep "+orgDefaultsFile+migratedSuffix, err)
	}
	if err = root.WriteFile(rel, out, orgFileMode); err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: rewrite "+orgDefaultsFile, err)
	}
	if !strings.HasPrefix(s.URL, folder.Scheme+"://") {
		slash := filepath.ToSlash(rel)
		if err = gitCommitFiles(ctx, dir, a.opts.Wall(), migrateOrgSubject, slash, slash+migratedSuffix); err != nil {
			return nil, err
		}
	}
	return []string{step}, nil
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
// already handled. Commits wait in the store clone for `lore sync`.
func (a *App) migrateOrg(ctx context.Context, dryRun bool, seen map[string]bool) ([]string, error) {
	org := a.orgStoresHere(ctx)
	if len(org) == 0 {
		return nil, nil
	}
	var steps []string
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
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
				return nil, errs.Wrap(errs.CodeOf(err), "migrate: sync org store "+string(s.Name)+" first", err)
			}
		}
	}
	for _, s := range org {
		got, oerr := a.migrateOrgStore(ctx, st, s, seen, dryRun)
		if oerr != nil {
			return nil, oerr
		}
		steps = append(steps, got...)
	}
	for _, s := range org {
		b, ok := st.byName[s.Name]
		if !ok {
			continue
		}
		for _, t := range rosterTables {
			got, err := rewriteStore(ctx, b, t, dryRun)
			if err != nil {
				return nil, errs.Wrap(errs.CodeOf(err), "migrate: org store "+string(s.Name), err)
			}
			if len(got) > 0 {
				steps = append(steps, fmt.Sprintf("org store %s: %d %s row(s) _v: 1 → _v: 2", s.Name, len(got), t))
			}
		}
		if !dryRun {
			if _, err := st.eng.Refresh(ctx, s.Name); err != nil {
				return nil, err
			}
		}
	}
	if !dryRun && len(steps) > 0 {
		steps = append(steps, "org: run `lore sync` to push the org store changes")
	}
	return steps, nil
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

// migrateTags turns the repo store's v0.1.17 entity_tags rows into the
// tag_ids of every row they tag (O11), for every table whose definition
// carries tag_ids (one pass, not per noun), written through the engine
// (refs checked: a tag that does not exist is refused, fail closed). The
// entity_tags files stay for the v0.1.17 readers of the other nouns.
func (a *App) migrateTags(ctx context.Context, dryRun bool) ([]string, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	reg, err := builtinRegistry()
	if err != nil {
		return nil, err
	}
	rows, _, err := st.backends[0].Read(ctx, "")
	if err != nil {
		return nil, err
	}
	var links []opsmemory.TagLink
	for _, sr := range rows {
		if sr.Table.String() != entityTagsTable || sr.Deleted {
			continue
		}
		var l v1TagLink
		if err := json.Unmarshal(sr.Doc, &l); err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "migrate: entity_tags/"+sr.ID.String()+" is not a v0.1.17 row", err)
		}
		links = append(links, opsmemory.TagLink{EntityTable: l.EntityTable, EntityID: l.EntityID, TagID: l.TagID})
	}
	if len(links) == 0 {
		return nil, nil
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
				return nil, err
			}
			r, err := value.RefOf(tid)
			if err != nil {
				return nil, err
			}
			items = append(items, r)
		}
		list, err := value.ListOf(value.KindRef, items...)
		if err != nil {
			return nil, err
		}
		ref := engine.Ref{Store: store.SourceRepo, Table: key.table, ID: key.id}
		if _, err := st.eng.Update(ctx, ref, map[string]value.Value{schema.FieldTagIDs: list}); err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "migrate: tags of "+key.id.String(), err)
		}
	}
	return steps, nil
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
	var found []ids.AnyID
	for i, b := range st.backends {
		got, err := rewriteStore(ctx, b, table, dryRun)
		if err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "migrate: store "+string(names[i]), err)
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
	rows, _, err := b.Read(ctx, "")
	if err != nil {
		return nil, err
	}
	var found []ids.AnyID
	var changes []store.Change
	for _, sr := range rows {
		if sr.Table != table {
			continue
		}
		r, err := sr.Decode()
		if err != nil {
			return nil, err
		}
		if r.SourceVersion == row.FormatVersion {
			continue
		}
		found = append(found, r.ID)
		if step, ok := formatSteps[table]; ok {
			r.Fields = step(r.Fields)
		}
		up, err := store.FromRow(r)
		if err != nil {
			return nil, err
		}
		changes = append(changes, store.Change{Row: up, Expect: sr.HLC})
	}
	if dryRun || len(changes) == 0 {
		return found, nil
	}
	return found, b.Write(ctx, changes)
}

// migrateStoreMarker converts one stores.yaml entry's v1 marker to v2 and
// returns its step line ("" when there is nothing to convert: no marker,
// already v2, or a folder another entry already handled, listed in seen).
// A read_only entry is reported and left alone: only its owner converts it.
func (a *App) migrateStoreMarker(ctx context.Context, e opsstores.Entry, seen map[string]bool, dryRun bool) (string, error) {
	dir, err := a.storeDir(ctx, attach.Store{URL: e.URL, Branch: e.Branch})
	if err != nil {
		return "", errs.Wrap(errs.CodeOf(err), "migrate: store "+e.Name, err)
	}
	m, err := rowfs.ReadMarker(dir)
	if err != nil || m.Version >= rowfs.MarkerVersion || seen[dir] {
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
	return gitCommitMarker(ctx, dir, a.opts.Wall())
}

// migrateOrgStore runs the per-store steps of migrateOrg for one org store:
// its folder's lore-org.yaml defaults become a policy row
// (org_defaults_to_policy), then its v1 marker becomes v2 unless seen
// already lists the folder.
func (a *App) migrateOrgStore(ctx context.Context, st *state, s attach.Store, seen map[string]bool, dryRun bool) ([]string, error) {
	dir, err := a.storeDir(ctx, s)
	if err != nil {
		return nil, errs.Wrap(errs.CodeOf(err), "migrate: org store "+string(s.Name), err)
	}
	var steps []string
	if s.Folder != "" {
		if steps, err = a.orgDefaultsToPolicy(ctx, st, s, dir, dryRun); err != nil {
			return nil, err
		}
	}
	m, err := rowfs.ReadMarker(dir)
	if err != nil || m.Version >= rowfs.MarkerVersion || seen[dir] {
		return steps, nil
	}
	seen[dir] = true
	steps = append(steps, "org store "+string(s.Name)+": "+rowfs.MarkerFile+" v1 → v2 (new sto_ id, visibility internal); its roster moves to the core")
	if dryRun {
		return steps, nil
	}
	return steps, a.upgradeMarker(ctx, dir, s.URL, m)
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
