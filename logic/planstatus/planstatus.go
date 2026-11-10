// Package planstatus is the one v0.1.17 plans.status_str → status rule
// (TSQ9), pure, so both the plan noun (ops/plan, on read and edit) and the
// engine's client write step (engine/clientstrip.go, PC-LEAK-1) apply the
// same mapping:
//
//	status_str "approved" (in the closed set) → approved
//	status_str "active" (v0.1.17's default)   → in_progress
//	anything else, empty                       → draft
package planstatus

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/schema"
)

// legacyActive is v0.1.17's only written status_str.
const legacyActive = "active"

// Upcast maps a v0.1.17 plans.status_str to the TSQ9 closed set (open
// question F2 in FORMAT_EVOLUTION_DESIGN; spec-deltas records this rule).
func Upcast(statusStr string) string {
	s := strings.TrimSpace(strings.ToLower(statusStr))
	switch {
	case slices.Contains(schema.PlanStatuses(), s):
		return s
	case s == legacyActive:
		return schema.PlanStatusInProgress
	}
	return schema.PlanStatusDraft
}
