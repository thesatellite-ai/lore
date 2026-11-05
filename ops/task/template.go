package task

// template.go — task templates (TASK_SYSTEM_DESIGN 11): settings rows of
// task_templates holding a task tree in the mission-file task grammar,
// applied with arguments.
//
//	template add|edit ─► readTemplateFile (YAML, unknown fields refused)
//	    ─► checkTemplate (snake_case name, args, ${arg:…} declared,
//	       after positions, depth) ─► Create / Update the ttp_ row
//	template add --from tsk_ ─► snapshotTree (forest) ─► same write
//	task add --template ─► findTemplate (nearest store; two levels → ask)
//	    ─► bindArgs (required, unknown, typed, defaults) ─► expand once
//	    ─► dry-run lines | one bat_ batch: o.add per node (--key on root)
//
// `${arg:name}` is expanded once at apply time and only in a template's
// task tree (11.2).

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/checklist"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Template operation names (11.3).
const (
	OpTemplateList   = "task.template.list"
	OpTemplateShow   = "task.template.show"
	OpTemplateAdd    = "task.template.add"
	OpTemplateEdit   = "task.template.edit"
	OpTemplateRename = "task.template.rename"
	OpTemplateDelete = "task.template.delete"
)

// tplTable is the task_templates table.
const tplTable row.Table = schema.TableTaskTemplates

// Template argument types (11.2 args: {type, required, default}).
const (
	ArgText   = "text"
	ArgNumber = "number"
)

// argPattern is the one `${arg:name}` placeholder (REFERENCES syntax).
var argPattern = regexp.MustCompile(`\$\{arg:([^}]*)\}`)

// TemplateArg is one declared argument.
type TemplateArg struct {
	Type     string `json:"type" yaml:"type"`
	Required bool   `json:"required,omitempty" yaml:"required"`
	Default  string `json:"default,omitempty" yaml:"default"`
}

// TemplateTask is one node of the task grammar (mission file + children
// and checklist, 11.2). After points at earlier siblings by position.
type TemplateTask struct {
	Title     string         `json:"title" yaml:"title"`
	Body      string         `json:"body,omitempty" yaml:"body"`
	Priority  string         `json:"priority,omitempty" yaml:"priority"`
	Repo      string         `json:"repo,omitempty" yaml:"repo"`
	Estimate  string         `json:"estimate,omitempty" yaml:"estimate"`
	Gate      string         `json:"gate,omitempty" yaml:"gate"`
	Role      string         `json:"role,omitempty" yaml:"role"`
	Checklist []string       `json:"checklist,omitempty" yaml:"checklist"`
	After     []int          `json:"after,omitempty" yaml:"after"`
	Children  []TemplateTask `json:"children,omitempty" yaml:"children"`
}

// TemplateFile is the YAML form a person writes (11.2).
type TemplateFile struct {
	Name        string                 `json:"name" yaml:"name"`
	Description string                 `json:"description,omitempty" yaml:"description"`
	Args        map[string]TemplateArg `json:"args,omitempty" yaml:"args"`
	Task        TemplateTask           `json:"task" yaml:"task"`
}

// TemplateNameIn names one template (name or ttp_ id).
type TemplateNameIn struct {
	Name string `json:"name" arg:"0" desc:"the template name (or its ttp_ id)"`
}

// TemplateAddIn is the input of task.template.add / edit.
type TemplateAddIn struct {
	Name     string `json:"name" arg:"0" desc:"the template name (snake_case)"`
	FromFile string `json:"from_file,omitempty" flag:"from-file" desc:"the YAML form (name, description, args, task) from this file"`
	From     string `json:"from,omitempty" flag:"from" desc:"snapshot this task and its sub-tasks (tsk_…) as the template (add only)"`
	To       string `json:"to,omitempty" flag:"to" desc:"store to write to (default repo)"`
}

// TemplateRenameIn is the input of task.template.rename.
type TemplateRenameIn struct {
	Name    string `json:"name" arg:"0" desc:"the template name (or its ttp_ id)"`
	NewName string `json:"new_name" arg:"1" desc:"the new name (snake_case); the id stays"`
}

// TemplateOut is one template.
type TemplateOut struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Store       string `json:"store,omitempty"`
	// Args and Nodes are the flat output views (an op output holds no
	// maps or recursive types): args sorted by name, nodes depth first.
	Args  []TemplateArgOut  `json:"args,omitempty"`
	Nodes []TemplateNodeOut `json:"nodes"`
	Human string            `json:"-"`
	// file is the template as stored (args map + task tree).
	file TemplateFile
}

// TemplateArgOut is one declared argument in an output.
type TemplateArgOut struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	Default  string `json:"default,omitempty"`
}

// TemplateNodeOut is one task of the tree in an output; Path is the
// position path ("0.1" = second child of the first child), After the
// earlier sibling positions it waits on.
type TemplateNodeOut struct {
	Path      string   `json:"path"`
	Depth     int      `json:"depth"`
	Title     string   `json:"title"`
	Priority  string   `json:"priority,omitempty"`
	Repo      string   `json:"repo,omitempty"`
	Estimate  string   `json:"estimate,omitempty"`
	Gate      string   `json:"gate,omitempty"`
	Role      string   `json:"role,omitempty"`
	Checklist []string `json:"checklist,omitempty"`
	After     []string `json:"after,omitempty"`
}

// flatNodes is t as output nodes, depth first.
func flatNodes(t TemplateTask, path string, depth int, out []TemplateNodeOut) []TemplateNodeOut {
	n := TemplateNodeOut{Path: path, Depth: depth, Title: t.Title, Priority: t.Priority, Repo: t.Repo, Estimate: t.Estimate, Gate: t.Gate, Role: t.Role, Checklist: t.Checklist}
	for _, a := range t.After {
		n.After = append(n.After, strconv.Itoa(a))
	}
	out = append(out, n)
	for i, c := range t.Children {
		p := strconv.Itoa(i)
		if path != "" {
			p = path + "." + p
		}
		out = flatNodes(c, p, depth+1, out)
	}
	return out
}

// Text implements op.Texter.
func (o TemplateOut) Text() string { return o.Human }

// TemplateListIn is the (empty) input of task.template.list.
type TemplateListIn struct{}

// TemplateListOut is the output of task.template.list.
type TemplateListOut struct {
	Data  []TemplateOut `json:"data"`
	Human string        `json:"-"`
}

// Text implements op.Texter.
func (o TemplateListOut) Text() string { return o.Human }

// templateOps are the task template operations.
func templateOps(o ops) []op.Entry {
	w := append(slices.Clone(work.WriteErrors), errs.Template, errs.AmbiguousID)
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpTemplateList, Summary: "List task templates (nearest store first)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: work.ReadErrors,
		}, o.templateList),
		op.Define(op.Spec{
			Name: OpTemplateShow, Summary: "Show a task template in its YAML form",
			Permission: op.PermRead, Access: op.AccessRead, Errors: append(slices.Clone(work.ReadErrors), errs.Template, errs.AmbiguousID),
		}, o.templateShow),
		op.Define(op.Spec{
			Name: OpTemplateAdd, Summary: "Add a task template from a YAML file or an existing task tree",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: w,
			Examples: []op.Example{{Title: "from a file", CLI: "lore task template add release --from-file release.yaml", Want: "ttp_"}},
		}, o.templateAdd),
		op.Define(op.Spec{
			Name: OpTemplateEdit, Summary: "Replace a task template's description, args and tree from a YAML file",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: w,
		}, o.templateEdit),
		op.Define(op.Spec{
			Name: OpTemplateRename, Summary: "Rename a task template (the id stays)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: w,
		}, o.templateRename),
		op.Define(op.Spec{
			Name: OpTemplateDelete, Summary: "Delete a task template (trash, like any row)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(w, errs.HasDependents),
			Confirm: op.ConfirmRule{Always: true},
		}, o.templateDelete),
	}
}

// tplErr is an E_TEMPLATE refusal.
func tplErr(msg string) *errs.Error { return errs.New(errs.Template, "task template: "+msg) }

// readTemplateFile reads the YAML form; unknown fields are refused (11.3).
func readTemplateFile(path string) (TemplateFile, error) {
	raw, err := os.ReadFile(filepath.Clean(path)) // a path the caller names (like split --from)
	if err != nil {
		return TemplateFile{}, errs.Wrap(errs.InvalidInput, "task template: read "+path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var f TemplateFile
	if err = dec.Decode(&f); err != nil {
		return TemplateFile{}, errs.Wrap(errs.Template, "task template: "+path+" is not a template YAML form", err)
	}
	return f, nil
}

// checkTemplate validates a template on save (11.3): snake_case name,
// known arg types, every ${arg:…} declared, positions, depth.
func checkTemplate(f TemplateFile, maxDepth int) error {
	if !row.ValidName(f.Name) {
		return tplErr(strconv.Quote(f.Name) + " is not a snake_case name (MQ10)")
	}
	for name, a := range f.Args {
		if !row.ValidName(name) {
			return tplErr("argument " + strconv.Quote(name) + " is not snake_case")
		}
		if a.Type == "" {
			a.Type = ArgText
		}
		if a.Type != ArgText && a.Type != ArgNumber {
			return tplErr("argument " + name + " has type " + strconv.Quote(a.Type) + "; use text or number")
		}
		if a.Default != "" {
			if err := argTyped(name, a.Type, a.Default); err != nil {
				return err
			}
		}
	}
	if h := treeHeight(f.Task); h > maxDepth {
		return tplErr("the tree is " + strconv.Itoa(h) + " levels deep; tasks.max_depth is " + strconv.Itoa(maxDepth))
	}
	return checkNode(f.Task, f.Args, "task", 0)
}

// checkNode validates one node and its children.
func checkNode(t TemplateTask, args map[string]TemplateArg, at string, self int) error {
	if strings.TrimSpace(t.Title) == "" {
		return tplErr(at + " has no title")
	}
	if t.Priority != "" && !strings.Contains(t.Priority, "${") && !slices.Contains(schema.TaskPriorities(), t.Priority) {
		return tplErr(at + ": priority " + strconv.Quote(t.Priority) + " is not one of " + strings.Join(schema.TaskPriorities(), ", "))
	}
	if t.Gate != "" && !slices.Contains(schema.TaskGates(), t.Gate) {
		return tplErr(at + ": gate " + strconv.Quote(t.Gate) + " is not one of " + strings.Join(schema.TaskGates(), ", "))
	}
	for _, p := range t.After {
		// DECISION OV15C-2: after points at an EARLIER sibling only, so
		// apply creates each task after the ones it waits on.
		if p < 0 || p >= self {
			return tplErr(at + ": after " + strconv.Itoa(p) + " is not an earlier sibling (0.." + strconv.Itoa(self-1) + ")")
		}
	}
	for _, s := range append([]string{t.Title, t.Body, t.Priority, t.Repo, t.Estimate, t.Role}, t.Checklist...) {
		for _, m := range argPattern.FindAllStringSubmatch(s, -1) {
			if _, ok := args[m[1]]; !ok {
				return tplErr(at + " uses ${arg:" + m[1] + "}, which args does not declare")
			}
		}
	}
	for i, c := range t.Children {
		if err := checkNode(c, args, at+".children["+strconv.Itoa(i)+"]", i); err != nil {
			return err
		}
	}
	return nil
}

// treeHeight is the number of levels of t.
func treeHeight(t TemplateTask) int {
	h := 0
	for _, c := range t.Children {
		h = max(h, treeHeight(c))
	}
	return h + 1
}

// countTree is the number of sub-tasks and checklist items under t.
func countTree(t TemplateTask) (kids, items int) {
	items = len(t.Checklist)
	for _, c := range t.Children {
		k, i := countTree(c)
		kids, items = kids+1+k, items+i
	}
	return kids, items
}

// argTyped checks one argument value against its type.
func argTyped(name, typ, v string) error {
	if typ == ArgNumber {
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return tplErr("argument " + name + " is a number; got " + strconv.Quote(v))
		}
	}
	return nil
}

// bindArgs reads --arg name=value against the declared args: unknown and
// missing required ones refuse, defaults fill the rest (11.3).
func bindArgs(decl map[string]TemplateArg, given []string) (map[string]string, error) {
	out := map[string]string{}
	for _, g := range given {
		k, v, ok := strings.Cut(g, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, tplErr("--arg " + strconv.Quote(g) + " is not name=value")
		}
		a, known := decl[k]
		if !known {
			return nil, tplErr("unknown argument " + strconv.Quote(k) + "; the template takes: " + strings.Join(argNames(decl), ", "))
		}
		if err := argTyped(k, orDefault(a.Type, ArgText), v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	for _, k := range argNames(decl) {
		if _, set := out[k]; set {
			continue
		}
		switch a := decl[k]; {
		case a.Default != "":
			out[k] = a.Default
		case a.Required:
			return nil, tplErr("argument " + k + " is required: --arg " + k + "=<value>")
		default:
			out[k] = ""
		}
	}
	return out, nil
}

// argNames are the declared names, sorted.
func argNames(decl map[string]TemplateArg) []string {
	out := make([]string, 0, len(decl))
	for k := range decl {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// expand replaces every ${arg:…} of the tree with its value (once).
func expand(t TemplateTask, vals map[string]string) TemplateTask {
	sub := func(s string) string {
		return argPattern.ReplaceAllStringFunc(s, func(m string) string { return vals[argPattern.FindStringSubmatch(m)[1]] })
	}
	t.Title, t.Body, t.Priority, t.Repo, t.Estimate, t.Role = sub(t.Title), sub(t.Body), sub(t.Priority), sub(t.Repo), sub(t.Estimate), sub(t.Role)
	cl := make([]string, len(t.Checklist))
	for i, c := range t.Checklist {
		cl[i] = sub(c)
	}
	t.Checklist = cl
	kids := make([]TemplateTask, len(t.Children))
	for i, c := range t.Children {
		kids[i] = expand(c, vals)
	}
	t.Children = kids
	return t
}

// templateOf decodes a task_templates row.
func templateOf(src store.Source, r row.Row) (TemplateOut, error) {
	out := TemplateOut{ID: r.ID.String(), Name: work.Field(r, schema.FieldTemplateName), Description: work.Field(r, "description"), Store: work.SourceName(src)}
	out.file.Name, out.file.Description = out.Name, out.Description
	if raw, ok := jsonField(r, schema.FieldTemplateArgs); ok {
		if err := json.Unmarshal(raw, &out.file.Args); err != nil {
			return TemplateOut{}, errs.Wrap(errs.Template, "task template "+out.Name+": bad args", err)
		}
	}
	if raw, ok := jsonField(r, schema.FieldTemplateTask); ok {
		if err := json.Unmarshal(raw, &out.file.Task); err != nil {
			return TemplateOut{}, errs.Wrap(errs.Template, "task template "+out.Name+": bad task", err)
		}
	}
	for _, k := range argNames(out.file.Args) {
		a := out.file.Args[k]
		out.Args = append(out.Args, TemplateArgOut{Name: k, Type: orDefault(a.Type, ArgText), Required: a.Required, Default: a.Default})
	}
	out.Nodes = flatNodes(out.file.Task, "", 0, nil)
	return out, nil
}

// jsonField is the JSON form of a row field (false when absent).
func jsonField(r row.Row, field string) ([]byte, bool) {
	v := r.Fields[field]
	if v == nil || v.Kind() == value.KindNull {
		return nil, false
	}
	raw, err := v.MarshalJSON()
	return raw, err == nil
}

// templateFields is f as a row's fields.
func templateFields(f TemplateFile) (map[string]value.Value, error) {
	fields, err := work.Texts(Noun, map[string]string{schema.FieldTemplateName: f.Name, "description": f.Description})
	if err != nil {
		return nil, err
	}
	args, err := json.Marshal(f.Args)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "task template: encode args", err)
	}
	task, err := json.Marshal(f.Task)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "task template: encode task", err)
	}
	if fields[schema.FieldTemplateArgs], err = value.JSONOf(args); err != nil {
		return nil, err
	}
	if fields[schema.FieldTemplateTask], err = value.JSONOf(task); err != nil {
		return nil, err
	}
	return fields, nil
}

// findTemplate resolves a name (or ttp_ id) nearest store first; the same
// name in two stores refuses with both ids (RQ10: a person picks).
func (o ops) findTemplate(ctx context.Context, name string) (store.Source, row.Row, error) {
	if strings.HasPrefix(name, string(ids.PrefixTaskTemplate)+"_") {
		id, err := work.ParseID("task template", ids.PrefixTaskTemplate, name)
		if err != nil {
			return "", row.Row{}, err
		}
		return work.Get(ctx, o.env, "task template", tplTable, id)
	}
	all, err := work.ListAll(ctx, o.env, tplTable, "")
	if err != nil {
		return "", row.Row{}, err
	}
	var hits []work.Sourced
	for _, s := range all {
		if !s.Row.Deleted && work.Field(s.Row, schema.FieldTemplateName) == name {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 0:
		return "", row.Row{}, errs.New(errs.NotFound, "task template "+strconv.Quote(name)+" not found").WithHint("lore task template list")
	case 1:
		return hits[0].Source, hits[0].Row, nil
	}
	var where []string
	for _, h := range hits {
		where = append(where, h.Row.ID.String()+" ("+orDefault(work.SourceName(h.Source), "repo")+")")
	}
	return "", row.Row{}, errs.New(errs.AmbiguousID, "task template "+strconv.Quote(name)+" is defined in more than one store: "+strings.Join(where, ", ")).
		WithHint("name it by id: --template " + hits[0].Row.ID.String())
}

// templateList lists every template, nearest store first.
func (o ops) templateList(ctx context.Context, _ TemplateListIn) (TemplateListOut, error) {
	all, err := work.ListAll(ctx, o.env, tplTable, "")
	if err != nil {
		return TemplateListOut{}, err
	}
	out := TemplateListOut{Data: []TemplateOut{}}
	var b strings.Builder
	for _, s := range all {
		if s.Row.Deleted {
			continue
		}
		t, terr := templateOf(s.Source, s.Row)
		if terr != nil {
			return TemplateListOut{}, terr
		}
		out.Data = append(out.Data, t)
		kids, items := countTree(t.file.Task)
		b.WriteString(t.Name + "  " + orDefault(t.Store, "repo") + "  1 task, " + strconv.Itoa(kids) + " sub-tasks, " + strconv.Itoa(items) + " checklist items\n")
	}
	if len(out.Data) == 0 {
		b.WriteString("no task templates (lore task template add <name> --from-file <file.yaml>)\n")
	}
	out.Human = b.String()
	return out, nil
}

// templateShow prints a template's YAML form.
func (o ops) templateShow(ctx context.Context, in TemplateNameIn) (TemplateOut, error) {
	src, r, err := o.findTemplate(ctx, in.Name)
	if err != nil {
		return TemplateOut{}, err
	}
	t, err := templateOf(src, r)
	if err != nil {
		return TemplateOut{}, err
	}
	raw, err := yaml.Marshal(t.file)
	if err != nil {
		return TemplateOut{}, errs.Wrap(errs.Internal, "task template: yaml", err)
	}
	t.Human = "# " + t.ID + " (" + orDefault(t.Store, "repo") + ")\n" + string(raw)
	return t, nil
}

// templateSource is the YAML form of an add or edit (--from-file, or
// --from tsk_ for add).
func (o ops) templateSource(ctx context.Context, in TemplateAddIn, allowFrom bool) (TemplateFile, error) {
	switch {
	case in.FromFile != "" && in.From != "":
		return TemplateFile{}, errs.New(errs.InvalidInput, "task template: give --from-file or --from, not both")
	case in.FromFile != "":
		return readTemplateFile(in.FromFile)
	case in.From != "" && allowFrom:
		return o.snapshot(ctx, in.From)
	}
	hint := "task template: give --from-file <file.yaml>"
	if allowFrom {
		hint += " or --from tsk_…"
	}
	return TemplateFile{}, errs.New(errs.InvalidInput, hint)
}

// templateAdd writes a new template row.
func (o ops) templateAdd(ctx context.Context, in TemplateAddIn) (TemplateOut, error) {
	f, err := o.templateSource(ctx, in, true)
	if err != nil {
		return TemplateOut{}, err
	}
	f.Name = strings.TrimSpace(in.Name)
	maxDepth, _, err := o.treePolicy(ctx, store.Source(in.To))
	if err != nil {
		return TemplateOut{}, err
	}
	if err = checkTemplate(f, maxDepth); err != nil {
		return TemplateOut{}, err
	}
	// PC-TASK-2 (RQ10): a name any attached store already holds is
	// refused, not only one in the target store, so add never makes a name
	// ambiguous; two stores can still meet through a sync, and
	// findTemplate refuses that case naming both.
	all, err := work.ListAll(ctx, o.env, tplTable, "")
	if err != nil {
		return TemplateOut{}, err
	}
	for _, s := range all {
		if !s.Row.Deleted && work.Field(s.Row, schema.FieldTemplateName) == f.Name {
			return TemplateOut{}, tplErr(strconv.Quote(f.Name) + " already exists (" + s.Row.ID.String() + " in " +
				orDefault(work.SourceName(s.Source), "repo") + ")").WithHint("lore task template edit " + s.Row.ID.String() + " --from-file <file.yaml>")
		}
	}
	fields, err := templateFields(f)
	if err != nil {
		return TemplateOut{}, err
	}
	src, w, err := work.Create(ctx, o.env, in.To, tplTable, fields)
	if err != nil {
		return TemplateOut{}, err
	}
	out, err := templateOf(src, w.Row)
	if err != nil {
		return TemplateOut{}, err
	}
	kids, items := countTree(f.Task)
	out.Human = work.Success + " " + out.ID + " — template " + f.Name + ": 1 task, " + strconv.Itoa(kids) + " sub-tasks, " + strconv.Itoa(items) + " checklist items\n"
	return out, nil
}

// templateEdit replaces description, args and tree (the name stays;
// rename changes it).
func (o ops) templateEdit(ctx context.Context, in TemplateAddIn) (TemplateOut, error) {
	src, cur, err := o.findTemplate(ctx, in.Name)
	if err != nil {
		return TemplateOut{}, err
	}
	f, err := o.templateSource(ctx, in, false)
	if err != nil {
		return TemplateOut{}, err
	}
	f.Name = work.Field(cur, schema.FieldTemplateName)
	maxDepth, _, err := o.treePolicy(ctx, src)
	if err != nil {
		return TemplateOut{}, err
	}
	if err = checkTemplate(f, maxDepth); err != nil {
		return TemplateOut{}, err
	}
	fields, err := templateFields(f)
	if err != nil {
		return TemplateOut{}, err
	}
	if f.Description == "" {
		fields["description"] = value.Null{}
	}
	w, err := o.hist().Update(ctx, engine.Ref{Table: tplTable, ID: cur.ID}, fields)
	if err != nil {
		return TemplateOut{}, err
	}
	out, err := templateOf(src, w.Row)
	if err != nil {
		return TemplateOut{}, err
	}
	out.Human = work.Success + " " + out.ID + " — template " + f.Name + " updated\n"
	return out, nil
}

// templateRename changes the label; the id is permanent (OQ20).
func (o ops) templateRename(ctx context.Context, in TemplateRenameIn) (TemplateOut, error) {
	src, cur, err := o.findTemplate(ctx, in.Name)
	if err != nil {
		return TemplateOut{}, err
	}
	name := strings.TrimSpace(in.NewName)
	if !row.ValidName(name) {
		return TemplateOut{}, tplErr(strconv.Quote(name) + " is not a snake_case name (MQ10)")
	}
	if _, _, ferr := o.findTemplate(ctx, name); ferr == nil || errs.Has(ferr, errs.AmbiguousID) {
		return TemplateOut{}, tplErr(strconv.Quote(name) + " already exists")
	}
	patch, err := work.Texts(Noun, map[string]string{schema.FieldTemplateName: name})
	if err != nil {
		return TemplateOut{}, err
	}
	w, err := o.hist().Update(ctx, engine.Ref{Table: tplTable, ID: cur.ID}, patch)
	if err != nil {
		return TemplateOut{}, err
	}
	out, err := templateOf(src, w.Row)
	if err != nil {
		return TemplateOut{}, err
	}
	out.Human = work.Success + " " + out.ID + " — template " + in.Name + " renamed to " + name + "\n"
	return out, nil
}

// templateDelete soft-deletes the row (trash like any row). A recurring
// series made from it keeps its own copied tree (11.4).
func (o ops) templateDelete(ctx context.Context, in TemplateNameIn) (work.WriteOut, error) {
	src, cur, err := o.findTemplate(ctx, in.Name)
	if err != nil {
		return work.WriteOut{}, err
	}
	if _, err = o.env.Delete(ctx, engine.Ref{Table: tplTable, ID: cur.ID}); err != nil {
		return work.WriteOut{}, err
	}
	return work.WriteOut{ID: cur.ID.String(), Store: work.SourceName(src), Human: trashline.Moved(cur.ID.String()+" — template "+work.Field(cur, schema.FieldTemplateName), cur.ID.String())}, nil
}

// snapshot reads task id and its sub-tasks as a template tree (11.3
// --from): ids, statuses, time and people dropped; after edges between
// siblings kept as positions.
func (o ops) snapshot(ctx context.Context, raw string) (TemplateFile, error) {
	id, err := taskID(raw)
	if err != nil {
		return TemplateFile{}, err
	}
	_, cur, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return TemplateFile{}, err
	}
	f, err := o.forest(ctx, work.Field(cur, work.FieldProject))
	if err != nil {
		return TemplateFile{}, err
	}
	return TemplateFile{Task: snapNode(f, cur, nil)}, nil
}

// snapNode is one row as a template node; sibs maps sibling ids to their
// positions (for after).
func snapNode(f forest, r row.Row, sibs map[string]int) TemplateTask {
	t := TemplateTask{
		Title: work.Field(r, work.FieldTitle), Body: work.Field(r, work.FieldBody), Priority: work.Field(r, fieldPriority),
		Repo: work.Field(r, work.FieldRepo), Estimate: work.Field(r, fieldEstimate), Gate: work.Field(r, fieldGate), Role: work.Field(r, fieldRole),
	}
	for _, it := range checklist.Live(Checklist(r)) {
		t.Checklist = append(t.Checklist, it.Text)
	}
	for _, d := range depsOf(r) {
		if p, ok := sibs[d.TaskID]; ok {
			t.After = append(t.After, p)
		}
	}
	kids := f.tree.Children(r.ID.String())
	pos := map[string]int{}
	for i, k := range kids {
		pos[k] = i
	}
	for _, k := range kids {
		t.Children = append(t.Children, snapNode(f, f.row(k).Row, pos))
	}
	return t
}

// TemplateAdded is one task a template apply added (or would add).
type TemplateAdded struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title"`
	Depth int    `json:"depth"`
}

// addTemplate is task add --template: bind args, expand, then either the
// dry-run tree or one bat_ batch of o.add calls (the root with --key, so a
// re-apply is a no-op).
func (o ops) addTemplate(ctx context.Context, in AddIn) (AddOut, error) {
	if strings.TrimSpace(in.Title) != "" {
		return AddOut{}, errs.New(errs.InvalidInput, "task add --template: the title comes from the template; drop the title argument")
	}
	if in.Parent != "" || in.Recur != "" {
		return AddOut{}, errs.New(errs.InvalidInput, "task add --template: --parent and --recur are not supported with a template yet")
	}
	src, r, err := o.findTemplate(ctx, in.Template)
	if err != nil {
		return AddOut{}, err
	}
	t, err := templateOf(src, r)
	if err != nil {
		return AddOut{}, err
	}
	vals, err := bindArgs(t.file.Args, in.Args)
	if err != nil {
		return AddOut{}, err
	}
	tree := expand(t.file.Task, vals)
	var plan []TemplateAdded
	walkTemplate(tree, 0, func(n TemplateTask, depth int) { plan = append(plan, TemplateAdded{Title: n.Title, Depth: depth}) })
	if in.DryRun {
		var b strings.Builder
		b.WriteString("would add " + strconv.Itoa(len(plan)) + " tasks (template " + t.Name + "):\n")
		for _, p := range plan {
			b.WriteString("  " + strings.Repeat("  ", p.Depth) + p.Title + "\n")
		}
		return AddOut{SchemaVersion: work.SchemaVersion, Kind: KindShow, Human: b.String(), Template: plan}, nil
	}
	ctx, _, err = bulk.Begin(ctx, bulk.KindTask)
	if err != nil {
		return AddOut{}, err
	}
	base := in
	base.Template, base.Args, base.DryRun = "", nil, false
	root, existed, err := o.applyNode(ctx, base, tree, "")
	if err != nil {
		return AddOut{}, err
	}
	if existed {
		root.Human = strings.TrimSuffix(root.Human, "\n") + "\n  template " + t.Name + ": nothing added\n"
		return root, nil
	}
	n, err := o.applyChildren(ctx, base, tree, root.Data.ID)
	if err != nil {
		return AddOut{}, err
	}
	root.Human += "  template " + t.Name + ": added 1 task and " + strconv.Itoa(n) + " sub-tasks\n"
	return root, nil
}

// walkTemplate visits t depth first.
func walkTemplate(t TemplateTask, depth int, visit func(TemplateTask, int)) {
	visit(t, depth)
	for _, c := range t.Children {
		walkTemplate(c, depth+1, visit)
	}
}

// applyNode adds one node through o.add (every add rule applies), then its
// checklist and gate in one update. existed is true when --key found it.
func (o ops) applyNode(ctx context.Context, base AddIn, n TemplateTask, parent string) (AddOut, bool, error) {
	in := base
	in.Title, in.Body, in.Role, in.Estimate = n.Title, n.Body, orDefault(n.Role, base.Role), orDefault(n.Estimate, base.Estimate)
	in.Priority = orDefault(n.Priority, base.Priority)
	in.Repo = orDefault(n.Repo, base.Repo)
	if parent != "" {
		in.Parent, in.Key, in.Mission, in.Tasklist, in.Plan = parent, "", "", "", ""
	}
	out, err := o.add(ctx, in)
	if err != nil {
		return AddOut{}, false, err
	}
	if out.existed {
		return out, true, nil
	}
	patch := map[string]value.Value{}
	if len(n.Checklist) > 0 {
		by, now := o.by(ctx), opclock.Now(o.env)
		var list []checklist.Item
		rank := ""
		for _, text := range n.Checklist {
			rank = tasktree.After(rank)
			it, cerr := checklist.New(text, rank, by, now)
			if cerr != nil {
				return AddOut{}, false, cerr
			}
			list = append(list, it)
		}
		if err = putChecklist(patch, list); err != nil {
			return AddOut{}, false, err
		}
	}
	if n.Gate != "" {
		if patch[fieldGate], err = value.TextOf(n.Gate); err != nil {
			return AddOut{}, false, err
		}
	}
	if len(patch) > 0 {
		id, perr := taskID(out.Data.ID)
		if perr != nil {
			return AddOut{}, false, perr
		}
		if _, err = o.hist().Update(ctx, engine.Ref{Table: table, ID: id}, patch); err != nil {
			return AddOut{}, false, err
		}
	}
	return out, false, nil
}

// applyChildren adds n's children under parent, in order; after edges
// name earlier siblings by position.
func (o ops) applyChildren(ctx context.Context, base AddIn, n TemplateTask, parent string) (int, error) {
	count := 0
	made := make([]string, 0, len(n.Children))
	for _, c := range n.Children {
		in := base
		in.After = nil
		for _, p := range c.After {
			in.After = append(in.After, made[p])
		}
		out, _, err := o.applyNode(ctx, in, c, parent)
		if err != nil {
			return count, err
		}
		made = append(made, out.Data.ID)
		k, err := o.applyChildren(ctx, base, c, out.Data.ID)
		if err != nil {
			return count, err
		}
		count += 1 + k
	}
	return count, nil
}
