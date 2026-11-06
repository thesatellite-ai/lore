package wire

// coord_standalone_client.go — a linked client repo's own task on a host
// without its own coordination machinery (cmd/lore, P3-SIGCOORD-4): the
// v0.1.17 clientRows (saas coord_store.go) on the engine.
//
//	claim / renew / release / done / note of a client repo's own task
//	  └► the claim on the linked store's signals ref, folder of the link
//	     (forTask ─► useStore, coordWorld.client set)
//	  └► clientRows.Stage, agent details internal (the default, D8):
//	       task row  ─► the client repo: no claim log, attempts, work
//	                    branch, time box or time data (only claim_epoch;
//	                    the engine's client strip and ActorAs do the rest)
//	                 ─► the store: the internal linked row (derived id,
//	                    role client-link) holding what the client copy
//	                    leaves out
//	       a note    ─► a comment row in the linked store, never the client
//	  └► agent_details: client: every row to the client repo, as an own task
//
// Get merges the linked row back, so the fence, attempts and claim log see
// one task.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// clientLinkRole marks a store-side linked row: `task next` offers a role
// task only to `--role <role>`, so a linked row is never claimed as work
// (v0.1.17 clientLinkRole; the same value, so both binaries write one row).
const clientLinkRole = "client-link"

// clientLinkKeyPrefix + <client project>/<task> is a linked row's
// idempotency_key: the pointer from the internal row to the client task.
const clientLinkKeyPrefix = "client-task:"

// Task fields that stay off the client copy while agent details are
// internal and live on the linked row instead (D8, coordinator ruling
// 15:05). claim_epoch is not among them: the client copy keeps it, the
// bare fencing counter (engine clientKeptFields).
var clientLinkedKeys = []string{"attempts", "claim_log", fieldWorkBranch, "timebox_seconds", "time_entries", "time_open"}

// Fields of a new linked row.
const (
	fieldTaskProject     = "project_id"
	fieldTaskTitle       = "title"
	fieldTaskPriority    = "priority"
	fieldTaskCommitment  = "commitment"
	fieldTaskRole        = "role"
	fieldTaskIdempotency = "idempotency_key"
)

// clientRows is coord.Rows for a client repo's own task coordinated from
// the linked store (store is the attached name of the org store).
type clientRows struct {
	engineRows
	store    store.Source
	internal bool
}

var _ coord.Rows = clientRows{}

// clientRowsFor is the rows of a client task for world w (its org entry's
// agent_details decides internal).
func clientRowsFor(w *coordWorld) clientRows {
	internal := w.sit.Org == nil || w.sit.Org.EffectiveAgentDetails() != orgconf.AgentDetailsClient
	return clientRows{engineRows: engineRows{a: w.a}, store: w.orgStore, internal: internal}
}

// linkedID is the store-side linked row's id: derived from the client
// project and task, so every machine writes the same row (v0.1.17
// clientRows.linkedID).
func linkedID(project, task string) (string, error) {
	id, err := identity.Deterministic(ids.PrefixTask, clientLinkRole, project, task)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "derive the linked row id of "+task, err)
	}
	return id.String(), nil
}

// Get implements coord.Rows: the client row merged with its linked row.
func (r clientRows) Get(ctx context.Context, id string) (coord.Doc, bool, error) {
	d, ok, err := r.engineRows.Get(ctx, id)
	if err != nil || !ok || !r.internal || coord.IsMission(id) {
		return d, ok, err
	}
	lid, err := linkedID(d.Str(fieldTaskProject), id)
	if err != nil {
		return nil, false, err
	}
	link, found, err := r.engineRows.Get(ctx, lid)
	if err != nil {
		return nil, false, err
	}
	if found {
		for _, k := range clientLinkedKeys {
			if v, has := link[k]; has {
				d[k] = v
			}
		}
	}
	return d, true, nil
}

// Stage implements coord.Rows (v0.1.17 clientRows.Stage).
func (r clientRows) Stage(ctx context.Context, rows ...coord.RowWrite) ([]signals.Companion, error) {
	if !r.internal {
		return r.engineRows.Stage(ctx, rows...)
	}
	c := &engineCompanion{a: r.a, targets: map[string]store.Source{}}
	for _, w := range rows {
		switch rowTable(w) {
		case schema.TableComments:
			// A note stays internal: a comment in the linked store.
			c.rows = append(c.rows, w)
			c.targets[w.ID] = r.store
		case schema.TableTasks:
			lid, ld, err := r.linkedDoc(ctx, w.ID, w.Doc)
			if err != nil {
				return nil, err
			}
			c.rows = append(c.rows, coord.RowWrite{Table: string(schema.TableTasks), ID: lid, Doc: ld})
			c.targets[lid] = r.store
			c.rows = append(c.rows, coord.RowWrite{Table: w.Table, ID: w.ID, Doc: redactClient(w.Doc)})
		default:
			c.rows = append(c.rows, w)
		}
	}
	return []signals.Companion{c}, nil
}

// linkedDoc is the next version of task id's linked row: the previous one
// (or a new row naming the client task), with the title, status and the
// linked keys of full.
func (r clientRows) linkedDoc(ctx context.Context, id string, full coord.Doc) (string, coord.Doc, error) {
	project := full.Str(fieldTaskProject)
	lid, err := linkedID(project, id)
	if err != nil {
		return "", nil, err
	}
	prev, found, err := r.engineRows.Get(ctx, lid)
	if err != nil {
		return "", nil, err
	}
	d := coord.Doc{}
	if found {
		d = prev.Clone()
	} else {
		// Through the core the linked task names the client repo's
		// project, as `task add --to <store>` does (P2B-OLD-1).
		d.SetStr(fieldTaskProject, project)
		d.SetStr(fieldTaskPriority, schema.TaskPriorityMedium)
		d.SetStr(fieldTaskCommitment, schema.TaskCommitmentAccepted)
		d.SetStr(fieldTaskRole, clientLinkRole)
		d.SetStr(fieldTaskIdempotency, clientLinkKeyPrefix+project+"/"+id)
	}
	d.SetStr(fieldTaskTitle, full.Str(fieldTaskTitle))
	d.SetStr(fieldStatus, full.Status())
	for _, k := range clientLinkedKeys {
		if v, has := full[k]; has {
			d[k] = v
		} else {
			delete(d, k)
		}
	}
	return lid, d, nil
}

// redactClient is the client copy of a task document: the linked keys
// cleared (they live on the linked row). Actor ids and the other internal
// fields are the engine's (ActorAs, stripInternal).
func redactClient(d coord.Doc) coord.Doc {
	out := d.Clone()
	for _, k := range clientLinkedKeys {
		out[k] = canonjson.Null()
	}
	return out
}
