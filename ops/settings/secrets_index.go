package settings

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/reveal"
	"github.com/thesatellite-ai/lore/schema"
)

// The secret reference index (SECRETS_AND_REFERENCES 15):
//
//	every attached store × every built-in table ─► live rows
//	   text / text-list fields ─► refs.Parse ─► ${secret:…} alternates
//	   resources.fields        ─► bare secret_ref values (manager://…)
//	   vars (type secret_ref)  ─► the value
//	─► []SecretUse (one per alternate; unstored placeholders numbered per row)
//
// DECISION SEC-IDX1: the index is built on each call from the row files'
// cache (no secret_refs table yet; that table and its stored "last check"
// belong to the cache owner). So a plain listing has no STATE from an
// earlier check; --missing runs the check itself.

// SecretUse is one secret reference target where it sits. Never a value.
type SecretUse struct {
	// Ref is this target as text: manager://locator?options#fragment, or
	// "unstored" / "redacted".
	Ref      string `json:"ref"`
	Manager  string `json:"manager,omitempty"`
	Locator  string `json:"locator,omitempty"`
	Fragment string `json:"fragment,omitempty"`
	Holder   string `json:"holder,omitempty"`
	Label    string `json:"label,omitempty"`
	Row      string `json:"row"`
	Table    string `json:"table"`
	Store    string `json:"store"`
	Field    string `json:"field"`
	// At is the 1-based position of an unstored placeholder in its row
	// (lore secrets link --at); 0 for other targets.
	At int `json:"at,omitempty"`
	// Alt is the 0-based alternate index inside its ${secret:…|…}.
	Alt int `json:"alt,omitempty"`
	// State is present | missing | not_checked after a check; "" otherwise.
	State string `json:"state,omitempty"`
	// CheckedAt is the date of the stored check State came from (a plain
	// list, FA-AGENT-3); "" for a fresh check or none.
	CheckedAt string `json:"checked_at,omitempty"`
	// Kind says where the reference sits: text, resource, var.
	Kind string `json:"kind"`
}

// Use kinds.
const (
	useText     = "text"
	useResource = "resource"
	useVar      = "var"
)

// Verify results (section 6).
const (
	statePresent    = "present"
	stateMissing    = "missing"
	stateNotChecked = "not_checked"
)

// secretRow is one row with the references it holds.
type secretRow struct {
	located
	uses []SecretUse
}

// secretIndex builds the index over every attached store.
func (o ops) secretIndex(ctx context.Context) ([]secretRow, error) {
	var out []secretRow
	for _, t := range schema.Builtin() {
		rows, err := liveRows(ctx, o.env, o.env.Stores(), t.Name)
		if err != nil {
			return nil, err
		}
		for _, l := range rows {
			if uses := o.rowUses(l); len(uses) > 0 {
				out = append(out, secretRow{located: l, uses: uses})
			}
		}
	}
	return out, nil
}

// rowUses lists the references of one row, fields in name order.
func (o ops) rowUses(l located) []SecretUse {
	base := SecretUse{Row: l.Row.ID.String(), Table: l.Row.Table.String(), Store: string(l.Src)}
	var out []SecretUse
	switch l.Row.Table {
	case schema.TableResources:
		return resourceUses(l, base)
	case schema.TableVars:
		if text(l.Row, vType) == varTypeSecretRef {
			if u, ok := bareUse(base, vValue, display(fromJSON(l.Row.Fields[vValue]))); ok {
				u.Kind = useVar
				return []SecretUse{u}
			}
		}
	}
	unstored := 0
	for _, f := range slices.Sorted(maps.Keys(l.Row.Fields)) {
		for _, s := range fieldTexts(l.Row.Fields[f]) {
			for _, p := range refs.Parse(s) {
				if p.Kind != refs.PartRef || p.Ref.Scheme != refs.SchemeSecret || p.Ref.Problem != "" {
					continue
				}
				for i, t := range p.Ref.Alternates {
					u := useOf(base, f, t)
					u.Alt, u.Kind = i, useText
					if t.Fixed == refs.TargetUnstored {
						unstored++
						u.At = unstored
					}
					out = append(out, u)
				}
			}
		}
	}
	return out
}

// fieldTexts are the texts of a text, text-list or json field (a json
// field's string values; other kinds hold no references a write would keep).
func fieldTexts(v value.Value) []string {
	switch t := v.(type) {
	case value.Text:
		return []string{t.String()}
	case value.JSON:
		return jsonStrings(t.Raw()) // OV13b: secrets_json.go
	case value.List:
		if t.Elem() != value.KindText {
			// a json field holding a list of objects (time entries) reads
			// back from its row file as a list of json items: its string
			// leaves are the same through the JSON form
			if raw, ok := value.JSONForm(t); ok {
				return jsonStrings(raw)
			}
			return nil
		}
		var out []string
		for _, it := range t.Items() {
			out = append(out, it.String())
		}
		return out
	}
	return nil
}

// useOf turns one parsed alternate into a SecretUse.
func useOf(base SecretUse, field string, t refs.SecretTarget) SecretUse {
	u := base
	u.Field = field
	if t.Fixed != "" {
		u.Ref = t.Fixed
		return u
	}
	u.Manager, u.Locator, u.Fragment = t.Manager, t.Locator, t.Fragment
	for _, op := range t.Options {
		switch op.Name {
		case refs.OptHolder:
			u.Holder = op.Value
		case refs.OptLabel:
			u.Label = op.Value
		}
	}
	u.Ref = targetText(t)
	return u
}

// bareUse reads a bare secret_ref value (manager://locator) of a resource
// field or variable; ok=false when it is not one of a known manager.
func bareUse(base SecretUse, field, raw string) (SecretUse, bool) {
	if _, err := value.SecretRefOf(raw); err != nil {
		return SecretUse{}, false
	}
	parts := refs.Parse("${secret:" + raw + "}")
	if len(parts) != 1 || parts[0].Ref.Problem != "" || len(parts[0].Ref.Alternates) != 1 {
		return SecretUse{}, false
	}
	return useOf(base, field, parts[0].Ref.Alternates[0]), true
}

// resourceUses are the secret_ref values of a resource row (15: resource
// secret_ref fields are in the index).
func resourceUses(l located, base SecretUse) []SecretUse {
	stored, err := decodeValues(l)
	if err != nil {
		return nil
	}
	var out []SecretUse
	for _, id := range slices.Sorted(maps.Keys(stored.vals)) {
		var s string
		if json.Unmarshal(stored.vals[id], &s) != nil {
			continue
		}
		name := stored.names[id]
		if name == "" {
			name = id
		}
		if u, ok := bareUse(base, name, s); ok {
			u.Kind = useResource
			out = append(out, u)
		}
	}
	return out
}

// targetText writes one alternate back as URI text (5.1: reserved
// characters escaped); shared with the reveal path.
func targetText(t refs.SecretTarget) string { return reveal.TargetText(t) }

// holderMatch: q is a holder id (or its short prefix), a label, or a
// person handle (label "<handle>.<machine>" or "<handle>").
func holderMatch(u SecretUse, q string) bool {
	if q == "" {
		return true
	}
	return u.Holder == q || (len(q) >= len("mch_")+4 && strings.HasPrefix(u.Holder, q)) ||
		u.Label == q || strings.HasPrefix(u.Label, q+".")
}

// rewriter changes the matching alternates of every secret reference in a
// text; it returns the new text and how many alternates changed.
type rewriter func(t refs.SecretTarget) (refs.SecretTarget, bool)

// rewriteText rebuilds s with fix applied to each secret reference; the
// rest of the text (prose, escapes, other references) is kept byte for
// byte. n counts changed alternates.
func rewriteText(s string, fix rewriter) (string, int) {
	parts := refs.Parse(s)
	var b strings.Builder
	n := 0
	for _, p := range parts {
		if p.Kind != refs.PartRef || p.Ref.Scheme != refs.SchemeSecret || p.Ref.Problem != "" {
			b.WriteString(p.Raw)
			continue
		}
		alts := slices.Clone(p.Ref.Alternates)
		changed := false
		for i, t := range alts {
			if nt, ok := fix(t); ok {
				alts[i], changed = nt, true
				n++
			}
		}
		if !changed {
			b.WriteString(p.Raw)
			continue
		}
		texts := make([]string, len(alts))
		for i, t := range alts {
			texts[i] = targetText(t)
		}
		b.WriteString("${secret:" + strings.Join(texts, "|") + "}")
	}
	return b.String(), n
}

// setOption sets (or adds) option name on t; value "" removes it.
func setOption(t refs.SecretTarget, name, val string) refs.SecretTarget {
	opts := slices.Clone(t.Options)
	i := slices.IndexFunc(opts, func(o refs.Option) bool { return o.Name == name })
	switch {
	case i >= 0 && val == "":
		opts = slices.Delete(opts, i, i+1)
	case i >= 0:
		opts[i].Value = val
	case val != "":
		opts = append(opts, refs.Option{Name: name, Value: val})
	}
	t.Options = opts
	return t
}

// targetOption reads option name of t.
func targetOption(t refs.SecretTarget, name string) string {
	for _, o := range t.Options {
		if o.Name == name {
			return o.Value
		}
	}
	return ""
}

// rowPatch applies fix to every text, text-list and json field of r; nil
// when nothing changed.
func rowPatch(r row.Row, fix rewriter) (map[string]value.Value, int, error) {
	patch := map[string]value.Value{}
	total := 0
	for f, v := range r.Fields {
		switch t := v.(type) {
		case value.Text:
			ns, n := rewriteText(t.String(), fix)
			if n == 0 {
				continue
			}
			nv, err := txt(ns)
			if err != nil {
				return nil, 0, err
			}
			patch[f], total = nv, total+n
		case value.JSON:
			// OV13b: references inside json string values (secrets_json.go)
			nv, n, err := jsonRewrite(t, fix)
			if err != nil {
				return nil, 0, err
			}
			if n > 0 {
				patch[f], total = nv, total+n
			}
		case value.List:
			if t.Elem() != value.KindText {
				// a json list of objects read back from its row file: rewrite
				// it through its JSON form, as a json value
				raw, ok := value.JSONForm(t)
				if !ok {
					continue
				}
				j, jerr := value.JSONOf(raw)
				if jerr != nil {
					return nil, 0, errs.Wrap(errs.Internal, "secrets: field "+f+" as json", jerr)
				}
				nv, n, err := jsonRewrite(j, fix)
				if err != nil {
					return nil, 0, err
				}
				if n > 0 {
					patch[f], total = nv, total+n
				}
				continue
			}
			items, changed := []string{}, 0
			for _, it := range t.Items() {
				ns, n := rewriteText(it.String(), fix)
				items, changed = append(items, ns), changed+n
			}
			if changed == 0 {
				continue
			}
			nv, err := textList(items)
			if err != nil {
				return nil, 0, err
			}
			patch[f], total = nv, total+changed
		}
	}
	return patch, total, nil
}
