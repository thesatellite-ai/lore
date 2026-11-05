package wire

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// The reference resolver (SECRETS_AND_REFERENCES 8.7, 9, 11.4; SQ3, VARS
// ARE DATA, D22) and the re-expansion that keeps row_expanded current:
//
//	cache Apply ─▶ expandField ─▶ refs.Expander ─▶ resolver.Resolve
//	                                                 │ var: nearest of repo, folder, store (store order)
//	                                                 │ res: name[/field] (+ ?kind=) in the reader's view
//	                                                 │ row: id[/field] through engine.Get
//	                                                 ▼ reads the row files (the truth), never a cache:
//	                                                   the cache is mid-transaction when it asks
//	engine ─▶ reexpandCache.Apply ─▶ caches.Apply, then for every var /
//	          resource / kind row applied: drop the snapshot, Reexpand(id)
//	          and ReexpandMatching("${var:<name>") in every cache file
//
// Secrets and env never reach the resolver (refs.Expander). A sensitive
// value stays as its reference (8.7, RQ1). In a client repo only the
// repo's own (shareable) variables and resources resolve, the same
// boundary as the client lens (MQ6): the repo cache never holds a private
// value a client-repo agent could read through lore sql.

// resolver implements refs.Resolver over the App's engine. st.eng is set
// after the caches open, so a resolve before that finds nothing.
type resolver struct {
	st *state

	mu   sync.Mutex
	snap *refSnapshot // nil = stale, rebuilt on the next resolve
}

// newResolver returns the resolver of st.
func newResolver(st *state) *resolver { return &resolver{st: st} }

// refSnapshot is the live var, resource, kind and kind field rows of every
// store the resolver sees, in store order (nearest first).
type refSnapshot struct {
	vars, resources, kinds, kindFields []located
}

// located is a row and the store it came from.
type located struct {
	src store.Source
	r   row.Row
}

// Resolve implements refs.Resolver.
func (rv *resolver) Resolve(ref refs.Ref) (refs.Resolved, error) {
	switch ref.Scheme {
	case refs.SchemeVar:
		return rv.resolveVar(ref)
	case refs.SchemeRes:
		return rv.resolveRes(ref)
	case refs.SchemeRow:
		return rv.resolveRow(ref)
	default:
		// meta needs the row the text sits in, which this port does not
		// carry; it stays as written (as lore_expand leaves it, 12.1). env
		// and secret are never resolved through this port either.
		return refs.Resolved{}, refs.ErrUnresolved
	}
}

// invalidate drops the snapshot (a var, resource or kind row changed).
func (rv *resolver) invalidate() {
	rv.mu.Lock()
	rv.snap = nil
	rv.mu.Unlock()
}

// snapshot returns the current snapshot, reading the row files once.
func (rv *resolver) snapshot() (*refSnapshot, error) {
	rv.mu.Lock()
	defer rv.mu.Unlock()
	if rv.snap != nil {
		return rv.snap, nil
	}
	eng := rv.st.eng
	if eng == nil {
		return nil, refs.ErrUnresolved
	}
	ctx := context.Background() // the cache port carries no context; reads are local files
	srcs := eng.Stores()
	if eng.IsClient(store.SourceRepo) {
		srcs = []store.Source{store.SourceRepo} // MQ6: a client repo resolves only its own rows
	}
	s := &refSnapshot{}
	for _, src := range srcs {
		for _, t := range []struct {
			table row.Table
			into  *[]located
		}{{schema.TableVars, &s.vars}, {schema.TableResources, &s.resources}, {schema.TableKinds, &s.kinds}, {schema.TableKindFields, &s.kindFields}} {
			rows, err := eng.List(ctx, src, t.table)
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				*t.into = append(*t.into, located{src: src, r: r})
			}
		}
	}
	rv.snap = s
	return s, nil
}

// visible reports whether src is a store the resolver may read.
func (rv *resolver) visible(src store.Source) bool {
	return src == store.SourceRepo || !rv.st.eng.IsClient(store.SourceRepo)
}

// resolveVar: name, alias or var_ id, nearest definition wins (11.4).
func (rv *resolver) resolveVar(ref refs.Ref) (refs.Resolved, error) {
	s, err := rv.snapshot()
	if err != nil {
		return refs.Resolved{}, err
	}
	key := ref.Target
	var hit *located
	for _, match := range []func(located) bool{
		func(l located) bool { return l.r.ID.String() == key || fieldText(l.r, fieldVarName) == key },
		func(l located) bool { return slices.Contains(value.ItemStrings(l.r.Fields[fieldVarAliases]), key) },
	} {
		if i := slices.IndexFunc(s.vars, match); i >= 0 {
			hit = &s.vars[i]
			break
		}
	}
	if hit == nil || refBool(hit.r, fieldSensitive) {
		return refs.Resolved{}, refs.ErrUnresolved // missing, or sensitive: stays as its reference (8.7)
	}
	v := refScalar(hit.r.Fields[fieldVarValue])
	if fieldText(hit.r, fieldVarType) == varTypeSecretRef {
		v = "${secret:" + v + "}" // 11.5: a secret_ref var becomes a secret reference, never a value
	}
	return refs.Resolved{Text: v, ID: hit.r.ID}, nil
}

// resolveRes: name/field[/sub], option kind (9). A sensitive field stays
// as the reference; a secret_ref field becomes a secret reference.
func (rv *resolver) resolveRes(ref refs.Ref) (refs.Resolved, error) {
	s, err := rv.snapshot()
	if err != nil {
		return refs.Resolved{}, err
	}
	name, field, _ := strings.Cut(ref.Target, "/")
	kindName, byKind := ref.Option(refs.OptKind)
	var hits []located
	for _, l := range s.resources {
		if fieldText(l.r, fieldName) != name || fieldText(l.r, fieldResArchived) != "" {
			continue
		}
		if byKind && kindNameOf(s, fieldText(l.r, fieldResKind)) != kindName {
			continue
		}
		hits = append(hits, l)
	}
	if len(hits) == 0 {
		return refs.Resolved{}, refs.ErrUnresolved
	}
	// nearest store first; two kinds with the same name in one store is
	// ambiguous, so it stays a reference (the person adds ?kind=).
	if len(hits) > 1 && hits[0].src == hits[1].src && !byKind {
		return refs.Resolved{}, refs.ErrUnresolved
	}
	res := hits[0]
	if field == "" {
		return refs.Resolved{Text: name, ID: res.r.ID}, nil
	}
	head, sub, _ := strings.Cut(field, "/")
	entries := resEntries(res.r)
	for fid, e := range entries {
		if e.Name != head {
			continue
		}
		def := kindFieldOf(s, fid)
		if def != nil && refBool(def.r, fieldSensitive) {
			return refs.Resolved{}, refs.ErrUnresolved
		}
		v, ok := subValue(e.V, sub)
		if !ok {
			return refs.Resolved{}, refs.ErrUnresolved
		}
		if def != nil && fieldText(def.r, fieldVarType) == varTypeSecretRef {
			v = "${secret:" + v + "}"
		}
		return refs.Resolved{Text: v, ID: res.r.ID}, nil
	}
	return refs.Resolved{}, refs.ErrUnresolved
}

// resolveRow: id → "title (id)"; id/field → that field (9). A deleted row
// is dangling and stays as written.
func (rv *resolver) resolveRow(ref refs.Ref) (refs.Resolved, error) {
	eng := rv.st.eng
	if eng == nil {
		return refs.Resolved{}, refs.ErrUnresolved
	}
	idText, field, _ := strings.Cut(ref.Target, "/")
	id, err := ids.ParseAny(idText)
	if err != nil {
		return refs.Resolved{}, refs.ErrUnresolved
	}
	src, r, err := eng.Get(context.Background(), engine.Ref{ID: id})
	switch {
	case errs.Has(err, errs.NotFound), errs.Has(err, errs.AmbiguousID):
		return refs.Resolved{}, refs.ErrUnresolved
	case err != nil:
		return refs.Resolved{}, err
	}
	if r.Deleted || !rv.visible(src) {
		return refs.Resolved{}, refs.ErrUnresolved
	}
	if field != "" {
		v, ok := r.Fields[field]
		if !ok || v == nil || v.Kind() == value.KindNull || sensitiveRowField(field) {
			return refs.Resolved{}, refs.ErrUnresolved
		}
		return refs.Resolved{Text: refScalar(v), ID: id}, nil
	}
	return refs.Resolved{Text: rowTitle(r) + " (" + id.String() + ")", ID: id}, nil
}

// sensitiveRowField: fields never filled in from a row reference.
func sensitiveRowField(f string) bool { return f == fieldSensitive }

// rowTitle is a row's title, or its name, or the first line of its body.
func rowTitle(r row.Row) string {
	for _, f := range []string{"title", fieldName} {
		if t := fieldText(r, f); t != "" {
			return t
		}
	}
	first, _, _ := strings.Cut(fieldText(r, "body"), "\n")
	return first
}

// Field names the resolver reads (schema.Builtin).
const (
	fieldName        = "name"
	fieldSensitive   = "sensitive"
	fieldVarName     = "name"
	fieldVarValue    = "value"
	fieldVarType     = "type"
	fieldVarAliases  = "aliases"
	fieldResKind     = "kind"
	fieldResArchived = "archived_at"
	fieldResFields   = "fields"
	fieldKindID      = "kind_id"
	varTypeSecretRef = "secret_ref"
)

func refBool(r row.Row, f string) bool {
	b, ok := r.Fields[f].(value.Bool)
	return ok && b.Bool()
}

// scalar prints a value: text as is, a JSON string unquoted, anything else
// as its JSON.
func refScalar(v value.Value) string {
	if t, ok := v.(value.Text); ok {
		return t.String()
	}
	raw, ok := value.JSONForm(v)
	if !ok {
		return ""
	}
	return refRawScalar(raw)
}

func refRawScalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// resEntry is one entry of a resource's fields object (STORE_SERVER_SPEC
// 4.4: {"fld_…": {"name": "ip", "v": …}}).
type resEntry struct {
	Name string          `json:"name"`
	V    json.RawMessage `json:"v"`
}

func resEntries(r row.Row) map[string]resEntry {
	raw, ok := value.JSONForm(r.Fields[fieldResFields])
	if !ok {
		return nil
	}
	var out map[string]resEntry
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// subValue reads key sub of an object value ("" = the value itself).
func subValue(v json.RawMessage, sub string) (string, bool) {
	if sub == "" {
		return refRawScalar(v), len(v) > 0 && string(v) != "null"
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(v, &obj) != nil {
		return "", false
	}
	head, rest, _ := strings.Cut(sub, "/")
	inner, ok := obj[head]
	if !ok {
		return "", false
	}
	return subValue(inner, rest)
}

func kindNameOf(s *refSnapshot, kindID string) string {
	for _, k := range s.kinds {
		if k.r.ID.String() == kindID {
			return fieldText(k.r, fieldName)
		}
	}
	return ""
}

func kindFieldOf(s *refSnapshot, fid string) *located {
	for i := range s.kindFields {
		if s.kindFields[i].r.ID.String() == fid {
			return &s.kindFields[i]
		}
	}
	return nil
}

// reexpandCache is the engine's cache: the caches router plus the
// re-expansion of SQ3 after every Apply.
type reexpandCache struct {
	*caches
	rv *resolver
}

var _ store.Cache = reexpandCache{}

// Apply implements store.Cache: apply, then refresh every row whose text
// refers to a changed var, resource or kind (8.7 "refresh by row_refs",
// plus a text scan for names that were dangling until now).
func (c reexpandCache) Apply(ctx context.Context, source store.Source, rows []store.Row, next store.Cursor) error {
	if err := c.caches.Apply(ctx, source, rows, next); err != nil {
		return err
	}
	var changed []string
	var needles []string
	for _, sr := range rows {
		switch sr.Table {
		case schema.TableVars, schema.TableResources, schema.TableKinds, schema.TableKindFields:
		default:
			changed = append(changed, sr.ID.String()) // a row others may cite as ${row:id}
			continue
		}
		changed = append(changed, sr.ID.String())
		r, err := sr.Decode()
		if err != nil {
			return err
		}
		switch sr.Table {
		case schema.TableVars:
			for _, n := range append([]string{fieldText(r, fieldVarName)}, value.ItemStrings(r.Fields[fieldVarAliases])...) {
				if n != "" {
					needles = append(needles, "${var:"+n)
				}
			}
		case schema.TableResources:
			if n := fieldText(r, fieldName); n != "" {
				needles = append(needles, "${res:"+n)
			}
		}
	}
	if len(needles) > 0 || slices.ContainsFunc(rows, func(sr store.Row) bool {
		return sr.Table == schema.TableKinds || sr.Table == schema.TableKindFields
	}) {
		c.rv.invalidate()
	}
	if len(changed) == 0 {
		return nil
	}
	return c.reexpand(ctx, changed, needles)
}

// reexpand runs Reexpand for each id and ReexpandMatching for needles in
// every distinct cache file.
func (c reexpandCache) reexpand(ctx context.Context, changed, needles []string) error {
	c.mu.Lock()
	var files []reexpander
	seen := map[reexpander]bool{}
	for _, s := range c.order {
		if f := c.by[s]; f != nil && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	c.mu.Unlock()
	slices.Sort(needles)
	needles = slices.Compact(needles)
	for _, f := range files {
		if _, err := f.ReexpandIDs(ctx, changed); err != nil {
			return err
		}
		if _, err := f.ReexpandMatching(ctx, needles); err != nil {
			return err
		}
	}
	return nil
}

// reexpander is the part of a cache file reexpand uses.
type reexpander interface {
	ReexpandIDs(ctx context.Context, refIDs []string) (int, error)
	ReexpandMatching(ctx context.Context, needles []string) (int, error)
}
