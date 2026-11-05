package trash

// local.go — `lore trash list --local` and `lore trash restore --local <n>`
// (CLI_REFERENCE 3.9, DELETION 501-502, DQ1): this machine's v0.1.17
// safety copies (lore.db `_lore_sync_trash`), the replacement of `lore
// sync trash`. The copies are machine-local and never shared; the core
// never makes new ones (sync merges row files, it never drops a row from a
// local database), so these are the copies v0.1.17 left behind.
//
//	list --local    ─► LocalCopies.LocalTrash ─► newest first (P3-TRASHLOCAL-1)
//	restore --local ─► LocalCopies.LocalTrash ─► the copy #n ─► refused:
//	                   every table is owned by the row files (git restores it)
//
// The copy's document is never printed: it may hold a value the row
// marks sensitive, and the restore hint points at git, which has it.

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// LocalCopy is one of this machine's safety copies (one `_lore_sync_trash`
// row, without its document).
type LocalCopy struct {
	// N is the copy's number (`lore trash restore --local <n>`).
	N      int64  `json:"id"`
	Table  string `json:"table"`
	RowID  string `json:"row_id"`
	Reason string `json:"reason"`
	At     string `json:"at"`
}

// LocalCopies is the optional Env upgrade --local uses: this machine's
// safety copies, newest first, empty when there are none (no lore.db, or
// no `_lore_sync_trash` table). An Env without it answers --local with
// E_UNSUPPORTED.
type LocalCopies interface {
	LocalTrash(ctx context.Context) ([]LocalCopy, error)
}

// localTrashHint points a --local reader at the listing.
const localTrashHint = "lore trash list --local"

// localCopies returns the Env's safety copies, or E_UNSUPPORTED.
func (o ops) localCopies(ctx context.Context) ([]LocalCopy, error) {
	lc, ok := o.env.(LocalCopies)
	if !ok {
		return nil, errs.New(errs.Unsupported, "trash --local: this machine's safety copies are not available in this binary")
	}
	return lc.LocalTrash(ctx)
}

// listLocal is `lore trash list --local`. The row-trash filters do not
// apply to the copies, so combining them is refused rather than ignored.
func (o ops) listLocal(ctx context.Context, in ListIn) (ListOut, error) {
	if in.FromHistory || in.Overruled {
		return ListOut{}, errs.New(errs.InvalidInput, "trash list: --local lists this machine's safety copies; it does not combine with --from-history or --overruled")
	}
	copies, err := o.localCopies(ctx)
	if err != nil {
		return ListOut{}, err
	}
	if copies == nil {
		copies = []LocalCopy{}
	}
	return ListOut{Data: []Item{}, Local: true, Copies: copies, Count: len(copies), Total: len(copies)}, nil
}

// restoreLocal is `lore trash restore --local <n>`. In 0.1.18 every synced
// table is owned by the row files (lore.db is only their mirror), so
// writing the copy back would be reported as done and never reach a file
// (v0.1.17 lsync.ErrCoreOwned); the refusal names the way that works.
func (o ops) restoreLocal(ctx context.Context, in TargetIn) (ResultOut, error) {
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(in.Target), "#"), 10, 64)
	if err != nil {
		return ResultOut{}, errs.New(errs.InvalidInput, "trash restore --local: "+strconv.Quote(in.Target)+" is not a copy number").WithHint(localTrashHint)
	}
	copies, err := o.localCopies(ctx)
	if err != nil {
		return ResultOut{}, err
	}
	for _, c := range copies {
		if c.N != n {
			continue
		}
		p := ".lore/data/" + c.Table + "/" + c.RowID + ".json"
		return ResultOut{}, errs.New(errs.InvalidInput, "trash restore --local: a restore of "+c.Table+" is not done in lore.db (the row files are the truth)").
			WithHint("restore the file from git (`git checkout HEAD -- " + p + "`, or from the commit before the one that deleted it: `git log --diff-filter=D -- " + p + "`)")
	}
	return ResultOut{}, errs.New(errs.NotFound, "trash restore --local: no local copy #"+strconv.FormatInt(n, 10)+" on this machine").WithHint(localTrashHint)
}

// localText is the human form of a --local listing (v0.1.17's `lore sync
// trash` lines).
func (o ListOut) localText() string {
	if len(o.Copies) == 0 {
		return "· trash is empty\n"
	}
	var b strings.Builder
	for _, c := range o.Copies {
		b.WriteString("#" + strconv.FormatInt(c.N, 10) + " " + c.Table + "/" + c.RowID + "  " + c.At + "  (" + c.Reason + ")\n")
	}
	return b.String()
}
