package settings

// Resource aliases (devco D-817): the other names a resource is known by.
//
//	resource alias add    --id robert-kim --alias Bobby [--kind person]
//	  ─► resolve the resource (name, or an alias already set)
//	  ─► refuse an alias that is its own name, or the name or alias of
//	     another resource of its kind in its store (E_RESOURCE_TAKEN)
//	  ─► resources.aliases += alias (a set: merged by union across clones)
//	resource alias remove --id robert-kim --alias Bobby
//	resource alias list   [--id robert-kim] [--kind person]
//
// Every lookup of a resource name (resource show/edit/…, --ref, ${res:…})
// tries the aliases when no name matches (ops/internal/resname.Pick), and
// lore ask matches a question's words against names and aliases, also one
// or two letters off (ops/ask).

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/resname"
	"github.com/thesatellite-ai/lore/schema"
)

// Operation names of the alias ops.
const (
	OpResAliasAdd    = "resource.alias.add"
	OpResAliasRemove = "resource.alias.remove"
	OpResAliasList   = "resource.alias.list"
)

// rAliases is resources.aliases.
const rAliases = schema.FieldResourceAliases

// maxAlias caps one alias, as a resource name is capped.
const maxAlias = maxResourceName

// ResAliasIn is the input of resource alias add and remove.
type ResAliasIn struct {
	// --store <org>[/<folder>]: where the resource is, as resource edit.
	op.StoreFlag
	Name string `json:"id" flag:"id" alias:"name" desc:"the resource (name, alias or res_ id)"`
	Kind string `json:"kind,omitempty" flag:"kind" desc:"only a resource of this kind (name or knd_ id)"`
	// Alias is checked after the lookup, so an unknown resource answers
	// E_NOT_FOUND as every lookup does (B-ERRS-1).
	Alias []string `json:"alias,omitempty" flag:"alias" desc:"the other name: a nickname, full name or role, any text (repeatable)"`
}

// ResAliasListIn is the input of resource alias list.
type ResAliasListIn struct {
	op.StoreFlag
	Name string `json:"id,omitempty" flag:"id" alias:"name" desc:"one resource (name, alias or res_ id); every resource with an alias when left out"`
	Kind string `json:"kind,omitempty" flag:"kind" desc:"only resources of this kind (name or knd_ id)"`
	// --limit / --page: the shared paging flags of every list; Invoke
	// cuts the page from Data (op.PageFlags, T-4).
	op.PageFlags
}

// AliasEntry is one resource and its aliases.
type AliasEntry struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Store   string   `json:"store"`
	Aliases []string `json:"aliases"`
}

// AliasListOut is the output of resource alias list.
type AliasListOut struct {
	Count int          `json:"count"`
	Data  []AliasEntry `json:"data"`
}

// Text implements op.Texter: one line per resource.
func (o AliasListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no aliases; add one: lore resource alias add --id <name> --alias <alias>)\n"
	}
	var b strings.Builder
	for _, e := range o.Data {
		fmt.Fprintf(&b, "%s/%s  %s  (%s)\n", e.Kind, e.Name, strings.Join(e.Aliases, ", "), e.Store)
	}
	return b.String()
}

func (o ops) aliasOps() []op.Entry {
	errsOf := append(slices.Clone(writeErrors), errs.ResourceTaken, errs.ResourceAmbiguous, errs.KindUnknown)
	return []op.Entry{
		op.Define(op.Spec{Name: OpResAliasAdd, Summary: "Give a resource another name (a nickname, full name or role) that every lookup and lore ask find it by", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors:   errsOf,
			Examples: []op.Example{{Title: "a nickname", CLI: "lore resource alias add --kind person --id robert-kim --alias Bobby", Want: "alias Bobby"}}}, o.resAliasAdd),
		op.Define(op.Spec{Name: OpResAliasRemove, Summary: "Remove an alias of a resource", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors:   errsOf,
			Examples: []op.Example{{Title: "drop a nickname", CLI: "lore resource alias remove --id robert-kim --alias Bobby"}}}, o.resAliasRemove),
		op.Define(op.Spec{Name: OpResAliasList, Summary: "List the aliases of a resource, or of every resource that has one", Permission: op.PermRead, Access: op.AccessRead,
			Errors:   append(slices.Clone(readErrors), errs.ResourceAmbiguous, errs.KindUnknown),
			Examples: []op.Example{{Title: "every person's aliases", CLI: "lore resource alias list --kind person"}}}, o.resAliasList),
	}
}

// aliasTarget resolves the resource an alias op names: --kind narrows it,
// and a folder-scoped call refuses a resource of another folder as
// resource edit does.
func (o ops) aliasTarget(ctx context.Context, key, kind, opName string) (located, error) {
	if strings.TrimSpace(key) == "" {
		return located{}, errs.New(errs.InvalidInput, "name the resource with --id").WithHint("lore " + strings.ReplaceAll(opName, ".", " ") + " --id <name or res_ id> --alias <alias>")
	}
	kindID := ""
	if strings.TrimSpace(kind) != "" {
		k, err := o.kindByName(ctx, kind)
		if err != nil {
			return located{}, err
		}
		kindID = k.Row.ID.String()
	}
	r, err := o.resourceIn(ctx, key, kindID, "")
	if err != nil {
		return located{}, err
	}
	if sc, scoped := o.folderScope(); scoped && !o.rowInScope(ctx, sc, r) {
		return o.resourceInScope(ctx, r.Row.ID.String(), opName) // its refusal, naming the way out
	}
	return r, nil
}

// cleanAliases trims each alias and refuses an empty, multi-line or
// overlong one; duplicates (any case) collapse to the first.
func cleanAliases(in []string) ([]string, error) {
	var out []string
	for _, a := range in {
		a = strings.Join(strings.Fields(a), " ")
		switch {
		case a == "":
			return nil, errs.New(errs.InvalidInput, "an alias is empty").WithHint("give --alias <alias>, for example --alias Bobby")
		case len(a) > maxAlias:
			return nil, errs.New(errs.InvalidInput, fmt.Sprintf("alias %s is longer than %d bytes", quote(a), maxAlias))
		case strings.ContainsFunc(a, unicode.IsControl):
			return nil, errs.New(errs.InvalidInput, "alias "+quote(a)+" holds a control character")
		}
		if !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, a) }) {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, errs.New(errs.InvalidInput, "no alias given").WithHint("give --alias <alias> (repeatable)")
	}
	return out, nil
}

func (o ops) resAliasAdd(ctx context.Context, in ResAliasIn) (RowOut, error) {
	r, err := o.aliasTarget(ctx, in.Name, in.Kind, OpResAliasAdd)
	if err != nil {
		return RowOut{}, err
	}
	adds, err := cleanAliases(in.Alias)
	if err != nil {
		return RowOut{}, err
	}
	name, kindID := text(r.Row, rName), text(r.Row, rKind)
	rows, err := liveRows(ctx, o.env, o.resStores(), schema.TableResources)
	if err != nil {
		return RowOut{}, err
	}
	have := texts(r.Row, rAliases)
	var added []string
	for _, a := range adds {
		if strings.EqualFold(a, name) {
			return RowOut{}, errs.New(errs.InvalidInput, "alias "+quote(a)+" is the resource's own name")
		}
		// one kind and store name one resource by it: another resource's
		// name or alias would make the alias ambiguous (RES 3.4)
		for _, l := range rows {
			c := resname.Of(l.Src, l.Row)
			if l.Row.ID == r.Row.ID || l.Src != r.Src || c.KindID != kindID {
				continue
			}
			if strings.EqualFold(c.Name, a) || slices.ContainsFunc(c.Aliases, func(x string) bool { return strings.EqualFold(x, a) }) {
				return RowOut{}, errs.New(errs.ResourceTaken, fmt.Sprintf("alias %s already names %s/%s in %s", quote(a), o.kindName(ctx, kindID), c.Name, r.Src)).
					WithHint(fmt.Sprintf("lore resource alias list --id %s lists its aliases", c.Name))
			}
		}
		if !slices.ContainsFunc(have, func(x string) bool { return strings.EqualFold(x, a) }) {
			have = append(have, a)
			added = append(added, a)
		}
	}
	if len(added) == 0 {
		return RowOut{ID: r.Row.ID.String(), Store: string(r.Src), Human: fmt.Sprintf("= %s already has alias %s; nothing written\n", name, strings.Join(adds, ", "))}, nil
	}
	return o.writeAliases(ctx, r, have, fmt.Sprintf("alias %s for %s/%s", strings.Join(added, ", "), o.kindName(ctx, kindID), name))
}

func (o ops) resAliasRemove(ctx context.Context, in ResAliasIn) (RowOut, error) {
	r, err := o.aliasTarget(ctx, in.Name, in.Kind, OpResAliasRemove)
	if err != nil {
		return RowOut{}, err
	}
	drops, err := cleanAliases(in.Alias)
	if err != nil {
		return RowOut{}, err
	}
	have := texts(r.Row, rAliases)
	name := text(r.Row, rName)
	for _, a := range drops {
		i := slices.IndexFunc(have, func(x string) bool { return strings.EqualFold(x, a) })
		if i < 0 {
			return RowOut{}, errs.New(errs.NotFound, fmt.Sprintf("%s has no alias %s", name, quote(a))).
				WithHint(fmt.Sprintf("lore resource alias list --id %s lists its aliases", r.Row.ID.String()))
		}
		have = slices.Delete(have, i, i+1)
	}
	return o.writeAliases(ctx, r, have, fmt.Sprintf("removed alias %s from %s", strings.Join(drops, ", "), name))
}

// writeAliases stores aliases as r's whole list.
func (o ops) writeAliases(ctx context.Context, r located, aliases []string, what string) (RowOut, error) {
	l, err := textList(aliases)
	if err != nil {
		return RowOut{}, err
	}
	return o.patchRow(ctx, r, map[string]value.Value{rAliases: l}, what)
}

func (o ops) resAliasList(ctx context.Context, in ResAliasListIn) (AliasListOut, error) {
	out := AliasListOut{Data: []AliasEntry{}}
	entry := func(l located) AliasEntry {
		return AliasEntry{ID: l.Row.ID.String(), Kind: o.kindName(ctx, text(l.Row, rKind)), Name: text(l.Row, rName), Store: string(l.Src), Aliases: aliasesOrEmpty(texts(l.Row, rAliases))}
	}
	if strings.TrimSpace(in.Name) != "" {
		r, err := o.aliasTarget(ctx, in.Name, in.Kind, OpResAliasList)
		if err != nil {
			return AliasListOut{}, err
		}
		out.Data = append(out.Data, entry(r))
		out.Count = 1
		return out, nil
	}
	kindID := ""
	if strings.TrimSpace(in.Kind) != "" {
		k, err := o.kindByName(ctx, in.Kind)
		if err != nil {
			return AliasListOut{}, err
		}
		kindID = k.Row.ID.String()
	}
	rows, err := liveRows(ctx, o.env, o.resStores(), schema.TableResources)
	if err != nil {
		return AliasListOut{}, err
	}
	sc, scoped := o.folderScope()
	for _, l := range rows {
		if len(texts(l.Row, rAliases)) == 0 || (kindID != "" && text(l.Row, rKind) != kindID) || (scoped && !o.rowInScope(ctx, sc, l)) {
			continue
		}
		out.Data = append(out.Data, entry(l))
	}
	slices.SortFunc(out.Data, func(a, b AliasEntry) int {
		return strings.Compare(a.Kind+"/"+a.Name+" "+a.Store, b.Kind+"/"+b.Name+" "+b.Store)
	})
	out.Count = len(out.Data)
	return out, nil
}

// aliasesOrEmpty is s, or an empty (not nil) list, so JSON prints [].
func aliasesOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
