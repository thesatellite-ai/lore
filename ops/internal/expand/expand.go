package expand

import (
	"context"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Env is the env upgrade --expand needs (wire.App: the cache's resolver,
// the write scanner for the output (13.2) and the table definitions for
// the sensitive flag). A noun env without it answers E_UNSUPPORTED.
type Env interface {
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
	RefResolver(ctx context.Context) (refs.Resolver, error)
	OutputScanner(ctx context.Context) (scan.Scanner, error)
	TableSchema(ctx context.Context, table row.Table) (schema.TableDef, bool)
}

// Flag is the --expand flag, embedded in each show input.
type Flag struct {
	Expand bool `json:"expand,omitempty" flag:"expand" desc:"fill in ${var:…}, ${res:…}, ${row:…} and ${env:…} references; secrets stay masked (SECRETS 2.7, 12)"`
}

// Block is what --expand adds to a show output (SECRETS 12: "--json adds
// expanded and refs"): each text field it changed, filled in, and every
// reference met with its value or why it stayed as written.
type Block struct {
	Expanded map[string]string
	Refs     []refpoint.Used
}

// Unsupported is the refusal for an env that cannot resolve references.
func Unsupported() error {
	return errs.New(errs.Unsupported, "show --expand: this build's store wiring cannot resolve references")
}

// Show reads the row id names (a noun show already answered for it) and
// expands it; env is nil when the caller's env does not implement Env.
func Show(ctx context.Context, env Env, id string) (Block, error) {
	if env == nil {
		return Block{}, Unsupported()
	}
	aid, err := ids.ParseAny(strings.TrimSpace(id))
	if err != nil {
		return Block{}, errs.Wrap(errs.InvalidID, id+" is not a row id", err)
	}
	_, r, err := env.Get(ctx, engine.Ref{ID: aid})
	if err != nil {
		return Block{}, err
	}
	return Row(ctx, env, r)
}

// Row expands r's non-sensitive text fields at the show point. env is
// nil when the caller's env does not implement Env.
func Row(ctx context.Context, env Env, r row.Row) (Block, error) {
	var b Block
	if env == nil {
		return b, Unsupported()
	}
	rv, err := env.RefResolver(ctx)
	if err != nil {
		return b, err
	}
	sc, err := env.OutputScanner(ctx)
	if err != nil {
		return b, err
	}
	def, _ := env.TableSchema(ctx, r.Table)
	opt := refpoint.Options{Point: refpoint.Show, Resolver: rv, Scanner: sc, Env: os.LookupEnv}
	for _, k := range slices.Sorted(maps.Keys(r.Fields)) {
		t, isText := r.Fields[k].(value.Text)
		if f, ok := def.Field(k); !isText || (ok && f.Sensitive) {
			continue // RQ1: a sensitive value is hidden, never expanded
		}
		res, err := refpoint.Expand(opt, k, t.String())
		if err != nil {
			return b, err
		}
		for _, u := range res.Refs {
			if !slices.ContainsFunc(b.Refs, func(v refpoint.Used) bool { return v.Ref == u.Ref }) {
				b.Refs = append(b.Refs, u)
			}
		}
		if res.Text != t.String() {
			if b.Expanded == nil {
				b.Expanded = map[string]string{}
			}
			b.Expanded[k] = res.Text
		}
	}
	return b, nil
}

// Text is the human block a noun show appends under --expand: the
// changed fields filled in, then the references ("" when nothing).
func Text(expanded map[string]string, used []refpoint.Used) string {
	var b strings.Builder
	if len(expanded) > 0 {
		b.WriteString("expanded:\n")
		for _, k := range slices.Sorted(maps.Keys(expanded)) {
			b.WriteString("  " + k + ": " + expanded[k] + "\n")
		}
	}
	b.WriteString(RefsText(used))
	return b.String()
}

// RefsText is the "references:" block ("" when none).
func RefsText(used []refpoint.Used) string {
	if len(used) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("references:\n")
	for _, u := range used {
		b.WriteString("  " + u.Ref + "  " + RefLine(u) + "\n")
	}
	return b.String()
}

// RefLine is one reference's state as show --expand prints it.
func RefLine(u refpoint.Used) string {
	parts := []string{}
	if u.State == refpoint.StateExpanded {
		parts = append(parts, u.Value)
	} else {
		parts = append(parts, string(u.State))
	}
	if u.Source != "" {
		parts = append(parts, "("+u.Source+")")
	}
	if u.Note != "" {
		parts = append(parts, u.Note)
	}
	return strings.Join(parts, "  ")
}
