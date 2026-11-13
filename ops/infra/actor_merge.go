package infra

// actor_merge.go — `lore actor merge <from> <into>`: one identity that
// ended up with two actor ids (owner report 2026-11-12: adopting a v0.1.17
// repo brought in act_A with stable_key human:<email> while the org
// roster already had act_B with the same key).
//
//	actor.merge ─▶ ActorMerger.MergeActor(plan): both actors read, the
//	               rows naming <from> counted per table
//	   ├▶ refused: the same actor, an unknown id, a human into an agent
//	   │           (or back), <from> already merged into another actor
//	   ├▶ not confirmed ─▶ E_NEEDS_CONFIRM naming the counts (--yes)
//	   └▶ ActorMerger.MergeActor: every row naming <from> in the stores
//	      this command writes is edited to name <into> (an ordinary edit:
//	      new HLC, the audit trail kept); <from> keeps its row, with
//	      merged_into <into>, inactive (actors are never deleted, so the
//	      merged row is the tombstone: reads and reports follow the chain)
//
// Through the registry like every write, so a coding agent runs it as its
// person; `lore doctor` names the pairs that share a stable key.

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
)

// OpActorMerge merges one actor into another.
const OpActorMerge = "actor.merge"

// ActorMergeIn is the input of actor.merge.
type ActorMergeIn struct {
	From string `json:"from" arg:"0" desc:"the actor id to merge away (act_…; its row stays, marked merged_into)"`
	Into string `json:"into" arg:"1" desc:"the actor id that stays (act_…; usually the org roster's)"`
	// DryRun counts what the merge would rewrite.
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"count the rows that name <from>; change nothing"`
}

// ActorMergeTable is how many rows of one table name the merged actor.
type ActorMergeTable struct {
	Table string `json:"table"`
	Rows  int    `json:"rows"`
}

// ActorMergeStore is how many rows of one store (an org store's folder:
// "<store>/<folder>") the merge edits there.
type ActorMergeStore struct {
	Store string `json:"store"`
	Rows  int    `json:"rows"`
}

// ActorMergeOut is the merge.
type ActorMergeOut struct {
	From     string `json:"from"`
	Into     string `json:"into"`
	FromName string `json:"from_name,omitempty"`
	IntoName string `json:"into_name,omitempty"`
	// Key is the stable key the two share ("" when they do not).
	Key string `json:"stable_key,omitempty"`
	// Rows counts the rows edited to name Into (would be, under DryRun),
	// Tables per table, Written per store or folder where they live;
	// Stores names every store read (an org store's folder by the store
	// holding it and the folder, never by the org's name alone).
	Rows    int               `json:"rows"`
	Tables  []ActorMergeTable `json:"tables,omitempty"`
	Written []ActorMergeStore `json:"written,omitempty"`
	Stores  []string          `json:"stores,omitempty"`
	// ReadOnly names stores holding rows that name From but attached
	// read-only here: those rows are left; merge again where they are
	// writable.
	ReadOnly []string `json:"read_only,omitempty"`
	// Marked counts the copies of From's actor row marked merged_into.
	Marked int  `json:"marked"`
	DryRun bool `json:"dry_run,omitempty"`
}

// Text is the result line and the counts.
func (o ActorMergeOut) Text() string {
	var b strings.Builder
	verb := work.Success + " merged "
	if o.DryRun {
		verb = "would merge "
	}
	b.WriteString(verb + o.From + " " + o.FromName + " into " + o.Into + " " + o.IntoName + "\n")
	if o.Key != "" {
		b.WriteString("  shared stable key: " + o.Key + "\n")
	}
	parts := make([]string, 0, len(o.Tables))
	for _, t := range o.Tables {
		parts = append(parts, t.Table+" "+strconv.Itoa(t.Rows))
	}
	rows := "no rows name " + o.From
	if o.Rows > 0 {
		rows = strconv.Itoa(o.Rows) + " rows now name " + o.Into + " (" + strings.Join(parts, ", ") + ")"
		if o.DryRun {
			rows = strconv.Itoa(o.Rows) + " rows would name " + o.Into + " (" + strings.Join(parts, ", ") + ")"
		}
	}
	b.WriteString("  " + rows + "\n")
	for _, w := range o.Written {
		verb := " edited"
		if o.DryRun {
			verb = " to edit"
		}
		b.WriteString("    " + w.Store + ": " + strconv.Itoa(w.Rows) + verb + "\n")
	}
	b.WriteString("  stores read: " + strings.Join(o.Stores, ", ") + "\n")
	if !o.DryRun {
		b.WriteString("  " + o.From + " keeps its row with merged_into " + o.Into + " (inactive; actors are never deleted)\n")
	}
	// history stamps are kept as written (adoptfix): say so plainly
	b.WriteString("  history stamps (created_by, updated_by) keep " + o.From + "; reads show " + o.Into + " " + o.IntoName + " for it\n")
	for _, s := range o.ReadOnly {
		b.WriteString("  left: " + s + " is attached read-only here; run the merge again where it is writable\n")
	}
	return b.String()
}

// ActorMerger is the optional Env upgrade behind actor.merge (wire.App).
type ActorMerger interface {
	// MergeActor plans (in.DryRun) or runs the merge; the op has asked.
	MergeActor(ctx context.Context, in ActorMergeIn) (ActorMergeOut, error)
}

// actorMergeOps is actor.merge when env can merge.
func (o ops) actorMergeOps() []op.Entry {
	m, ok := upgrade.As[ActorMerger](o.env)
	if !ok {
		return nil
	}
	return []op.Entry{op.Define(op.Spec{Name: OpActorMerge, Summary: "Merge one actor into another (one person, two actor ids): rows naming it are edited to name the other; its row stays, marked merged_into; asks first",
		Permission: op.PermWrite, Access: op.AccessWrite, Errors: append([]errs.Code{errs.Refused}, writeErrors...),
		Confirm: op.ConfirmRule{MayAsk: true},
		Examples: []op.Example{
			{Title: "see what a merge rewrites", CLI: "lore actor merge --from act_<from> --into act_<into> --dry-run", Want: "would merge"},
			{Title: "merge a duplicate into the roster's actor", CLI: "lore actor merge --from act_<from> --into act_<into> --yes", Want: "merged"},
		}},
		func(ctx context.Context, in ActorMergeIn) (ActorMergeOut, error) {
			in.From, in.Into = strings.TrimSpace(in.From), strings.TrimSpace(in.Into)
			if in.From == "" || in.Into == "" {
				return ActorMergeOut{}, errs.New(errs.InvalidInput, "actor merge: give two actor ids: lore actor merge --from <from> --into <into>").
					WithHint("`lore doctor` names the actors that share a stable key, with the command")
			}
			plan := in
			plan.DryRun = true
			p, err := m.MergeActor(ctx, plan)
			if err != nil || in.DryRun {
				return p, err
			}
			if !engine.Confirmed(ctx) && !op.Confirmed(ctx) {
				q := "merge " + p.From + " " + p.FromName + " into " + p.Into + " " + p.IntoName + "? " + strconv.Itoa(p.Rows) +
					" rows naming " + p.From + " are edited to name " + p.Into + " (each an ordinary edit, history kept); " + p.From + " keeps its row, marked merged_into " + p.Into
				return ActorMergeOut{}, errs.New(errs.NeedsConfirm, "actor merge: "+q).WithQuestion(q).WithHint("ask the person, then rerun with --yes")
			}
			return m.MergeActor(ctx, in)
		})}
}
