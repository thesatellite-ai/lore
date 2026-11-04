package settings

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Store names with a fixed settings level (STORE TARGETS).
const (
	storeMe     = "me"
	orgFolder   = "org"
	folderSep   = "/"
	scopeStore  = "store"
	scopeRepo   = "repo"
	listSep     = ","
	shortIDTail = 4
)

// writeErrors are the codes any settings write can return (engine write
// path plus the settings checks).
var writeErrors = []errs.Code{
	errs.InvalidInput, errs.InvalidID, errs.NotFound, errs.AmbiguousID, errs.SecretDetected,
	errs.NeedsConfirm, errs.Cancelled, errs.PolicyDenied, errs.ConfigInvalid, errs.Stale,
	errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable, errs.OldLore, errs.Internal,
	errs.NameTaken, errs.MetaTaken, errs.ResourceInUse,
}

// readErrors are the codes a settings read can return.
var readErrors = []errs.Code{errs.InvalidInput, errs.InvalidID, errs.NotFound, errs.AmbiguousID, errs.StoreUnreachable, errs.OldLore, errs.Internal}

// located is one live row and the attached store that holds it.
type located struct {
	Src store.Source
	Row row.Row
}

// Leveler is an optional Env upgrade: the settings level the engine gave
// an attached store (Attached.Level, set by wire from the store's folder).
// ok=false for a store it does not know. An org-attached store's name does
// not tell its level (`--to team` attaches the linked folder under a plain
// name), so the engine's answer wins whenever the Env has one.
type Leveler interface {
	StoreLevel(src store.Source) (policy.Level, bool)
}

// storeLevel is src's settings level: the Env's own answer (Leveler),
// else the name rule (levelByName).
func storeLevel(env Env, src store.Source) policy.Level {
	if l, ok := env.(Leveler); ok {
		if lv, known := l.StoreLevel(src); known && lv != "" {
			return lv
		}
	}
	return levelByName(src)
}

// levelByName is the settings level a store's rows count at, read from its
// name the way wire attaches it (engine.levelOf for stores without an
// explicit level): repo, me, <store>/org, <store>/<folder>, <store>. Only
// the fallback when the Env cannot say (storeLevel).
func levelByName(src store.Source) policy.Level {
	s := string(src)
	switch {
	case src == store.SourceRepo:
		return policy.LevelRepo
	case s == storeMe:
		return policy.LevelPersonal
	case strings.HasSuffix(s, folderSep+orgFolder):
		return policy.LevelOrg
	case strings.Contains(s, folderSep):
		return policy.LevelFolder
	}
	return policy.LevelStoreRoot
}

// scopeOf is the `scope` word a settings row written to src carries (spec
// 4.3: `store` for a store root, else the place's folder; `repo` for the
// repo store).
func scopeOf(src store.Source) string {
	s := string(src)
	switch {
	case src == "" || src == store.SourceRepo:
		return scopeRepo
	case strings.Contains(s, folderSep):
		_, f, _ := strings.Cut(s, folderSep)
		return f
	}
	return scopeStore
}

// targetOf is the store a write lands in: --to, else the repo store.
func targetOf(to string) store.Source {
	if to == "" {
		return store.SourceRepo
	}
	return store.Source(to)
}

// sources is every attached store, or only the one --to names.
func sources(env Env, to string) []store.Source {
	if to != "" {
		return []store.Source{store.Source(to)}
	}
	return env.Stores()
}

// liveRows reads the live rows of table in srcs, in precedence order.
func liveRows(ctx context.Context, env Env, srcs []store.Source, table row.Table) ([]located, error) {
	var out []located
	for _, src := range srcs {
		rows, err := env.List(ctx, src, table)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !r.Deleted {
				out = append(out, located{Src: src, Row: r})
			}
		}
	}
	return out, nil
}

// where keeps the rows whose fields match every name=value pair.
func where(rows []located, match map[string]string) []located {
	var out []located
	for _, l := range rows {
		ok := true
		for f, v := range match {
			if text(l.Row, f) != v {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, l)
		}
	}
	return out
}

// byNameOrID finds one live row whose id is key or whose nameField is key
// (plus the extra match), the nearest store first. More than one at the
// SAME nearest store is E_AMBIGUOUS_ID; several stores is fine (the nearest
// wins, as a name resolves to the nearest definition).
func byNameOrID(ctx context.Context, env Env, srcs []store.Source, table row.Table, nameField, key string, extra map[string]string) (located, error) {
	rows, err := liveRows(ctx, env, srcs, table)
	if err != nil {
		return located{}, err
	}
	var hits []located
	for _, l := range where(rows, extra) {
		if l.Row.ID.String() == key || text(l.Row, nameField) == key {
			hits = append(hits, l)
		}
	}
	switch {
	case len(hits) == 0:
		return located{}, errs.New(errs.NotFound, "no "+string(table)+" row named "+quote(key))
	case len(hits) > 1 && hits[1].Src == hits[0].Src:
		return located{}, errs.New(errs.AmbiguousID, "several "+string(table)+" rows are named "+quote(key)+" in "+string(hits[0].Src)).
			WithHint("name it by its id")
	}
	return hits[0], nil
}

// create writes a new settings row, refusing first when a LIVE row with the
// same natural key (match) exists in target (a clearer message than the
// engine's). A natural id held by a tombstone (the row was deleted before)
// is re-created under a fresh random id by engine.Create itself.
func create(ctx context.Context, env Env, target store.Source, table row.Table, match map[string]string, fields map[string]value.Value) (engine.Written, error) {
	if len(match) > 0 {
		rows, err := liveRows(ctx, env, []store.Source{target}, table)
		if err != nil {
			return engine.Written{}, err
		}
		if hit := where(rows, match); len(hit) > 0 {
			return engine.Written{}, errs.New(takenCode(table), string(table)+": "+describeMatch(match)+" already exists in "+string(target)+" ("+hit[0].Row.ID.String()+")").
				WithHint("edit the existing row instead")
		}
	}
	return env.Create(ctx, target, table, fields)
}

// takenCode is the refusal of a create onto a live natural key: metadata
// keys and values answer E_META_TAKEN, every other settings row
// E_NAME_TAKEN.
func takenCode(table row.Table) errs.Code {
	if table == schema.TableMetaKeys || table == schema.TableMetaValues {
		return errs.MetaTaken
	}
	return errs.NameTaken
}

func describeMatch(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+" "+quote(v))
	}
	return strings.Join(parts, ", ")
}

// text is a field's text form ("" when absent or null).
func text(r row.Row, f string) string {
	v, ok := r.Fields[f]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// flag is a bool field (false when absent).
func flag(r row.Row, f string) bool {
	b, ok := r.Fields[f].(value.Bool)
	return ok && b.Bool()
}

// texts is a list-of-text field's items.
func texts(r row.Row, f string) []string {
	l, ok := r.Fields[f].(value.List)
	if !ok {
		if t := text(r, f); t != "" {
			return []string{t}
		}
		return nil
	}
	out := make([]string, 0, len(l.Items()))
	for _, it := range l.Items() {
		out = append(out, it.String())
	}
	return out
}

// jsonText is a value's JSON form ("" for none).
func jsonText(v value.Value) string {
	if v == nil {
		return ""
	}
	b, err := v.MarshalJSON()
	if err != nil {
		return ""
	}
	return string(b)
}

// txt builds a text value.
func txt(s string) (value.Value, error) { return value.TextOf(s) }

// textList builds a list of text.
func textList(xs []string) (value.Value, error) {
	items := make([]value.Value, 0, len(xs))
	for _, x := range xs {
		t, err := value.TextOf(x)
		if err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return value.ListOf(value.KindText, items...)
}

// splitList splits a comma list, trimming and dropping empty items.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, listSep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseAs parses CLI text as a value of kind k: number, bool, json and list
// read the text as JSON; every other kind reads it as a string.
func parseAs(k value.Kind, raw string) (value.Value, error) {
	switch k {
	case value.KindNumber, value.KindBool, value.KindJSON:
		return value.Parse(k, []byte(raw))
	case value.KindList:
		if canon.KindOf([]byte(raw)) == canon.KindArray {
			return value.Parse(k, []byte(raw))
		}
		return textList(splitList(raw))
	default:
		// Every other kind (text, date, ip, url, ref, …) reads the CLI text
		// as a JSON string.
		return value.Parse(k, canon.String(raw))
	}
}

// inferText reads CLI text as plain JSON when it is JSON (365, true,
// ["a"]), else as text (ask).
func inferText(raw string) (value.Value, error) {
	if v, err := value.Infer([]byte(raw)); err == nil {
		return v, nil
	}
	return value.TextOf(raw)
}

// asJSON wraps a value as a json field.
func asJSON(v value.Value) (value.Value, error) {
	b, err := v.MarshalJSON()
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "settings: encode a value", err)
	}
	return value.JSONOf(b)
}

// fromJSON reads a json field back as its inferred value (a text field
// written by hand stays text).
func fromJSON(v value.Value) value.Value {
	if j, ok := v.(value.JSON); ok {
		if in, err := value.Infer(j.Raw()); err == nil {
			return in
		}
	}
	return v
}

// display prints a value for a person: text as is, others as JSON.
func display(v value.Value) string {
	if v == nil {
		return ""
	}
	if t, ok := v.(value.Text); ok {
		return t.String()
	}
	if l, ok := v.(value.List); ok {
		return l.String()
	}
	return jsonText(v)
}

// short is v0.1.17's short id form for human lines: the prefix and the
// last four characters (pol_…7e5f).
func short(id ids.AnyID) string {
	s := id.String()
	p, rest, ok := strings.Cut(s, "_")
	if !ok || len(rest) <= shortIDTail {
		return s
	}
	return p + "_…" + rest[len(rest)-shortIDTail:]
}

// quote is a value in double quotes for messages.
func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	return string(b)
}

// ref builds a reference value.
func ref(id ids.AnyID) (value.Value, error) { return value.RefOf(id) }

// place names a store for a human line ("this repo" for the repo store).
func place(src store.Source) string {
	if src == store.SourceRepo || src == "" {
		return "this repo"
	}
	return string(src)
}
