package work

import (
	"context"
	"strings"
	"time"
)

// HandlePrefix starts an @handle (a person or a team on the roster).
const HandlePrefix = "@"

// Team is the host's team side of the work operations: @handles live on a
// roster and claims on a signals ref, neither of which is a row store the
// engine reads. The host (the CLI, which owns the roster and signals
// machinery) supplies one per command with WithTeam; without one the
// operations refuse the v0.1.17 team flags (task --assign @h, --mine,
// --claimed-by, mission --approvers @h) as not available here, never
// guess.
type Team interface {
	// ResolveHandle resolves "@handle" to a person (Assignee.ActorID) or,
	// when allowTeam is set and no person holds it, a team
	// (Assignee.TeamID). A handle on no roster is E_NOT_FOUND with the
	// roster's own hint.
	ResolveHandle(ctx context.Context, handle string, allowTeam bool) (Assignee, error)
	// Claims returns the claims board as this caller sees it now. An
	// unreadable board is an error: a filter it cannot answer is never
	// shown as every row matching.
	Claims(ctx context.Context) (ClaimBoard, error)
}

// Assignee is what an @handle resolved to: exactly one field is set.
type Assignee struct {
	ActorID string
	TeamID  string
}

// ClaimBoard is a read of the claims on a signals ref.
type ClaimBoard interface {
	// Me is the caller's actor id (the holder its own claims carry).
	Me() string
	// Claim reports who holds task id: claimed is false when nobody ever
	// did; active is false once the lease expired (as seen by this clone).
	Claim(taskID string) (holder string, claimed, active bool)
	// State is the task's §3.5 STATE column (working / EXPIRED / gate /
	// done · merged), "" when there is nothing to say. The host renders
	// it: holder labels and the gate's approval hash are its machinery.
	State(t TaskView) string
}

// TaskView is what a task row contributes to its STATE column.
type TaskView struct {
	ID, Title, Body, Status string
	Gate, ApprovedHash      string
	DoneCommit, DonePR      string
	// MergedAt is merged_at (zero when not merged).
	MergedAt time.Time
}

// teamKey is the context key of the command's Team.
type teamKey struct{}

// WithTeam returns ctx carrying t for the operations run under it.
func WithTeam(ctx context.Context, t Team) context.Context {
	return context.WithValue(ctx, teamKey{}, t)
}

// TeamFrom returns the Team the host supplied, if any.
func TeamFrom(ctx context.Context) (Team, bool) {
	t, ok := ctx.Value(teamKey{}).(Team)
	return t, ok && t != nil
}

// IsHandle reports whether s is an @handle.
func IsHandle(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), HandlePrefix) }
