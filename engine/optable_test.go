package engine

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// TestOpTableEveryMetaNoun pins opTable (OV10-BIND, later 253): every
// noun whose table takes --meta maps its ops (add, edit and sub-ops such as
// task.checklist.add) to that table, so a command's --meta binds to its
// own row and never to a row it creates on the side (task add's default
// task list); a noun that is not a metadata table maps to "".
func TestOpTableEveryMetaNoun(t *testing.T) {
	want := map[string]row.Table{
		"architecturenote.add": schema.TableArchitectureNotes, "behaviour.add": schema.TableBehaviours,
		"comment.add": schema.TableComments, "cookbookrecipe.edit": schema.TableCookbookRecipes,
		"decision.add": schema.TableDecisions, "external_source.add": schema.TableExternalSources,
		"handoff.add": schema.TableHandoffs, "hotfix.edit": schema.TableHotfixes,
		"incident.add": schema.TableIncidents, "memory.add": schema.TableMemories,
		"mission.add": schema.TableMissions, "pattern.edit": schema.TablePatterns,
		"plan.add": schema.TablePlans, "playbook.add": schema.TablePlaybooks,
		"prompt.edit": schema.TablePrompts, "reminder.add": schema.TableReminders,
		"rule.add": schema.TableRules, "snapshot.add": schema.TableSnapshots,
		"suggestion.edit": schema.TableSuggestions, "tasklist.add": schema.TableTasklists,
		"task.add": schema.TableTasks, "task.checklist.add": schema.TableTasks,
		"tastepref.add": schema.TableTastePrefs, "techdoc.add": schema.TableTechDocs,
		"workflow.edit": schema.TableWorkflows, "workspace.add": schema.TableWorkspaces,
		"resource.add": schema.TableResources,
		// not metadata tables: the first metadata write takes the flags.
		"meta.add": "", "link.add": "", "repo.add": "", "row.edit": "", "pii_pattern.add": "", "": "", ".add": "",
	}
	covered := map[row.Table]bool{}
	for op, table := range want {
		if got := opTable(op); got != table {
			t.Errorf("opTable(%q) = %q, want %q", op, got, table)
		}
		covered[table] = true
	}
	for table := range metaTables {
		if !covered[table] {
			t.Errorf("metadata table %s has no opTable case", table)
		}
	}
}
