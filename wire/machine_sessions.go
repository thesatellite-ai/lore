package wire

// machine_sessions.go — this machine's agent session rows through the
// engine (P3-LOCAL-5, P3-COORD-SESS-1, AGENT_IDENTITY 6.2): one sessions
// row per coding-agent, CI or LORE_SESSION session, found or created by
// (actor_id, tool, tool_session_id), in the machine store, so cmd/lore and
// the old binary read the same rows (`session list`, `agent start`,
// `task show --who`, the session-id leak rule).
//
//	FindOrCreateSession(project, d)
//	  ├─ a live row with the same (actor, tool, tool session) ─▶ ended_at = now,
//	  │    fill the fields still empty, merge sources/flags/outer tools
//	  └─ none ─▶ create (started_at = ended_at = now)
//	Sessions(limit) ─▶ newest ended_at first
//	KnownToolSessionIDs ─▶ raw ids >= 8 chars, synthetic keys left out
//
// The raw tool session id may be stored here because the row never leaves
// the machine (OQ14); the table marks it LoreRecord so the leak rule does
// not refuse the one row meant to hold it.

import (
	"context"
	"encoding/json"
	"github.com/thesatellite-ai/lore/logic/auditfill"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// SessionSyntheticPrefix starts the key lore makes when a tool exposes no
// session id (6.2: "synthetic:<tool>:<pid>"); it is lore's own, never a
// leak.
const SessionSyntheticPrefix = "synthetic:"

// minLeakSessionID is the shortest tool session id the leak rule checks
// (OV11-g: shorter values match ordinary text).
const minLeakSessionID = 8

// SessionDetails are a session's provenance fields (6.2). Every string is
// "" when unknown, never guessed (OQ8).
type SessionDetails struct {
	ActorID, Tool, ToolSessionID, ToolVersion, Model, MachineID, RunID string
	Repo, Branch, Cwd, Terminal, CI, ParentSessionID, Signal           string
	Sources                                                            map[string]string
	OuterTools                                                         []string
	DeclaredHuman                                                      bool
	Flags                                                              []string
}

// SessionRow is one stored session.
type SessionRow struct {
	ID        string
	ProjectID string
	SessionDetails
	StartedAt, EndedAt time.Time
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1),
	// read through auditfill.Defaults so an old row says unknown.
	CreatedBy, UpdatedBy string
}

// sessionTextFields maps each text field to its SessionDetails value.
func sessionTextFields(d SessionDetails) map[string]string {
	return map[string]string{
		schema.FieldSessionActorID: d.ActorID, schema.FieldSessionTool: d.Tool, schema.FieldSessionToolSessionID: d.ToolSessionID,
		schema.FieldSessionToolVersion: d.ToolVersion, schema.FieldSessionModel: d.Model, schema.FieldSessionMachineID: d.MachineID,
		schema.FieldSessionRunID: d.RunID, schema.FieldSessionRepo: d.Repo, schema.FieldSessionBranch: d.Branch,
		schema.FieldSessionCwd: d.Cwd, schema.FieldSessionTerminal: d.Terminal, schema.FieldSessionCI: d.CI,
		schema.FieldSessionParentSessionID: d.ParentSessionID, schema.FieldSessionSignal: d.Signal,
	}
}

// FindOrCreateSession returns the ses_ id of d's session in projectID,
// creating the row on first use and otherwise moving ended_at to now and
// filling the fields still empty; it never clears a filled field.
func (a *App) FindOrCreateSession(ctx context.Context, projectID string, d SessionDetails) (string, error) {
	// A session touch is the caller's bookkeeping, never part of the bulk
	// action whose command touched it: without this, `lore batch show`
	// listed the session row and `lore batch undo` put its ended_at back.
	ctx = engine.WithBatch(ctx, engine.Batch{})
	at := value.DateTimeFrom(a.opts.Wall())
	all, err := a.Sessions(ctx, 0)
	if err != nil {
		return "", err
	}
	var cur *SessionRow
	for i := range all {
		r := &all[i]
		if r.ActorID == d.ActorID && r.Tool == d.Tool && r.ToolSessionID == d.ToolSessionID && (cur == nil || r.StartedAt.Before(cur.StartedAt)) {
			cur = r
		}
	}
	if cur == nil {
		fields := map[string]value.Value{schema.FieldSessionStartedAt: at, schema.FieldSessionEndedAt: at}
		texts := sessionTextFields(d)
		texts[schema.FieldSessionProjectID] = projectID
		texts[schema.FieldSessionAgentKind] = d.Tool // the v0.1.17 column mirrors tool (6.2)
		if err = setTexts(fields, texts); err != nil {
			return "", err
		}
		if err = setSessionLists(fields, d.Sources, d.OuterTools, d.Flags); err != nil {
			return "", err
		}
		if d.DeclaredHuman {
			fields[schema.FieldSessionDeclaredHuman] = value.BoolOf(true)
		}
		w, cerr := a.Create(ctx, store.SourceMachine, schema.TableSessions, fields)
		if cerr != nil {
			return "", cerr
		}
		return w.Row.ID.String(), nil
	}
	patch := map[string]value.Value{schema.FieldSessionEndedAt: at}
	have := sessionTextFields(cur.SessionDetails)
	fill := map[string]string{}
	for name, give := range sessionTextFields(d) {
		if have[name] == "" && give != "" {
			fill[name] = give
		}
	}
	if err = setTexts(patch, fill); err != nil {
		return "", err
	}
	var sources map[string]string
	if len(d.Sources) > 0 {
		sources = maps.Clone(cur.Sources)
		if sources == nil {
			sources = map[string]string{}
		}
		for k, v := range d.Sources {
			if _, ok := sources[k]; !ok {
				sources[k] = v
			}
		}
	}
	var outer, flags []string
	if u := unionStrings(cur.OuterTools, d.OuterTools); len(u) != len(cur.OuterTools) {
		outer = u
	}
	if u := unionStrings(cur.Flags, d.Flags); len(u) != len(cur.Flags) {
		flags = u
	}
	if err = setSessionLists(patch, sources, outer, flags); err != nil {
		return "", err
	}
	ref, err := machineRef(cur.ID)
	if err != nil {
		return "", err
	}
	if _, err = a.Update(ctx, ref, patch); err != nil {
		return "", err
	}
	return cur.ID, nil
}

// Sessions returns this machine's sessions, newest ended_at first (ties by
// id); limit <= 0 returns all.
func (a *App) Sessions(ctx context.Context, limit int) ([]SessionRow, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := eng.List(ctx, store.SourceMachine, schema.TableSessions)
	if err != nil {
		return nil, err
	}
	out := make([]SessionRow, 0, len(rows))
	for _, r := range rows {
		if !r.Deleted {
			out = append(out, sessionRowOf(r))
		}
	}
	slices.SortFunc(out, func(x, y SessionRow) int {
		if c := y.EndedAt.Compare(x.EndedAt); c != 0 {
			return c
		}
		return strings.Compare(y.ID, x.ID)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// KnownToolSessionIDs lists every raw tool session id on this machine long
// enough to be a real id (the leak rule, AGENT_IDENTITY 9), synthetic keys
// left out.
func (a *App) KnownToolSessionIDs(ctx context.Context) ([]string, error) {
	all, err := a.Sessions(ctx, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range all {
		id := s.ToolSessionID
		if len(id) >= minLeakSessionID && !strings.HasPrefix(id, SessionSyntheticPrefix) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// sessionRowOf reads a sessions row.
func sessionRowOf(r row.Row) SessionRow {
	w, _ := auditfill.Defaults(r)
	s := SessionRow{ID: r.ID.String(), ProjectID: fieldText(r, schema.FieldSessionProjectID), CreatedBy: w.CreatedBy.String(), UpdatedBy: w.UpdatedBy.String()}
	d := &s.SessionDetails
	for name, dst := range map[string]*string{
		schema.FieldSessionActorID: &d.ActorID, schema.FieldSessionTool: &d.Tool, schema.FieldSessionToolSessionID: &d.ToolSessionID,
		schema.FieldSessionToolVersion: &d.ToolVersion, schema.FieldSessionModel: &d.Model, schema.FieldSessionMachineID: &d.MachineID,
		schema.FieldSessionRunID: &d.RunID, schema.FieldSessionRepo: &d.Repo, schema.FieldSessionBranch: &d.Branch,
		schema.FieldSessionCwd: &d.Cwd, schema.FieldSessionTerminal: &d.Terminal, schema.FieldSessionCI: &d.CI,
		schema.FieldSessionParentSessionID: &d.ParentSessionID, schema.FieldSessionSignal: &d.Signal,
	} {
		*dst = fieldText(r, name)
	}
	if b, ok := r.Fields[schema.FieldSessionDeclaredHuman].(value.Bool); ok {
		d.DeclaredHuman = b.Bool()
	}
	d.OuterTools = textItems(r.Fields[schema.FieldSessionOuterTools])
	d.Flags = textItems(r.Fields[schema.FieldSessionFlags])
	if j, ok := r.Fields[schema.FieldSessionSources].(value.JSON); ok {
		d.Sources = jsonStringMap(j)
	}
	s.StartedAt = fieldTime(r, schema.FieldSessionStartedAt)
	s.EndedAt = fieldTime(r, schema.FieldSessionEndedAt)
	return s
}

// fieldTime reads a datetime field: a DateTime, or the RFC 3339 text a
// row file decodes to before a schema-aware pass (zero when absent).
func fieldTime(r row.Row, name string) time.Time {
	switch v := r.Fields[name].(type) {
	case value.DateTime:
		return v.Time()
	case value.Text:
		if t, err := time.Parse(time.RFC3339Nano, v.String()); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// setSessionLists sets the sources object and the two lists when given.
func setSessionLists(fields map[string]value.Value, sources map[string]string, outer, flags []string) error {
	if len(sources) > 0 {
		j, err := jsonOfStringMap(sources)
		if err != nil {
			return err
		}
		fields[schema.FieldSessionSources] = j
	}
	for name, items := range map[string][]string{schema.FieldSessionOuterTools: outer, schema.FieldSessionFlags: flags} {
		if len(items) == 0 {
			continue
		}
		vals := make([]value.Value, 0, len(items))
		for _, it := range items {
			t, err := value.TextOf(it)
			if err != nil {
				return errs.Wrap(errs.InvalidInput, "wire: session "+name, err)
			}
			vals = append(vals, t)
		}
		l, err := value.ListOf(value.KindText, vals...)
		if err != nil {
			return errs.Wrap(errs.Internal, "wire: session "+name, err)
		}
		fields[name] = l
	}
	return nil
}

// textItems is the text items of a list value (nil otherwise).
func textItems(v value.Value) []string {
	l, ok := v.(value.List)
	if !ok {
		return nil
	}
	var out []string
	for _, it := range l.Items() {
		out = append(out, it.String())
	}
	return out
}

// unionStrings is have plus the values of add it lacks, in order.
func unionStrings(have, add []string) []string {
	out := slices.Clone(have)
	for _, x := range add {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// machineRef is the engine ref of a machine-local row id.
func machineRef(id string) (engine.Ref, error) {
	rid, err := ids.ParseAny(id)
	if err != nil {
		return engine.Ref{}, errs.Wrap(errs.InvalidID, "wire: session id", err)
	}
	return engine.Ref{Store: store.SourceMachine, Table: schema.TableSessions, ID: rid}, nil
}

// jsonOfStringMap is m as a JSON object value.
func jsonOfStringMap(m map[string]string) (value.JSON, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return value.JSON{}, errs.Wrap(errs.Internal, "wire: session sources", err)
	}
	return value.JSONOf(b)
}

// jsonStringMap reads a JSON object of strings (nil when it is not one).
func jsonStringMap(j value.JSON) map[string]string {
	var m map[string]string
	if err := json.Unmarshal(j.Raw(), &m); err != nil {
		return nil
	}
	return m
}
