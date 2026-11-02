// task_from.go — `task show|edit <id> --from <org>[/<folder>]` (fixed
// rule 5, §1 flags): read or edit a task in a named source — a store
// project of an org entry the normal lookup does not reach (a staging
// entry), or the one meant when an id exists in several sources.
package main

import (
	"context"
	"fmt"
	"time"

	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

// showTaskFrom prints a task read from the named source.
func showTaskFrom(ctx context.Context, id, from string, jsonOut bool) error {
	_, t, err := openTaskCoord(ctx, id, from)
	if err != nil {
		return err
	}
	defer t.Close()
	d, ok, err := t.Rows.Get(ctx, id)
	if err != nil {
		return coordError(err)
	}
	if !ok {
		return errcodes.New(errcodes.NotFound, "task "+id+" not found in "+from)
	}
	if jsonOut {
		printJSON(constants.KindTaskShow, map[string]any(d), 0)
		return nil
	}
	fmt.Printf("%s %s\n", id, style.Code(id))
	fmt.Printf("  title:    %s\n", d.Str("title"))
	fmt.Printf("  status:   %s\n", d.Str("status"))
	fmt.Printf("  priority: %s\n", d.Str("priority"))
	fmt.Printf("  source:   %s\n", from)
	if b := d.Str("body"); b != "" {
		fmt.Println()
		fmt.Println(b)
	}
	return nil
}

// editTaskFrom applies the edit flags a store row supports (title, body,
// priority, status) to a task in the named source, lease-protected like
// every coordination write; other flags are refused rather than ignored.
func editTaskFrom(cmd *cobra.Command, id string, f *taskEditFlags) error {
	ctx := cmd.Context()
	ch := cmd.Flags().Changed
	for _, name := range []string{constants.FlagCommitment, constants.FlagDeferUntil, constants.FlagDue, constants.FlagTasklist,
		constants.FlagMission, constants.FlagPlan, constants.FlagAssignedTo, flagAfter, flagAssign, flagTimebox, flagRole, flagRef} {
		if ch(name) {
			return errcodes.New(errcodes.NotImplemented, "--"+name+" with --from "+f.from+" is not supported; edit it where the row lives")
		}
	}
	w, t, err := openTaskCoord(ctx, id, f.from)
	if err != nil {
		return err
	}
	defer t.Close()
	_, err = coord.Update(ctx, w.env(t), id, func(d coord.Doc, _ time.Time) error {
		if ch(constants.FlagTitle) {
			v, err := textnorm.Normalize(f.title)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
			}
			d["title"] = v
		}
		if ch(constants.FlagBody) {
			v, err := textnorm.Normalize(f.body)
			if err != nil {
				return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
			}
			d["body"] = v
		}
		if ch(constants.FlagPriority) {
			d["priority"] = string(taskPriority(f.priority))
		}
		if ch(constants.FlagStatus) {
			if entTask.StatusValidator(entTask.Status(f.status)) != nil {
				return errcodes.New(errcodes.InvalidInput, "bad --status "+f.status)
			}
			d["status"] = f.status
		}
		return nil
	})
	if err != nil {
		return coordError(err)
	}
	fmt.Printf("%s %s updated (%s)\n", style.Success("✓"), id, f.from)
	return nil
}
