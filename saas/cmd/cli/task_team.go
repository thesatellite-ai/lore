// task_team.go — the lore-for-teams fields of `lore task add|edit`
// (LORE_TEAM_SPEC §1 "Flags", §7.2, §7.4, C23): --after with done/merged
// edges and the E_CYCLE refusal, --assign @handle (person or team),
// --timebox, --role, --key (idempotent plan rows), an optional --tasklist
// (the project's default list, created on demand) and `task add --json`.
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dbent/gen/ent"
	entRepo "dbent/gen/ent/repo"
	entTask "dbent/gen/ent/task"
	entTasklist "dbent/gen/ent/tasklist"
	entTeam "dbent/gen/ent/team"
	"dbent/schema"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/queue"

	"github.com/spf13/cobra"
)

// Flag names (§1 "Flags").
const (
	flagAfter   = "after"
	flagKey     = "key"
	flagTimebox = "timebox"
	flagJSON    = "json"
)

// teamTaskFlags are the team flags shared by add and edit.
type teamTaskFlags struct {
	after   []string
	assign  string
	timebox time.Duration
	role    string
	key     string
	json    bool
	refs    []string
}

func bindTeamTaskFlags(cmd *cobra.Command, f *teamTaskFlags, add bool) {
	cmd.Flags().StringSliceVar(&f.after, flagAfter, nil, "dependencies: <id>[:done|:merged][,…] (default: done in the same repo, merged across repos)")
	cmd.Flags().StringVar(&f.assign, flagAssign, "", "assign to @handle (a person sets the assignee, a team the assigned team)")
	cmd.Flags().DurationVar(&f.timebox, flagTimebox, 0, "time box, e.g. 60m (active lease time summed across holders)")
	cmd.Flags().StringVar(&f.role, flagRole, "", "plan role matched by `task next --role` (e.g. coordinator)")
	cmd.Flags().StringArrayVar(&f.refs, flagRef, nil, "a pointer to another row (<id> or <project>:<id>) or a commit (commit:<sha>); repeatable")
	if add {
		cmd.Flags().StringVar(&f.key, flagKey, "", "idempotency key: a re-run with the same key (in the same mission) adds nothing")
		cmd.Flags().BoolVar(&f.json, flagJSON, false, "print the new task as JSON (incl. its id)")
	}
}

// depSuffixes are the `:done` / `:merged` suffixes of an --after entry.
var depSuffixes = map[string]schema.DependencyUntil{
	":" + string(schema.DependencyUntilDone):   schema.DependencyUntilDone,
	":" + string(schema.DependencyUntilMerged): schema.DependencyUntilMerged,
}

// parseAfter turns --after entries into dependency edges. An entry without
// a suffix waits for done when the upstream task is in the same repo as the
// dependent and for merged otherwise (F9).
func parseAfter(ctx context.Context, client *ent.Client, projectID string, entries []string, repoID string) ([]schema.TaskDependency, error) {
	var out []schema.TaskDependency
	seen := map[string]bool{}
	for _, raw := range entries {
		e := strings.TrimSpace(raw)
		if e == "" {
			continue
		}
		until := schema.DependencyUntil("")
		for suf, u := range depSuffixes {
			if s, ok := strings.CutSuffix(e, suf); ok {
				e, until = s, u
			}
		}
		up, err := client.Task.Query().Where(entTask.ID(e), entTask.ProjectID(projectID)).Only(ctx)
		if err != nil {
			return nil, errcodes.New(errcodes.NotFound, "--after "+e+": no such task in this project").WithCause(err)
		}
		if until == "" {
			until = schema.DependencyUntilDone
			if deref(up.RepoID) != repoID {
				until = schema.DependencyUntilMerged
			}
		}
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, schema.TaskDependency{TaskID: e, Until: until})
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// checkNoCycle refuses edges that would close a dependency cycle (E_CYCLE,
// §7.2): id --after deps over every task of the project.
func checkNoCycle(ctx context.Context, client *ent.Client, projectID, id string, deps []schema.TaskDependency) error {
	if len(deps) == 0 {
		return nil
	}
	all, err := client.Task.Query().Where(entTask.ProjectID(projectID)).All(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "read tasks for the cycle check").WithCause(err)
	}
	tasks := make([]queue.Task, 0, len(all))
	for _, t := range all {
		qt := queue.Task{ID: t.ID}
		for _, d := range t.BlockedBy {
			qt.After = append(qt.After, queue.Dep{TaskID: d.TaskID, Until: queue.Until(d.Until)})
		}
		tasks = append(tasks, qt)
	}
	ids := make([]string, 0, len(deps))
	for _, d := range deps {
		ids = append(ids, d.TaskID)
	}
	if cyc := queue.CheckCycle(tasks, id, ids); cyc != nil {
		return errcodes.New(errcodes.Cycle, "dependency cycle: "+strings.Join(cyc, " → ")).
			WithHint("drop one --after edge; a task may not wait on itself, even through others")
	}
	return nil
}

// assignTarget is what --assign @handle resolved to.
type assignTarget struct {
	ActorID string
	TeamID  string
}

// resolveAssign resolves --assign: an actor handle (person) or a team
// handle (§7.2 "Assignment"). A bare actor id is accepted too.
func resolveAssign(ctx context.Context, client *ent.Client, projectID, ref string) (assignTarget, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return assignTarget{}, nil
	}
	h := strings.TrimPrefix(ref, "@")
	if !strings.HasPrefix(ref, "@") {
		id, err := resolveActorRef(ctx, client, ref)
		return assignTarget{ActorID: id}, err
	}
	id, actorErr := resolveHandleActor(ctx, ref)
	if actorErr == nil {
		return assignTarget{ActorID: id}, nil
	}
	tm, err := client.Team.Query().Where(entTeam.ProjectID(projectID), entTeam.Handle(h), entTeam.Active(true)).Only(ctx)
	if err == nil {
		return assignTarget{TeamID: tm.ID}, nil
	}
	// The roster said where it lives: a store roster's miss is most often a
	// stale store copy; a repo roster's is a person or team not added yet.
	hint := "people join with `lore actor register`; teams with `lore team add`"
	var miss *errcodes.CLIError
	if errors.As(actorErr, &miss) && miss.Hint == rosterMissHint {
		hint = rosterMissHint
	}
	return assignTarget{}, &handleMissError{CLIError: errcodes.New(errcodes.NotFound, "no person or team with handle @"+h).
		WithHint(hint)}
}

// handleMissError is an @handle that is on no roster. It is E_NOT_FOUND
// like a missing row id, but its hint is about the roster, so the live
// "peek origin/main" explanation for missing ids (explainLiveNotFound)
// leaves it alone.
type handleMissError struct {
	*errcodes.CLIError
}

// Unwrap exposes the CLIError for the exit code and the hint line.
func (e *handleMissError) Unwrap() error { return e.CLIError }

// preResolveHandle resolves an @handle naming a person on the roster ("" =
// not a person handle, or not found: the caller then tries teams).
func preResolveHandle(ctx context.Context, ref string) string {
	if !strings.HasPrefix(strings.TrimSpace(ref), "@") {
		return ""
	}
	id, err := resolveHandleActor(ctx, ref)
	if err != nil {
		return ""
	}
	return id
}

// defaultTasklistTitle names the list a task joins when --tasklist is left
// out (coordinator answer Q1: created on demand).
const defaultTasklistTitle = "default"

// defaultTasklistStatus is a new list's status_str.
const defaultTasklistStatus = "active"

// defaultTasklist returns the project's default task list, creating it.
func defaultTasklist(ctx context.Context, client *ent.Client, projectID string) (string, error) {
	tl, err := client.TaskList.Query().Where(entTasklist.ProjectID(projectID), entTasklist.Title(defaultTasklistTitle)).First(ctx)
	if err == nil {
		return tl.ID, nil
	}
	if !ent.IsNotFound(err) {
		return "", errcodes.New(errcodes.Internal, "find the default task list").WithCause(err)
	}
	created, err := client.TaskList.Create().SetProjectID(projectID).SetTitle(defaultTasklistTitle).SetStatusStr(defaultTasklistStatus).Save(ctx)
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "create the default task list").WithCause(err)
	}
	return created.ID, nil
}

// findByKey returns the task already added with this idempotency key in
// the same project and mission (§7.4 "Idempotent plans"), or nil.
func findByKey(ctx context.Context, client *ent.Client, projectID, mission, key string) (*ent.Task, error) {
	if key == "" {
		return nil, nil
	}
	q := client.Task.Query().Where(entTask.ProjectID(projectID), entTask.IdempotencyKey(key))
	if mission != "" {
		q = q.Where(entTask.MissionID(mission))
	} else {
		q = q.Where(entTask.MissionIDIsNil())
	}
	t, err := q.First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "look up --key").WithCause(err)
	}
	return t, nil
}

// kindTask / kindMission are the row kinds routeWrite takes (F14,
// defaults.client_repos keys).
const (
	kindTask    = "task"
	kindMission = "mission"
)

// afterIDs strips the :done / :merged suffixes of --after entries.
func afterIDs(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		id := strings.TrimSpace(e)
		for suf := range depSuffixes {
			id = strings.TrimSuffix(id, suf)
		}
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

// timeboxSeconds converts --timebox; zero means none.
func timeboxSeconds(d time.Duration) (int64, error) {
	if d < 0 {
		return 0, errcodes.New(errcodes.InvalidInput, fmt.Sprintf("--timebox %s must be positive", d))
	}
	return int64(d / time.Second), nil
}

// ensureGroupRepo returns the repo row named name in a store project,
// creating it. A store project (a group) has no checkout of its own, so the
// repos its tasks name (`--repo backend`, `repo:` in a plan) are its linked
// code repos, recorded by name (§7.3 step 3: "X resolves through the
// group's links to a repo mount name").
func ensureGroupRepo(ctx context.Context, client *ent.Client, projectID, name string) (string, error) {
	r, err := client.Repo.Query().Where(entRepo.ProjectID(projectID), entRepo.MountName(name)).First(ctx)
	if err == nil {
		return r.ID, nil
	}
	if !ent.IsNotFound(err) {
		return "", errcodes.New(errcodes.Internal, "find repo "+name).WithCause(err)
	}
	created, err := client.Repo.Create().SetProjectID(projectID).SetMountName(name).Save(ctx)
	if err != nil {
		return "", errcodes.New(errcodes.Internal, "record repo "+name+" in the group").WithCause(err)
	}
	return created.ID, nil
}

// repoIDFor resolves a --repo name for the routed project: this repo's own
// repos table, or the group's repo rows for a store project.
func repoIDFor(ctx context.Context, client *ent.Client, projectID, name string, external bool) (string, error) {
	if name == "" {
		return "", nil
	}
	if external {
		return ensureGroupRepo(ctx, client, projectID, name)
	}
	return resolveRepoID(ctx, client, projectID, name)
}
