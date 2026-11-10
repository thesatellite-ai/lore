package infra

// actor_source.go — actor list / show read an actor's source of truth
// (SOURCE_OF_TRUTH actors, logic/actorsource) through the host's
// profilestore.ActorSources upgrade, and `lore actor refresh` rewrites
// stale copies now (the doctor's fix). An Env without the upgrade shows
// each store's copy as before.

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/logic/actorsource"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
)

// OpActorRefresh is `lore actor refresh [<actor>]`.
const OpActorRefresh = "actor.refresh"

// ActorSourceOut is actor show's source block.
type ActorSourceOut struct {
	// Where names the source: "acme/org" (the roster store), "setup solo1".
	Where string `json:"where"`
	// Origin is what a current copy's origin_store says.
	Origin string `json:"origin"`
	// Version is the source's version ("" when it could not be read).
	Version   string `json:"version,omitempty"`
	Reachable bool   `json:"reachable"`
	// DisplayName is the source's name ("" when it could not be read).
	DisplayName string `json:"display_name,omitempty"`
}

// ActorCopyOut is one store's copy in actor show.
type ActorCopyOut struct {
	Store         string `json:"store"`
	DisplayName   string `json:"display_name"`
	State         string `json:"state"`
	OriginStore   string `json:"origin_store,omitempty"`
	OriginVersion string `json:"origin_version,omitempty"`
}

// ActorCopyCounts is the per-actor copy tally for a stores column of actor
// list: how many stores hold a copy that is current, stale, unstamped or
// not judgeable (source unreachable). ok is false when the Env has no
// source seam (no column then).
func ActorCopyCounts(ctx context.Context, env Env, id string) (actorsource.Counts, bool) {
	rep, ok := actorReport(ctx, env, id)
	if !ok {
		return actorsource.Counts{}, false
	}
	return rep.Counts(), true
}

// actorReport is the host's report for actor id (ok false without the
// seam, for an id that does not parse, or when the host fails: a read
// never fails over it).
func actorReport(ctx context.Context, env Env, id string) (actorsource.Report, bool) {
	src, ok := upgrade.As[profilestore.ActorSources](env)
	if !ok {
		return actorsource.Report{}, false
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		return actorsource.Report{}, false
	}
	rep, err := src.ActorReport(ctx, aid)
	if err != nil {
		return actorsource.Report{}, false
	}
	return rep, true
}

// applySource sets a's display name to the value a merged read shows (the
// source's; else the newest copy, with SourceNote saying so) and, for
// show, its source and every copy with its state. pinned is a read of one
// store's copy (--store): its key is left as that copy's file has it.
func (o ops) applySource(ctx context.Context, a *Actor, show, pinned bool) {
	rep, ok := actorReport(ctx, o.env, a.ID)
	if !ok {
		return
	}
	sh := rep.Shown()
	if sh.Values.Name != "" {
		a.DisplayName = sh.Values.Name
	}
	// The current email after an email change, before every copy follows.
	// A merged read shows it; a pinned read of a projected copy (`--store
	// repo` in a repo with no org, a client store) shows no email, as its
	// file has none (F18).
	if sh.Values.StableKey != "" && (!pinned || a.StableKey != "") {
		a.StableKey = sh.Values.StableKey
	}
	a.SourceNote = sh.Mark
	if !show || !rep.Source.Known {
		// No source: the copies are worth listing only when there are
		// several (one copy is simply the actor's row).
		if show && len(rep.Copies) > 1 {
			a.Copies = copiesOut(rep)
		}
		return
	}
	a.Source = ActorSourceOut{Where: rep.Source.Where, Origin: rep.Source.Origin, Version: rep.Source.Version,
		Reachable: rep.Source.Reachable, DisplayName: rep.Source.Values.Name}
	a.Copies = copiesOut(rep)
}

// copiesOut lists rep's copies with their states.
func copiesOut(rep actorsource.Report) []ActorCopyOut {
	out := make([]ActorCopyOut, 0, len(rep.Copies))
	for _, c := range rep.Copies {
		out = append(out, ActorCopyOut{Store: c.Store, DisplayName: c.Values.Name, State: string(actorsource.StateOf(rep.Source, c)),
			OriginStore: c.OriginStore, OriginVersion: c.OriginVersion})
	}
	return out
}

// sourceText is actor show's source and copies lines ("" without them).
func sourceText(a Actor) string {
	var b strings.Builder
	if s := a.Source; s.Where != "" {
		if s.Reachable {
			fmt.Fprintf(&b, "  source:      %s (%s)\n", s.Where, s.DisplayName)
		} else {
			fmt.Fprintf(&b, "  source:      %s (unreachable here)\n", s.Where)
		}
	}
	lead := "  copies:      "
	for _, c := range a.Copies {
		state := c.State
		if c.State == string(actorsource.StateStale) && a.Source.Reachable {
			if c.DisplayName != a.Source.DisplayName {
				state += fmt.Sprintf(": has %q, source says %q", c.DisplayName, a.Source.DisplayName)
			} else {
				state += ": made from an older version of the source"
			}
		}
		fmt.Fprintf(&b, "%s%-12s %s\n", lead, c.Store, state)
		lead = "               "
	}
	return b.String()
}

// ActorRefreshIn is the input of actor.refresh.
type ActorRefreshIn struct {
	Actor string `json:"actor,omitempty" arg:"0" desc:"the actor: act_ id, @handle or stable key (default: every actor with a copy here)"`
}

// ActorRefreshOut is what actor.refresh rewrote.
type ActorRefreshOut struct {
	Refreshed []profilestore.RefreshedCopy `json:"refreshed"`
}

// Text is one line per rewritten copy, or "(every copy is current)".
func (o ActorRefreshOut) Text() string {
	if len(o.Refreshed) == 0 {
		return "(every copy is current)\n"
	}
	var b strings.Builder
	for _, r := range o.Refreshed {
		if r.From != r.To {
			fmt.Fprintf(&b, "%s %s in %s: %q -> %q\n", work.Success, r.Actor, r.Store, r.From, r.To)
		} else {
			fmt.Fprintf(&b, "%s %s in %s: stamped from its source (%s)\n", work.Success, r.Actor, r.Store, r.To)
		}
	}
	return b.String()
}

// actorSourceOps is actor.refresh when the Env has the source seam.
func (o ops) actorSourceOps() []op.Entry {
	src, ok := upgrade.As[profilestore.ActorSources](o.env)
	if !ok {
		return nil
	}
	return []op.Entry{op.Define(op.Spec{Name: OpActorRefresh, Summary: "Rewrite stale copies of an actor from its source (the roster row in a team, your identity in solo)",
		Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
		Examples: []op.Example{{Title: "refresh every stale copy", CLI: "lore actor refresh", Want: ""}}},
		func(ctx context.Context, in ActorRefreshIn) (ActorRefreshOut, error) {
			var only []ids.AnyID
			if ref := strings.TrimSpace(in.Actor); ref != "" {
				id, err := o.actorRef(ctx, ref)
				if err != nil {
					return ActorRefreshOut{}, err
				}
				if id.IsZero() {
					return ActorRefreshOut{}, errs.New(errs.NotFound, "actor "+work.Quote(ref)+" not found").WithHint("use `lore actor list` to see available actor IDs")
				}
				only = []ids.AnyID{id}
			}
			got, err := src.RefreshActorCopies(ctx, only)
			return ActorRefreshOut{Refreshed: got}, err
		})}
}
