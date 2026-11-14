package wire

// doctor_teamnames.go — how doctor's "Team versions" notes name a writer,
// its machine and a save (CLI_REFERENCE 3.7): the person's handle (else
// display name), the machine's label (else host name) and the save's UTC
// date, not the raw act_ / mch_ ids and a nanosecond timestamp. An id
// shows only when nothing names it.
//
//	writer  → a named actors row in any attached store
//	        → this machine's own identities (the caller, the person it acts
//	          for, and each detected agent of that person in a tool this
//	          machine's sessions rows name): their only saves may be
//	          machine-local rows, and the machine store holds no actors
//	          rows (P3-LOCAL-1), so no store names them
//	        → the id as stamped
//	machine → a machines row's label, else hostname (any attached store),
//	          then this machine's own profile → mch_ + 8 hex

import (
	"context"
	"path/filepath"
	"time"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// unknownWriter names a save whose row records no writer (rows lore itself
// seeds at init, such as policies and repos, carry no created_by).
const unknownWriter = "(no writer recorded)"

// viaTool joins a person's name and a tool in a detected agent's name, as
// the agent's own display name reads ("ana via claude_code", 6.1).
const viaTool = " via "

// teamActorNamer returns the writer-name function of one doctor run: each
// actor id is looked up once (see the file comment for the order).
func (a *App) teamActorNamer(ctx context.Context) func(string) string {
	st, err := a.state(ctx)
	seen := map[string]string{}
	var local map[string]string
	return func(actor string) string {
		if actor == "" {
			return unknownWriter
		}
		if n, ok := seen[actor]; ok {
			return n
		}
		n := ""
		if err == nil {
			if id, perr := ids.Parse[ids.Actor](actor); perr == nil {
				n = namedActorRow(ctx, st, id)
			}
			if n == "" {
				if local == nil {
					local = a.localActorNames(ctx, st)
				}
				n = local[actor]
			}
		}
		if n == "" {
			n = actor
		}
		seen[actor] = n
		return n
	}
}

// namedActorRow is the name of id's live actors row in the first attached
// store whose row names it (a store that holds only a nameless row, or
// cannot be read, does not stop the search); "" when none does.
func namedActorRow(ctx context.Context, st *state, id ids.ID[ids.Actor]) string {
	for _, name := range st.eng.Stores() {
		_, r, err := st.eng.Get(ctx, engine.Ref{Store: name, Table: schema.TableActors, ID: id.Any()})
		if err != nil || r.Deleted {
			continue
		}
		if n := actorDisplayName(r, ""); n != "" {
			return n
		}
	}
	return ""
}

// localActorNames names the identities this machine writes as, by actor
// id: the caller, the person it acts for, and that person's detected agent
// in every tool this machine's sessions rows name. A detected agent's
// session hook (`lore agent start`) saves only its machine-local session
// row, so without this its id was the one writer doctor could not name.
func (a *App) localActorNames(ctx context.Context, st *state) map[string]string {
	out := map[string]string{}
	defer a.addUnscopedActorName(ctx, out)
	person := st.who
	if person.Owner != nil {
		person = *person.Owner
	}
	if person.StableKey == "" {
		return out
	}
	pid, err := person.ActorID()
	if err != nil {
		return out
	}
	pname := namedActorRow(ctx, st, pid)
	if pname == "" {
		pname = person.Handle
	}
	if pname == "" {
		pname = person.DisplayName
	}
	if pname == "" {
		return out
	}
	out[pid.String()] = pname
	if !st.actor.IsZero() && st.who.DisplayName != "" {
		out[st.actor.String()] = st.who.DisplayName
	}
	sessions, err := a.Sessions(ctx, 0)
	if err != nil {
		return out
	}
	for _, s := range sessions {
		if s.Tool == "" {
			continue
		}
		agents, aerr := identity.DetectedAgentIDs(pid, s.Tool)
		if aerr != nil {
			continue
		}
		for _, id := range agents {
			if _, ok := out[id.String()]; !ok {
				out[id.String()] = pname + viaTool + s.Tool
			}
		}
	}
	return out
}

// unscopedNote marks the identity this machine writes as outside every
// setup's folders and org: lore setup steps run there (in the home folder,
// before the folders step) stamp their machine-local rows with it, and no
// store holds an actors row for it, so doctor names it instead of showing
// an id `lore actor show` cannot find (I-3).
const unscopedNote = ", outside any setup"

// addUnscopedActorName adds to out (when absent) the identity this machine
// resolves outside any setup or org: the chain with no org entry and no
// setup, run in the folder holding the lore home, named "<name> (<step>,
// outside any setup)". A chain that does not resolve adds nothing.
func (a *App) addUnscopedActorName(ctx context.Context, out map[string]string) {
	mc, err := readMachineConfig(a.opts.Home)
	if err != nil {
		return
	}
	r, err := identity.Resolve(ctx, identity.Inputs{Home: a.opts.Home, Config: mc.Config, Dir: filepath.Dir(a.opts.Home)})
	if err != nil || r.StableKey == "" {
		return
	}
	id, err := r.ActorID()
	if err != nil {
		return
	}
	if _, ok := out[id.String()]; ok {
		return
	}
	name := r.DisplayName
	if name == "" {
		name = r.StableKey
	}
	out[id.String()] = name + " (" + string(r.Step) + unscopedNote + ")"
}

// teamMachineNamer returns the machine-name function of one doctor run:
// a machines row's label, else its hostname, from any attached store,
// then this machine's own profile; else mch_ + 8 hex (shortMachine).
func (a *App) teamMachineNamer(ctx context.Context) func(string) string {
	names := map[string]string{}
	if st, err := a.state(ctx); err == nil {
		for _, name := range st.eng.Stores() {
			rows, lerr := st.eng.List(ctx, name, schema.TableMachines)
			if lerr != nil {
				continue
			}
			for _, r := range rows {
				id := fieldText(r, schema.FieldMachineID)
				if r.Deleted || id == "" || names[id] != "" {
					continue
				}
				names[id] = machineDisplayName(r)
			}
		}
	}
	if me, err := a.ThisMachine(ctx); err == nil && !me.ID.IsZero() && names[me.ID.String()] == "" {
		names[me.ID.String()] = machineDisplayName(row.Row{Fields: me.Fields})
	}
	return func(id string) string {
		if n := names[id]; n != "" {
			return n
		}
		return shortMachine(id)
	}
}

// machineFieldHostname is the machines row's host name column.
const machineFieldHostname = "hostname"

// machineDisplayName is a machines row's label, else its hostname, else "".
func machineDisplayName(r row.Row) string {
	if l := fieldText(r, schema.FieldMachineLabel); l != "" {
		return l
	}
	return fieldText(r, machineFieldHostname)
}

// actorDisplayName is the handle of actors row r, else its display name,
// else fallback.
func actorDisplayName(r row.Row, fallback string) string {
	for _, k := range []string{actorFieldHandle, actorFieldDisplay} {
		if v := r.Fields[k]; v != nil {
			if s := v.String(); s != "" {
				return s
			}
		}
	}
	return fallback
}

// saveDate is a `_w` save time (RFC 3339) as its UTC date; text that is
// not a time is shown as it is.
func saveDate(at string) string {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return at
	}
	return t.UTC().Format(time.DateOnly)
}
