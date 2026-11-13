package engine

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// purge flags of a delete (op.PurgeFlags), bound by Run for every surface
// (P3-DEL-1 ONE-PATH follow-up: CLI, `lore api` and cmd/lore alike; before
// this, only the old binary's CLI wrapper knew --purge):
//
//	decode ─► bindInputPurge ─┬─ purge, no confirm ─► E_NEEDS_CONFIRM (nothing runs)
//	                          ├─ confirm           ─► caller.Yes (DQ8: answers the delete's question)
//	                          └─ PurgeRequest on ctx ─► softDelete purges, fills Purged
//	invoke ─► purgedText edit on the result's human line

// purgeHint is the hint of every refused --purge (DQ2: only the literal
// --confirm purges, --yes never does).
const purgeHint = "ask the person, then rerun with --confirm (--yes does not purge)"

// trashLineMark is the phrase of every noun delete's success line
// (ops/internal/trashline.Moved / MovedBatch).
const trashLineMark = "moved to the trash"

// softDeleteNote is DQ8's hint after a plain delete given v0.1.17's
// --confirm: the delete is soft, the purge is a separate command.
const softDeleteNote = "  note: a delete is soft now (restorable); to remove content for good: lore trash purge --id <id> --confirm\n"

// bindInputPurge is Run's step for an input embedding op.PurgeFlags (any
// other input passes through unchanged, pr nil). It refuses --purge
// without --confirm up front, turns --confirm into the caller's yes, and
// puts the PurgeRequest on ctx (reusing one a surface already attached,
// so that surface still sees Purged).
func bindInputPurge(ctx context.Context, in op.Input, c *op.Caller) (context.Context, *PurgeRequest, error) {
	f, ok := in.Purge()
	if !ok {
		return ctx, nil, nil
	}
	if f.Purge && !f.Confirm { // DQ2/DEL-R4: a purge never prompts, it needs the literal --confirm
		return ctx, nil, purgeRefusal(in)
	}
	if f.Confirm {
		c.Yes = true // DQ8 (FA-DEL-1): a delete is soft, so --confirm answers its question like --yes
	}
	pr := purgeOf(ctx)
	if pr == nil {
		pr = &PurgeRequest{}
		ctx = WithPurge(ctx, pr)
	}
	pr.Asked = pr.Asked || f.Purge
	pr.Confirmed = pr.Confirmed || f.Confirm
	return ctx, pr, nil
}

// purgeRefusal is the E_NEEDS_CONFIRM of a --purge without --confirm: the
// question for the person, and next naming --confirm (never --yes): the
// whole command when the row is named, else (the bulk form, whose filter
// flags the input does not echo back) the flag to add.
func purgeRefusal(in op.Input) error {
	args := in.Positionals()
	msg := "--purge removes " + purgeTarget(args) + " for good, without the trash (only a tombstone stays)"
	e := errs.New(errs.NeedsConfirm, msg).WithHint(purgeHint).WithQuestion(msg + "?")
	if len(args) == 0 {
		return e.WithAddArgs([]string{"--" + flagConfirm})
	}
	next := append(op.CLIPath(in.Op()), args...)
	return e.WithNext(cliName, append(next, "--"+flagPurge, "--"+flagConfirm)...)
}

// CLI words of the refusal's next.
const (
	cliName     = "lore"
	flagPurge   = "purge"
	flagConfirm = "confirm"
)

// purgeTarget names what a refused --purge would have removed: the named
// row, or the bulk form's matches (resource delete --kind --where).
func purgeTarget(args []string) string {
	if len(args) == 0 {
		return "every matching row"
	}
	return strings.Join(args, " ")
}

// purgedText rewrites a delete's "moved to the trash" line when the engine
// purged instead (--purge, or policy trash.purge_on_delete), and adds DQ8's
// note to a plain delete given --confirm. Anything else is unchanged.
func purgedText(text string, pr *PurgeRequest, confirm bool) string {
	if len(pr.Purged) == 0 {
		if confirm && strings.Contains(text, trashLineMark) {
			text += softDeleteNote
		}
		return text
	}
	names := make([]string, 0, len(pr.Purged))
	for _, id := range pr.Purged {
		names = append(names, id.String())
	}
	lines := strings.SplitAfter(text, "\n")
	for i, l := range lines {
		if strings.Contains(l, trashLineMark) {
			lines[i] = "✓ purged for good: " + strings.Join(names, ", ") + " (tombstones written; old content is still in git history)\n"
		}
	}
	return strings.Join(lines, "")
}
