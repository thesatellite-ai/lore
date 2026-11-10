// Package metashow is the metadata block of a noun's show (METADATA 3.17
// "Values on every read", decision MQ13: every row an agent reads carries
// its metadata as name: value): the row's entries by name, sensitive
// values masked (3.20, one masker: the entry's label or the key row's
// flag), and the text block printed under the show.
//
// Flow:
//
//	<noun> show ─ Of(env, id) ─ Get row ─ per meta entry: DecodeEntry
//	                                     └ key row sensitive? ─ masked
//	             └ Block{Meta, MetaMasked} inline in --json; Text() "meta:" lines
package metashow

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Env is the part of an ops Env the block needs.
type Env interface {
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
}

// Block is a row's metadata block. A show output copies it into its own
// Meta / MetaMasked fields (json meta / meta_masked, omitempty; the op
// registry needs every output field tagged), so a v0.1.17 envelope of a
// row without metadata is unchanged.
type Block struct {
	// Meta is the row's metadata, name → display value (meta.Show:
	// allowed values by name, sensitive values masked).
	Meta map[string]string `json:"meta,omitempty"`
	// MetaMasked names the keys printed masked (3.20 meta_masked).
	MetaMasked []string `json:"meta_masked,omitempty"`
}

// fieldSensitive is the meta_keys flag that marks a key sensitive.
const fieldSensitive = "sensitive"

// maskedNote follows a masked value in the text block (METADATA 3.17).
const maskedNote = "   (sensitive)"

// Of reads the row id (any store) and returns its metadata block. A row
// that cannot be read yields an empty block: the show itself already read
// it, and the block must never fail a show.
func Of(ctx context.Context, env Env, id string) Block {
	rid, err := ids.ParseAny(strings.TrimSpace(id))
	if err != nil || env == nil {
		return Block{}
	}
	_, r, err := env.Get(ctx, engine.Ref{ID: rid})
	if err != nil {
		return Block{}
	}
	return Row(ctx, env, r)
}

// Row is Of for a row already read.
func Row(ctx context.Context, env Env, r row.Row) Block {
	var b Block
	labels := map[string]string{}
	for kid, j := range r.Meta {
		en, err := meta.DecodeEntry(j)
		if err != nil || en.Name == "" {
			continue // a quarantined entry is shown by lore meta lint, not here
		}
		if !en.Sensitive && keySensitive(ctx, env, kid) {
			en.Sensitive = true // 3.20: an entry written before the key turned sensitive carries no label
		}
		if en.Sensitive {
			b.MetaMasked = append(b.MetaMasked, en.Name)
		}
		if b.Meta == nil {
			b.Meta = map[string]string{}
		}
		b.Meta[en.Name] = meta.Show(en)
		if l := Label(ctx, env, en); l != "" {
			labels[en.Name] = l
		}
	}
	AddLabels(b.Meta, labels)
	slices.Sort(b.MetaMasked)
	return b
}

// Label is the display label of entry en's allowed value (a list: labels
// joined " | ", an item without one by its name); "" when en is no
// allowed value, is sensitive, or no value of it has a label (METADATA
// 3.17, G-META-10).
func Label(ctx context.Context, env Env, en meta.Entry) string {
	if en.Sensitive || len(en.VName) == 0 || env == nil {
		return ""
	}
	return valueLabel(ctx, env, en.V)
}

// AddLabels adds "<name>_label" to values for each labelled name (the
// --json form of METADATA 3.17); a real key of that name already in
// values wins (its value stays, no label is added for that key).
func AddLabels(values, labels map[string]string) {
	for n, l := range labels {
		if _, real := values[n+LabelSuffix]; !real {
			values[n+LabelSuffix] = l
		}
	}
}

// LabelSuffix marks the display label of an allowed value in a Block's
// Meta: "<name>_label" beside "<name>" (METADATA 3.17 --json form).
const LabelSuffix = "_label"

// fieldLabel is the meta_values field that holds a value's display label.
const fieldLabel = "label"

// valueLabel is the display label of a stored allowed value: the value
// id(s) in v (one val_ id, or a list of them) → their meta_values labels,
// a list joined as meta.Show joins one; "" when no value has a label.
func valueLabel(ctx context.Context, env Env, v []byte) string {
	var vids []string
	if err := json.Unmarshal(v, &vids); err != nil {
		var one string
		if json.Unmarshal(v, &one) != nil {
			return ""
		}
		vids = []string{one}
	}
	parts := make([]string, 0, len(vids))
	hasLabel := false
	for _, s := range vids {
		id, err := ids.ParseAny(s)
		if err != nil {
			return ""
		}
		_, r, err := env.Get(ctx, engine.Ref{Table: schema.TableMetaValues, ID: id})
		if err != nil {
			return ""
		}
		l := ""
		if t, ok := r.Fields[fieldLabel].(value.Text); ok {
			l = t.String()
		}
		if l == "" {
			l = fieldName(r)
		} else {
			hasLabel = true
		}
		parts = append(parts, l)
	}
	if !hasLabel {
		return ""
	}
	return strings.Join(parts, " | ")
}

// fieldName is a meta_values row's name ("" when absent).
func fieldName(r row.Row) string {
	if t, ok := r.Fields["name"].(value.Text); ok {
		return t.String()
	}
	return ""
}

// keySensitive reports the key row's sensitive flag (false when unread).
func keySensitive(ctx context.Context, env Env, kid ids.ID[ids.MetaKey]) bool {
	_, k, err := env.Get(ctx, engine.Ref{Table: schema.TableMetaKeys, ID: kid.Any()})
	if err != nil {
		return false
	}
	v, ok := k.Fields[fieldSensitive].(value.Bool)
	return ok && v.Bool()
}

// Display is the text form of a Block's Meta: an allowed value shows its
// label (METADATA 3.17) and its "<name>_label" entry, the json form only,
// gets no line of its own; masked names are left as they are.
func Display(values map[string]string, masked []string) map[string]string {
	shown := make(map[string]string, len(values))
	for n, v := range values {
		if base, isLabel := strings.CutSuffix(n, LabelSuffix); isLabel {
			if _, has := values[base]; has && !slices.Contains(masked, base) {
				continue
			}
		}
		shown[n] = v
		if l, has := values[n+LabelSuffix]; has && !slices.Contains(masked, n) {
			shown[n] = l
		}
	}
	return shown
}

// Text is the block under a show: "meta:" then one "  name: value" line
// per key, sorted by name, a masked value marked (sensitive). "" when the
// row has no metadata.
func Text(values map[string]string, masked []string) string {
	if len(values) == 0 {
		return ""
	}
	values = Display(values, masked)
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	slices.Sort(names)
	var sb strings.Builder
	sb.WriteString("meta:\n")
	for _, n := range names {
		sb.WriteString("  " + n + ": " + values[n])
		if slices.Contains(masked, n) {
			sb.WriteString(maskedNote)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
