// secrets_hookaudit.go — secret.hook_refused (SECRETS 16, FA-AGENT-7):
// a commit or push hook that refuses a secret records one audit event per
// finding: hook, path and rule id, never the text or its preview.
//
//	runLeakCheck ─▶ blocking findings ─▶ auditHookRefused (commit / push only)
//	             ─▶ coreApp.AuditSecret(secret.hook_refused)
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"saas/pkg/aicoder/leakcheck"

	"github.com/thesatellite-ai/lore/engine"
)

// patternDetailLead starts a secret finding's detail ("credential pattern
// <rule> …", leakcheck); the rule id is the next word.
const patternDetailLead = "credential pattern "

// auditHookRefused records secret.hook_refused for every secret finding a
// commit or push check refused. Other checks (a write, a PR body, a move)
// are not hooks and record nothing here. Best effort by design: the hook
// already refuses; a failed audit append must not turn the refusal into a
// different error, so it is reported on stderr only.
func auditHookRefused(ctx context.Context, fs []leakcheck.Finding, lc leakContext) {
	hook := ""
	switch lc.action {
	case leakActionCommit:
		hook = hookPreCommit
	case leakActionPush:
		hook = hookPrePush
	default:
		return
	}
	if coreApp == nil {
		return
	}
	for _, f := range fs {
		if f.Rule != leakcheck.RuleSecret {
			continue
		}
		rule := f.Secret.Rule
		if rule == "" {
			if rest, ok := strings.CutPrefix(f.Detail, patternDetailLead); ok {
				rule, _, _ = strings.Cut(rest, " ")
			}
		}
		if err := coreApp.AuditSecret(ctx, engine.ActionLeakHookRefused, engine.SecretAudit{Command: hook,
			Result: "hook=" + hook + " path=" + f.Where + " rule=" + rule}); err != nil {
			fmt.Fprintln(os.Stderr, "lore: warning: secret.hook_refused was not recorded: "+err.Error())
		}
	}
}
