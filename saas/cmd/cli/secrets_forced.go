// secrets_forced.go — forced reveals for `lore inbox` (SECRETS 16, SQ10:
// "lore inbox lists every forced reveal since the person last looked, so
// a person sees what an agent revealed alone").
//
//	inbox ─▶ forcedRevealItems(ctx) ─▶ the audit chain (machine store, P3-LOCAL-7)
//	         action = secret.revealed AND reason carries " FORCED ("
//	         (core_audit.go writes that marker for engine.SecretAudit.Forced)
//	      ─▶ one inboxItem per event (kind "FORCED"), filtered by the ack time
package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
)

// inboxForced is the inbox kind of a forced reveal: upper case like
// EXPIRED, because it is loud on purpose.
const inboxForced = "FORCED"

// forcedMarker is the text core_audit.go adds to a forced reveal's reason.
const forcedMarker = " FORCED ("

// forcedRevealItems lists every forced reveal in the project's audit
// chain, oldest first. No project (or no core) lists nothing; the inbox
// must not fail on it.
func forcedRevealItems(ctx context.Context) ([]inboxItem, error) {
	if coreApp == nil {
		return nil, nil
	}
	dbPath, err := auditDBPath("")
	if err != nil {
		return nil, nil // no project here: nothing was revealed in it
	}
	recs, err := auditRecords(ctx, coreApp, dbPath)
	if err != nil {
		return nil, errors.Join(errors.New("inbox: read the forced reveals"), err)
	}
	var out []inboxItem
	for _, r := range recs {
		if r.Action != string(engine.ActionSecretRevealed) || !strings.Contains(r.Reason, forcedMarker) {
			continue
		}
		text := "secret revealed without a person's answer: " + strings.TrimSpace(r.Reason)
		if r.TargetID != "" {
			text += " (row " + r.TargetID + ")"
		}
		at, _ := time.Parse(time.RFC3339Nano, r.CreatedAt) // a v0.1.17 time that does not parse sorts as zero
		out = append(out, inboxItem{Kind: inboxForced, Who: r.ActorID, Text: text, At: at})
	}
	return out, nil
}
