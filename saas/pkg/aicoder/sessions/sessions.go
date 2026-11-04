// Package sessions keeps the machine-local session rows of
// AGENT_IDENTITY_DESIGN 6.2: one row per coding-agent (or CI, or
// LORE_SESSION) session, found or created by (actor_id, tool,
// tool_session_id), linked from audit_logs.session_id (6.6).
//
// MACHINE-LOCAL (R11, OQ14, Q-LOCAL): the rows live in lore.db's
// `sessions` table, a telemetry table lsync never exports, so the raw tool
// session id, model, cwd and CI details never reach .lore/data, a store,
// the live ref or a client repo.
//
// Flow:
//
//	FindOrCreate(ctx, client, project, d)
//	  ├─ Ready? (the 0.1.18 columns exist; else "" — `lore setup` adds them)
//	  ├─ query (actor_id, tool, tool_session_id)
//	  │    ├─ found → ended_at = now, fill fields still empty, merge sources
//	  │    └─ none  → create (started_at = ended_at = now)
//	  └─ return lore's ses_ id
package sessions

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"dbent/gen/ent"
	entsession "dbent/gen/ent/session"
)

// SourceSessionHook is the source of every field `lore agent start
// --tool` fills (6.4).
const SourceSessionHook = "session_hook"

// SignalSessionHook is the signal of a row first created by the hook.
const SignalSessionHook = "session_hook"

// readyColumn is one 0.1.18 column; a lore.db without it was built by
// v0.1.17 and not migrated yet.
const readyColumn = "tool_session_id"

// Details are a session's provenance fields (6.2). Every string is ""
// when unknown — never guessed (OQ8).
type Details struct {
	ActorID         string
	Tool            string
	ToolSessionID   string
	ToolVersion     string
	Model           string
	MachineID       string
	RunID           string
	Repo            string
	Branch          string
	Cwd             string
	Terminal        string
	CI              string
	ParentSessionID string
	Signal          string
	Sources         map[string]string
	OuterTools      []string
	DeclaredHuman   bool
	Flags           []string
}

// Ready reports whether lore.db's sessions table has the 0.1.18 columns.
func Ready(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = ?`, readyColumn).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("sessions: read the columns: %w", err)
	}
	return n == 1, nil
}

// FindOrCreate returns the ses_ id of d's session, creating the row on
// first use and otherwise moving ended_at to now and filling the fields
// still empty (a value found later, such as the model from the hook,
// lands on the same row; 6.2). It never clears a filled field.
func FindOrCreate(ctx context.Context, c *ent.Client, projectID string, d Details) (string, error) {
	now := time.Now().UTC()
	cur, err := c.Session.Query().Where(
		entsession.ActorID(d.ActorID), entsession.Tool(d.Tool), entsession.ToolSessionID(d.ToolSessionID),
	).Order(ent.Asc(entsession.FieldCreatedAt)).First(ctx)
	if ent.IsNotFound(err) {
		cr := c.Session.Create().SetProjectID(projectID).SetStartedAt(now).SetEndedAt(now).
			SetActorID(d.ActorID).SetTool(d.Tool).SetToolSessionID(d.ToolSessionID).
			SetToolVersion(d.ToolVersion).SetModel(d.Model).SetMachineID(d.MachineID).SetRunID(d.RunID).
			SetRepo(d.Repo).SetBranch(d.Branch).SetCwd(d.Cwd).SetTerminal(d.Terminal).SetCi(d.CI).
			SetParentSessionID(d.ParentSessionID).SetSignal(d.Signal).SetDeclaredHuman(d.DeclaredHuman).
			SetSources(nonNilMap(d.Sources)).SetOuterTools(nonNil(d.OuterTools)).SetFlags(nonNil(d.Flags))
		if d.Tool != "" {
			cr.SetAgentKind(d.Tool) // the v0.1.17 column mirrors tool (6.2)
		}
		row, cerr := cr.Save(ctx)
		if cerr != nil {
			return "", fmt.Errorf("sessions: create: %w", cerr)
		}
		return row.ID, nil
	}
	if err != nil {
		return "", fmt.Errorf("sessions: find: %w", err)
	}
	up := cur.Update().SetEndedAt(now)
	fill := func(have, give string, set func(string) *ent.SessionUpdateOne) {
		if have == "" && give != "" {
			set(give)
		}
	}
	fill(cur.ToolVersion, d.ToolVersion, up.SetToolVersion)
	fill(cur.Model, d.Model, up.SetModel)
	fill(cur.MachineID, d.MachineID, up.SetMachineID)
	fill(cur.RunID, d.RunID, up.SetRunID)
	fill(cur.Repo, d.Repo, up.SetRepo)
	fill(cur.Branch, d.Branch, up.SetBranch)
	fill(cur.Cwd, d.Cwd, up.SetCwd)
	fill(cur.Terminal, d.Terminal, up.SetTerminal)
	fill(cur.Ci, d.CI, up.SetCi)
	fill(cur.ParentSessionID, d.ParentSessionID, up.SetParentSessionID)
	fill(cur.Signal, d.Signal, up.SetSignal)
	if len(d.Sources) > 0 {
		merged := maps.Clone(nonNilMap(cur.Sources))
		for k, v := range d.Sources {
			if _, ok := merged[k]; !ok {
				merged[k] = v
			}
		}
		up.SetSources(merged)
	}
	if extra := union(cur.Flags, d.Flags); len(extra) != len(cur.Flags) {
		up.SetFlags(extra)
	}
	if extra := union(cur.OuterTools, d.OuterTools); len(extra) != len(cur.OuterTools) {
		up.SetOuterTools(extra)
	}
	if _, err := up.Save(ctx); err != nil {
		return "", fmt.Errorf("sessions: update: %w", err)
	}
	return cur.ID, nil
}

// List returns the newest sessions first (limit <= 0 means all).
func List(ctx context.Context, c *ent.Client, limit int) ([]*ent.Session, error) {
	q := c.Session.Query().Order(ent.Desc(entsession.FieldEndedAt), ent.Desc(entsession.FieldID))
	if limit > 0 {
		q = q.Limit(limit)
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("sessions: list: %w", err)
	}
	return rows, nil
}

// minLeakID is the shortest tool session id the leak rule checks: shorter
// values (a counter, "1") would match ordinary text.
const minLeakID = 8

// KnownToolSessionIDs lists every raw tool session id on this machine
// that is long enough to be a real id (the leak rule of section 9). It
// returns nil on a lore.db without the 0.1.18 columns.
func KnownToolSessionIDs(ctx context.Context, db *sql.DB) ([]string, error) {
	ok, err := Ready(ctx, db)
	if err != nil || !ok {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT tool_session_id FROM sessions WHERE length(tool_session_id) >= ?`, minLeakID)
	if err != nil {
		return nil, fmt.Errorf("sessions: read the tool session ids: %w", err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("sessions: scan a tool session id: %w", err)
		}
		if !strings.HasPrefix(s, SyntheticPrefix) { // a synthetic key is lore's own, not the tool's
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

// SyntheticPrefix starts the key lore makes when a tool exposes no session
// id and LORE_SESSION is unset (6.2): "synthetic:<tool>:<pid>".
const SyntheticPrefix = "synthetic:"

// SyntheticKey is that key for tool and the agent's process id.
func SyntheticKey(tool string, pid int) string {
	return fmt.Sprintf("%s%s:%d", SyntheticPrefix, tool, pid)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// union is have plus the values of add it lacks, in order.
func union(have, add []string) []string {
	out := slices.Clone(nonNil(have))
	for _, a := range add {
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}
