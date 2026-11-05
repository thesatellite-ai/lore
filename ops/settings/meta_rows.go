package settings

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// metaRow is one data row that carries metadata, with its entries decoded
// (the maintenance commands of METADATA 3.8, 3.18, 3.19 read every row's
// `meta` column: forget, --unused, lint, --where, export, merge-into).
type metaRow struct {
	located
	entries map[ids.ID[ids.MetaKey]]meta.Entry
}

// metaDataTables are the tables whose rows take metadata (engine.AcceptsMeta),
// in a stable order.
func metaDataTables() []row.Table {
	var out []row.Table
	for _, t := range schema.Builtin() {
		if engine.AcceptsMeta(t.Name) {
			out = append(out, t.Name)
		}
	}
	slices.Sort(out)
	return out
}

// metaRows reads every row with metadata in every attached store. Archived
// rows count (3.19: archived rows are in use). An entry that does not
// decode is skipped here; `meta lint` reports it.
func (o ops) metaRows(ctx context.Context) ([]metaRow, error) { return o.rowsOf(ctx, false) }

// rowsOf is metaRows; all keeps rows without metadata too (a --where
// filter such as `jira!?` must see them).
func (o ops) rowsOf(ctx context.Context, all bool) ([]metaRow, error) {
	sens, err := o.sensitiveKeyIDs(ctx)
	if err != nil {
		return nil, err
	}
	var out []metaRow
	for _, t := range metaDataTables() {
		rows, err := liveRows(ctx, o.env, o.env.Stores(), t)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if len(r.Row.Meta) == 0 && !all {
				continue
			}
			mr := metaRow{located: r, entries: decodeEntries(r.Row, sens)}
			out = append(out, mr)
		}
	}
	return out, nil
}

// sensitiveKeyIDs is the id of every live key row, in any attached store,
// marked sensitive. METADATA 3.20 "one masker": a value is masked when its
// entry's label OR the registry's flag says so, whichever does; a row
// written before its key turned sensitive (or one whose entry kept its
// flags through a fold) carries no label, so every read path ORs this set
// into the entry (decodeEntries) before anything prints a value.
func (o ops) sensitiveKeyIDs(ctx context.Context) (map[string]bool, error) {
	keys, err := liveRows(ctx, o.env, o.env.Stores(), schema.TableMetaKeys)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, k := range keys {
		if flag(k.Row, mSensitive) {
			out[k.Row.ID.String()] = true
		}
	}
	return out, nil
}

// decodeEntries decodes r's meta entries (an entry that does not decode is
// skipped; lint reports it) with the registry's sensitive flag ORed in
// (3.20). The flag is display-only here: no caller writes these entries
// back as they are.
func decodeEntries(r row.Row, sens map[string]bool) map[ids.ID[ids.MetaKey]]meta.Entry {
	out := map[ids.ID[ids.MetaKey]]meta.Entry{}
	for kid, j := range r.Meta {
		if en, derr := meta.DecodeEntry(j); derr == nil {
			en.Sensitive = en.Sensitive || sens[kid.String()]
			out[kid] = en
		}
	}
	return out
}

// trasher is the optional Env upgrade the used-key check needs: the rows
// in the trash of every attached store (wire.App has it).
type trasher interface {
	Trashed(ctx context.Context) ([]engine.Trashed, error)
}

// usedRows is metaRows plus the trashed rows that carry metadata (OV58):
// a key or value used only by a row in the trash is still used, because
// restoring that row would bring back an id nothing defines. An Env
// without a trash reads live rows only.
func (o ops) usedRows(ctx context.Context) ([]metaRow, error) {
	out, err := o.metaRows(ctx)
	if err != nil {
		return nil, err
	}
	tr, ok := upgrade.As[trasher](o.env)
	if !ok {
		return out, nil
	}
	all, err := tr.Trashed(ctx)
	if err != nil {
		return nil, err
	}
	sens, err := o.sensitiveKeyIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, it := range all {
		if len(it.Row.Meta) == 0 || !engine.AcceptsMeta(it.Row.Table) {
			continue
		}
		mr := metaRow{located: located{Src: it.Store, Row: it.Row}, entries: decodeEntries(it.Row, sens)}
		out = append(out, mr)
	}
	return out, nil
}

// keyUse is how a key is used: the rows (by table) that carry it.
type keyUse struct {
	rows    int
	byTable map[row.Table]int
	values  map[string]bool
}

// usage counts, per key id, the rows that carry it.
func usage(rows []metaRow) map[string]*keyUse {
	out := map[string]*keyUse{}
	for _, r := range rows {
		for kid, en := range r.entries {
			u := out[kid.String()]
			if u == nil {
				u = &keyUse{byTable: map[row.Table]int{}, values: map[string]bool{}}
				out[kid.String()] = u
			}
			u.rows++
			u.byTable[r.Row.Table]++
			for _, s := range meta.Strings(en.V) {
				u.values[s] = true
			}
		}
	}
	return out
}

// tableCounts renders "38 tasks, 2 memories".
func (u *keyUse) tableCounts() string {
	if u == nil {
		return ""
	}
	ts := make([]string, 0, len(u.byTable))
	for t := range u.byTable {
		ts = append(ts, string(t))
	}
	slices.Sort(ts)
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		parts = append(parts, strconv.Itoa(u.byTable[row.Table(t)])+" "+t)
	}
	return strings.Join(parts, ", ")
}

// checkRegistryCaps applies meta.max_key_length (E_META_KEY) and
// meta.max_keys_per_registry (E_META_SIZE: a data limit, every writer) to
// a new key named name in target's registry (METADATA 3.8 R11, 3.13,
// FA-META-11). An Env without a policy reader checks the defaults.
func (o ops) checkRegistryCaps(ctx context.Context, target store.Source, name string) error {
	maxLen, maxKeys, err := registryLimits(ctx, o.env, target)
	if err != nil {
		return err
	}
	if int64(len(name)) > maxLen {
		return errs.New(errs.MetaKey, fmt.Sprintf("meta: %q is longer than %d characters (meta.max_key_length)", name, maxLen))
	}
	keys, err := liveRows(ctx, o.env, []store.Source{target}, schema.TableMetaKeys)
	if err != nil {
		return err
	}
	if int64(len(keys)) >= maxKeys {
		return errs.New(errs.MetaSize, fmt.Sprintf("meta: %s already holds %d key rows (meta.max_keys_per_registry %d)", target, len(keys), maxKeys)).
			WithHint(registryCapHint)
	}
	return nil
}

// registryCapHint is the fix for a registry at or past its key cap.
const registryCapHint = "fold or forget unused keys: lore meta keys --unused; lore meta forget <key> --confirm"

// registryLimits reads meta.max_key_length and meta.max_keys_per_registry
// for target (the defaults when env reads no policy).
func registryLimits(ctx context.Context, env Env, target store.Source) (maxLen, maxKeys int64, err error) {
	maxLen, maxKeys = int64(meta.MaxKeyLen), int64(DefaultMaxKeysPerRegistry)
	if p, ok := env.(bulk.Policier); ok {
		set, perr := p.Policy(ctx, target)
		if perr != nil {
			return 0, 0, perr
		}
		if n, isSet := set.Limit(policy.KeyMetaMaxKeyLength); isSet && n > 0 {
			maxLen = n
		}
		if n, isSet := set.Limit(policy.KeyMetaMaxKeysPerRegistry); isSet && n >= 0 {
			maxKeys = n
		}
	}
	return maxLen, maxKeys, nil
}

// DefaultMaxKeysPerRegistry is meta.max_keys_per_registry's default (R11).
const DefaultMaxKeysPerRegistry = 500

// RegistryOver is one registry holding more live key rows than its
// meta.max_keys_per_registry: writes refuse new keys there, but rows that
// arrive by sync are never refused, so lint and sync status report it
// (FA-META-11, METADATA 3.13).
type RegistryOver struct {
	Store string `json:"store"`
	Keys  int    `json:"keys"`
	Cap   int64  `json:"cap"`
}

// Line is the one-line report of r.
func (r RegistryOver) Line() string {
	return fmt.Sprintf("%s: %d key rows, past meta.max_keys_per_registry %d (%s)", place(store.Source(r.Store)), r.Keys, r.Cap, registryCapHint)
}

// RegistriesOverCap lists every attached store whose registry is past its
// key cap, in env.Stores() order.
func RegistriesOverCap(ctx context.Context, env Env) ([]RegistryOver, error) {
	var out []RegistryOver
	for _, src := range env.Stores() {
		keys, err := liveRows(ctx, env, []store.Source{src}, schema.TableMetaKeys)
		if err != nil {
			return nil, err
		}
		_, maxKeys, err := registryLimits(ctx, env, src)
		if err != nil {
			return nil, err
		}
		if int64(len(keys)) > maxKeys {
			out = append(out, RegistryOver{Store: string(src), Keys: len(keys), Cap: maxKeys})
		}
	}
	return out, nil
}
