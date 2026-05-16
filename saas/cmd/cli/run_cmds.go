// run_cmds.go — `lore run start / step / end / cancel / replay / show`
//
// What this is: a descriptive log of every agent attempt at a task. Mini
// does NOT execute runs — the agent (Claude Code / Cursor / human / hook)
// opens a run before starting work, appends steps as it goes, closes the
// run when done. The DB later answers "why did T-7 fail twice?", "how much
// did Opus cost us this week?", "show me the transcript of R-3"
//
// Schema is intentionally minimal:
//
//	runs(id, project_id, id, kind, status_str, actor_id,
//	     started_at, completed_at, notes)
//	run_steps(id, project_id, run_id, seq, kind, status_str, payload)
//
// Rich fields (task_id, mission_id, agent_kind, model_id, retry_of_run_id,
// tokens_in, tokens_out, cost, outcome, summary, error_message) are packed
// into `notes` / `payload` as JSON. A future migration can unpack them into
// typed columns; the CLI surface stays the same
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"saas/pkg/constants"
	"time"

	"dbent/gen/ent"
	entRunStep "dbent/gen/ent/runstep"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

// runNotes is the JSON shape stored in runs.notes
type runNotes struct {
	TaskID       string  `json:"task_id,omitempty"`
	MissionID    string  `json:"mission_id,omitempty"`
	AgentKind    string  `json:"agent_kind,omitempty"`
	ModelID      string  `json:"model_id,omitempty"`
	RetryOfRunID string  `json:"retry_of_run_id,omitempty"`
	Goal         string  `json:"goal,omitempty"`
	Outcome      string  `json:"outcome,omitempty"`
	Summary      string  `json:"summary,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
	TokensIn     int     `json:"tokens_in,omitempty"`
	TokensOut    int     `json:"tokens_out,omitempty"`
	Cost         float64 `json:"cost,omitempty"`
}

// stepPayload is the JSON shape stored in run_steps.payload
type stepPayload struct {
	Name       string  `json:"name,omitempty"`
	Summary    string  `json:"summary,omitempty"`
	Body       string  `json:"body,omitempty"`
	TokensIn   int     `json:"tokens_in,omitempty"`
	TokensOut  int     `json:"tokens_out,omitempty"`
	Cost       float64 `json:"cost,omitempty"`
	DurationMS int64   `json:"duration_ms,omitempty"`
}

// expandRunGroup extends the existing run cobra group (in generated_commands.go)
// with the start/step/end/cancel/replay/show subcommands. Called from main.go
// after newRunGroup() registers `list`
func attachRunWriters(parent *cobra.Command) {
	parent.AddCommand(newRunStartCommand())
	parent.AddCommand(newRunStepCommand())
	parent.AddCommand(newRunEndCommand())
	parent.AddCommand(newRunCancelCommand())
	parent.AddCommand(newRunShowCommand())
	parent.AddCommand(newRunReplayCommand())
}

// ── run start ────────────────────────────────────────────────────────────

func newRunStartCommand() *cobra.Command {
	var f commonFlags
	var task, mission, model, agent, retryOf, goal, kind string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Open a run (records what agent is attempting which task with which model)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, "")
			if err != nil {
				return err
			}

			// Resolve T-N → opaque IDs so notes payload is clickable later
			var taskID, missionID string
			if task != "" {
				resolvedTask, err := resolvePrettyID(cmd.Context(), client, task)
				if err == nil {
					taskID = resolvedTask
				} else {
					taskID = task // pass through as-is
				}
			}
			if mission != "" {
				resolvedMission, err := resolvePrettyID(cmd.Context(), client, mission)
				if err == nil {
					missionID = resolvedMission
				} else {
					missionID = mission
				}
			}

			actorID, err := resolveCurrentActorID(cmd.Context(), client)
			if err != nil {
				return err
			}

			notes := runNotes{
				TaskID: taskID, MissionID: missionID,
				AgentKind: agent, ModelID: model, RetryOfRunID: retryOf, Goal: goal,
			}
			notesJSON, _ := json.Marshal(notes)
			runKind := kind
			if runKind == "" {
				if taskID != "" {
					runKind = "task"
				} else {
					runKind = "general"
				}
			}

			r, err := client.Run.Create().
				SetProjectID(projectID).
				SetKind(runKind).
				SetStatusStr("in_progress").
				SetActorID(actorID).
				SetStartedAt(time.Now()).
				SetNotes(string(notesJSON)).
				Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create run").WithCause(err)
			}
			fmt.Printf("%s %s %s started\n", style.Success("✓"), r.ID, style.Code(r.ID))
			fmt.Println(r.ID) // bare ID on stdout for scripting
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&task, "task", "", "task ID (T-N or opaque tsk_*)")
	cmd.Flags().StringVar(&mission, constants.FlagMission, "", "mission ID (MS-N or opaque msn_*)")
	cmd.Flags().StringVar(&model, constants.FlagModel, "", "model identifier (e.g. claude-opus-4-7)")
	cmd.Flags().StringVar(&agent, constants.FlagAgent, "", "agent kind: claude-code | cursor | aider | codex-cli | manual")
	cmd.Flags().StringVar(&retryOf, "retry-of", "", "previous run-id this is retrying")
	cmd.Flags().StringVar(&goal, constants.FlagGoal, "", "free-form description of what the run is trying to do")
	cmd.Flags().StringVar(&kind, constants.FlagKind, "", "run kind (default: 'task' if --task given, else 'general')")
	return cmd
}

// ── run step ─────────────────────────────────────────────────────────────

func newRunStepCommand() *cobra.Command {
	var f commonFlags
	var kind, name, summary, body string
	var passed bool
	var passedSet bool
	var tokensIn, tokensOut int
	var cost float64
	var durationMS int64
	var payloadStdin bool
	cmd := &cobra.Command{
		Use:   "step <run-id>",
		Short: "Append a step to a run (prompt / tool / verify / reflect / decide / error / note)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			passedSet = cmd.Flags().Changed("passed")
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			runID := args[0]
			run, err := client.Run.Get(cmd.Context(), runID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, "run "+runID+" not found")
			}

			// Stdin payload trumps --summary if both passed
			if payloadStdin {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "read stdin").WithCause(err)
				}
				body = string(b)
			}

			seq := nextRunStepSeq(cmd.Context(), client, runID)
			payload := stepPayload{
				Name: name, Summary: summary, Body: body,
				TokensIn: tokensIn, TokensOut: tokensOut, Cost: cost, DurationMS: durationMS,
			}
			payloadJSON, _ := json.Marshal(payload)

			status := "pending"
			if passedSet {
				if passed {
					status = "passed"
				} else {
					status = "failed"
				}
			}
			if kind == "" {
				kind = "note"
			}

			s, err := client.RunStep.Create().
				SetProjectID(run.ProjectID).
				SetRunID(runID).
				SetSeq(seq).
				SetKind(kind).
				SetStatusStr(status).
				SetPayload(string(payloadJSON)).
				Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create step").WithCause(err)
			}
			fmt.Printf("%s step #%d [%s/%s] %s\n", style.Success("✓"), s.Seq, kind, status, style.Code(s.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&kind, constants.FlagKind, "", "prompt | tool | verify | reflect | decide | error | note (default: note)")
	cmd.Flags().StringVar(&name, constants.FlagName, "", "short name for the step (e.g. tool name, prompt label)")
	cmd.Flags().StringVar(&summary, "summary", "", "one-line description")
	cmd.Flags().BoolVar(&passed, "passed", false, "for verify steps: did the check pass?")
	cmd.Flags().IntVar(&tokensIn, "tokens-in", 0, "input tokens consumed")
	cmd.Flags().IntVar(&tokensOut, "tokens-out", 0, "output tokens generated")
	cmd.Flags().Float64Var(&cost, "cost", 0, "cost in USD")
	cmd.Flags().Int64Var(&durationMS, "duration-ms", 0, "wall clock for this step")
	cmd.Flags().BoolVar(&payloadStdin, "payload-stdin", false, "read full step body from stdin")
	return cmd
}

func nextRunStepSeq(ctx context.Context, client *ent.Client, runID string) int {
	last, _ := client.RunStep.Query().
		Where(entRunStep.RunID(runID)).
		Order(ent.Desc(entRunStep.FieldSeq)).
		Limit(1).
		All(ctx)
	if len(last) == 0 {
		return 1
	}
	return last[0].Seq + 1
}

// ── run end ──────────────────────────────────────────────────────────────

func newRunEndCommand() *cobra.Command {
	var f commonFlags
	var outcome, summary, errMsg string
	var tokensIn, tokensOut int
	var cost float64
	cmd := &cobra.Command{
		Use:   "end <run-id>",
		Short: "Close a run with outcome (success | partial | failed | cancelled)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			if outcome == "" {
				return errcodes.New(errcodes.InvalidInput,
					"--outcome required: success | partial | failed | cancelled")
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			runID := args[0]
			run, err := client.Run.Get(cmd.Context(), runID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, "run "+runID+" not found")
			}

			// Merge into existing notes
			notes := runNotes{}
			if run.Notes != nil {
				_ = json.Unmarshal([]byte(*run.Notes), &notes)
			}
			notes.Outcome = outcome
			if summary != "" {
				notes.Summary = summary
			}
			if errMsg != "" {
				notes.ErrorMessage = errMsg
			}
			// Sum tokens / cost from steps if not given
			if tokensIn == 0 && tokensOut == 0 && cost == 0 {
				inSum, outSum, costSum := sumRunStepCosts(cmd.Context(), client, runID)
				notes.TokensIn = inSum
				notes.TokensOut = outSum
				notes.Cost = costSum
			} else {
				notes.TokensIn = tokensIn
				notes.TokensOut = tokensOut
				notes.Cost = cost
			}
			notesJSON, _ := json.Marshal(notes)

			upd := client.Run.UpdateOneID(runID).
				SetStatusStr(outcome).
				SetCompletedAt(time.Now()).
				SetNotes(string(notesJSON))
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "close run").WithCause(err)
			}
			elapsed := time.Since(run.StartedAt.UTC()).Round(time.Second)
			fmt.Printf("%s %s [%s] (%s)\n",
				style.Success("✓"), run.ID, outcome, elapsed)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&outcome, "outcome", "", "success | partial | failed | cancelled (required)")
	cmd.Flags().StringVar(&summary, "summary", "", "human summary of what happened")
	cmd.Flags().StringVar(&errMsg, constants.FlagError, "", "error message (for failed/partial outcomes)")
	cmd.Flags().IntVar(&tokensIn, "tokens-in", 0, "total input tokens (default: sum of steps)")
	cmd.Flags().IntVar(&tokensOut, "tokens-out", 0, "total output tokens (default: sum of steps)")
	cmd.Flags().Float64Var(&cost, "cost", 0, "total cost USD (default: sum of steps)")
	_ = cmd.MarkFlagRequired("outcome")
	return cmd
}

func sumRunStepCosts(ctx context.Context, client *ent.Client, runID string) (int, int, float64) {
	steps, _ := client.RunStep.Query().
		Where(entRunStep.RunID(runID)).
		All(ctx)
	var tIn, tOut int
	var cost float64
	for _, s := range steps {
		if s.Payload == nil {
			continue
		}
		var p stepPayload
		if err := json.Unmarshal([]byte(*s.Payload), &p); err == nil {
			tIn += p.TokensIn
			tOut += p.TokensOut
			cost += p.Cost
		}
	}
	return tIn, tOut, cost
}

// ── run cancel ───────────────────────────────────────────────────────────

func newRunCancelCommand() *cobra.Command {
	var f commonFlags
	var reason string
	cmd := &cobra.Command{
		Use:   "cancel <run-id>",
		Short: "Cancel a run (alias for `end --outcome=cancelled`)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			run, err := client.Run.Get(cmd.Context(), args[0])
			if err != nil {
				return errcodes.New(errcodes.NotFound, "run "+args[0]+" not found")
			}
			notes := runNotes{}
			if run.Notes != nil {
				_ = json.Unmarshal([]byte(*run.Notes), &notes)
			}
			notes.Outcome = "cancelled"
			if reason != "" {
				notes.Summary = reason
			}
			notesJSON, _ := json.Marshal(notes)
			if _, err := client.Run.UpdateOneID(run.ID).
				SetStatusStr("cancelled").
				SetCompletedAt(time.Now()).
				SetNotes(string(notesJSON)).
				Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "cancel run").WithCause(err)
			}
			fmt.Printf("%s %s cancelled\n", style.Success("✓"), run.ID)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&reason, "reason", "", "why cancelled")
	return cmd
}

// ── run show ─────────────────────────────────────────────────────────────

func newRunShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <run-id>",
		Short: "Show a run's metadata (status, outcome, tokens, cost)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			run, err := client.Run.Get(cmd.Context(), args[0])
			if err != nil {
				return errcodes.New(errcodes.NotFound, "run "+args[0]+" not found")
			}
			notes := runNotes{}
			if run.Notes != nil {
				_ = json.Unmarshal([]byte(*run.Notes), &notes)
			}
			stepCount, _ := client.RunStep.Query().Where(entRunStep.RunID(run.ID)).Count(cmd.Context())
			if jsonOut {
				printJSON(constants.KindRunShow, map[string]any{
					"id": run.ID, "kind": run.Kind, "status": run.StatusStr,
					"started_at": run.StartedAt, "completed_at": derefTime(run.CompletedAt),
					"step_count": stepCount, "notes": notes,
				}, 0)
				return nil
			}
			fmt.Printf("%s %s\n", run.ID, style.Code(run.ID))
			fmt.Printf("  kind:     %s\n", run.Kind)
			fmt.Printf("  status:   %s\n", run.StatusStr)
			fmt.Printf("  started:  %s\n", run.StartedAt.Format(time.RFC3339))
			if run.CompletedAt != nil && run.StartedAt != nil {
				fmt.Printf("  ended:    %s (took %s)\n",
					run.CompletedAt.Format(time.RFC3339),
					run.CompletedAt.Sub(*run.StartedAt).Round(time.Second))
			}
			if notes.TaskID != "" {
				fmt.Printf("  task:     %s\n", notes.TaskID)
			}
			if notes.ModelID != "" {
				fmt.Printf("  model:    %s\n", notes.ModelID)
			}
			if notes.AgentKind != "" {
				fmt.Printf("  agent:    %s\n", notes.AgentKind)
			}
			if notes.Goal != "" {
				fmt.Printf("  goal:     %s\n", notes.Goal)
			}
			if notes.Outcome != "" {
				fmt.Printf("  outcome:  %s\n", notes.Outcome)
			}
			if notes.Summary != "" {
				fmt.Printf("  summary:  %s\n", notes.Summary)
			}
			if notes.ErrorMessage != "" {
				fmt.Printf("  error:    %s\n", notes.ErrorMessage)
			}
			fmt.Printf("  steps:    %d\n", stepCount)
			if notes.TokensIn+notes.TokensOut > 0 {
				fmt.Printf("  tokens:   in=%d  out=%d\n", notes.TokensIn, notes.TokensOut)
			}
			if notes.Cost > 0 {
				fmt.Printf("  cost:     $%.4f\n", notes.Cost)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// ── run replay ───────────────────────────────────────────────────────────

func newRunReplayCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "replay <run-id>",
		Short: "Print the full step-by-step transcript of a run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			run, err := client.Run.Get(cmd.Context(), args[0])
			if err != nil {
				return errcodes.New(errcodes.NotFound, "run "+args[0]+" not found")
			}
			steps, err := client.RunStep.Query().
				Where(entRunStep.RunID(run.ID)).
				Order(ent.Asc(entRunStep.FieldSeq)).
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "fetch steps").WithCause(err)
			}
			if jsonOut {
				out := make([]map[string]any, 0, len(steps))
				for _, s := range steps {
					var p stepPayload
					if s.Payload != nil {
						_ = json.Unmarshal([]byte(*s.Payload), &p)
					}
					out = append(out, map[string]any{
						"seq": s.Seq, "kind": s.Kind, "status": s.StatusStr,
						"payload": p,
					})
				}
				printJSON(constants.KindRunReplay, map[string]any{
					"run_id": run.ID, "steps": out,
				}, len(out))
				return nil
			}
			fmt.Printf("%s  %s  [%s]\n", run.ID, style.Code(run.ID), run.StatusStr)
			fmt.Println()
			for _, s := range steps {
				var p stepPayload
				if s.Payload != nil {
					_ = json.Unmarshal([]byte(*s.Payload), &p)
				}
				header := fmt.Sprintf("#%d [%s/%s]", s.Seq, s.Kind, s.StatusStr)
				if p.Name != "" {
					header += "  " + p.Name
				}
				fmt.Println(style.Code(header))
				if p.Summary != "" {
					fmt.Println("  " + p.Summary)
				}
				if p.Body != "" {
					fmt.Println()
					fmt.Println(p.Body)
				}
				if p.TokensIn+p.TokensOut > 0 || p.Cost > 0 || p.DurationMS > 0 {
					fmt.Printf("  meta: tokens-in=%d tokens-out=%d cost=$%.4f duration=%dms\n",
						p.TokensIn, p.TokensOut, p.Cost, p.DurationMS)
				}
				fmt.Println()
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}
