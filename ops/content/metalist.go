package content

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
)

// MetaFlags are the shared --meta / --place / --sort / --archived list
// flags (METADATA 3.7, 3.19; CLI_REFERENCE 2.2); every content ListIn
// embeds them, so a filter means the same on every noun.
type MetaFlags = metalist.Flags

// Narrow applies the metadata flags to listed rows: keeps the matching
// ones in --sort order and returns the note lines the list prints after
// its rows. Archived rows are left out unless --archived (OV30-2); with
// no other flag nothing is read.
func (o Ops) Narrow(ctx context.Context, rows []Listed, f MetaFlags) ([]Listed, []string, error) {
	plain := make([]row.Row, len(rows))
	for i, l := range rows {
		plain[i] = l.Row
	}
	res, err := metalist.Select(ctx, o.Env, f, plain)
	if err != nil {
		return nil, nil, err
	}
	out := make([]Listed, 0, len(res.Order))
	for _, i := range res.Order {
		out = append(out, rows[i])
	}
	return out, append(res.Notes, f.TrashNotes(rowsOf(out))...), nil
}

// NotesText is the notes as lines after a list ("" for none).
func NotesText(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return strings.Join(notes, "\n") + "\n"
}

// rowsOf is the rows of listed items (for the trash note).
func rowsOf(items []Listed) []row.Row {
	out := make([]row.Row, len(items))
	for i, it := range items {
		out[i] = it.Row
	}
	return out
}
