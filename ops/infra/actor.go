package infra

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// ActorListIn is the input of actor.list.
type ActorListIn struct {
	Store string `json:"store,omitempty" flag:"store" desc:"only actors of this store (default every attached store)"`
}

// Actor is one actor in v0.1.17's --json form (the ent Actor struct; the
// machine-local last_seen_at no longer travels in a row).
type Actor struct {
	ID          string          `json:"id"`
	CreatedAt   string          `json:"created_at,omitempty"`
	UpdatedAt   string          `json:"updated_at,omitempty"`
	Kind        string          `json:"kind"`
	DisplayName string          `json:"display_name"`
	StableKey   string          `json:"stable_key"`
	Handle      string          `json:"handle,omitempty"`
	Label       string          `json:"label,omitempty"`
	OwnerID     string          `json:"owner_actor_id,omitempty"`
	Active      bool            `json:"active"`
	LeftAt      string          `json:"left_at,omitempty"`
	Store       store.SourceRef `json:"_source,omitempty,omitzero"`
}

// ActorListOut is v0.1.17's list envelope.
type ActorListOut struct {
	SchemaVersion int     `json:"schema_version"`
	Kind          string  `json:"kind"`
	Count         int     `json:"count,omitempty"`
	Data          []Actor `json:"data"`
}

// Text is v0.1.17's list: "<id> [<kind>] <display name>".
func (o ActorListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no actors)\n"
	}
	var b strings.Builder
	for _, a := range o.Data {
		fmt.Fprintf(&b, "%s [%s] %s\n", a.ID, a.Kind, a.DisplayName)
	}
	return b.String()
}

// ActorShowOut is v0.1.17's show envelope.
type ActorShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Actor  `json:"data"`
}

// Text is v0.1.17's show block (last_seen is machine-local and gone).
func (o ActorShowOut) Text() string {
	a := o.Data
	return fmt.Sprintf("%s\n  kind:        %s\n  display:     %s\n  stable_key:  %s\n", a.ID, a.Kind, a.DisplayName, a.StableKey)
}

// actorOf converts a row of src into its v0.1.17 record.
func actorOf(lab work.Labeler, s work.Sourced) Actor {
	r := s.Row
	return Actor{
		ID: r.ID.String(), CreatedAt: timeOf(r, true), UpdatedAt: timeOf(r, false),
		Kind: work.Field(r, fieldKind), DisplayName: work.Field(r, fieldDisplayName), StableKey: work.Field(r, fieldStableKey),
		Handle: work.Field(r, fieldHandle), Label: work.Field(r, fieldLabel), OwnerID: work.Field(r, fieldOwner),
		Active: activeOf(r), LeftAt: work.TimeText(r, fieldLeftAt), Store: sourceOf(lab, s.Source),
	}
}

// activeOf is the active flag; a row without one is active (v0.1.17's
// ent default true).
func activeOf(r row.Row) bool { return work.Field(r, fieldActive) != "false" }

// actorList lists the live actors of every attached store, oldest first
// (v0.1.17 ordered by created_at); one actor seen in several stores (same
// deterministic id) is listed once, from the first store.
func (o ops) actorList(ctx context.Context, in ActorListIn) (ActorListOut, error) {
	rows, err := work.ListAll(ctx, o.env, schema.TableActors, in.Store)
	if err != nil {
		return ActorListOut{}, err
	}
	out := ActorListOut{SchemaVersion: SchemaVersion, Kind: KindActorList, Data: []Actor{}}
	seen := map[string]bool{}
	for _, s := range sortByCreated(rows) {
		if seen[s.Row.ID.String()] {
			continue
		}
		seen[s.Row.ID.String()] = true
		out.Data = append(out.Data, actorOf(work.LabelerOf(o.env), s))
	}
	out.Count = len(out.Data)
	return out, nil
}

// ActorShowIn is the input of actor.show.
type ActorShowIn struct {
	ID string `json:"id" arg:"0" desc:"the actor id"`
	// Store picks one store's copy of the actor row.
	Store string `json:"store,omitempty" flag:"store" desc:"show this store's copy of the actor (default: the first attached store that has it, the repo first)"`
}

// actorShow shows one actor by id (E_NOT_FOUND "actor "<id>" not found").
// An actor row is written into every store the person writes to under the
// same deterministic id (the actor row once per store), so the same id in
// the repo and the personal store `me` is one actor, not an ambiguity:
// without --store the first copy in store precedence order (the repo
// first) is shown; --store names one copy.
func (o ops) actorShow(ctx context.Context, in ActorShowIn) (ActorShowOut, error) {
	id, err := ids.ParseAny(strings.TrimSpace(in.ID))
	if err != nil || id.Prefix() != ids.PrefixActor {
		return ActorShowOut{}, errs.New(errs.NotFound, "actor "+work.Quote(in.ID)+" not found").
			WithHint("use `lore actor list` to see available actor IDs")
	}
	if in.Store == "" {
		src, r, gerr := work.Get(ctx, o.env, NounActor, schema.TableActors, id)
		if gerr == nil {
			return ActorShowOut{SchemaVersion: SchemaVersion, Kind: KindActorShow, Data: actorOf(work.LabelerOf(o.env), work.Sourced{Source: src, Row: r})}, nil
		}
		if !errs.Has(gerr, errs.AmbiguousID) {
			return ActorShowOut{}, gerr
		}
	}
	rows, err := work.ListAll(ctx, o.env, schema.TableActors, in.Store)
	if err != nil {
		return ActorShowOut{}, err
	}
	for _, s := range rows {
		if s.Row.ID == id {
			return ActorShowOut{SchemaVersion: SchemaVersion, Kind: KindActorShow, Data: actorOf(work.LabelerOf(o.env), s)}, nil
		}
	}
	if in.Store == "" {
		return ActorShowOut{}, errs.New(errs.NotFound, "actor "+work.Quote(in.ID)+" not found").
			WithHint("use `lore actor list` to see available actor IDs")
	}
	return ActorShowOut{}, errs.New(errs.NotFound, "actor "+work.Quote(in.ID)+" not found in store "+work.Quote(in.Store)).
		WithHint("use `lore actor list --store " + in.Store + "` to see that store's actors")
}
