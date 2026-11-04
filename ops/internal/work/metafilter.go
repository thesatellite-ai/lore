package work

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
)

// MetaFlags are the shared --meta / --place / --sort list flags (METADATA
// 3.7); a list input embeds them.
type MetaFlags = metalist.Flags

// Narrow applies the metadata flags to listed rows: keeps the matching
// ones in --sort order and returns the note lines the list prints.
func Narrow(ctx context.Context, env Env, items []Sourced, f MetaFlags) ([]Sourced, []string, error) {
	if f.Empty() {
		return items, nil, nil
	}
	rows := make([]row.Row, len(items))
	for i, it := range items {
		rows[i] = it.Row
	}
	res, err := metalist.Select(ctx, env, f, rows)
	if err != nil {
		return nil, nil, err
	}
	out := make([]Sourced, 0, len(res.Order))
	for _, i := range res.Order {
		out = append(out, items[i])
	}
	return out, res.Notes, nil
}

// NotesText is the notes as lines after a list ("" for none).
func NotesText(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return strings.Join(notes, "\n") + "\n"
}
