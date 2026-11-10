package wire

// coord_standalone_forced.go — forced reveals in `lore inbox` on cmd/lore
// (SECRETS 16, SQ10: "lore inbox lists every forced reveal since the
// person last looked, so a person sees what an agent revealed alone";
// v0.1.17 saas/cmd/cli secrets_forced.go, later.md 272).
//
//	Inbox ─► forcedItems ─► the repo audit sink's chain (opsaudit.Env)
//	         action secret.revealed whose reason carries " FORCED ("
//	         (ChainRecordOf writes that marker for engine.SecretAudit.Forced)
//	      ─► one item per event, kind FORCED, filtered by the ack time

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
)

// inboxForced is the inbox kind of a forced reveal: upper case like
// EXPIRED, because it is loud on purpose (v0.1.17 text).
const inboxForced = "FORCED"

// forcedReasonMarker is the text ChainRecordOf adds to a forced reveal's
// reason.
const forcedReasonMarker = " FORCED ("

// forcedItems lists every forced reveal in the project's audit chain,
// oldest first. A sink that keeps no chain lists nothing; a chain that
// cannot be read fails the inbox, as v0.1.17 did (a person must not be
// told "nothing was revealed" when lore could not look).
func (cv *coordView) forcedItems(ctx context.Context) ([]opscoord.InboxItem, error) {
	c := cv.a.chainReader()
	if c == nil {
		return nil, nil
	}
	entries, err := c.AuditLog(ctx, 0)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "inbox: read the forced reveals", err)
	}
	slices.Reverse(entries) // AuditLog is newest first
	var out []opscoord.InboxItem
	for _, e := range entries {
		if e.Action != string(engine.ActionSecretRevealed) || !strings.Contains(e.Reason, forcedReasonMarker) {
			continue
		}
		text := "secret revealed without a person's answer: " + strings.TrimSpace(e.Reason)
		if e.TargetID != "" {
			text += " (row " + e.TargetID + ")"
		}
		at, _ := time.Parse(time.RFC3339Nano, e.CreatedAt) // a v0.1.17 time that does not parse sorts as zero (always shown)
		out = append(out, opscoord.InboxItem{Kind: inboxForced, Who: cv.label(ctx, e.ActorID), Text: text, At: at})
	}
	return out, nil
}
