// session_list.go — `lore session list` (AGENT_IDENTITY_DESIGN 6.2): the
// machine-local session rows with their provenance columns. The raw tool
// session id is shown only on a terminal outside CI (section 9, the
// quietOutput rule of whoami); otherwise it reads "<redacted>".
package main

import (
	"fmt"
	"time"

	"dbent/gen/ent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/sessions"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

// sessionListLimit is how many sessions the list shows, newest first.
const sessionListLimit = 50

// sessionRowJSON is one row of `lore session list --json`.
type sessionRowJSON struct {
	ID              string            `json:"id"`
	ActorID         string            `json:"actor_id,omitempty"`
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
	StartedAt       string            `json:"started_at"`
	LastWriteAt     string            `json:"last_write_at,omitempty"`
}

// redactToolSession hides a raw tool session id unless stdout is a
// terminal and CI is unset (section 9).
func redactToolSession(id string) string {
	if id == "" || !quietOutput() {
		return id
	}
	return redactedRepo
}

func sessionRowOf(s *ent.Session) sessionRowJSON {
	r := sessionRowJSON{ID: s.ID, Tool: s.Tool, ToolSessionID: redactToolSession(s.ToolSessionID), ToolVersion: s.ToolVersion,
		Model: s.Model, MachineID: s.MachineID, RunID: s.RunID, Repo: s.Repo, Branch: s.Branch, Cwd: s.Cwd,
		Terminal: s.Terminal, CI: s.Ci, ParentSessionID: redactToolSession(s.ParentSessionID), Signal: s.Signal,
		Sources: s.Sources, OuterTools: s.OuterTools, DeclaredHuman: s.DeclaredHuman, Flags: s.Flags,
		StartedAt: s.StartedAt.UTC().Format(time.RFC3339)}
	if s.ActorID != nil {
		r.ActorID = *s.ActorID
	}
	if s.EndedAt != nil {
		r.LastWriteAt = s.EndedAt.UTC().Format(time.RFC3339)
	}
	return r
}

func newSessionGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "session", Short: "Inspect this machine's agent sessions (local only, never synced)"}
	cmd.AddCommand(newSessionListCommand())
	return cmd
}

func newSessionListCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List agent sessions on this machine: tool, session, model, version, place",
		Example: `  lore session list
  lore session list --json`,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer joinClose(&err, client, "the project DB")
			rows, err := sessions.List(cmd.Context(), client, sessionListLimit)
			if err != nil {
				return errcodes.New(errcodes.Internal, "list sessions").WithCause(err)
			}
			out := make([]sessionRowJSON, 0, len(rows))
			for _, r := range rows {
				out = append(out, sessionRowOf(r))
			}
			if jsonOut {
				printJSON(constants.KindSessionList, out, len(out))
				return nil
			}
			if len(out) == 0 {
				fmt.Println(style.Muted("(no sessions)"))
				return nil
			}
			fmt.Printf("%-38s %-12s %-12s %-10s %-20s %-20s %s\n", "ID", "TOOL", "VERSION", "MODEL", "LAST WRITE", "PLACE", "TOOL SESSION")
			for _, r := range out {
				place := r.Repo
				if r.Branch != "" {
					place += "@" + r.Branch
				}
				fmt.Printf("%-38s %-12s %-12s %-10s %-20s %-20s %s\n", style.Code(r.ID), dash(r.Tool), dash(r.ToolVersion),
					dash(r.Model), dash(r.LastWriteAt), dash(place), dash(r.ToolSessionID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// dash shows "-" for an empty cell.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
