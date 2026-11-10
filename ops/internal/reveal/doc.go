// Package reveal is the one --reveal path (SECRETS_AND_REFERENCES 2.7,
// 10.2, 16; SQ10, D18), shared by `lore secrets show --reveal` (ops/settings)
// and `lore show --reveal` (ops/internal/expand). It sits below both so
// neither imports the other (ops/settings imports ops/task, which imports
// expand).
//
//	caller ─▶ Gate (SQ10: --yes passes; --force or an agent under
//	          secrets.agent_reveal: allow passes as forced; else
//	          E_NEEDS_CONFIRM with the question)
//	       ─▶ Auditor present? no ─▶ refused (a reveal is never unaudited)
//	       ─▶ Group per reference: alternates in order (19.7)
//	              manager.Reveal ─▶ AuditSecret revealed | reveal_failed
//	              first answer ─▶ expose (the ONLY Secret.Expose caller)
//
// Layer L5 helper (ops/internal).
package reveal
