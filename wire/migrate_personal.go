package wire

// Step 12, personal half (db_settings_to_rows, CFG12; FORMAT_EVOLUTION
// section 3 step list): v0.1.17 settings that are one person's choice move
// into ~/.lore/config.yaml, the only home of personal settings in 0.1.18.
//
//	migrateDBSettings ─► project_configs rows (team file)  ─┐
//	readV017Config    ─► .lore/lore.db `config` table       ─┤ (`lore config set` of v0.1.17)
//	                                                         ▼
//	classifyPersonal: a key config.yaml reads, its value valid ─► set in config.yaml
//	   unless config.yaml already sets it (kept) ─► journal ConfigKeys (rollback)
//	internal v0.1.17 keys: skipped silently; anything else: reported, left in place
//
// FA-FMT-1: runs inside `lore migrate run` (journaled, rollback-able), not
// on an arbitrary first command, so config.yaml never changes as a side
// effect of a read. Two sources disagreeing on one key, or a value
// config.yaml cannot hold, is reported and never guessed (CFG12).

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"

	"gopkg.in/yaml.v3"
	// The pure-Go SQLite driver (registered as "sqlite"); the v0.1.17
	// lore.db is read once, read-only, by this step.
	_ "modernc.org/sqlite"
)

// v017LoreDB is the v0.1.17 database of a checkout (Mode A), relative to
// the repo top. The core never writes it; this step only reads it.
const v017LoreDB = ".lore/lore.db"

// v017ConfigTable is v0.1.17's DB-level settings table (dbent/schema/
// config.go, ent DBConfig): `lore config set` of v0.1.17 wrote here.
const v017ConfigTable = "config"

// sqliteDriver is modernc's driver name.
const sqliteDriver = "sqlite"

// v017InternalKeys are the `config` rows v0.1.17 wrote for itself (seeds,
// stamps, the clock high-water mark): never a person's setting, so the
// step skips them without a line.
var v017InternalKeys = []string{
	"schema_version", "db_uuid", "db_created_at", "last_vacuum_at", "last_checkpoint_at",
	"tx_clock_high_water", "fts_registry_fingerprint", "schema_lore_version",
}

// personalKind is how a personal config.yaml key's value is checked and
// written (mirrors lorecfg.Config's types and Validate rules).
type personalKind int

const (
	pkBool personalKind = iota + 1
	pkInt
	pkDuration
	pkRemote
	pkAdapter
)

// personalKey is one config.yaml key the step may write.
type personalKey struct {
	kind personalKind
	// min is the smallest int, or for a duration whether 0 is refused
	// (min 1 = must be greater than zero).
	min int
}

// personalConfigKeys are the scalar config.yaml keys that are a person's
// own choice (CFG12): live sync, leases, agent limits, the merge check,
// the link index and the upgrade notices. Lists (identity, orgs, stores,
// profiles) and default_branch (git ref rules) are not classified here:
// such a v0.1.17 value is reported and left in place.
var personalConfigKeys = map[string]personalKey{
	"live.autopush":               {kind: pkBool},
	"live.max_age":                {kind: pkDuration, min: 1},
	"live.fetch_ttl":              {kind: pkDuration},
	"live.compact_after":          {kind: pkInt, min: 1},
	"live.remote":                 {kind: pkRemote},
	"lease.ttl":                   {kind: pkDuration, min: 1},
	"lease.renew_every":           {kind: pkDuration, min: 1},
	"lease.grace":                 {kind: pkDuration},
	"lease.push_retries":          {kind: pkInt},
	"agents.max_claims_per_agent": {kind: pkInt, min: 1},
	"agents.max_attempts":         {kind: pkInt, min: 1},
	"merge_check.adapter":         {kind: pkAdapter},
	"link_index.max_age":          {kind: pkDuration},
	"upgrade_notice":              {kind: pkBool},
	"auto_upgrade":                {kind: pkBool},
}

// Lease keys checked together (renew_every must be shorter than ttl).
const (
	keyLeaseTTL        = "lease.ttl"
	keyLeaseRenewEvery = "lease.renew_every"
)

// mergeCheckAdapters is lorecfg's closed set for merge_check.adapter.
var mergeCheckAdapters = []string{"none", "github"}

// loreDay is the one unit lore adds to Go durations (lorecfg.Day).
const loreDay = 24 * time.Hour

// personalSetting is one v0.1.17 setting offered to the personal step.
type personalSetting struct {
	Key   string
	Value string
	// From names the source for the report ("project_configs" or
	// ".lore/lore.db").
	From string
}

// journalConfigKey is one config.yaml key a run set (7.6 rollback).
type journalConfigKey struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// readV017Config returns the person's rows of the checkout's v0.1.17
// `config` table (internal keys dropped); a missing database or table is
// none. Opened read-only: v0.1.17 may still be using the file.
func readV017Config(ctx context.Context, top string) ([]personalSetting, error) {
	path := filepath.Join(top, filepath.FromSlash(v017LoreDB))
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: stat "+v017LoreDB, err)
	}
	dsn := fileuri.URL(path, v017ReadQuery(ctx))
	db, err := sql.Open(sqliteDriver, dsn)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: open "+v017LoreDB, err)
	}
	defer func() { _ = db.Close() }() // read-only handle: nothing to flush
	var n int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", v017ConfigTable).Scan(&n); err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: read "+v017LoreDB, err)
	}
	if n == 0 {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, "SELECT key, value FROM "+v017ConfigTable+" ORDER BY key")
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: read the "+v017ConfigTable+" table of "+v017LoreDB, err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	var out []personalSetting
	for rows.Next() {
		var k string
		var v sql.NullString
		if err = rows.Scan(&k, &v); err != nil {
			return nil, errs.Wrap(errs.Internal, "migrate: read "+v017LoreDB, err)
		}
		if !v.Valid || slices.Contains(v017InternalKeys, k) {
			continue
		}
		out = append(out, personalSetting{Key: k, Value: v.String, From: v017LoreDB})
	}
	if err = rows.Err(); err != nil {
		return nil, errs.Wrap(errs.Internal, "migrate: read "+v017LoreDB, err)
	}
	return out, nil
}

// parseLoreDuration is lorecfg.ParseDuration: Go syntax, whole days `7d`
// or plain seconds; never negative.
func parseLoreDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseInt(d, 10, 64)
		return time.Duration(n) * loreDay, err == nil && n >= 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Duration(n) * time.Second, n >= 0
	}
	v, err := time.ParseDuration(s)
	return v, err == nil && v >= 0
}

// personalNode checks value for key and returns the YAML node to write;
// ok=false when config.yaml could not hold it (never guessed).
func personalNode(pk personalKey, value string) (*yaml.Node, bool) {
	v := strings.TrimSpace(value)
	switch pk.kind {
	case pkBool:
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, false
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}, true
	case pkInt:
		n, err := strconv.Atoi(v)
		if err != nil || n < pk.min {
			return nil, false
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(n)}, true
	case pkDuration:
		d, ok := parseLoreDuration(v)
		if !ok || (pk.min > 0 && d <= 0) {
			return nil, false
		}
		return scalar(v), true
	case pkRemote:
		if v == "" || strings.ContainsAny(v, " \t/:") {
			return nil, false
		}
		return scalar(v), true
	case pkAdapter:
		if !slices.Contains(mergeCheckAdapters, v) {
			return nil, false
		}
		return scalar(v), true
	}
	return nil, false
}

// configLookup returns the scalar text at a dotted key of the config root
// (ok=false when absent).
func configLookup(root *yaml.Node, key string) (string, bool) {
	n := root
	for _, p := range strings.Split(key, ".") {
		if n == nil || n.Kind != yaml.MappingNode {
			return "", false
		}
		n = mapGet(n, p)
	}
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", n != nil
	}
	return n.Value, true
}

// configSetPath sets a dotted key, creating the parent mappings.
func configSetPath(root *yaml.Node, key string, v *yaml.Node) {
	parts := strings.Split(key, ".")
	m := root
	for _, p := range parts[:len(parts)-1] {
		m = mapChild(m, p)
	}
	mapSet(m, parts[len(parts)-1], v)
}

// configDeletePath removes a dotted key and any parent mapping it leaves
// empty; false when it was absent.
func configDeletePath(root *yaml.Node, key string) bool {
	parts := strings.Split(key, ".")
	chain := []*yaml.Node{root}
	for _, p := range parts[:len(parts)-1] {
		c := mapGet(chain[len(chain)-1], p)
		if c == nil || c.Kind != yaml.MappingNode {
			return false
		}
		chain = append(chain, c)
	}
	if !mapDelete(chain[len(chain)-1], parts[len(parts)-1]) {
		return false
	}
	for i := len(chain) - 1; i > 0 && len(chain[i].Content) == 0; i-- {
		mapDelete(chain[i-1], parts[i-1])
	}
	return true
}

// readConfigRoot reads config.yaml's top mapping (an empty one when the
// file is absent).
func readConfigRoot(home string) (*yaml.Node, error) {
	b, err := readConfigRaw(home)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return &yaml.Node{Kind: yaml.MappingNode}, nil
	}
	doc := &yaml.Node{}
	if err := yaml.Unmarshal(b, doc); err != nil {
		return nil, errs.Wrap(errs.ConfigInvalid, "wire: ~/.lore/"+ConfigFile+" is not valid YAML", err)
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode}, nil
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, errs.New(errs.ConfigInvalid, "wire: ~/.lore/"+ConfigFile+" is not a mapping")
	}
	return doc.Content[0], nil
}

// personalLine starts every line of the personal half.
func personalLine(s personalSetting) string {
	return StepDBSettingsToRows + ": " + s.From + " key " + s.Key
}

// migratePersonalSettings is step 12's personal half over cands (the
// project_configs keys the team half did not take, plus the checkout's
// v0.1.17 `config` rows). It returns the step lines; config.yaml counts no
// rows (opsmigrate.Settings).
func (a *App) migratePersonalSettings(ctx context.Context, cands []personalSetting, dryRun bool) ([]string, error) {
	if len(cands) == 0 {
		return nil, nil
	}
	root, err := readConfigRoot(a.opts.Home)
	if err != nil {
		return nil, err
	}
	// One value per key across sources; a disagreement is reported (CFG12:
	// never guessed).
	byKey := map[string][]personalSetting{}
	var order []string
	for _, c := range cands {
		if _, ok := byKey[c.Key]; !ok {
			order = append(order, c.Key)
		}
		byKey[c.Key] = append(byKey[c.Key], c)
	}
	slices.Sort(order)
	var steps []string
	var pending [][2]string // key, line: emitted once the lease check passed
	set := map[string]*yaml.Node{}
	for _, k := range order {
		group := byKey[k]
		first := group[0]
		pk, personal := personalConfigKeys[k]
		if !personal {
			for _, s := range group {
				steps = append(steps, personalLine(s)+" has no 0.1.18 home; left in place")
			}
			continue
		}
		if slices.ContainsFunc(group[1:], func(s personalSetting) bool { return strings.TrimSpace(s.Value) != strings.TrimSpace(first.Value) }) {
			steps = append(steps, StepDBSettingsToRows+": key "+k+" has different values in "+v017LoreDB+" and project_configs; left in place (set it with `lore config set`)")
			continue
		}
		node, valid := personalNode(pk, first.Value)
		if !valid {
			steps = append(steps, personalLine(first)+" holds a value ~/.lore/"+ConfigFile+" cannot hold; left in place")
			continue
		}
		line := personalLine(first) + " → ~/.lore/" + ConfigFile + " " + k + " = " + node.Value
		if _, have := configLookup(root, k); have {
			steps = append(steps, line+" (config.yaml already sets it; kept)")
			continue
		}
		set[k] = node
		pending = append(pending, [2]string{k, line})
	}
	// lorecfg Validate: lease.renew_every must be shorter than lease.ttl.
	if bad := leasePairInvalid(root, set); bad {
		for _, k := range []string{keyLeaseTTL, keyLeaseRenewEvery} {
			if _, ok := set[k]; ok {
				delete(set, k)
				steps = append(steps, StepDBSettingsToRows+": key "+k+" would make lease.renew_every not shorter than lease.ttl; left in place")
			}
		}
	}
	for _, p := range pending {
		if _, ok := set[p[0]]; ok {
			steps = append(steps, p[1])
		}
	}
	if dryRun || len(set) == 0 {
		return steps, nil
	}
	keys := slices.Sorted(maps.Keys(set))
	if err := editMachineConfig(a.opts.Home, func(r *yaml.Node) error {
		for _, k := range keys {
			configSetPath(r, k, set[k])
		}
		return nil
	}); err != nil {
		return nil, err
	}
	written := make([]journalConfigKey, 0, len(keys))
	for _, k := range keys {
		written = append(written, journalConfigKey{Key: k, Value: set[k].Value})
	}
	return steps, a.journalConfigKeys(ctx, written)
}

// leasePairInvalid reports whether the lease pair after this step's sets
// breaks lorecfg's rule (renew_every < ttl).
func leasePairInvalid(root *yaml.Node, set map[string]*yaml.Node) bool {
	val := func(k string) (time.Duration, bool) {
		if n, ok := set[k]; ok {
			return parseLoreDuration(n.Value)
		}
		if s, ok := configLookup(root, k); ok {
			return parseLoreDuration(s)
		}
		return 0, false
	}
	_, touched1 := set[keyLeaseTTL]
	_, touched2 := set[keyLeaseRenewEvery]
	if !touched1 && !touched2 {
		return false
	}
	ttl, ok1 := val(keyLeaseTTL)
	renew, ok2 := val(keyLeaseRenewEvery)
	return ok1 && ok2 && renew >= ttl
}

// journalConfigKeys records the config.yaml keys this run set in the
// running journal, so `lore migrate rollback` can take them out again. No
// running journal (a Rewriter used without Begin): nothing to record.
func (a *App) journalConfigKeys(ctx context.Context, keys []journalConfigKey) error {
	_, path, err := a.journalPath(ctx)
	if err != nil {
		return err
	}
	j, ok, err := readJournal(path)
	if err != nil || !ok || j.Phase != phaseRunning {
		return err
	}
	j.ConfigKeys = append(j.ConfigKeys, keys...)
	return writeJournal(path, j)
}

// rollbackConfigKeys removes the config.yaml keys a run set that still hold
// the value it wrote; a key changed since is kept and listed (7.6: a
// rollback never loses a later edit).
func (a *App) rollbackConfigKeys(keys []journalConfigKey) (line string, kept []string, err error) {
	if len(keys) == 0 {
		return "", nil, nil
	}
	root, err := readConfigRoot(a.opts.Home)
	if err != nil {
		return "", nil, err
	}
	var drop []string
	for _, k := range keys {
		cur, ok := configLookup(root, k.Key)
		switch {
		case !ok:
		case cur == k.Value:
			drop = append(drop, k.Key)
		default:
			kept = append(kept, "~/.lore/"+ConfigFile+" "+k.Key+" (changed after the migration)")
		}
	}
	if len(drop) == 0 {
		return "", kept, nil
	}
	if err := editMachineConfig(a.opts.Home, func(r *yaml.Node) error {
		for _, k := range drop {
			configDeletePath(r, k)
		}
		return nil
	}); err != nil {
		return "", nil, err
	}
	return "~/.lore/" + ConfigFile + ": " + strconv.Itoa(len(drop)) + " personal settings removed (" + strings.Join(drop, ", ") + ")", kept, nil
}
