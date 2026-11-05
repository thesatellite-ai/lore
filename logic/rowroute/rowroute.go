// Package rowroute is the ONE write router of lore for teams
// (LORE_TEAM_SPEC.md §3.2, F1, F2, F14): for every row a command writes it
// decides WHEN the row is shared (with the code in the branch's .lore/data,
// or at once on the live ref) and WHERE it goes (this repo, a store
// project, the personal store), and hands the write to the destination
// registered for that target.
//
// The package holds decisions and one small registry; it imports no git,
// no DB and no config package, so every rule is table tested and any other
// project can drive it with its own destinations. The CLI registers the
// repo destination; the stores work registers store and personal
// destinations; signals never pass through here (they are not rows, fixed
// rule 10).
package rowroute

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Place is when a row is shared.
type Place string

const (
	// PlaceBranch means the row travels with the code in this branch's
	// .lore/data and reaches main when the branch merges.
	PlaceBranch Place = "branch"
	// PlaceLive means the row goes on the target's live ref at once.
	PlaceLive Place = "live"
)

// Choice is the --live / --no-live flag of one command.
type Choice int

const (
	// ChoiceDefault means no flag; the kind's default applies.
	ChoiceDefault Choice = iota
	// ChoiceLive means --live.
	ChoiceLive
	// ChoiceNoLive means --no-live.
	ChoiceNoLive
)

// String is the flag spelling.
func (c Choice) String() string {
	switch c {
	case ChoiceLive:
		return "--live"
	case ChoiceNoLive:
		return "--no-live"
	case ChoiceDefault:
	}
	return "default"
}

// Row is what the router knows about one row a command wrote.
type Row struct {
	Table string
	ID    string
	// InLive: the row's current version is on the live ref (a live file or a
	// tombstone). Live wins, always (fixed rule 4).
	InLive bool
	// InBranch: the row has a file in this branch's .lore/data.
	InBranch bool
	// Deleted: the write removes the row.
	Deleted bool
}

// Request is the command-level input: the flag and the table it applies to
// (the command's own kind: `lore task add --live` sets it for tasks, not for
// an actor row the command happens to create), plus live.defaults.
type Request struct {
	Choice      Choice
	ChoiceTable string
	// Defaults is live.defaults keyed by table name (tasks, rules, …).
	Defaults map[string]bool
}

// Decision is where one row goes, and why (printed by --trace and
// `lore explain`).
type Decision struct {
	Place  Place
	Reason string
}

// Reasons, as printed.
const (
	ReasonLiveWins     = "the row is live; live wins (rule 4)"
	ReasonFlagLive     = "--live"
	ReasonFlagNoLive   = "--no-live"
	ReasonBranchRow    = "the row is in this branch's data; an edit stays with the code (F1)"
	ReasonDefaultLive  = "live.defaults: live"
	ReasonDefaultLocal = "live.defaults: with the code"
	ReasonDeleteBranch = "a branch row's delete is a file delete on the branch"
)

// Decide places one written row. Order:
//  1. a row that is live stays live, whatever the flag (rule 4; a
//     --no-live edit of a live row would be shadowed by live forever);
//  2. the command's own flag, for rows of the command's table;
//  3. a row already in branch data stays there (F1: "edits the branch
//     file; reaches main at merge");
//  4. a new row follows live.defaults for its table.
func Decide(req Request, row Row) Decision {
	if row.InLive {
		return Decision{Place: PlaceLive, Reason: ReasonLiveWins}
	}
	if row.Table == req.ChoiceTable {
		switch req.Choice {
		case ChoiceLive:
			// Also for a delete: a tombstone in live hides the branch copy
			// from everyone at once, which is what --live asks for.
			return Decision{Place: PlaceLive, Reason: ReasonFlagLive}
		case ChoiceNoLive:
			return Decision{Place: PlaceBranch, Reason: ReasonFlagNoLive}
		case ChoiceDefault:
			// no flag: fall through to the branch/kind rules below
		}
	}
	if row.InBranch {
		if row.Deleted {
			return Decision{Place: PlaceBranch, Reason: ReasonDeleteBranch}
		}
		return Decision{Place: PlaceBranch, Reason: ReasonBranchRow}
	}
	if req.Defaults[row.Table] {
		return Decision{Place: PlaceLive, Reason: ReasonDefaultLive}
	}
	return Decision{Place: PlaceBranch, Reason: ReasonDefaultLocal}
}

// ReasonCompanion explains a supporting row that follows the command's live
// rows.
const ReasonCompanion = "created alongside live rows of this command (a supporting row follows them)"

// DecideAll places every row one command wrote. It applies Decide to each
// row, then one batch rule: a NEW supporting row (a table that has no entry
// in live.defaults, such as the writer's actor row created on their first
// write, or a tag) goes live when any content row of the batch goes live.
// Without it, the first live write of a new teammate would leave their
// actor file in the branch's .lore/data, touching the work tree a live
// write promises not to touch (F2), and the live row would reference an
// actor nobody else has yet.
func DecideAll(req Request, rows []Row) []Decision {
	out := make([]Decision, len(rows))
	anyLive := false
	for i, r := range rows {
		out[i] = Decide(req, r)
		if _, content := req.Defaults[r.Table]; content && out[i].Place == PlaceLive {
			anyLive = true
		}
	}
	if !anyLive {
		return out
	}
	for i, r := range rows {
		if _, content := req.Defaults[r.Table]; content || r.InLive || r.InBranch || r.Deleted {
			continue
		}
		if r.Table == req.ChoiceTable && req.Choice == ChoiceNoLive {
			continue
		}
		out[i] = Decision{Place: PlaceLive, Reason: ReasonCompanion}
	}
	return out
}

// ---------------------------------------------------------------- targets

// TargetKind names who a row is written for (§3.2 `--to`).
type TargetKind string

const (
	// TargetRepo means this repo's own project (no --to, or --to repo).
	TargetRepo TargetKind = "repo"
	// TargetStore means a project in an org store (--to <org>[/<folder>]).
	TargetStore TargetKind = "store"
	// TargetPersonal means the personal store (--to me).
	TargetPersonal TargetKind = "personal"
)

// Target is the project a row is written to.
type Target struct {
	Kind TargetKind
	// Org and Folder name a store project (TargetStore) or the personal
	// folder (TargetPersonal: Folder "everywhere" or an org name).
	Org    string
	Folder string
}

// Write is one routed row write.
type Write struct {
	Target   Target
	Decision Decision
	Table    string
	ID       string
	// Doc is the canonical row document (nil when Deleted).
	Doc     []byte
	Deleted bool
}

// Destination stores routed writes for one target kind. Implementations:
// the CLI's repo destination (branch data + refs/lore/live), the stores
// work's store and personal destinations.
type Destination interface {
	// Apply stores the writes (all for the same target kind) and reports
	// per-row outcomes.
	Apply(ctx context.Context, writes []Write) ([]Outcome, error)
}

// Outcome is what happened to one write.
type Outcome struct {
	Table, ID string
	// Where is a short human description ("live, pushed",
	// "live, not pushed yet: 2 waiting", "branch").
	Where string
}

// ErrNoDestination means no destination is registered for a target kind.
var ErrNoDestination = errors.New("rowroute: no destination registered")

// Router dispatches writes to registered destinations. The zero value is
// ready to use; it is safe for concurrent use.
type Router struct {
	mu    sync.RWMutex
	dests map[TargetKind]Destination
}

// Register sets the destination for a target kind, replacing any earlier
// one (the one typed escape hatch: a caller may route a kind anywhere).
func (r *Router) Register(kind TargetKind, d Destination) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dests == nil {
		r.dests = map[TargetKind]Destination{}
	}
	r.dests[kind] = d
}

// Registered lists the registered target kinds, sorted.
func (r *Router) Registered() []TargetKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]TargetKind, 0, len(r.dests))
	for k := range r.dests {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Apply groups writes by target kind and hands each group to its
// destination, in a fixed order (repo, store, personal) so output is
// stable. A missing destination fails before anything is written.
func (r *Router) Apply(ctx context.Context, writes []Write) ([]Outcome, error) {
	r.mu.RLock()
	groups := map[TargetKind][]Write{}
	for _, w := range writes {
		if _, ok := r.dests[w.Target.Kind]; !ok {
			r.mu.RUnlock()
			return nil, fmt.Errorf("%w for %q (%s/%s)", ErrNoDestination, w.Target.Kind, w.Table, w.ID)
		}
		groups[w.Target.Kind] = append(groups[w.Target.Kind], w)
	}
	dests := make(map[TargetKind]Destination, len(groups))
	for k := range groups {
		dests[k] = r.dests[k]
	}
	r.mu.RUnlock()
	var out []Outcome
	for _, k := range []TargetKind{TargetRepo, TargetStore, TargetPersonal} {
		g, ok := groups[k]
		if !ok {
			continue
		}
		o, err := dests[k].Apply(ctx, g)
		out = append(out, o...)
		if err != nil {
			return out, fmt.Errorf("rowroute: %s: %w", k, err)
		}
	}
	return out, nil
}
