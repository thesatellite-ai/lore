// Package resname is the one resolver of a typed resource name (RES 3.4),
// shared by every place that turns a user's word into a resource:
// `lore resource …` and ref values (ops/settings), a resource import's own
// rows (ops/settings pendingRow) and `--ref` on task, decision and incident
// (ops/internal/rowref).
//
//	key ─▶ Forms (bare | <kind>/<name> | <place>:<kind>/<name>)
//	    ─▶ Matches (id, bare name, or kind + name + place agree)
//	    ─▶ no name matched: ByAlias (an alias, any case: Bobby, the CFO)
//	    ─▶ 0 hits: the caller's not-found; 1: the row;
//	       2+: Ambiguous (E_RESOURCE_AMBIGUOUS naming the qualified names)
//
// A bare name at two levels or in two kinds is never resolved to the
// nearest one (BUGFIX-11): lore does not guess.
package resname

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

const (
	// PlaceSep ends the place of a qualified name: <place>:<kind>/<name>.
	PlaceSep = ":"
	// KindSep ends the kind: everything after it is the name, so a text
	// name may hold `/` and an ip name `:`.
	KindSep = "/"
	// PlaceRepo is the place word of the repo store.
	PlaceRepo = "repo"
	// placeStoreWord is the `scope` word a store root's settings rows
	// carry; accepted as a place for a store root.
	placeStoreWord = "store"
	// FieldKind and FieldName are the resources row fields read here.
	FieldKind = "kind"
	FieldName = "name"
	// FieldAliases is resources.aliases: the other names a resource is
	// known by (nicknames, full names, roles; devco D-817).
	FieldAliases = "aliases"
	// fieldKindName is kinds.name.
	fieldKindName = "name"
)

// Key is one reading of a typed key as <place>:<kind>/<name> (Place empty
// for the short form <kind>/<name>).
type Key struct{ Place, Kind, Name string }

// Forms are the qualified readings of key: the short form (split at the
// first `/`, the kind holding no `:`) and the qualified form (split at the
// first `:`, then the first `/`). A reading only matches a row whose kind,
// name and place all agree, so a bare name that holds `/` or `:` still
// finds itself through the bare comparison of Matches.
func Forms(key string) []Key {
	var out []Key
	if k, n, ok := strings.Cut(key, KindSep); ok && k != "" && n != "" && !strings.Contains(k, PlaceSep) {
		out = append(out, Key{Kind: k, Name: n})
	}
	if p, rest, ok := strings.Cut(key, PlaceSep); ok && p != "" {
		if k, n, ok := strings.Cut(rest, KindSep); ok && k != "" && n != "" {
			out = append(out, Key{Place: p, Kind: k, Name: n})
		}
	}
	return out
}

// Place is the <place> of a row held in src: `repo`, the folder part of
// <store>/<folder> (`org` or a project folder), or a store root's own name
// (the design's levels have no word for a store root, and its name is
// what tells two stores apart).
func Place(src store.Source) string {
	if src == "" || src == store.SourceRepo {
		return PlaceRepo
	}
	if _, f, ok := strings.Cut(string(src), KindSep); ok {
		return f
	}
	return string(src)
}

// PlaceMatches accepts the place Place prints, the attached store's full
// name (<store>/<folder>), and `store` for a store root.
func PlaceMatches(src store.Source, place string) bool {
	if place == Place(src) || place == string(src) {
		return true
	}
	return place == placeStoreWord && src != store.SourceRepo && src != "" && !strings.Contains(string(src), KindSep)
}

// Candidate is one resource as the resolver sees it: id, kind id, name
// and the store holding it.
type Candidate struct {
	ID, KindID, Name string
	Src              store.Source
	// Aliases are its other names (resources.aliases): a key that names
	// no resource by its name resolves through them (ByAlias).
	Aliases []string
}

// Of is the Candidate of a resources row held in src.
func Of(src store.Source, r row.Row) Candidate {
	return Candidate{ID: r.ID.String(), KindID: str(r, FieldKind), Name: str(r, FieldName), Src: src, Aliases: value.ItemStrings(r.Fields[FieldAliases])}
}

// Matches reports whether c is named by key (its id, its bare name, or one
// of forms = Forms(key)); kindName maps a kind id to its name.
func Matches(c Candidate, key string, forms []Key, kindName func(id string) string) bool {
	if c.ID == key || c.Name == key {
		return true
	}
	for _, f := range forms {
		if f.Name != c.Name || (f.Kind != c.KindID && f.Kind != kindName(c.KindID)) {
			continue
		}
		if f.Place == "" || PlaceMatches(c.Src, f.Place) {
			return true
		}
	}
	return false
}

// ByAlias reports whether key names c through one of its aliases: the
// bare alias or <kind>/<alias> (<place>:<kind>/<alias>), letter case
// ignored ("bobby" finds the alias Bobby). Callers try it only when no
// resource matched by id or name (Matches), so a name always wins over an
// alias and an alias never makes a name ambiguous (devco D-817).
func ByAlias(c Candidate, key string, forms []Key, kindName func(id string) string) bool {
	for _, a := range c.Aliases {
		if strings.EqualFold(a, key) {
			return true
		}
		for _, f := range forms {
			if !strings.EqualFold(f.Name, a) || (f.Kind != c.KindID && f.Kind != kindName(c.KindID)) {
				continue
			}
			if f.Place == "" || PlaceMatches(c.Src, f.Place) {
				return true
			}
		}
	}
	return false
}

// Qualified is c's <place>:<kind>/<name>.
func Qualified(c Candidate, kindName func(id string) string) string {
	return Place(c.Src) + PlaceSep + kindName(c.KindID) + KindSep + c.Name
}

// Ambiguous is the one refusal of a key that names several resources
// (RES 3.4): E_RESOURCE_AMBIGUOUS listing the qualified names, the hint
// naming them as the way out.
func Ambiguous(key string, hits []Candidate, kindName func(id string) string) *errs.Error {
	names := make([]string, len(hits))
	for i, h := range hits {
		names[i] = Qualified(h, kindName)
	}
	return errs.New(errs.ResourceAmbiguous, quote(key)+" matches "+strconv.Itoa(len(hits))+" resources: "+strings.Join(names, ", ")).
		WithHint("name it by its qualified name (" + strings.Join(names, " or ") + ") or its res_ id")
}

// Pick is the candidates key names: those it names by id or name
// (Matches), else those it names by an alias (ByAlias). The one two-pass
// rule every resolver of a resource name shares (devco D-817).
func Pick(cs []Candidate, key string, forms []Key, kindName func(id string) string) []Candidate {
	var hits []Candidate
	for _, c := range cs {
		if Matches(c, key, forms, kindName) {
			hits = append(hits, c)
		}
	}
	if len(hits) > 0 {
		return hits
	}
	for _, c := range cs {
		if ByAlias(c, key, forms, kindName) {
			hits = append(hits, c)
		}
	}
	return hits
}

// Lister reads a table of one attached store.
type Lister interface {
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
}

// KindNames maps every live kind id of srcs to its name (a kind of any
// level: ids are global).
func KindNames(ctx context.Context, env Lister, srcs []store.Source) (func(id string) string, error) {
	m := map[string]string{}
	for _, src := range srcs {
		rows, err := env.List(ctx, src, schema.TableKinds)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !r.Deleted {
				m[r.ID.String()] = str(r, fieldKindName)
			}
		}
	}
	return func(id string) string {
		if n, ok := m[id]; ok {
			return n
		}
		return id
	}, nil
}

// Find resolves key among the live resources of srcs (kindID non-empty:
// only that kind): found false when none matches, the one match, or
// Ambiguous. A store that cannot be listed refuses (fail closed: it might
// hold a second match).
func Find(ctx context.Context, env Lister, srcs []store.Source, key, kindID string) (Candidate, bool, error) {
	kindName, err := KindNames(ctx, env, srcs)
	if err != nil {
		return Candidate{}, false, err
	}
	forms := Forms(key)
	var live []Candidate
	for _, src := range srcs {
		rows, err := env.List(ctx, src, schema.TableResources)
		if err != nil {
			return Candidate{}, false, err
		}
		for _, r := range rows {
			if c := Of(src, r); !r.Deleted && (kindID == "" || c.KindID == kindID) {
				live = append(live, c)
			}
		}
	}
	hits := Pick(live, key, forms, kindName)
	switch len(hits) {
	case 0:
		return Candidate{}, false, nil
	case 1:
		return hits[0], true, nil
	}
	return Candidate{}, false, Ambiguous(key, hits, kindName)
}

func str(r row.Row, f string) string {
	v, ok := r.Fields[f]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

func quote(s string) string { return strconv.Quote(s) }
