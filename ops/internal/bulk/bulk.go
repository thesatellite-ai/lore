// Package bulk is the shared mechanism of every bulk action (a --where
// delete, an import): the batch it writes under and the preview that stops
// it above the policy's row count.
//
// Why one package: XQ1 gives every bulk action of every noun one batch id
// prefix (bat_) and a kind, and XQ2 one preview key (batch.preview_rows,
// default 20). Each noun calling these two helpers is what keeps the rule
// one rule; a noun that counted or asked on its own would drift.
//
// Invariant: nothing here writes a row. Begin only marks a context, the
// engine stamps the batch on every audit event written with it.
package bulk

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/store"
)

// Kind is what a batch did; `lore batch show` reports it and `lore batch
// list --kind` filters by it (XQ1: the kind is a field, not a prefix).
type Kind string

// The closed set of batch kinds (CLI_REFERENCE 3.10 `--kind`).
const (
	KindTask     Kind = "task"
	KindMeta     Kind = "meta"
	KindDelete   Kind = "delete"
	KindResource Kind = "resource"
	KindImport   Kind = "import"
	KindSecrets  Kind = "secrets"
)

// Kinds lists every kind in CLI_REFERENCE order.
func Kinds() []Kind {
	return []Kind{KindTask, KindMeta, KindDelete, KindResource, KindImport, KindSecrets}
}

// Valid reports whether k is one of Kinds.
func (k Kind) Valid() bool {
	for _, x := range Kinds() {
		if x == k {
			return true
		}
	}
	return false
}

// DefaultPreviewRows is batch.preview_rows when no policy level sets it and
// the Env offers no Policier (XQ2's default; logic/policy holds the same
// number as the key's default).
const DefaultPreviewRows = 20

// Policier is the optional Env upgrade that reads the effective policy of
// a target store (wire.App has it). Without it the preview count is
// DefaultPreviewRows.
type Policier interface {
	Policy(ctx context.Context, target store.Source) (policy.Set, error)
}

// PreviewRows returns the effective batch.preview_rows for target.
func PreviewRows[E any](ctx context.Context, env E, target store.Source) (int, error) {
	p, ok := upgrade.As[Policier](env)
	if !ok {
		return DefaultPreviewRows, nil
	}
	set, err := p.Policy(ctx, target)
	if err != nil {
		return 0, err
	}
	n, isSet := set.Limit(policy.KeyBatchPreviewRows)
	if !isSet {
		return DefaultPreviewRows, nil
	}
	return int(n), nil
}

// Item is one row a bulk action will change, as the preview lists it.
type Item struct {
	ID    string
	Label string // a name or title; may be empty
}

// String renders the item as "id (label)".
func (i Item) String() string {
	if i.Label == "" {
		return i.ID
	}
	return i.ID + " (" + i.Label + ")"
}

// Check is the XQ2 decision, pure: a bulk action of len(items) rows above
// limit, not yet confirmed, is E_NEEDS_CONFIRM whose message lists the
// first limit rows and how many more, so the person (or the agent's
// person) sees what would change before saying yes. At or below the limit,
// or confirmed, it passes. what names the action ("resource delete").
func Check(what string, items []Item, limit int, confirmed bool) error {
	if confirmed || len(items) <= limit {
		return nil
	}
	shown := items
	if limit >= 0 && limit < len(shown) {
		shown = shown[:limit]
	}
	parts := make([]string, len(shown))
	for i, it := range shown {
		parts[i] = it.String()
	}
	msg := what + " would change " + strconv.Itoa(len(items)) + " rows, over batch.preview_rows " + strconv.Itoa(limit) +
		": " + strings.Join(parts, ", ")
	if more := len(items) - len(shown); more > 0 {
		msg += " and " + strconv.Itoa(more) + " more"
	}
	return errs.New(errs.NeedsConfirm, msg).WithHint("check the rows, then re-run with --yes")
}

// Preview applies Check with the effective preview count for target and
// whether the running operation's caller already said yes.
func Preview[E any](ctx context.Context, env E, target store.Source, what string, items []Item) error {
	limit, err := PreviewRows(ctx, env, target)
	if err != nil {
		return err
	}
	return Check(what, items, limit, engine.Confirmed(ctx))
}

// Begin starts one batch of kind: a fresh bat_ id and a context that makes
// the engine stamp it on the audit event of every row written with it
// (XQ1). The id is returned so the action can print it.
func Begin(ctx context.Context, kind Kind) (context.Context, ids.ID[ids.Batch], error) {
	if !kind.Valid() {
		return ctx, ids.ID[ids.Batch]{}, errs.New(errs.Internal, "bulk: unknown batch kind "+string(kind))
	}
	id, err := ids.New[ids.Batch]()
	if err != nil {
		return ctx, ids.ID[ids.Batch]{}, err
	}
	return engine.WithBatch(ctx, engine.Batch{ID: id, Kind: string(kind)}), id, nil
}

// Ask is the confirmation of an operation that both asks always for one
// row and previews for many (`resource delete <name>` and `resource delete
// --kind --where`): its spec can only say MayAsk (an up-front Always would
// ask before the bulk form could count and preview, XQ2), so the one-row
// form asks here: E_NEEDS_CONFIRM with question unless the caller already
// said yes. The CLI prompts a terminal on it and re-runs with yes.
func Ask(ctx context.Context, question string) error {
	if engine.Confirmed(ctx) {
		return nil
	}
	return errs.New(errs.NeedsConfirm, question).WithHint("ask the person, then rerun with --yes")
}
