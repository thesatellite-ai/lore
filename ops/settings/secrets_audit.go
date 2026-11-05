package settings

// secrets_audit.go — the named secret.* audit events of allow entries and
// rule rows (SECRETS 16; FA-AGENT-4).
//
//	secrets allow / allow rm        ─▶ row write ─▶ secret.allow_added / _removed
//	secrets rule add|edit|off|on    ─▶ row write ─▶ secret.rule_changed
//
// The row write keeps its own plain audit entry; the secret.* event beside
// it says what the write meant. It carries ids, rule ids, places, states and
// the stated reason only, never a matched text (the allow row holds only
// its hash).

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
)

// The secret.* actions of 16 these ops emit.
const (
	auditAllowAdded   = engine.ActionSecretAllowAdded
	auditAllowRemoved = engine.ActionSecretAllowRemoved
	auditRuleChanged  = engine.ActionSecretRuleChanged
)

// auditSecretEvent records one event when the Env can (SecretAuditor);
// the parts are joined into the event's result text ("k=v" words).
func (o ops) auditSecretEvent(ctx context.Context, action engine.Action, command string, row ids.AnyID, parts ...string) error {
	a, ok := o.env.(SecretAuditor)
	if !ok {
		return nil
	}
	return a.AuditSecret(ctx, action, engine.SecretAudit{Command: command, Row: row, Result: strings.Join(parts, " ")})
}
