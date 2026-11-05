// Package coord defines the team coordination operations (layer L5,
// TASK_SYSTEM_DESIGN 3.3, CLI_REFERENCE 2.11 and RES 3.17): the claim verbs
// of `lore task` (claim, heartbeat, merged, next, note, reassign, release,
// revoke), `lore hold claim|release`, `lore gate request|approve`, `lore
// inbox` and `lore mission status`.
//
//	surface (CLI, lore api) ─► registry ─► engine.Run ─► run ─► Env.Coordinator ─► Host (signals ref, roster, store links)
//
// PASS3 ONE-PATH: each verb is an operation here, so the CLI, REST and the
// generated docs come from one definition. The signals machinery is still
// the host's (P3-COORD-1); without a host Coordinator the operations answer
// E_UNSUPPORTED instead of guessing.
package coord

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Nouns of the operations defined only here (task and mission belong to
// ops/task and ops/mission; their names are repeated so this package does
// not import those).
const (
	NounTask    = "task"
	NounMission = "mission"
	NounHold    = "hold"
	NounGate    = "gate"
	NounInbox   = "inbox"
)

// Operation names.
const (
	OpTaskClaim     = "task.claim"
	OpTaskHeartbeat = "task.heartbeat"
	OpTaskMerged    = "task.merged"
	OpTaskNext      = "task.next"
	OpTaskNote      = "task.note"
	OpTaskReassign  = "task.reassign"
	OpTaskRelease   = "task.release"
	OpTaskRevoke    = "task.revoke"
	OpHoldClaim     = "hold.claim"
	OpHoldRelease   = "hold.release"
	OpGateRequest   = "gate.request"
	OpGateApprove   = "gate.approve"
	// OpInbox is `lore inbox show`; hosts mount the bare `lore inbox` as its
	// shortcut (CLI_REFERENCE 2.11 spells it without a verb).
	OpInbox         = "inbox.show"
	OpMissionStatus = "mission.status"
	// InboxVerb is OpInbox's verb, for the host's bare-noun shortcut.
	InboxVerb = "show"
)

// SignalsOnly reports whether an operation writes only the signals ref
// (claims, leases, holds), never a row the machine profile should follow:
// the host skips the after-write profile refresh for these.
func SignalsOnly(name string) bool {
	switch name {
	case OpTaskClaim, OpTaskHeartbeat, OpTaskRelease, OpTaskRevoke, OpHoldClaim, OpHoldRelease, OpInbox, OpMissionStatus:
		return true
	}
	return false
}

// Env is what the coordination operations need; *wire.App implements it.
type Env interface {
	// Coordinator returns the host's coordination machinery; ok is false
	// on a host without one (the standalone binary until the signals
	// packages are ported, P3-COORD-1).
	Coordinator(ctx context.Context) (h Host, ok bool)
}

// Ops returns every coordination operation bound to env.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	claimErrs := []errs.Code{errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.Offline, errs.Contended,
		errs.LostClaim, errs.Cancelled, errs.Reassigned, errs.AmbiguousID, errs.StoreUnreachable, errs.Internal}
	write := op.Spec{Permission: op.PermWrite, Access: op.AccessWrite, Errors: claimErrs}
	read := op.Spec{Permission: op.PermRead, Access: op.AccessRead, Errors: claimErrs}
	return []op.Entry{
		op.Define(with(write, OpTaskClaim, "Claim a task (or coordinate a mission) on the signals ref, with a lease",
			op.Example{Title: "claim with owned paths", CLI: "lore task claim tsk_31 --owns src/routes/invite/", Want: "claimed"}), o.claim),
		op.Define(with(write, OpTaskHeartbeat, "Renew your claim's lease (alive, not progress)",
			op.Example{Title: "renew one claim", CLI: "lore task heartbeat tsk_31", Want: "renewed"}), o.heartbeat),
		op.Define(with(write, OpTaskMerged, "Record that a done task's work reached the default branch",
			op.Example{Title: "mark merged", CLI: "lore task merged tsk_31", Want: "merged"}), o.merged),
		op.Define(with(write, OpTaskNext, "Claim the next ready task (resume-first, leaves only, dependencies, gates, urgency)",
			op.Example{Title: "rank without claiming", CLI: "lore task next --mission mis_12 --why --dry-run", Want: "nothing written"}), o.next),
		op.Define(with(write, OpTaskNote, "Record a progress note on a task (pushes your work branch)",
			op.Example{Title: "a checkpoint note", CLI: `lore task note tsk_31 "route + loader done, form next"`, Want: "note"}), o.note),
		op.Define(with(write, OpTaskReassign, "Assign a task to someone else, dropping any claim (human override)",
			op.Example{Title: "offboarding", CLI: "lore task reassign --from @bob --assign @alice", Want: "reassigned"}), o.reassign),
		op.Define(with(write, OpTaskRelease, "Give back a claim you hold",
			op.Example{Title: "release", CLI: "lore task release tsk_31", Want: "released"}), o.release),
		op.Define(with(write, OpTaskRevoke, "Drop the current claim on a task (human override; the holder is fenced out)",
			op.Example{Title: "revoke", CLI: "lore task revoke tsk_31", Want: "revoked"}), o.revoke),
		op.Define(withErrs(with(write, OpHoldClaim, "Hold a resource exclusively (same lease, token and renew as a task claim)",
			op.Example{Title: "hold a test suite", CLI: "lore hold claim full-suite --ttl 30m", Want: "holding"}), errs.NotImplemented), o.holdClaim),
		op.Define(with(write, OpHoldRelease, "Release a resource you hold",
			op.Example{Title: "release a hold", CLI: "lore hold release full-suite", Want: "released"}), o.holdRelease),
		op.Define(with(write, OpGateRequest, "Put a gate on a task (agents stop at it until a person approves)",
			op.Example{Title: "request a gate", CLI: "lore gate request tsk_33", Want: "gate requested"}), o.gateRequest),
		op.Define(with(write, OpGateApprove, "Approve a gate (a person on the roster, from the identity file, never an agent)",
			op.Example{Title: "approve", CLI: "lore gate approve tsk_33", Want: "approved"}), o.gateApprove),
		op.Define(with(read, OpInbox, "What needs you: assignments, handoffs, gates, expired claims, blocked tasks, findings",
			op.Example{Title: "recent items", CLI: "lore inbox --since 2h", Want: ""}), o.inbox),
		op.Define(with(read, OpMissionStatus, "The whole mission at a glance: ready / waiting / gated / claimed / expired / blocked / done",
			op.Example{Title: "as JSON", CLI: "lore mission status mis_12 --json", Want: `"counts"`}), o.missionStatus),
	}
}

// with is base named name, with its summary and one example.
func with(base op.Spec, name, summary string, ex op.Example) op.Spec {
	base.Name, base.Summary, base.Examples = name, summary, []op.Example{ex}
	base.Errors = append([]errs.Code(nil), base.Errors...)
	return base
}

// withErrs adds codes to s's error list (s.Errors is already a copy).
func withErrs(s op.Spec, codes ...errs.Code) op.Spec {
	s.Errors = append(s.Errors, codes...)
	return s
}

// Values of hold claim --on (CLI_REFERENCE 3.3, XQ6).
const (
	holdOnProject = "project"
	holdOnMachine = "machine"
)

// ops binds the run functions to one Env.
type ops struct{ env Env }

// host returns the Coordinator, or E_UNSUPPORTED naming the command.
func (o ops) host(ctx context.Context, name string) (Host, error) {
	h, ok := o.env.Coordinator(ctx)
	if !ok || h == nil {
		return nil, errs.New(errs.Unsupported, "lore "+spaced(name)+" needs the team coordination machinery (the signals ref), which this binary does not have yet").
			WithHint("run it with the release binary")
	}
	return h, nil
}

// spaced turns an operation name into its CLI words.
func spaced(name string) string {
	out := []byte(name)
	for i, c := range out {
		if c == '.' || c == '_' {
			out[i] = ' '
		}
	}
	return string(out)
}

func (o ops) claim(ctx context.Context, in ClaimIn) (Report, error) {
	h, err := o.host(ctx, OpTaskClaim)
	if err != nil {
		return Report{}, err
	}
	return h.TaskClaim(ctx, in)
}

func (o ops) heartbeat(ctx context.Context, in HeartbeatIn) (Report, error) {
	h, err := o.host(ctx, OpTaskHeartbeat)
	if err != nil {
		return Report{}, err
	}
	return h.TaskHeartbeat(ctx, in)
}

func (o ops) merged(ctx context.Context, in MergedIn) (Report, error) {
	h, err := o.host(ctx, OpTaskMerged)
	if err != nil {
		return Report{}, err
	}
	return h.TaskMerged(ctx, in)
}

func (o ops) next(ctx context.Context, in NextIn) (NextOut, error) {
	h, err := o.host(ctx, OpTaskNext)
	if err != nil {
		return NextOut{}, err
	}
	return h.TaskNext(ctx, in)
}

func (o ops) note(ctx context.Context, in NoteIn) (Report, error) {
	h, err := o.host(ctx, OpTaskNote)
	if err != nil {
		return Report{}, err
	}
	return h.TaskNote(ctx, in)
}

func (o ops) reassign(ctx context.Context, in ReassignIn) (Report, error) {
	h, err := o.host(ctx, OpTaskReassign)
	if err != nil {
		return Report{}, err
	}
	return h.TaskReassign(ctx, in)
}

func (o ops) release(ctx context.Context, in ReleaseIn) (Report, error) {
	h, err := o.host(ctx, OpTaskRelease)
	if err != nil {
		return Report{}, err
	}
	return h.TaskRelease(ctx, in)
}

func (o ops) revoke(ctx context.Context, in RevokeIn) (Report, error) {
	h, err := o.host(ctx, OpTaskRevoke)
	if err != nil {
		return Report{}, err
	}
	return h.TaskRevoke(ctx, in)
}

func (o ops) holdClaim(ctx context.Context, in HoldClaimIn) (Report, error) {
	// CLI_REFERENCE 3.3: --on machine is not built in 0.1.18 (it needs the
	// machine-local store attached in wire, phase C; PASS3-A, P3-RES-3), on
	// every binary, before the host is asked; any other value is a typo.
	switch in.On {
	case "", holdOnProject:
	case holdOnMachine:
		return Report{}, errs.New(errs.NotImplemented, "lore hold claim --on machine is not built yet").
			WithHint("run the command under your machine's own lock tool (for example flock) instead")
	default:
		return Report{}, errs.New(errs.InvalidInput, "lore hold claim --on "+in.On+": use project or machine")
	}
	h, err := o.host(ctx, OpHoldClaim)
	if err != nil {
		return Report{}, err
	}
	return h.HoldClaim(ctx, in)
}

func (o ops) holdRelease(ctx context.Context, in HoldReleaseIn) (Report, error) {
	h, err := o.host(ctx, OpHoldRelease)
	if err != nil {
		return Report{}, err
	}
	return h.HoldRelease(ctx, in)
}

func (o ops) gateRequest(ctx context.Context, in GateIn) (Report, error) {
	h, err := o.host(ctx, OpGateRequest)
	if err != nil {
		return Report{}, err
	}
	return h.GateRequest(ctx, in)
}

func (o ops) gateApprove(ctx context.Context, in GateIn) (Report, error) {
	h, err := o.host(ctx, OpGateApprove)
	if err != nil {
		return Report{}, err
	}
	return h.GateApprove(ctx, in)
}

func (o ops) inbox(ctx context.Context, in InboxIn) (InboxOut, error) {
	h, err := o.host(ctx, OpInbox)
	if err != nil {
		return InboxOut{}, err
	}
	return h.Inbox(ctx, in)
}

func (o ops) missionStatus(ctx context.Context, in MissionStatusIn) (MissionStatusOut, error) {
	h, err := o.host(ctx, OpMissionStatus)
	if err != nil {
		return MissionStatusOut{}, err
	}
	return h.MissionStatus(ctx, in)
}
