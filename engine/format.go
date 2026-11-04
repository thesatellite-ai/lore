package engine

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/formatgate"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// The format gate on every engine write (FORMAT_EVOLUTION rule 2):
//
//	put / remove / purge ─► gate(st, rows) ─► FormatReader.Format (the folder's _meta.json)
//	                                      ─► formatgate.Check(binary) ─► E_OLD_LORE, nothing written
//	                                      ─► Verdict.LockedTable per row (plugin features, FQ4)
//
// The backend raises the declaration itself in the same batch as the rows
// (rowfs.Folder.Apply → declare), so the engine only refuses. Reads never
// pass here: a project this binary cannot write stays readable.

// FormatReader is the optional backend capability that reads the store
// folder's format declaration. A backend without it declares nothing (a
// test fake, a future server backend that gates on its side).
type FormatReader interface {
	Format(ctx context.Context) (formatgate.Declared, error)
}

// gate refuses a write into st when this binary may not write the folder.
// An unreadable declaration is already an E_OLD_LORE error (fail closed).
func (e *Engine) gate(ctx context.Context, st Attached, rows ...row.Row) error {
	fr, ok := formatReaderOf(st.Backend)
	if !ok {
		return nil
	}
	d, err := fr.Format(ctx)
	if err != nil {
		return err
	}
	v := formatgate.Check(e.binaryVersion(), d)
	if err := v.Err(); err != nil {
		return err
	}
	for _, r := range rows {
		// FQ4: plugins own no tables yet, so ownerOf is nil (the stub);
		// the call site is where a plugin registry plugs in.
		if v.LockedTable(r.Table.String(), nil) {
			return errs.New(errs.OldLore, "engine: table "+r.Table.String()+" belongs to a plugin this lore does not have; reading works, writing is off").
				WithHint("install the plugin, or lore upgrade")
		}
	}
	return nil
}

// unwrapper is a backend wrapping another (wire.Unwrapper's shape: the
// CLI wraps every backend, so the capability sits on the inner one).
type unwrapper interface{ Unwrap() store.Backend }

// formatReaderOf finds the FormatReader under any wrapper layers.
func formatReaderOf(b store.Backend) (FormatReader, bool) {
	for b != nil {
		if fr, ok := b.(FormatReader); ok {
			return fr, true
		}
		u, ok := b.(unwrapper)
		if !ok {
			return nil, false
		}
		b = u.Unwrap()
	}
	return nil, false
}

// binaryVersion is this lore's version (the `_w` stamp's), "" when the
// composition root gave none (tests): Check then compares features only.
func (e *Engine) binaryVersion() string {
	if e.prov == nil {
		return ""
	}
	v, _ := e.prov.Writer()
	return v
}
