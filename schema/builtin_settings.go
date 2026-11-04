package schema

import "github.com/thesatellite-ai/lore/model/ids"

// TableTaskTemplates holds task templates (TASK 11.2): settings rows, one
// per template, resolved nearest store first (repo → project → org).
const TableTaskTemplates = "task_templates"

// Task template fields (TASK 11.2).
const (
	FieldTemplateName = "name"
	FieldTemplateArgs = "args"
	FieldTemplateTask = "task"
)

// settingsDefs returns the built-in settings tables beyond coreDefs.
func settingsDefs() []TableDef {
	return []TableDef{
		Table(TableTaskTemplates, ids.PrefixTaskTemplate, Synced(), Description("a named task tree applied with arguments (TASK 11)"),
			Text(FieldTemplateName, Required(), Describe("snake_case label; the id is permanent (OQ20)")),
			Text("description"),
			JSON(FieldTemplateArgs, Describe("{name: {type, required, default}}")),
			// One task grammar (11.2): the mission-file task shape with
			// children[] and checklist[]; one field, so a tree edited on two
			// branches goes to a person.
			JSON(FieldTemplateTask, Required(), Describe("the mission-file task grammar: title, priority, repo, estimate, gate, checklist, children[] with after:[positions]")),
			IndexOn(FieldTemplateName),
		),
	}
}
