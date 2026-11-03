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
		Long: `Apply reads a plan:

  mission: {key: invite, title: "Invite landing page"}
  approvers: ["@alice"]
  tasks:
    - {key: backend,  title: "invite mutation + email", timebox: 60m}
    - {key: frontend, title: "invite landing route", after: [backend]}
    - {key: deploy,   title: "deploy to staging", role: coordinator, after: [frontend], gate: required}

and creates every row and edge in one command. Rows are found again by their
key, so running it twice changes nothing.`,
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
	var deps []schema.TaskDependency
	for _, k := range pt.After {
		up, ok := earlier[k]
		if !ok {
			return nil, errcodes.New(errcodes.InvalidInput, "plan: task "+pt.Key+" is after "+k+", which is not an earlier task of the plan")
		}
		until := schema.DependencyUntilDone
		if deref(up.RepoID) != repoID {
			until = schema.DependencyUntilMerged
		}
		deps = append(deps, schema.TaskDependency{TaskID: up.ID, Until: until})
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
