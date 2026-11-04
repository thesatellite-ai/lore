package wire

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsplan "github.com/thesatellite-ai/lore/ops/plan"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
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
	if err := os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
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
	legacy, ok, err := identity.ReadLegacy(a.opts.Home)
	if err != nil {
		return nil, err
	}
	if ok {
		mc, err := readMachineConfig(a.opts.Home)
		if err != nil {
			return nil, err
		}
		step := "identity: ~/.lore/" + identity.LegacyFile + " moved into ~/.lore/" + ConfigFile
		if mc.Identity.Key != "" {
			step = "identity: ~/.lore/" + identity.LegacyFile + " removed; " + ConfigFile + " already sets identity: (kept)"
		}
		steps = append(steps, step)
		if !dryRun {
			if mc.Identity.Key == "" {
				if err := setTopIdentity(a.opts.Home, legacy); err != nil {
					return nil, err
				}
			}
			if err := os.Remove(filepath.Join(a.opts.Home, identity.LegacyFile)); err != nil {
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
		dir, err := a.storeDir(ctx, e.URL)
		if err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), "migrate: store "+e.Name, err)
		}
		m, err := rowfs.ReadMarker(dir)
		if err != nil || m.Version >= rowfs.MarkerVersion || seen[dir] {
			continue // no marker (an empty git store) or already v2: nothing to convert
		}
		seen[dir] = true
		if e.ReadOnly {
			steps = append(steps, "store "+e.Name+": "+rowfs.MarkerFile+" is v1 but the store is read_only here; skipped (its owner runs lore migrate run)")
			continue
		}
		steps = append(steps, "store "+e.Name+": "+rowfs.MarkerFile+" v1 → v2 (new sto_ id)")
		if dryRun {
			continue
		}
		if m.ID, err = ids.New[ids.Store](); err != nil {
			return nil, errs.Wrap(errs.Internal, "wire: new store id", err)
		}
		if err := rowfs.WriteMarker(dir, m); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(e.URL, folder.Scheme+"://") {
			if err := gitCommitMarker(ctx, dir, a.opts.Wall()); err != nil {
				return nil, err
			}
		}
	}
	tagSteps, err := a.migrateTags(ctx, dryRun)
	if err != nil {
		return nil, err
	}
	return append(steps, tagSteps...), nil
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
		var out []string
		if _, r, gerr := st.eng.Get(ctx, engine.Ref{Store: store.SourceRepo, Table: t, ID: id}); gerr == nil {
			if list, ok := r.Fields[schema.FieldTagIDs].(value.List); ok {
				for _, it := range list.Items() {
					out = append(out, it.String())
				}
			}
		}
		return out
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
