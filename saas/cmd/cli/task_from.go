// task_from.go — `task show|edit <id> --from <org>[/<folder>]` (fixed
// rule 5, §1 flags): read or edit a task in a named source — a store
// project of an org entry the normal lookup does not reach (a staging
// entry), or the one meant when an id exists in several sources.
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
	w, err := openCoordWorld(ctx)
	if err != nil {
		return err
	}
	return showCoordTask(ctx, w, id, from, jsonOut)
}

// showStoreTask is `task show <id>` for an id this repo's lore.db does not
// hold: F1 lookup walks every loaded source, so a store task of the linked
// project is shown from any client repo of the group without --from, as
// `task done` and `mission status` already find it (lore-docs BUG-D3).
// handled is false when no linked source has the id either; the caller
// then reports its own not-found error.
func showStoreTask(ctx context.Context, id string, jsonOut bool) (handled bool, err error) {
	w, werr := openCoordWorld(ctx)
	if werr != nil || len(w.Links) == 0 {
		return false, nil
	}
	err = showCoordTask(ctx, w, id, "", jsonOut)
	if errcodes.IsCode(err, errcodes.NotFound) {
		return false, nil
	}
	return true, err
}

// showCoordTask prints a task row read through the coordination target of
// from ("" = the normal lookup): everything a replacement reads first
// (§7.5 step 5) — the row, its after list and gate, the claim as this clone
// sees it now, and its notes.
func showCoordTask(ctx context.Context, w *coordWorld, id, from string, jsonOut bool) error {
	t, err := openCoordTargetFrom(ctx, w, id, from)
	if err != nil {
		return err
	}
	defer t.Close()
	d, ok, err := t.Rows.Get(ctx, id)
	if err != nil {
		return coordError(err)
	}
	if !ok {
		msg := "task " + id + " not found"
		if from != "" {
			msg += " in " + from
		}
		return errcodes.New(errcodes.NotFound, msg)
	}
	if jsonOut {
		printJSON(constants.KindTaskShow, map[string]any(d), 0)
		return nil
	}
	source := from
	if source == "" {
		source = coordSourceName(w, t)
	}
	labels := &actorLabels{}
	defer labels.close()
	printCoordTask(ctx, t, d, source, labels)
	if nl, ok := t.Rows.(coordNoteLister); ok {
		notes, err := nl.Notes(ctx, id)
		if err != nil {
			return coordError(err)
		}
		printCoordNotes(ctx, notes, labels)
	}
	return printClaimOn(ctx, t.Signals, t.Folder, id)
}

// coordNoteLister is a coordination target that can list a task's notes
// itself (a store project read from git, not from lore.db).
type coordNoteLister interface {
	Notes(ctx context.Context, taskID string) ([]coord.Doc, error)
}

// coordSourceName names where a row was read: <org>/<folder> for a store
// project, repo for this checkout's own project.
func coordSourceName(w *coordWorld, t coordTarget) string {
	if isStoreTarget(t) && w.Org != nil {
		return w.Org.Name + "/" + t.Folder
	}
	return toRepo
}

// printCoordTask prints the row fields, in `task show`'s layout.
func printCoordTask(ctx context.Context, t coordTarget, d coord.Doc, source string, labels *actorLabels) {
	s := coord.Summarize(d)
	field := func(name, v string) {
		if v != "" {
			fmt.Printf("  %-9s %s\n", name+":", v)
		}
	}
	fmt.Printf("%s\n", style.Code(s.ID))
	field("title", s.Title)
	field("status", s.Status)
	field("priority", s.Priority)
	field("commit", s.Commitment)
	field("mission", s.MissionID)
	if s.RepoID != "" {
		repo := s.RepoID
		if l, ok := t.Rows.(coordLister); ok {
			repo = l.RepoName(s.RepoID)
		}
		field("repo", repo)
	}
	field("role", s.Role)
	field("assigned", labels.label(ctx, s.Assignee))
	if len(s.After) > 0 {
		deps := make([]string, 0, len(s.After))
		for _, dep := range s.After {
			deps = append(deps, fmt.Sprintf("%s (until %s)", dep.TaskID, dep.Until))
		}
		field("after", strings.Join(deps, ", "))
	}
	field("gate", s.Gate)
	if s.Timebox > 0 {
		field("timebox", shortDuration(s.Timebox))
	}
	if s.Attempts > 0 {
		field("attempts", strconv.Itoa(s.Attempts))
	}
	field("branch", s.WorkBranch)
	field("done by", labels.label(ctx, s.DoneBy))
	if s.DonePR > 0 {
		field("pr", "#"+strconv.Itoa(s.DonePR))
	}
	field("commit", s.DoneCommit)
	if !s.MergedAt.IsZero() {
		field("merged", s.MergedAt.UTC().Format(time.RFC3339))
	}
	field("source", source)
	if s.Body != "" {
		fmt.Println()
		fmt.Println(s.Body)
	}
}

// printCoordNotes lists notes oldest first with their author's label.
func printCoordNotes(ctx context.Context, notes []coord.Doc, labels *actorLabels) {
	if len(notes) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("  notes:")
	for _, n := range notes {
		line := "    " + n.Time("created_at").UTC().Format("2006-01-02 15:04") + "  " + n.Str("body")
		if who := labels.label(ctx, n.Str("created_by_actor_id")); who != "" {
			line += "  (" + who + ")"
		}
		fmt.Println(line)
	}
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
