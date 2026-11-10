package trash

// local_purged.go — `lore trash purge --local --purged [--dry-run |
// --confirm]` (DELETION 3.10, OV31, P2-OLDSYNC-1): a v0.1.17 safety copy
// (lore.db `_lore_sync_trash`) of a row purged on ANOTHER machine still
// holds the purged content here. This drops those copies, reading only the
// repo's own `.lore/data/_purged.json`. (A purge run on this machine drops
// its own copies in the same step: LocalCopyDropper in local.go.)
//
//	--local without --purged, or the reverse ─► E_INVALID_INPUT
//	with a target / --older-than / --expired / --archived-before ─► E_INVALID_INPUT
//	LocalCopies.LocalTrash ∩ RepoPurged.RepoPurged ─► the copies
//	none ─► empty result (nothing to confirm)
//	--dry-run ─► listed, nothing dropped
//	no --confirm ─► E_NEEDS_CONFIRM naming --confirm (DQ2, never --yes)
//	--confirm ─► LocalCopyDropper.DropLocalCopies(row ids)
//
// Only ids the repo lists as purged are touched: a copy of a row that is
// merely deleted stays (it may still be restored from git).

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// RepoPurged is the optional Env upgrade `purge --local --purged` and the
// doctor line use: the ids the repo's own `.lore/data/_purged.json` lists
// (empty when the file is absent). A file that cannot be read is an error,
// never an empty set, so no copy is reported clean by mistake.
type RepoPurged interface {
	RepoPurged(ctx context.Context) (map[string]bool, error)
}

// Actions of a --local --purged result.
const (
	actionDropped   = "dropped"
	actionWouldDrop = "would drop"
)

// localPurgedNext is the command that drops the copies for good.
var localPurgedNext = []string{"trash", "purge", "--local", "--purged", "--confirm"}

// purgedCopies returns this machine's safety copies of rows the repo lists
// as purged, newest first (LocalTrash's order).
func (o ops) purgedCopies(ctx context.Context) ([]LocalCopy, error) {
	rp, ok := upgrade.As[RepoPurged](o.env)
	if !ok {
		return nil, errs.New(errs.Unsupported, "trash purge --local --purged: the repo's purged ids are not available in this binary")
	}
	copies, err := o.localCopies(ctx)
	if err != nil {
		return nil, err
	}
	if len(copies) == 0 {
		return nil, nil
	}
	set, err := rp.RepoPurged(ctx)
	if err != nil {
		return nil, err
	}
	var out []LocalCopy
	for _, c := range copies {
		if set[c.RowID] {
			out = append(out, c)
		}
	}
	return out, nil
}

// purgeLocalPurged is `trash purge --local --purged`.
func (o ops) purgeLocalPurged(ctx context.Context, in PurgeIn) (ResultOut, error) {
	if !in.Local || !in.Purged {
		return ResultOut{}, errs.New(errs.InvalidInput, "trash purge: --local and --purged go together (lore trash purge --local --purged --confirm)")
	}
	if strings.TrimSpace(in.Target) != "" || in.OlderThan != "" || in.Expired || strings.TrimSpace(in.ArchivedBefore) != "" {
		return ResultOut{}, errs.New(errs.InvalidInput, "trash purge: --local --purged drops this machine's copies of purged rows; it takes no <id|batch>, --older-than, --expired or --archived-before")
	}
	found, err := o.purgedCopies(ctx)
	if err != nil {
		return ResultOut{}, err
	}
	out := ResultOut{Action: actionDropped, Rows: []Item{}, Local: true, Copies: found, Count: len(found), DryRun: in.DryRun}
	if out.Copies == nil {
		out.Copies = []LocalCopy{}
	}
	if len(found) == 0 {
		return out, nil
	}
	if in.DryRun {
		out.Action = actionWouldDrop
		return out, nil
	}
	if !in.Confirm {
		return ResultOut{}, needsConfirm("drop "+strconv.Itoa(len(found))+" local cop(ies) of purged rows on this machine for good", localPurgedNext...)
	}
	d, ok := upgrade.As[LocalCopyDropper](o.env)
	if !ok {
		return ResultOut{}, errs.New(errs.Unsupported, "trash purge --local --purged: this binary cannot drop this machine's safety copies")
	}
	rowIDs := make([]string, 0, len(found))
	for _, c := range found {
		if !slices.Contains(rowIDs, c.RowID) {
			rowIDs = append(rowIDs, c.RowID)
		}
	}
	if _, err = d.DropLocalCopies(ctx, rowIDs); err != nil {
		return ResultOut{}, errs.Wrap(errs.Internal, "trash purge --local --purged: the local copies could not be dropped", err).WithHint(localTrashHint)
	}
	return out, nil
}

// localPurgedText is the human form of a --local --purged result.
func (o ResultOut) localPurgedText() string {
	if len(o.Copies) == 0 {
		return "· no local copies of purged rows\n"
	}
	var b strings.Builder
	for _, c := range o.Copies {
		b.WriteString("#" + strconv.FormatInt(c.N, 10) + " " + c.Table + "/" + c.RowID + "  " + c.At + "\n")
	}
	b.WriteString(o.Action + " " + strconv.Itoa(len(o.Copies)) + " local cop(ies) of purged rows (this machine only)\n")
	if o.DryRun {
		b.WriteString("dry run: nothing written\n")
	}
	return b.String()
}

// localPurgedWarning is the doctor line for copies of purged rows ("" when
// there are none, or the Env has no copies or no purged list). An
// unreadable _purged.json warns too (FB-DOC-3).
func localPurgedWarning(ctx context.Context, env Env) string {
	o := ops{env: env}
	if _, ok := upgrade.As[RepoPurged](env); !ok {
		return ""
	}
	if _, ok := upgrade.As[LocalCopies](env); !ok {
		return ""
	}
	found, err := o.purgedCopies(ctx)
	if err != nil {
		return "trash: this machine's copies of purged rows could not be checked: " + err.Error()
	}
	if len(found) == 0 {
		return ""
	}
	return "trash: " + strconv.Itoa(len(found)) + " local cop(ies) of purged rows on this machine; drop them with lore trash purge --local --purged --confirm"
}
