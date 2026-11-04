// generated_reminder.go — the reminder command group, moved verbatim out of generated_commands.go so each
// phase 5 noun group owns its own file.
package main

import (
	"fmt"
	"saas/pkg/constants"
	"strings"
	"time"

	"dbent/gen/ent"
	entReminder "dbent/gen/ent/reminder"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

	"github.com/spf13/cobra"
)

func newReminderGroup() *cobra.Command {
	cmd := &cobra.Command{Use: "reminder", Short: "Manage time-based reminders"}
	cmd.AddCommand(newReminderListCommand())
	cmd.AddCommand(newReminderAddCommand())
	cmd.AddCommand(newReminderDoneCommand())
	cmd.AddCommand(newDeleteCommand(reminderDeleteTarget))
	return cmd
}

func newReminderListCommand() *cobra.Command {
	var f commonFlags
	var showDone, jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List pending reminders (or --done to see completed)",
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			q := client.Reminder.Query().Where(entReminder.ProjectID(projectID))
			if showDone {
				q = q.Where(entReminder.DoneAtNotNil())
			} else {
				q = q.Where(entReminder.DoneAtIsNil())
			}
			rows, err := q.Order(ent.Asc(entReminder.FieldDueAt)).Limit(100).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list reminders").WithCause(err)
			}
			if jsonOut {
				out := make([]map[string]any, 0, len(rows))
				for _, r := range rows {
					row := map[string]any{
						"id": r.ID, "due_at": r.DueAt.Format(time.RFC3339),
						"message": r.Message,
					}
					if r.Recurrence != nil {
						row["recurrence"] = string(*r.Recurrence)
					}
					if r.DoneAt != nil {
						row["done_at"] = r.DoneAt.Format(time.RFC3339)
					}
					out = append(out, row)
				}
				printJSON(constants.KindReminderList, out, len(out))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println(style.Muted("(no reminders)"))
				return nil
			}
			now := cliClock().Now()
			for _, r := range rows {
				marker := " "
				if r.DueAt.Before(now) && r.DoneAt == nil {
					marker = style.Error("!")
				}
				rec := ""
				if r.Recurrence != nil {
					rec = " (" + string(*r.Recurrence) + ")"
				}
				fmt.Printf("%s %s  %s%s\n", marker, r.DueAt.Format("2006-01-02"), r.Message, rec)
				fmt.Printf("    %s\n", style.Code(r.ID))
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&showDone, "done", false, "show completed instead of pending")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

func newReminderAddCommand() *cobra.Command {
	var f commonFlags
	var due, recurrence, targetTable, targetID, createdBy string
	cmd := &cobra.Command{
		Use:   "add <message>",
		Short: "Add a reminder (--due YYYY-MM-DD)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			msg := strings.Join(args, " ")
			t, err := time.Parse("2006-01-02", due)
			if err != nil {
				t, err = time.Parse(time.RFC3339, due)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "bad --due (want YYYY-MM-DD)").WithCause(err)
				}
			}
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			create := client.Reminder.Create().SetProjectID(projectID).SetDueAt(t).SetMessage(msg)
			if recurrence != "" {
				rec := entReminder.Recurrence(recurrence)
				if err := entReminder.RecurrenceValidator(rec); err != nil {
					return errcodes.New(errcodes.InvalidInput,
						fmt.Sprintf("bad --recurrence %q (want %s)", recurrence,
							strings.Join(allRecurrenceValues(), " | ")))
				}
				create.SetRecurrence(rec)
			}
			if targetTable != "" {
				create.SetTargetTable(targetTable)
			}
			if targetID != "" {
				create.SetTargetID(targetID)
			}
			actorID, err := resolveCreatedBy(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			create.SetCreatedByActorID(actorID)
			r, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create reminder").WithCause(err)
			}
			fmt.Printf("%s reminder %s due %s\n", style.Success("✓"), style.Code(r.ID), t.Format("2006-01-02"))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&due, constants.FlagDue, "", "due date YYYY-MM-DD or RFC3339 (required)")
	cmd.Flags().StringVar(&recurrence, "recurrence", "", "recurrence: "+strings.Join(allRecurrenceValues(), " | "))
	cmd.Flags().StringVar(&targetTable, constants.FlagOnTable, "", "optional target entity table")
	cmd.Flags().StringVar(&targetID, constants.FlagOnID, "", "optional target entity id")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id (act_*); defaults to current identity")
	_ = cmd.MarkFlagRequired(constants.FlagDue)
	return cmd
}

func newReminderDoneCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Mark a reminder done (recurring → reschedules)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			_prettyID, perr := resolvePrettyID(cmd.Context(), client, args[0])

			if perr != nil {
				return errcodes.New(errcodes.NotFound, perr.Error())
			}

			r, err := client.Reminder.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("reminder %q not found", args[0]))
			}
			if r.Recurrence != nil && *r.Recurrence != "" {
				next := bumpRecurrence(r.DueAt, *r.Recurrence)
				_, err = client.Reminder.UpdateOne(r).SetDueAt(next).Save(cmd.Context())
				if err != nil {
					return errcodes.New(errcodes.Internal, "reschedule reminder").WithCause(err)
				}
				fmt.Printf("%s rescheduled to %s\n", style.Success("✓"), next.Format("2006-01-02"))
				return nil
			}
			_, err = client.Reminder.UpdateOne(r).SetDoneAt(cliClock().Now()).Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "mark done").WithCause(err)
			}
			fmt.Printf("%s reminder %s completed\n", style.Success("✓"), style.Code(r.ID))
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

// recurrenceShift maps each ent-generated Recurrence enum to its Y/M/D delta
// Single source of truth: changing the enum in dbent/schema/reminder.go and
// regenerating forces this map to compile-fail if not updated
type ymdShift struct{ y, m, d int }

// ds:def id=reminder-recurrence-gs41nylk owner=@khanakia stability=stable desc="reminder --recurrence values and how far each advances the due date"
var recurrenceShift = map[entReminder.Recurrence]ymdShift{
	entReminder.Recurrence7d:  {0, 0, 7},
	entReminder.Recurrence30d: {0, 0, 30},
	entReminder.Recurrence1m:  {0, 1, 0},
	entReminder.Recurrence3m:  {0, 3, 0},
	entReminder.Recurrence6m:  {0, 6, 0},
	entReminder.Recurrence1y:  {1, 0, 0},
}

// bumpRecurrence advances `from` by the recurrence pattern. The stored value
// is guaranteed by RecurrenceValidator (enforced at write time) to be one of
// the keys above, so a missing key is an "impossible" state and panics
func bumpRecurrence(from time.Time, pattern entReminder.Recurrence) time.Time {
	s, ok := recurrenceShift[pattern]
	if !ok {
		panic(fmt.Sprintf("invariant: persisted recurrence %q has no shift mapping", pattern))
	}
	return from.AddDate(s.y, s.m, s.d)
}

// allRecurrenceValues returns the canonical ordered list of recurrence
// strings, derived directly from the ent enum so docs/flag help stay in sync
func allRecurrenceValues() []string {
	vals := []entReminder.Recurrence{
		entReminder.Recurrence7d,
		entReminder.Recurrence30d,
		entReminder.Recurrence1m,
		entReminder.Recurrence3m,
		entReminder.Recurrence6m,
		entReminder.Recurrence1y,
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}

// (memory list now lives in archive_commands.go alongside memory show/archive/invalidate.)
