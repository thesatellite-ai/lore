// task_team.go — shared pieces of the lore-for-teams task fields
// (LORE_TEAM_SPEC §1 "Flags", §7.2, §7.4, C23). `lore task add|edit` run on
// the 0.1.18 core surface; what stays here is used by the plan importer
// (--after suffixes), the live flags (the @handle miss error) and the
// coordination views.
package main

import (
	"dbent/schema"
	"saas/pkg/aicoder/errcodes"
)

// depSuffixes are the `:done` / `:merged` suffixes of an --after entry.
var depSuffixes = map[string]schema.DependencyUntil{
	":" + string(schema.DependencyUntilDone):   schema.DependencyUntilDone,
	":" + string(schema.DependencyUntilMerged): schema.DependencyUntilMerged,
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// handleMissError is an @handle that is on no roster. It is E_NOT_FOUND
// like a missing row id, but its hint is about the roster, so the live
// "peek origin/main" explanation for missing ids (explainLiveNotFound)
// leaves it alone.
type handleMissError struct {
	*errcodes.CLIError
}

// Unwrap exposes the CLIError for the exit code and the hint line.
func (e *handleMissError) Unwrap() error { return e.CLIError }

// kindTask is the row kind routeWrite takes for task work (F14,
// defaults.client_repos keys).
const kindTask = "task"
