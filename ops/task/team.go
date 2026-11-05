package task

// team.go — v0.1.17's team flags on the core (LORE_TEAM_SPEC §1 "Flags",
// §3.5, §7.2): `task add|edit --assign @handle` (a person sets the
// assignee, a team the assigned team) and `task list --mine` /
// `--claimed-by`. Handles and claims come from the host's work.Team (the
// roster and the signals ref are not row stores); without one these flags
// are refused, never guessed.

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/direction"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/internal/pointer"
	"github.com/thesatellite-ai/lore/store"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// Team fields the team flags read and write.
const (
	fieldTeam         = "assigned_team"
	fieldGate         = "gate"
	fieldApprovedHash = "approved_hash"
	fieldMergedAt     = "merged_at"
	fieldTeamActive   = "active"
	// teamActive is a bool field's String() when set.
	teamActive = "true"
)

// team returns the host's Team, or the not-on-this-surface refusal for flag.
func team(ctx context.Context, flag, hint string) (work.Team, error) {
	t, ok := work.TeamFrom(ctx)
	if !ok {
		return nil, notYet(flag, hint)
	}
	return t, nil
}

// resolveAssign resolves --assign: an @handle (a person, else a team) or an
// actor reference (id, stable key, display name). "" assigns nothing.
func (o ops) resolveAssign(ctx context.Context, raw string) (work.Assignee, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return work.Assignee{}, nil
	}
	if !work.IsHandle(ref) {
		id, err := o.actorRef(ctx, ref)
		return work.Assignee{ActorID: id}, err
	}
	t, err := team(ctx, "assign", "assign a person by actor id: --assigned-to act_…")
	if err != nil {
		return work.Assignee{}, err
	}
	return t.ResolveHandle(ctx, ref, true)
}

// teamView is the team side of one `task list`: the --mine / --claimed-by
// keep test (nil = no filter) and the board that fills the STATE column
// (nil = rows only).
type teamView struct {
	keep  func(row.Row) bool
	board work.ClaimBoard
}

// state is r's STATE column, "" without a board.
func (v teamView) state(r row.Row) string {
	if v.board == nil {
		return ""
	}
	return v.board.State(work.TaskView{
		ID: r.ID.String(), Title: work.Field(r, work.FieldTitle), Body: work.Field(r, work.FieldBody), Status: work.Field(r, work.FieldStatus),
		Gate: work.Field(r, fieldGate), ApprovedHash: work.Field(r, fieldApprovedHash),
		DoneCommit: work.Field(r, fieldDoneCommit), DonePR: work.Field(r, fieldDonePR), MergedAt: work.Time(r, fieldMergedAt),
	})
}

// teamList reads the host's claims for `task list` (v0.1.17
// applyTaskListTeam): with --mine or --claimed-by a board that cannot be
// read (or no host Team) is the command's error; without them the rows
// are shown without states.
func (o ops) teamList(ctx context.Context, in ListIn) (teamView, error) {
	filter := in.Mine || in.ClaimedBy != ""
	flag := "mine"
	if in.ClaimedBy != "" {
		flag = "claimed-by"
	}
	t, ok := work.TeamFrom(ctx)
	if !ok {
		if filter {
			return teamView{}, notYet(flag, "filter by assignee: --assignee act_…")
		}
		return teamView{}, nil
	}
	holder := ""
	var err error
	if in.ClaimedBy != "" {
		if work.IsHandle(in.ClaimedBy) {
			a, herr := t.ResolveHandle(ctx, in.ClaimedBy, false)
			if herr != nil {
				return teamView{}, herr
			}
			holder = a.ActorID
		} else if holder, err = o.actorRef(ctx, in.ClaimedBy); err != nil {
			return teamView{}, err
		}
	}
	board, err := t.Claims(ctx)
	if err != nil {
		if filter {
			return teamView{}, err
		}
		return teamView{}, nil
	}
	if !filter {
		return teamView{board: board}, nil
	}
	me := board.Me()
	myTeams := map[string]bool{}
	if in.Mine {
		if myTeams, err = o.teamsOf(ctx, me); err != nil {
			return teamView{}, err
		}
	}
	return teamView{board: board, keep: func(r row.Row) bool {
		h, claimed, active := board.Claim(r.ID.String())
		if holder != "" && (!claimed || !active || h != holder) {
			return false
		}
		if in.Mine {
			assigned := (me != "" && work.Field(r, fieldAssigned) == me) || myTeams[work.Field(r, fieldTeam)]
			free := !claimed || !active || h == me
			if !assigned || !free {
				return false
			}
		}
		return true
	}}, nil
}

// teamsOf returns the ids of the active teams (every attached store) that
// list actor as a member.
func (o ops) teamsOf(ctx context.Context, actor string) (map[string]bool, error) {
	out := map[string]bool{}
	if actor == "" {
		return out, nil
	}
	all, err := work.ListAll(ctx, o.env, schema.TableTeams, "")
	if err != nil {
		return nil, err
	}
	for _, s := range all {
		if work.Field(s.Row, fieldTeamActive) != teamActive {
			continue
		}
		// ItemStrings: in-memory and file-decoded members read the same.
		if slices.Contains(value.ItemStrings(s.Row.Fields[schema.FieldTeamMembers]), actor) {
			out[s.Row.ID.String()] = true
		}
	}
	return out, nil
}

// The host side of the team flags, exported for hosts outside ops (the CLI
// lives in another module and cannot import ops/internal/work).
type (
	// Team supplies @handles and claims (work.Team).
	Team = work.Team
	// Assignee is what an @handle resolved to (work.Assignee).
	Assignee = work.Assignee
	// ClaimBoard is a read of the claims (work.ClaimBoard).
	ClaimBoard = work.ClaimBoard
	// View is a task's STATE column input (work.TaskView).
	View = work.TaskView
)

// ShowDetailer is an optional upgrade of the host's Team: what `task show`
// adds for a task held in a store, from the coordination machinery the
// host owns (LORE_TEAM_SPEC §7.5 step 5: what a replacement agent reads
// first, from any linked repo of the group, lore-docs BUG-D3): the after
// list, the gate, the time box, attempts and outcome as field lines, and
// the notes and the claim as this clone sees it now after the body. ok is
// false when the host has nothing to add (the task is the repo's own, or
// no coordination target holds it). A host without it shows the row's own
// fields only.
type ShowDetailer interface {
	ShowDetail(ctx context.Context, taskID string) (detail ShowDetail, ok bool, err error)
}

// ShowDetail is the host's part of the human `task show` (ShowDetailer).
// It never reaches --json, whose shape stays the row's (Full).
type ShowDetail struct {
	// Fields are printed after the row's own fields, in order.
	Fields []DetailField
	// Tail is printed verbatim after the body: whole lines, each ending
	// in a newline.
	Tail string
}

// DetailField is one "  name: value" line of the human show.
type DetailField struct{ Name, Value string }

// WithTeam returns ctx carrying t for the task, mission and plan
// operations run under it (work.WithTeam).
func WithTeam(ctx context.Context, t Team) context.Context { return work.WithTeam(ctx, t) }

// HasTeam reports whether ctx already carries a Team (a host's own or a
// test's), so a default one is never layered over it (P3-PERF-2).
func HasTeam(ctx context.Context) bool { _, ok := work.TeamFrom(ctx); return ok }

// refuseClientTeam is TEAM-030 (LORE_TEAM_SPEC §7.2, ADR TEAM-030): a team
// is never assigned on a task written into a linked client repo, because
// team names are internal. target is the store the task is (or will be)
// in ("" = the default target). An Env that cannot place stores counts the
// repo store and the default target as client (fail closed, like
// ops/internal/pointer). The message and hint are v0.1.17's.
func (o ops) refuseClientTeam(ctx context.Context, a work.Assignee, target store.Source) error {
	if a.TeamID == "" {
		return nil
	}
	class := direction.Client
	if c, ok := o.env.(pointer.Classifier); ok {
		var err error
		if class, err = c.StoreClass(ctx, target); err != nil {
			return err
		}
	} else if target != "" && target != store.SourceRepo {
		return nil
	}
	if class != direction.Client {
		return nil
	}
	return errs.New(errs.InvalidInput, "a team cannot be assigned in a linked client repo: team names are internal (TEAM-030)").
		WithHint("assign a person (--assign @handle), or keep the task internal with --to <org>")
}
