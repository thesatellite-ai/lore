package trash

// `lore trash list|show|restore --from-history` (DELETION 2.7, 3.6):
//
//	list --from-history ─► HistoryReader.PurgedHistory (wire: git log of
//	    .lore/data in the repo checkout and each git store clone, D = old
//	    hard delete, M to a tombstone = purge; then the live refs, rows
//	    their store no longer holds) ─► filters (--type, --since, --search,
//	    --by, --to)
//	show <id> --from-history ─► the same rows ─► the last content, decoded
//	    (sensitive fields hidden unless --show-sensitive, 3.11)
//	restore <id> --from-history ─► E_PURGED, always (DQ4: a purged id never
//	    comes back; add what you need as a new row, which is scanned)

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/store"
)

// HistoryRow is one row whose content once existed in the place's git
// history and no longer does (3.6).
type HistoryRow struct {
	Store store.Source
	Table row.Table
	ID    string
	// PurgedAt / PurgedBy come from the tombstone (updated_at / updated_by);
	// PurgedBy is "" for a row an older lore hard-deleted (no tombstone).
	PurgedAt time.Time
	PurgedBy string
	// Commit is the commit that removed the content; the content is read
	// from its parent (Commit^), dated CommitAt.
	Commit   string
	CommitAt time.Time
	// Ref is the live ref the row was read from ("" = the checkout's
	// branch history; P3-DEL-HIST-1).
	Ref string
	// Doc is the row file as it was in Commit^ (in Commit itself when
	// Uncommitted).
	Doc []byte
	// Uncommitted: the purge or delete is in the working tree, not
	// committed yet; Commit is HEAD, which still holds the content.
	Uncommitted bool
}

// HistoryReader is the optional Env upgrade --from-history reads through
// (*wire.App has it). Rows come newest removal first.
type HistoryReader interface {
	PurgedHistory(ctx context.Context) ([]HistoryRow, error)
}

// unknownBy is the deleter of an old hard delete (no tombstone recorded one).
const unknownBy = "?"

// shortSHA is how much of a commit id the list prints.
const shortSHA = 7

// historyRows reads the purged rows, or E_UNSUPPORTED when env cannot.
func (o ops) historyRows(ctx context.Context) ([]HistoryRow, error) {
	h, ok := upgrade.As[HistoryReader](o.env)
	if !ok {
		return nil, errs.New(errs.Unsupported, "trash: --from-history needs a git checkout; this surface reads none")
	}
	return h.PurgedHistory(ctx)
}

// historyItem is one listed history row; r is its decoded last content.
func historyItem(h HistoryRow) (Item, row.Row, bool) {
	it := Item{ID: h.ID, Table: h.Table.String(), Store: string(h.Store), DeletedBy: h.PurgedBy, at: h.PurgedAt}
	if it.DeletedBy == "" {
		it.DeletedBy = unknownBy
	}
	if it.at.IsZero() {
		it.at = h.CommitAt
	}
	it.DeletedAt = it.at.UTC().Format(jsonTimeLayout)
	sha := h.Commit
	if len(sha) > shortSHA {
		sha = sha[:shortSHA]
	}
	it.LastIn = sha + "^ (" + h.CommitAt.Local().Format(dateLayout) + ")"
	if h.Uncommitted {
		it.LastIn = sha + " (" + h.CommitAt.Local().Format(dateLayout) + "; its removal is not committed yet)"
	}
	if h.Ref != "" {
		it.LastIn = sha + "^ on " + h.Ref + " (" + h.CommitAt.Local().Format(dateLayout) + ")"
	}
	r, err := row.Decode(h.Doc)
	if err != nil {
		it.Title = "(content not readable by this lore)"
		return it, row.Row{}, false
	}
	it.Title = titleOf(r)
	if h.PurgedBy == "" && it.Title == "" {
		it.Title = "(hard-deleted by an older lore)"
	}
	return it, r, true
}

// listHistory is `lore trash list --from-history`.
func (o ops) listHistory(ctx context.Context, in ListIn, since time.Time, by string) (ListOut, error) {
	hs, err := o.historyRows(ctx)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{Data: []Item{}, History: true}
	for _, h := range hs {
		it, _, _ := historyItem(h)
		switch {
		case in.To != "" && it.Store != strings.TrimSpace(in.To):
		case in.Type != "" && !typeMatches(it.Table, in.Type):
		case by != "" && it.DeletedBy != by:
		case in.Search != "" && !matches(it, in.Search):
		case !since.IsZero() && it.at.Before(since):
		default:
			out.Data = append(out.Data, it)
		}
	}
	slices.SortStableFunc(out.Data, func(a, b Item) int { return b.at.Compare(a.at) })
	out.Total = len(out.Data)
	if out.Data, out.From, err = page(out.Data, in.Limit, in.Page); err != nil {
		return ListOut{}, err
	}
	out.Count = len(out.Data)
	o.labelDeleters(ctx, out.Data) // W-7: the purger's name, not the act_ id
	return out, nil
}

// showHistory is `lore trash show <id> --from-history`.
func (o ops) showHistory(ctx context.Context, in ShowIn) (ShowOut, error) {
	hs, err := o.historyRows(ctx)
	if err != nil {
		return ShowOut{}, err
	}
	id := strings.TrimSpace(in.ID)
	for _, h := range hs {
		if h.ID != id {
			continue
		}
		it, r, ok := historyItem(h)
		one := []Item{it}
		o.labelDeleters(ctx, one)
		out := ShowOut{Data: one[0], Fields: map[string]string{}, History: true}
		if !ok {
			return out, nil
		}
		out.Fields = o.shownFields(ctx, r, in.ShowSensitive) // 3.11
		return out, nil
	}
	return ShowOut{}, errs.New(errs.NotFound, "trash: "+strconv.Quote(id)+" is not in this place's git history as a purged or deleted row").
		WithHint("lore trash list --from-history")
}

// purgedForever is restore --from-history: always E_PURGED (DQ4).
func purgedForever(id string) error {
	id = strings.TrimSpace(id)
	return errs.New(errs.Purged, "trash: "+id+" was purged; purged rows never come back (DQ4)").
		WithHint("read it: lore trash show --id " + id + " --from-history; then add what you still need as a new row")
}
