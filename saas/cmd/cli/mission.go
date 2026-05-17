// mission.go — `lore mission add/list/done/show` (NEW per user)
//
// Missions group related Tasks under an overarching initiative
package main

import (
	"fmt"
	"saas/pkg/constants"
	"strings"
	"time"

	"dbent/gen/ent"
	entMission "dbent/gen/ent/mission"
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

func newMissionCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "mission", Short: "Manage missions (containers for tasks)"}
	cmd.AddCommand(newMissionAddCommand())
	cmd.AddCommand(newMissionListCommand())
	cmd.AddCommand(newMissionDoneCommand())
	cmd.AddCommand(newMissionShowCommand())
	cmd.AddCommand(newMissionEditCommand())
	cmd.AddCommand(newMissionPauseCommand())
	cmd.AddCommand(newMissionResumeCommand())
	cmd.AddCommand(newMissionSearchCommand())
	cmd.AddCommand(newDeleteCommand(missionDeleteTarget))
	return cmd
}

func newMissionEditCommand() *cobra.Command {
	var f commonFlags
	var title, body, target, status string
	var clearTarget bool
	cmd := &cobra.Command{
		Use:   "edit <id|MS-N>",
		Short: "Edit mission fields (only flags you pass are applied)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f); err != nil {
				return err
			}
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			_ = rctx
			_prettyID, perr := resolvePrettyID(cmd.Context(), client, args[0])

			if perr != nil {
				return errcodes.New(errcodes.NotFound, perr.Error())
			}

			m, err := client.Mission.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("mission %q not found", args[0]))
			}
			upd := client.Mission.UpdateOne(m)
			ch := cmd.Flags().Changed
			if ch(constants.FlagTitle) {
				v, err := textnorm.Normalize(title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				upd.SetTitle(v)
			}
			if ch(constants.FlagBody) {
				v, err := textnorm.Normalize(body)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
				}
				upd.SetBody(v)
			}
			if ch(constants.FlagStatus) {
				v := entMission.Status(status)
				if entMission.StatusValidator(v) != nil {
					return errcodes.New(errcodes.InvalidInput, "bad --status "+status)
				}
				upd.SetStatus(v)
			}
			if ch(constants.FlagTarget) {
				t, err := time.Parse("2006-01-02", target)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "--target format: YYYY-MM-DD").WithCause(err)
				}
				upd.SetTargetDate(t)
			}
			if clearTarget {
				upd.ClearTargetDate()
			}
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "update mission").WithCause(err)
			}
			fmt.Printf("%s %s updated\n", style.Success("✓"), m.ID)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&title, constants.FlagTitle, "", "new title")
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "new body")
	cmd.Flags().StringVar(&status, constants.FlagStatus, "", "active | paused | done | cancelled")
	cmd.Flags().StringVar(&target, constants.FlagTarget, "", "target completion date (YYYY-MM-DD)")
	cmd.Flags().BoolVar(&clearTarget, constants.FlagClearTarget, false, "remove target date")
	return cmd
}

func newMissionAddCommand() *cobra.Command {
	var f commonFlags
	var body, target, createdBy string
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a new mission",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title, err := textnorm.Normalize(strings.Join(args, " "))
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
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

			create := client.Mission.Create().
				SetProjectID(projectID).
				SetTitle(title)
			if body != "" {
				b, err := textnorm.Normalize(body)
				if err == nil {
					create.SetBody(b)
				}
			}
			if target != "" {
				t, err := time.Parse("2006-01-02", target)
				if err == nil {
					create.SetTargetDate(t)
				}
			}
			actorID, err := resolveActorIDFlag(cmd.Context(), client, createdBy)
			if err != nil {
				return err
			}
			if actorID == "" {
				actorID, err = resolveCurrentActorID(cmd.Context(), client)
				if err != nil {
					return err
				}
			}
			create.SetCreatedByActorID(actorID)

			m, err := create.Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "create mission").WithCause(err)
			}
			fmt.Printf("%s %s %s — %s\n", style.Success("✓"), m.ID, style.Code(m.ID), title)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&body, constants.FlagBody, "", "longer description")
	cmd.Flags().StringVar(&target, constants.FlagTarget, "", "target completion date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&createdBy, constants.FlagCreatedBy, "", "actor_id (act_*); defaults to current identity")
	return cmd
}

func newMissionListCommand() *cobra.Command {
	var f commonFlags
	var status string
	var jsonOut, all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List missions (with attached tasks in --json mode)",
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
			q := client.Mission.Query().Where(entMission.ProjectID(projectID))
			if status != "" {
				v := entMission.Status(status)
				if entMission.StatusValidator(v) == nil {
					q = q.Where(entMission.StatusEQ(v))
				}
			} else if !all {
				q = q.Where(entMission.StatusNotIn(entMission.StatusDone, entMission.StatusCancelled))
			}
			rows, err := q.Order(ent.Asc(entMission.FieldID)).All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "list missions").WithCause(err)
			}

			if jsonOut {
				out := make([]missionWithTasks, 0, len(rows))
				for _, m := range rows {
					tasks, _ := client.Task.Query().
						Where(entTask.MissionID(m.ID)).
						Order(ent.Asc(entTask.FieldID)).
						All(cmd.Context())
					tBriefs := make([]taskBrief, 0, len(tasks))
					for _, t := range tasks {
						tBriefs = append(tBriefs, briefFromTask(t))
					}
					out = append(out, missionWithTasks{
						ID:          m.ID,
						Title:       m.Title,
						Body:        derefStr(m.Body),
						Status:      string(m.Status),
						TargetDate:  derefTime(m.TargetDate),
						CompletedAt: derefTime(m.CompletedAt),
						CreatedAt:   m.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
						Tasks:       tBriefs,
					})
				}
				printJSON(constants.KindMissionList, out, len(out))
				return nil
			}

			if len(rows) == 0 {
				fmt.Println(style.Muted("(no missions)"))
				return nil
			}
			for _, m := range rows {
				count, _ := client.Task.Query().Where(entTask.MissionID(m.ID)).Count(cmd.Context())
				fmt.Printf("%s [%s] %s   (%d tasks)\n", m.ID, m.Status, m.Title, count)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().StringVar(&status, constants.FlagStatus, "", "filter: active | paused | done | cancelled (empty = default open set, see --all)")
	cmd.Flags().BoolVar(&all, constants.FlagAll, false, "include done + cancelled missions (default: hide them)")
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output with eager-loaded tasks (stable schema)")
	return cmd
}

// briefFromTask is the canonical task → JSON projection used in eager-load
// contexts (mission show, plan show, tasklist show, task list)
func briefFromTask(t *ent.Task) taskBrief {
	due := ""
	if t.DueAt != nil {
		due = t.DueAt.Format("2006-01-02")
	}
	mid := ""
	if t.MissionID != nil {
		mid = *t.MissionID
	}
	deferred := ""
	if t.DeferredUntil != nil {
		deferred = t.DeferredUntil.Format("2006-01-02")
	}
	return taskBrief{
		ID:            t.ID,
		Title:         t.Title,
		Status:        string(t.Status),
		Priority:      string(t.Priority),
		Commitment:    string(t.Commitment),
		DeferredUntil: deferred,
		DueAt:         due,
		MissionID:     mid,
	}
}

func newMissionDoneCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Mark mission as done",
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

			m, err := client.Mission.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("mission %q not found", args[0]))
			}
			_, err = client.Mission.UpdateOne(m).
				SetStatus(entMission.StatusDone).
				SetCompletedAt(time.Now()).
				Save(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "update mission").WithCause(err)
			}
			fmt.Printf("%s %s completed\n", style.Success("✓"), m.ID)
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newMissionShowCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show mission details + attached tasks",
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

			m, err := client.Mission.Get(cmd.Context(), _prettyID)
			if err != nil {
				return errcodes.New(errcodes.NotFound, fmt.Sprintf("mission %q not found", args[0]))
			}
			fmt.Printf("%s %s\n", m.ID, style.Code(m.ID))
			fmt.Printf("  title:  %s\n", m.Title)
			fmt.Printf("  status: %s\n", m.Status)
			if m.TargetDate != nil {
				fmt.Printf("  target: %s\n", m.TargetDate.Format("2006-01-02"))
			}
			if m.Body != nil && *m.Body != "" {
				fmt.Println()
				fmt.Println(*m.Body)
			}
			tasks, _ := client.Task.Query().
				Where(entTask.MissionID(m.ID)).
				Order(ent.Asc(entTask.FieldID)).
				All(cmd.Context())
			if len(tasks) > 0 {
				fmt.Println()
				fmt.Println(style.Muted("Tasks:"))
				for _, t := range tasks {
					fmt.Printf("  %s [%s] %s\n", t.ID, t.Status, t.Title)
				}
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}
