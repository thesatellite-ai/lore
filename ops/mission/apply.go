package mission

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// OpApply is `lore mission apply <file.yaml>` (TSQ9: replaces v0.1.17's
// `lore plan apply`, same file format, idempotent).
const OpApply = "mission.apply"

// Plan-file defaults (v0.1.17 plan_apply.go and defaultTasklist).
const (
	applyTasklistTitle  = "default"
	applyTasklistStatus = "active"
	untilDone           = "done"
	untilMerged         = "merged"
	fieldTimebox        = "timebox_seconds"
	fieldRole           = "role"
	fieldGate           = "gate"
	fieldBlockedBy      = "blocked_by"
	fieldCommitment     = "commitment"
	fieldPriority       = "priority"
	fieldStatusStr      = "status_str"
)

// File is the YAML shape of a plan file (LORE_TEAM_SPEC 7.5 invite.yaml;
// field names are the file format users write).
type File struct {
	Mission struct {
		Key   string `yaml:"key"`
		Title string `yaml:"title"`
		To    string `yaml:"to"`
	} `yaml:"mission"`
	Approvers []string   `yaml:"approvers"`
	Tasks     []FileTask `yaml:"tasks"`
}

// FileTask is one task of a plan file.
type FileTask struct {
	Key     string   `yaml:"key"`
	Title   string   `yaml:"title"`
	Repo    string   `yaml:"repo"`
	After   []string `yaml:"after"`
	Timebox string   `yaml:"timebox"`
	Role    string   `yaml:"role"`
	Gate    string   `yaml:"gate"`
}

// ApplyIn is the input of mission.apply.
type ApplyIn struct {
	// File is a path on the machine running lore (the CLI's, or `lore
	// api`'s for REST).
	File string `json:"file" arg:"0" desc:"the YAML plan file"`
}

// ApplyOut is the output of mission.apply.
type ApplyOut struct {
	MissionID string        `json:"mission_id"`
	Created   bool          `json:"created"`
	Store     string        `json:"store"`
	Tasks     []AppliedTask `json:"tasks"`
	Human     string        `json:"-"`
}

// AppliedTask is one task of the plan as applied.
type AppliedTask struct {
	Key     string `json:"key"`
	ID      string `json:"id"`
	Created bool   `json:"created"`
}

// Text implements op.Texter.
func (o ApplyOut) Text() string { return o.Human }

// applyOp is the mission.apply entry.
func applyOp(o ops) op.Entry {
	return op.Define(op.Spec{
		Name: OpApply, Summary: "Create a mission and its tasks from a YAML plan; a re-run adds nothing",
		Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(append([]errs.Code{}, work.WriteErrors...), errs.Unsupported),
		Confirm:  op.ConfirmRule{MayAsk: true},
		Examples: []op.Example{{Title: "apply a plan file", CLI: "lore mission apply invite.yaml", Want: "msn_"}},
	}, o.apply)
}

// apply reads the plan file and writes what is missing: the mission (found
// by key, else by exact title), the project's default task list, and each
// task (found by key in the mission).
func (o ops) apply(ctx context.Context, in ApplyIn) (ApplyOut, error) {
	raw, err := os.ReadFile(in.File)
	if err != nil {
		return ApplyOut{}, errs.Wrap(errs.InvalidInput, "read plan", err)
	}
	var p File
	if yerr := yaml.Unmarshal(raw, &p); yerr != nil {
		return ApplyOut{}, errs.Wrap(errs.InvalidInput, "plan is not valid YAML", yerr)
	}
	if cerr := checkFile(p); cerr != nil {
		return ApplyOut{}, cerr
	}
	project, err := o.env.Project(ctx)
	if err != nil {
		return ApplyOut{}, err
	}
	to := strings.TrimSpace(p.Mission.To)
	m, created, src, err := o.planMission(ctx, to, project, p)
	if err != nil {
		return ApplyOut{}, err
	}
	out := ApplyOut{MissionID: m.ID.String(), Created: created, Store: string(src), Tasks: []AppliedTask{}}
	verb := "created"
	if !created {
		verb = "found"
	}
	where := ""
	if l := work.Label(work.LabelerOf(o.env), src); l != "" {
		where = " → " + l // v0.1.17 plan_apply: " → " + route.Label
	}
	var b strings.Builder
	b.WriteString(work.Success + " " + m.ID.String() + " " + work.Field(m, work.FieldTitle) + " (" + verb + ")" + where + "\n")
	tl, err := o.applyTasklist(ctx, src, project)
	if err != nil {
		return ApplyOut{}, err
	}
	earlier := map[string]row.Row{}
	for _, pt := range p.Tasks {
		t, made, terr := o.planTask(ctx, src, project, m.ID.String(), tl, pt, earlier)
		if terr != nil {
			return ApplyOut{}, terr
		}
		earlier[pt.Key] = t
		out.Tasks = append(out.Tasks, AppliedTask{Key: pt.Key, ID: t.ID.String(), Created: made})
		extra := ""
		if n := work.Int(t, fieldTimebox); n > 0 {
			extra += "  timebox " + shortDuration(time.Duration(n)*time.Second)
		}
		if len(pt.After) > 0 {
			extra += "  after " + strings.Join(pt.After, ",")
		}
		if g := work.Field(t, fieldGate); g != "" {
			extra += "  gate " + g
		}
		title := work.Field(t, work.FieldTitle)
		b.WriteString("  " + t.ID.String() + " " + title + strings.Repeat(" ", max(0, applyTitleWidth-len(title))) + extra + "\n")
	}
	out.Human = b.String()
	return out, nil
}

// applyTitleWidth is v0.1.17's %-28s title column.
const applyTitleWidth = 28

// shortDuration prints 20m instead of 20m0s (v0.1.17 task_coord.go).
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// checkFile refuses a plan without a mission title, or a task without key
// and title, or a key used twice.
func checkFile(p File) error {
	if strings.TrimSpace(p.Mission.Title) == "" {
		return errs.New(errs.InvalidInput, "plan: mission.title is required")
	}
	keys := map[string]bool{}
	for i, t := range p.Tasks {
		if t.Key == "" || t.Title == "" {
			return errs.New(errs.InvalidInput, "plan: task "+strconv.Itoa(i+1)+" needs key and title")
		}
		if keys[t.Key] {
			return errs.New(errs.InvalidInput, "plan: task key "+t.Key+" appears twice")
		}
		if t.Gate != "" && t.Gate != schema.TaskGateRequired {
			return errs.New(errs.InvalidInput, "plan: task "+t.Key+": gate must be required")
		}
		// after: is checked here, before anything is written, so a bad
		// key never leaves a half-applied mission behind (planDeps would
		// refuse it only after the mission and earlier tasks exist).
		earlier := make(map[string]row.Row, len(keys))
		for k := range keys {
			earlier[k] = row.Row{}
		}
		if _, err := planDeps(t, earlier, ""); err != nil {
			return err
		}
		keys[t.Key] = true
	}
	return nil
}

// planMission finds the plan's mission (by key, else by exact title) in the
// target store or creates it.
func (o ops) planMission(ctx context.Context, to string, project ids.ID[ids.Project], p File) (row.Row, bool, store.Source, error) {
	title := strings.TrimSpace(p.Mission.Title)
	src := store.Source(to)
	if src == "" {
		src = store.SourceRepo
	}
	rows, err := o.env.List(ctx, src, table)
	if err != nil {
		return row.Row{}, false, "", err
	}
	for _, r := range rows {
		if r.Deleted || work.Field(r, work.FieldProject) != project.String() {
			continue
		}
		if (p.Mission.Key != "" && work.Field(r, work.FieldKey) == p.Mission.Key) || (p.Mission.Key == "" && work.Field(r, work.FieldTitle) == title) {
			return r, false, src, nil
		}
	}
	fields, err := work.Texts(Noun, map[string]string{work.FieldTitle: title, work.FieldStatus: schema.MissionStatusActive, work.FieldKey: p.Mission.Key})
	if err != nil {
		return row.Row{}, false, "", err
	}
	if fields[work.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return row.Row{}, false, "", err
	}
	if len(p.Approvers) > 0 {
		if fields[fieldApprovers], err = approvers(ctx, p.Approvers); err != nil {
			return row.Row{}, false, "", err
		}
	}
	w, err := o.env.Create(ctx, store.Source(to), table, fields)
	if err != nil {
		return row.Row{}, false, "", err
	}
	return w.Row, true, src, nil
}

// applyTasklist is the project's default task list in src, created on
// demand.
func (o ops) applyTasklist(ctx context.Context, src store.Source, project ids.ID[ids.Project]) (string, error) {
	rows, err := o.env.List(ctx, src, schema.TableTasklists)
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		if !r.Deleted && work.Field(r, work.FieldProject) == project.String() && work.Field(r, work.FieldTitle) == applyTasklistTitle {
			return r.ID.String(), nil
		}
	}
	fields, err := work.Texts(Noun, map[string]string{work.FieldTitle: applyTasklistTitle, fieldStatusStr: applyTasklistStatus})
	if err != nil {
		return "", err
	}
	if fields[work.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return "", err
	}
	w, err := o.env.Create(ctx, src, schema.TableTasklists, fields)
	if err != nil {
		return "", err
	}
	return w.Row.ID.String(), nil
}

// dep is one blocked_by edge.
type dep struct {
	TaskID string `json:"task_id"`
	Until  string `json:"until"`
}

// planTask finds a task by its plan key in the mission, or creates it.
func (o ops) planTask(ctx context.Context, src store.Source, project ids.ID[ids.Project], mission, tasklist string, pt FileTask, earlier map[string]row.Row) (row.Row, bool, error) {
	rows, err := o.env.List(ctx, src, schema.TableTasks)
	if err != nil {
		return row.Row{}, false, err
	}
	for _, r := range rows {
		if !r.Deleted && work.Field(r, work.FieldMission) == mission && work.Field(r, work.FieldKey) == pt.Key {
			return r, false, nil
		}
	}
	fields, err := work.Texts(Noun, map[string]string{
		work.FieldTitle: strings.TrimSpace(pt.Title), work.FieldKey: pt.Key, work.FieldStatus: schema.TaskStatusTodo,
		fieldPriority: schema.TaskPriorityMedium, fieldCommitment: schema.TaskCommitmentAccepted, fieldRole: pt.Role, fieldGate: pt.Gate,
	})
	if err != nil {
		return row.Row{}, false, err
	}
	refs := map[string]string{work.FieldProject: project.String(), work.FieldMission: mission, work.FieldTasklist: tasklist}
	repoID := ""
	if pt.Repo != "" {
		r, rerr := work.RepoFor(ctx, o.env, src, pt.Repo)
		if rerr != nil {
			return row.Row{}, false, rerr
		}
		repoID = r.String()
		refs[work.FieldRepo] = repoID
	}
	for f, raw := range refs {
		id, perr := ids.ParseAny(raw)
		if perr != nil {
			return row.Row{}, false, errs.Wrap(errs.Internal, "plan: "+f, perr)
		}
		if fields[f], err = value.RefOf(id); err != nil {
			return row.Row{}, false, err
		}
	}
	deps, err := planDeps(pt, earlier, repoID)
	if err != nil {
		return row.Row{}, false, err
	}
	if len(deps) > 0 {
		j, jerr := json.Marshal(deps)
		if jerr != nil {
			return row.Row{}, false, errs.Wrap(errs.Internal, "plan: encode after", jerr)
		}
		if fields[fieldBlockedBy], err = value.JSONOf(j); err != nil {
			return row.Row{}, false, err
		}
	}
	if pt.Timebox != "" {
		d, derr := time.ParseDuration(pt.Timebox)
		if derr != nil {
			return row.Row{}, false, errs.Wrap(errs.InvalidInput, "plan: task "+pt.Key+": timebox "+pt.Timebox, derr)
		}
		fields[fieldTimebox] = value.Int(int64(d / time.Second))
	}
	w, err := o.env.Create(ctx, src, schema.TableTasks, fields)
	if err != nil {
		return row.Row{}, false, err
	}
	return w.Row, true, nil
}

// planDeps turns a plan task's after: keys into blocked_by edges (v0.1.17
// plan_apply.go planDeps): each key names an earlier task of the plan,
// optionally with :done or :merged; without a suffix a task of the same
// repo waits for done and one of another repo for merged (F9). A key named
// twice is one edge (the first wins); a key that is not an earlier task,
// or an unknown suffix, is E_INVALID_INPUT.
func planDeps(pt FileTask, earlier map[string]row.Row, repoID string) ([]dep, error) {
	var deps []dep
	seen := map[string]bool{}
	for _, raw := range pt.After {
		k, until := strings.TrimSpace(raw), ""
		for _, mode := range []string{untilDone, untilMerged} {
			if base, ok := strings.CutSuffix(k, ":"+mode); ok {
				k, until = base, mode
			}
		}
		up, ok := earlier[k]
		if !ok {
			return nil, errs.New(errs.InvalidInput, "plan: task "+pt.Key+" is after "+raw+", which is not an earlier task of the plan").
				WithHint("after: takes keys of earlier tasks, optionally with :done or :merged (e.g. backend:merged)")
		}
		if until == "" {
			until = untilDone
			if work.Field(up, work.FieldRepo) != repoID {
				until = untilMerged
			}
		}
		if !seen[up.ID.String()] {
			seen[up.ID.String()] = true
			deps = append(deps, dep{TaskID: up.ID.String(), Until: until})
		}
	}
	return deps, nil
}
