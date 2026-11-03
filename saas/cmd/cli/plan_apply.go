// plan_apply.go — `lore plan apply <file>` (LORE_TEAM_SPEC §7.4 "Idempotent
// plans", §7.5 step 1): a coordinator writes a mission and its tasks once,
// with dependencies by key, time boxes, roles and gates; a re-run adds
// nothing (every row carries its plan key).
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"dbent/gen/ent"
	entMission "dbent/gen/ent/mission"
	entTask "dbent/gen/ent/task"
	"dbent/schema"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// planFile is the YAML shape of a plan (§7.5 invite.yaml). Field names are
// the file format users write.
// ds:def id=plan-file-zo3uef01 owner=@khanakia stability=stable desc="the YAML shape of a plan file (lore plan apply)"
type planFile struct {
	Mission struct {
		Key   string `yaml:"key"`
		Title string `yaml:"title"`
		To    string `yaml:"to"`
	} `yaml:"mission"`
	Approvers []string   `yaml:"approvers"`
	Tasks     []planTask `yaml:"tasks"`
}

type planTask struct {
	Key     string   `yaml:"key"`
	Title   string   `yaml:"title"`
	Repo    string   `yaml:"repo"`
	After   []string `yaml:"after"`
	Timebox string   `yaml:"timebox"`
	Role    string   `yaml:"role"`
	Gate    string   `yaml:"gate"`
}

// planGroupName is the existing `lore plan` entity group apply joins.
const planGroupName = "plan"

func newPlanApplyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "apply <file>",
		Short: "Create a mission and its tasks from a YAML plan; a re-run adds nothing",
		// The first paragraph is the whole description on its own: the
		// command reference shows only it, then usage and examples.
		Long: `Apply creates a mission and all of its tasks from one YAML plan file, in one command: titles, dependencies (after), time boxes, roles, gates and the people who may approve them. Every row keeps its plan key, so running the same plan again finds the rows and adds nothing.

The plan file:

  mission:
    key      a stable key; a re-run finds the mission by it (else by its exact title)
    title    required
    to       where every row of the plan goes, like --to: repo | <org> | <org>/<folder>
  approvers  handles that may approve the mission's gates (default: any human on the roster)
  tasks:     a list; each task needs key and title
    key      unique in the plan; a re-run finds the task by it
    title    required
    repo     the repo of the group the task belongs to (a repo name)
    after    keys of earlier tasks it waits for: done when both are in the
             same repo, merged when they are in different repos; add
             :done or :merged to choose (e.g. backend:merged)
    timebox  how long the task should take, e.g. 60m or 2h
    role     only ` + "`" + `lore task next --role <role>` + "`" + ` takes it, e.g. coordinator
    gate     required: a human must approve it before an agent can take it`,
		Example: `  # invite.yaml
  mission: {key: invite, title: "Invite landing page", to: agency/client2}
  approvers: ["@alice"]
  tasks:
    - {key: backend,  title: "invite mutation + email", repo: api, timebox: 60m}
    - {key: frontend, title: "invite landing route", repo: app, after: [backend]}
    - {key: deploy,   title: "deploy to staging", role: coordinator, after: [frontend], gate: required}

  lore plan apply invite.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runPlanApply(cmd.Context(), args[0]) },
	}
}

func runPlanApply(ctx context.Context, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return errcodes.New(errcodes.InvalidInput, "read plan").WithCause(err)
	}
	var p planFile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return errcodes.New(errcodes.InvalidInput, "plan is not valid YAML").WithCause(err)
	}
	if strings.TrimSpace(p.Mission.Title) == "" {
		return errcodes.New(errcodes.InvalidInput, "plan: mission.title is required")
	}
	keys := map[string]bool{}
	for i, t := range p.Tasks {
		if t.Key == "" || t.Title == "" {
			return errcodes.New(errcodes.InvalidInput, fmt.Sprintf("plan: task %d needs key and title", i+1))
		}
		if keys[t.Key] {
			return errcodes.New(errcodes.InvalidInput, "plan: task key "+t.Key+" appears twice")
		}
		keys[t.Key] = true
	}
	var f commonFlags
	var approverIDs []string
	if len(p.Approvers) > 0 { // before routing: see preResolveHandle
		if approverIDs, err = resolveApprovers(ctx, p.Approvers); err != nil {
			return err
		}
	}
	// F14: `to:` picks the project like `mission add --to` (a store project
	// for a group of client repos, §7.5); all plan rows go there.
	route, err := routeWrite(ctx, &f, toFlags{To: p.Mission.To}, kindMission, p.Mission.Title)
	if err != nil {
		return err
	}
	rctx, client, err := resolveContext(&f)
	if err != nil {
		return err
	}
	defer client.Close()
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}
	external := !route.Repo
	by, err := resolveCurrentActorID(ctx, client)
	if err != nil {
		return err
	}
	m, created, err := planMission(ctx, client, projectID, p, by, approverIDs)
	if err != nil {
		return err
	}
	verb := "created"
	if !created {
		verb = "found"
	}
	where := ""
	if route.Label != "" {
		where = " → " + route.Label
	}
	fmt.Printf("%s %s %s (%s)%s\n", style.Success("✓"), m.ID, m.Title, verb, where)
	tl, err := defaultTasklist(ctx, client, projectID)
	if err != nil {
		return err
	}
	ids := map[string]*ent.Task{}
	for _, pt := range p.Tasks {
		t, err := planTaskRow(ctx, client, projectID, m.ID, tl, pt, ids, by, external)
		if err != nil {
			return err
		}
		ids[pt.Key] = t
		extra := ""
		if t.TimeboxSeconds != nil {
			extra += "  timebox " + shortDuration(time.Duration(*t.TimeboxSeconds)*time.Second)
		}
		if len(pt.After) > 0 {
			extra += "  after " + strings.Join(pt.After, ",")
		}
		if t.Gate != nil {
			extra += "  gate " + string(*t.Gate)
		}
		fmt.Printf("  %s %-28s%s\n", t.ID, t.Title, extra)
	}
	return nil
}

// planMission finds the plan's mission by its key (or, for a plan without
// a mission key, by its exact title) or creates it.
func planMission(ctx context.Context, client *ent.Client, projectID string, p planFile, by string, approverIDs []string) (*ent.Mission, bool, error) {
	title, err := textnorm.Normalize(p.Mission.Title)
	if err != nil {
		return nil, false, errcodes.New(errcodes.InvalidInput, "plan: mission title: "+err.Error())
	}
	q := client.Mission.Query().Where(entMission.ProjectID(projectID), entMission.Title(title))
	if p.Mission.Key != "" {
		q = client.Mission.Query().Where(entMission.ProjectID(projectID), entMission.IdempotencyKey(p.Mission.Key))
	}
	if m, err := q.First(ctx); err == nil {
		return m, false, nil
	} else if !ent.IsNotFound(err) {
		return nil, false, errcodes.New(errcodes.Internal, "find mission").WithCause(err)
	}
	create := client.Mission.Create().SetProjectID(projectID).SetTitle(title).SetCreatedByActorID(by)
	if p.Mission.Key != "" {
		create.SetIdempotencyKey(p.Mission.Key)
	}
	if len(approverIDs) > 0 {
		create.SetApprovers(approverIDs)
	}
	m, err := create.Save(ctx)
	if err != nil {
		return nil, false, errcodes.New(errcodes.Internal, "create mission").WithCause(err)
	}
	return m, true, nil
}

// planTaskRow finds a task by its plan key in the mission or creates it.
func planTaskRow(ctx context.Context, client *ent.Client, projectID, missionID, tasklist string, pt planTask, earlier map[string]*ent.Task, by string, external bool) (*ent.Task, error) {
	if t, err := findByKey(ctx, client, projectID, missionID, pt.Key); err != nil || t != nil {
		return t, err
	}
	title, err := textnorm.Normalize(pt.Title)
	if err != nil {
		return nil, errcodes.New(errcodes.InvalidInput, "plan: task "+pt.Key+": "+err.Error())
	}
	repoID, err := repoIDFor(ctx, client, projectID, pt.Repo, external)
	if err != nil {
		return nil, err
	}
	deps, err := planDeps(pt, earlier, repoID)
	if err != nil {
		return nil, err
	}
	create := client.Task.Create().SetProjectID(projectID).SetTitle(title).SetMissionID(missionID).SetTasklistID(tasklist).
		SetIdempotencyKey(pt.Key).SetCreatedByActorID(by).SetCommitment(entTask.CommitmentAccepted)
	if repoID != "" {
		create.SetRepoID(repoID)
	}
	if len(deps) > 0 {
		create.SetBlockedBy(deps)
	}
	if pt.Timebox != "" {
		d, err := time.ParseDuration(pt.Timebox)
		if err != nil {
			return nil, errcodes.New(errcodes.InvalidInput, "plan: task "+pt.Key+": timebox "+pt.Timebox).WithCause(err)
		}
		create.SetTimeboxSeconds(int64(d / time.Second))
	}
	if pt.Role != "" {
		create.SetRole(pt.Role)
	}
	switch pt.Gate {
	case "":
	case string(schema.TaskGateRequired):
		create.SetGate(entTask.GateRequired)
	default:
		return nil, errcodes.New(errcodes.InvalidInput, "plan: task "+pt.Key+": gate must be required")
	}
	t, err := create.Save(ctx)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "create task "+pt.Key).WithCause(err)
	}
	return t, nil
}

// planDeps turns a plan task's after: entries into dependency edges. An
// entry is an earlier task's key, optionally with the same :done / :merged
// suffix `task add --after <id>[:merged]` takes (depSuffixes). Without a
// suffix it waits for done when both tasks are in the same repo and for
// merged otherwise (F9); a suffix overrides that, e.g. backend:merged makes
// a same-repo task wait until the backend code is on main.
func planDeps(pt planTask, earlier map[string]*ent.Task, repoID string) ([]schema.TaskDependency, error) {
	var deps []schema.TaskDependency
	seen := map[string]bool{}
	for _, raw := range pt.After {
		k := strings.TrimSpace(raw)
		until := schema.DependencyUntil("")
		for suf, u := range depSuffixes {
			if base, ok := strings.CutSuffix(k, suf); ok {
				k, until = base, u
			}
		}
		up, ok := earlier[k]
		if !ok {
			return nil, errcodes.New(errcodes.InvalidInput, "plan: task "+pt.Key+" is after "+raw+", which is not an earlier task of the plan").
				WithHint("after: takes keys of earlier tasks, optionally with :done or :merged (e.g. backend:merged)")
		}
		if until == "" {
			until = schema.DependencyUntilDone
			if deref(up.RepoID) != repoID {
				until = schema.DependencyUntilMerged
			}
		}
		if seen[up.ID] {
			continue
		}
		seen[up.ID] = true
		deps = append(deps, schema.TaskDependency{TaskID: up.ID, Until: until})
	}
	return deps, nil
}
