// Package session defines `lore session list` (layer L5, CLI_REFERENCE
// 3.8, AGENT_IDENTITY 6.2, OV11): this machine's agent session rows with
// their provenance columns.
//
//	session.list ─▶ Env.SessionHost ─▶ Host.ListSessions(limit) ─▶ rows, newest first
//	             └▶ raw tool / parent session ids kept only when the caller is
//	                interactive AND Host.RevealToolSessions (a terminal, CI
//	                unset); otherwise "<redacted>" (OV11-f, the whoami rule)
//
// The session rows are machine-local and live in the machine store
// (P3-COORD-SESS-1); this is a Local operation over a host port: the App
// supplies one over the machine store, and a binary may supply its own
// (the old binary adds the audit counts; PASS3 ONE-PATH, P3-RUNS-4).
package session

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore session list`, REST /v1/sessions).
const (
	Noun   = "session"
	OpList = "session.list"
)

// ListLimit is how many sessions the list shows, newest first (v0.1.17
// sessionListLimit).
const ListLimit = 50

// Redacted replaces a hidden tool session id (whoami's redactedRepo).
const Redacted = "<redacted>"

// emptyCell shows an empty column; noSessions is the empty list's line.
const (
	emptyCell  = "-"
	noSessions = "(no sessions)"
)

// Row is one session row (AGENT_IDENTITY 6.2 fields), with the two counts
// the list adds (CLI_REFERENCE 3.8: WRITES, NEW KEYS).
type Row struct {
	ID string `json:"id"`
	// ActorID is the id the session's writes are stamped with (OV11-d);
	// Actor is how the list shows it ("tomas via codex"), the id when the
	// host cannot name it.
	ActorID         string            `json:"actor_id,omitempty"`
	Actor           string            `json:"actor,omitempty"`
	Tool            string            `json:"tool,omitempty"`
	ToolSessionID   string            `json:"tool_session_id,omitempty"`
	ToolVersion     string            `json:"tool_version,omitempty"`
	Model           string            `json:"model,omitempty"`
	MachineID       string            `json:"machine_id,omitempty"`
	RunID           string            `json:"run_id,omitempty"`
	Repo            string            `json:"repo,omitempty"`
	Branch          string            `json:"branch,omitempty"`
	Cwd             string            `json:"cwd,omitempty"`
	Terminal        string            `json:"terminal,omitempty"`
	CI              string            `json:"ci,omitempty"`
	ParentSessionID string            `json:"parent_session_id,omitempty"`
	Signal          string            `json:"signal,omitempty"`
	Sources         map[string]string `json:"sources,omitempty"`
	OuterTools      []string          `json:"outer_tools,omitempty"`
	DeclaredHuman   bool              `json:"declared_human,omitempty"`
	Flags           []string          `json:"flags,omitempty"`
	// StartedAt and LastWriteAt are RFC 3339 UTC ("" = unknown).
	StartedAt   string `json:"started_at"`
	LastWriteAt string `json:"last_write_at,omitempty"`
	// Writes is how many audit entries carry this session; NewKeys how
	// many distinct metadata keys those entries wrote (P3-RUNS-5).
	Writes  int `json:"writes"`
	NewKeys int `json:"new_keys"`
}

// Host is the machinery behind session.list, supplied by the binary that
// owns the machine-local database.
//
// Invariants: ListSessions returns raw ids (session.list redacts); it
// writes nothing.
type Host interface {
	// ListSessions returns up to limit sessions, newest last write first
	// (limit <= 0: all).
	ListSessions(ctx context.Context, limit int) ([]Row, error)
	// RevealToolSessions reports whether this process may show raw tool
	// session ids: stdout is a terminal and CI is unset (AGENT_IDENTITY 9).
	RevealToolSessions(ctx context.Context) bool
}

// Env is what session.list needs; *wire.App implements it.
type Env interface {
	// SessionHost is the binary's Host (E_UNSUPPORTED when it has none).
	SessionHost(ctx context.Context) (Host, error)
}

// ListIn is the input of session.list (no flags but --json).
type ListIn struct{}

// ListOut is the output of session.list.
type ListOut struct {
	Sessions []Row `json:"sessions"`
}

// Text implements op.Texter: the CLI_REFERENCE 3.8 table.
func (o ListOut) Text() string {
	if len(o.Sessions) == 0 {
		return noSessions + "\n"
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tACTOR\tTOOL\tTOOL SESSION\tSTARTED\tLAST WRITE\tWRITES\tNEW KEYS\tFLAGS") // a strings.Builder never fails
	for _, r := range o.Sessions {
		flags := append([]string(nil), r.Flags...)
		if r.DeclaredHuman && !slices.Contains(flags, flagDeclaredHuman) {
			flags = append(flags, flagDeclaredHuman)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n", r.ID, cell(r.Actor), cell(r.Tool), cell(r.ToolSessionID),
			cell(minute(r.StartedAt)), cell(minute(r.LastWriteAt)), r.Writes, r.NewKeys, strings.Join(flags, ","))
	}
	_ = tw.Flush()
	return b.String()
}

// flagDeclaredHuman is the FLAGS note of a LORE_HUMAN=1 session (the
// design's example row).
const flagDeclaredHuman = "declared_human"

// Ops returns the session operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpList, Summary: "List this machine's agent sessions: actor, tool, tool session, writes, new keys, flags (local only)",
			Permission: op.PermRead, Access: op.AccessRead, Local: true,
			Errors:   []errs.Code{errs.Unsupported, errs.Internal},
			Examples: []op.Example{{Title: "this machine's sessions", CLI: "lore session list", Want: ""}},
		}, func(ctx context.Context, _ ListIn) (ListOut, error) {
			h, err := env.SessionHost(ctx)
			if err != nil {
				return ListOut{}, err
			}
			c, _ := engine.CallerOf(ctx) // outside Run: not interactive, ids hidden
			return List(ctx, h, c.Interactive)
		}),
	}
}

// List is session.list over h; interactive is the caller's.
func List(ctx context.Context, h Host, interactive bool) (ListOut, error) {
	rows, err := h.ListSessions(ctx, ListLimit)
	if err != nil {
		return ListOut{}, errs.Wrap(errs.Internal, "list sessions", err)
	}
	reveal := interactive && h.RevealToolSessions(ctx)
	out := ListOut{Sessions: make([]Row, 0, len(rows))}
	for _, r := range rows {
		if !reveal {
			r.ToolSessionID, r.ParentSessionID = redact(r.ToolSessionID), redact(r.ParentSessionID)
		}
		if r.Actor == "" {
			r.Actor = r.ActorID
		}
		out.Sessions = append(out.Sessions, r)
	}
	return out, nil
}

// redact hides a non-empty id.
func redact(id string) string {
	if id == "" {
		return ""
	}
	return Redacted
}

// cell shows "-" for an empty cell.
func cell(s string) string {
	if s == "" {
		return emptyCell
	}
	return s
}

// minute shortens an RFC 3339 time to "2006-01-02 15:04" (the design's
// columns); anything else is shown as is.
func minute(t string) string {
	const dateLen, minuteLen = 10, 16
	if len(t) < minuteLen || t[dateLen] != 'T' {
		return t
	}
	return t[:dateLen] + " " + t[dateLen+1:minuteLen]
}
