// secrets_forced.go — forced reveals for `lore inbox` (SECRETS 16, SQ10:
// "lore inbox lists every forced reveal since the person last looked, so
// a person sees what an agent revealed alone").
//
//	inbox ─▶ forcedRevealItems(ctx) ─▶ lore.db audit_logs
//	         action = secret.revealed AND reason carries " FORCED ("
//	         (core_audit.go writes that marker for engine.SecretAudit.Forced)
//	      ─▶ one inboxItem per event (kind "FORCED"), filtered by the ack time
package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
)

// inboxForced is the inbox kind of a forced reveal: upper case like
// EXPIRED, because it is loud on purpose.
const inboxForced = "FORCED"

// forcedMarker is the text core_audit.go adds to a forced reveal's reason.
const forcedMarker = " FORCED ("

// forcedRevealItems lists every forced reveal in the project's lore.db
// audit log, oldest first. No lore.db (or no audit table yet) lists
// nothing; the inbox must not fail on it.
func forcedRevealItems(ctx context.Context) ([]inboxItem, error) {
	db, err := openCoreAuditDB(ctx)
	if err != nil {
		return nil, nil // no lore.db here: nothing was revealed through it
	}
	defer func() { _ = db.Close() }() // read-only use
	rows, err := db.QueryContext(ctx, `SELECT actor_id, COALESCE(target_id, ''), COALESCE(reason, ''), created_at
		FROM audit_logs WHERE action = ? AND reason LIKE ? ORDER BY rowid`, string(engine.ActionSecretRevealed), "%"+forcedMarker+"%")
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	var out []inboxItem
	for rows.Next() {
		var actor, target, reason string
		var at sql.NullTime
		if err := rows.Scan(&actor, &target, &reason, &at); err != nil {
			return nil, errors.Join(errors.New("inbox: read a forced reveal"), err)
		}
		text := "secret revealed without a person's answer: " + strings.TrimSpace(reason)
		if target != "" {
			text += " (row " + target + ")"
		}
		out = append(out, inboxItem{Kind: inboxForced, Who: actor, Text: text, At: at.Time})
	}
	return out, rows.Err()
}
