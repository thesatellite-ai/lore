package knowledge

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
)

// MetaFlags are the shared --meta / --place / --sort list flags (METADATA
// 3.7); a list input embeds them.
type MetaFlags = metalist.Flags

// Narrow applies the metadata flags to listed items: keeps the matching
// ones in --sort order and returns the note lines (unknown key, skipped,
// not checked) the list prints after its rows.
func Narrow(ctx context.Context, env Env, items []Item, f MetaFlags) ([]Item, []string, error) {
	if f.Empty() {
		return items, f.TrashNotes(rowsOf(items)), nil
	}
	rows := make([]row.Row, len(items))
	for i, it := range items {
		rows[i] = it.Row
	}
	res, err := metalist.Select(ctx, env, f, rows)
	if err != nil {
		return nil, nil, err
	}
	out := make([]Item, 0, len(res.Order))
	for _, i := range res.Order {
		out = append(out, items[i])
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
func rowsOf(items []Item) []row.Row {
	out := make([]row.Row, len(items))
	for i, it := range items {
		out[i] = it.Row
	}
	return out
}
