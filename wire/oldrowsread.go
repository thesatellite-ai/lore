package wire

// oldrowsread.go — decision D7 (B2-20): in a repo whose rows lore 0.1.17
// wrote and that is not migrated yet, only `lore fix` / `lore migrate
// run` (and a person's first write, through its documented auto-fix)
// change tracked files. `lore sync` there is a read: it says "run lore
// fix" once and changes nothing tracked (no repo or actor row, no
// LORE.md, no .gitignore, no _meta.json), so a teammate who cloned before
// the admin's `lore fix` commit pulls it cleanly. The host's Runner marks
// such a run (WithOldRowsRead); its hooks then run as for a read, and the
// sync itself does nothing in the repo.

import (
	"context"
	"errors"

	"github.com/thesatellite-ai/lore/model/errs"
)

// oldRowsReadKey marks a run that must change no tracked file because the
// repo still holds lore 0.1.17 rows (D7).
type oldRowsReadKey struct{}

// WithOldRowsRead returns ctx for a `lore sync` (or another maintenance
// run) in a repo with lore 0.1.17 rows: it runs as a read (WithNoCreate).
func WithOldRowsRead(ctx context.Context) context.Context {
	return context.WithValue(WithNoCreate(ctx), oldRowsReadKey{}, true)
}

// OldRowsRead reports a run marked by WithOldRowsRead.
func OldRowsRead(ctx context.Context) bool {
	v, _ := ctx.Value(oldRowsReadKey{}).(bool)
	return v
}

// HasV017RowsAt reports whether the git repo dir is in still holds rows
// lore 0.1.17 wrote and was never migrated (hasV017Rows).
func HasV017RowsAt(ctx context.Context, dir string) bool {
	top := gitTopLevel(ctx, dir)
	return top != "" && ProjectFormatOld(top)
}

// OldRowsSyncLine is what `lore sync` says in such a repo instead of
// syncing it.
const OldRowsSyncLine = "nothing synced in this repo: it is on the lore 0.1.17 format and this lore writes nothing there until lore migrate run (a teammate's migration commit then pulls cleanly)"

// metaUnreadable is the refusal text of a project whose _meta.json cannot
// be read ("" when it can): the same words a write there refuses with
// (formatgate.Unreadable, E_OLD_LORE), then its hint. ProjectFormatOld
// counts such a project as old to fail closed, but its lines must not
// claim the 0.1.17 format: lore cannot tell, and lore migrate run would be
// a guess.
func metaUnreadable(top string) string {
	if top == "" {
		return ""
	}
	_, err := ProjectFormatAt(top)
	if err == nil {
		return ""
	}
	var e *errs.Error
	if !errors.As(err, &e) {
		return err.Error()
	}
	if e.Hint != "" {
		return e.Message + "; " + e.Hint
	}
	return e.Message
}

// OldRowsReadLineAt is the read notice of the project at top: the
// unreadable _meta.json's own line, else OldRowsReadLine.
func OldRowsReadLineAt(top string) string {
	if m := metaUnreadable(top); m != "" {
		return "lore: " + m
	}
	return OldRowsReadLine
}

// OldRowsSyncLineAt is what `lore sync` says in the repo dir is in
// instead of syncing it: the unreadable _meta.json's own line, else
// OldRowsSyncLine.
func OldRowsSyncLineAt(ctx context.Context, dir string) string {
	if m := metaUnreadable(gitTopLevel(ctx, dir)); m != "" {
		return "nothing synced in this repo: " + m
	}
	return OldRowsSyncLine
}
