package tuigen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbent"
	"dbent/gen/ent"
	entActivityArchive "dbent/gen/ent/activityarchive"
	entActor "dbent/gen/ent/actor"
	entArchitectureNote "dbent/gen/ent/architecturenote"
	entAssembleCitation "dbent/gen/ent/assemblecitation"
	entAssembleRun "dbent/gen/ent/assemblerun"
	entAuditLog "dbent/gen/ent/auditlog"
	entBehaviour "dbent/gen/ent/behaviour"
	entBenchEval "dbent/gen/ent/bencheval"
	entBenchResult "dbent/gen/ent/benchresult"
	entBenchRun "dbent/gen/ent/benchrun"
	entCodeFile "dbent/gen/ent/codefile"
	entCodeSymbol "dbent/gen/ent/codesymbol"
	entComment "dbent/gen/ent/comment"
	entCommitLink "dbent/gen/ent/commitlink"
	entCompressRun "dbent/gen/ent/compressrun"
	entCookbookRecipe "dbent/gen/ent/cookbookrecipe"
	entDBConfig "dbent/gen/ent/dbconfig"
	entDecision "dbent/gen/ent/decision"
	entDraft "dbent/gen/ent/draft"
	entEntityTag "dbent/gen/ent/entitytag"
	entExternalSource "dbent/gen/ent/externalsource"
	entHandoff "dbent/gen/ent/handoff"
	entHotfix "dbent/gen/ent/hotfix"
	entIdentityProfile "dbent/gen/ent/identityprofile"
	entIncident "dbent/gen/ent/incident"
	entInterventionMetric "dbent/gen/ent/interventionmetric"
	entKnowledgeRef "dbent/gen/ent/knowledgeref"
	entKnowledgeRevision "dbent/gen/ent/knowledgerevision"
	entLearnCandidate "dbent/gen/ent/learncandidate"
	entLearnRun "dbent/gen/ent/learnrun"
	entMemory "dbent/gen/ent/memory"
	entMemoryCodeRef "dbent/gen/ent/memorycoderef"
	entMission "dbent/gen/ent/mission"
	entMountAlias "dbent/gen/ent/mountalias"
	entPattern "dbent/gen/ent/pattern"
	entPiiPattern "dbent/gen/ent/piipattern"
	entPlan "dbent/gen/ent/plan"
	entPlaybook "dbent/gen/ent/playbook"
	entProject "dbent/gen/ent/project"
	entProjectConfig "dbent/gen/ent/projectconfig"
	entPrompt "dbent/gen/ent/prompt"
	entQueryLog "dbent/gen/ent/querylog"
	entReminder "dbent/gen/ent/reminder"
	entRenderHistory "dbent/gen/ent/renderhistory"
	entRepo "dbent/gen/ent/repo"
	entRule "dbent/gen/ent/rule"
	entRuleVerifierRef "dbent/gen/ent/ruleverifierref"
	entRun "dbent/gen/ent/run"
	entRunStep "dbent/gen/ent/runstep"
	entSchemaMigration "dbent/gen/ent/schemamigration"
	entSession "dbent/gen/ent/session"
	entSnapshot "dbent/gen/ent/snapshot"
	entSuggestion "dbent/gen/ent/suggestion"
	entTag "dbent/gen/ent/tag"
	entTask "dbent/gen/ent/task"
	entTaskList "dbent/gen/ent/tasklist"
	entTaskView "dbent/gen/ent/taskview"
	entTastePref "dbent/gen/ent/tastepref"
	entTeam "dbent/gen/ent/team"
	entTechDoc "dbent/gen/ent/techdoc"
	entTechDocPage "dbent/gen/ent/techdocpage"
	entTrustedPlugin "dbent/gen/ent/trustedplugin"
	entUser "dbent/gen/ent/user"
	entWorkflow "dbent/gen/ent/workflow"
	entWorkspace "dbent/gen/ent/workspace"
	"dbent/pkg/dbtemplate"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lsync"
)

// templateDB is a migrated lore.db built once and copied per test.
var templateDB string

func TestMain(m *testing.M) { os.Exit(runTests(m)) }

// runTests builds the template DB, runs the tests and cleans up (split
// from TestMain because os.Exit skips deferred calls).
func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "tuigen-template-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	templateDB = filepath.Join(dir, "template.db")
	if err := dbtemplate.Build(context.Background(), templateDB); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

// entTypeTables is every ent type with the table ent really uses for it,
// read from the generated packages (so a renamed table fails to compile
// or fails the matrix, never drifts silently).
func entTypeTables() []struct{ typ, table string } {
	return []struct{ typ, table string }{
		{ent.TypeActivityArchive, entActivityArchive.Table},
		{ent.TypeActor, entActor.Table},
		{ent.TypeArchitectureNote, entArchitectureNote.Table},
		{ent.TypeAssembleCitation, entAssembleCitation.Table},
		{ent.TypeAssembleRun, entAssembleRun.Table},
		{ent.TypeAuditLog, entAuditLog.Table},
		{ent.TypeBehaviour, entBehaviour.Table},
		{ent.TypeBenchEval, entBenchEval.Table},
		{ent.TypeBenchResult, entBenchResult.Table},
		{ent.TypeBenchRun, entBenchRun.Table},
		{ent.TypeCodeFile, entCodeFile.Table},
		{ent.TypeCodeSymbol, entCodeSymbol.Table},
		{ent.TypeComment, entComment.Table},
		{ent.TypeCommitLink, entCommitLink.Table},
		{ent.TypeCompressRun, entCompressRun.Table},
		{ent.TypeCookbookRecipe, entCookbookRecipe.Table},
		{ent.TypeDBConfig, entDBConfig.Table},
		{ent.TypeDecision, entDecision.Table},
		{ent.TypeDraft, entDraft.Table},
		{ent.TypeEntityTag, entEntityTag.Table},
		{ent.TypeExternalSource, entExternalSource.Table},
		{ent.TypeHandoff, entHandoff.Table},
		{ent.TypeHotfix, entHotfix.Table},
		{ent.TypeIdentityProfile, entIdentityProfile.Table},
		{ent.TypeIncident, entIncident.Table},
		{ent.TypeInterventionMetric, entInterventionMetric.Table},
		{ent.TypeKnowledgeRef, entKnowledgeRef.Table},
		{ent.TypeKnowledgeRevision, entKnowledgeRevision.Table},
		{ent.TypeLearnCandidate, entLearnCandidate.Table},
		{ent.TypeLearnRun, entLearnRun.Table},
		{ent.TypeMemory, entMemory.Table},
		{ent.TypeMemoryCodeRef, entMemoryCodeRef.Table},
		{ent.TypeMission, entMission.Table},
		{ent.TypeMountAlias, entMountAlias.Table},
		{ent.TypePattern, entPattern.Table},
		{ent.TypePiiPattern, entPiiPattern.Table},
		{ent.TypePlan, entPlan.Table},
		{ent.TypePlaybook, entPlaybook.Table},
		{ent.TypeProject, entProject.Table},
		{ent.TypeProjectConfig, entProjectConfig.Table},
		{ent.TypePrompt, entPrompt.Table},
		{ent.TypeQueryLog, entQueryLog.Table},
		{ent.TypeReminder, entReminder.Table},
		{ent.TypeRenderHistory, entRenderHistory.Table},
		{ent.TypeRepo, entRepo.Table},
		{ent.TypeRule, entRule.Table},
		{ent.TypeRuleVerifierRef, entRuleVerifierRef.Table},
		{ent.TypeRun, entRun.Table},
		{ent.TypeRunStep, entRunStep.Table},
		{ent.TypeSchemaMigration, entSchemaMigration.Table},
		{ent.TypeSession, entSession.Table},
		{ent.TypeSnapshot, entSnapshot.Table},
		{ent.TypeSuggestion, entSuggestion.Table},
		{ent.TypeTag, entTag.Table},
		{ent.TypeTask, entTask.Table},
		{ent.TypeTaskList, entTaskList.Table},
		{ent.TypeTaskView, entTaskView.Table},
		{ent.TypeTastePref, entTastePref.Table},
		{ent.TypeTeam, entTeam.Table},
		{ent.TypeTechDoc, entTechDoc.Table},
		{ent.TypeTechDocPage, entTechDocPage.Table},
		{ent.TypeTrustedPlugin, entTrustedPlugin.Table},
		{ent.TypeUser, entUser.Table},
		{ent.TypeWorkflow, entWorkflow.Table},
		{ent.TypeWorkspace, entWorkspace.Table},
	}
}

// tableOfType must name ent's real table for every type the guard can
// refuse: a wrong plural (Memory → memorys, Hotfix → hotfixs, TaskList →
// tasklists) would let a TUI edit through to a core-owned mirror row, or
// refuse a table the core does not own.
func TestTableOfType_MatchesEntForEveryGuardedType(t *testing.T) {
	t.Parallel()
	covered := map[string]bool{}
	for _, c := range entTypeTables() {
		got := tableOfType(c.typ)
		if lsync.CoreOwned(c.table) || lsync.CoreOwned(got) {
			if got != c.table {
				t.Errorf("%s: tableOfType = %q, ent table = %q", c.typ, got, c.table)
			}
			covered[c.table] = true
		}
	}
	// Every core-owned table that lore.db still has must be reachable by
	// the guard (tables only the core knows, such as kinds or vars, have no
	// ent type and no TUI view).
	have := map[string]bool{}
	for _, c := range entTypeTables() {
		have[c.table] = true
	}
	for _, table := range lsync.CoreOwnedTables() {
		if have[table] && !covered[table] {
			t.Errorf("core-owned table %s has an ent type the guard never maps to it", table)
		}
	}
}

// The named cases from the plural rules (y → ies, x → es, compound names)
// plus types that are NOT core-owned.
func TestTableOfType_PluralRules(t *testing.T) {
	t.Parallel()
	for typ, want := range map[string]string{
		"Memory": "memories", "Hotfix": "hotfixes", "TaskList": "task_lists", "CommitLink": "commit_links",
		"EntityTag": "entity_tags", "Behaviour": "behaviours", "TastePref": "taste_prefs", "Policy": "policies",
		"A": "as", "": "s",
	} {
		if got := tableOfType(typ); got != want {
			t.Errorf("tableOfType(%q) = %q, want %q", typ, got, want)
		}
	}
	if lsync.CoreOwned(tableOfType(ent.TypeEntityTag)) {
		t.Fatal("entity_tags is not core-owned: the TUI must still edit it")
	}
}

// hintOfType names a command that exists: overrides for the types whose
// noun is not their lowercased name, the default form for the rest.
func TestHintOfType(t *testing.T) {
	t.Parallel()
	for typ, want := range map[string]string{
		ent.TypeMemory:         "`lore memory add|edit|delete …`",
		ent.TypeTaskList:       "`lore tasklist add|edit|delete …`",
		ent.TypeCommitLink:     "`lore link …`",
		ent.TypeExternalSource: "`lore external-source add|edit|delete …`",
		ent.TypeKnowledgeRef:   "`lore task add|edit --ref …`",
		ent.TypeActor:          "`lore actor register|deactivate …`",
		ent.TypeTeam:           "no lore command writes teams",
	} {
		if got := hintOfType(typ); !strings.Contains(got, want) {
			t.Errorf("hintOfType(%s) = %q, want it to contain %s", typ, got, want)
		}
	}
	for typ := range cliHintOverrides {
		found := false
		for _, c := range entTypeTables() {
			if c.typ == typ {
				found = lsync.CoreOwned(c.table)
			}
		}
		if !found {
			t.Errorf("override for %s: not a core-owned ent type (stale entry)", typ)
		}
	}
}

// newGuardedClient opens a fresh migrated lore.db with the TUI's guard on.
func newGuardedClient(t *testing.T) *ent.Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lore.db")
	if err := dbtemplate.Copy(templateDB, path); err != nil {
		t.Fatal(err)
	}
	db := dbent.InitDB(path)
	if err := dbent.ApplyPragmas(db); err != nil {
		t.Fatal(err)
	}
	client := dbent.New(db).Client()
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	GuardCoreOwned(client)
	return client
}

// The guard as the TUI runs it: a create on a core-owned table (and the
// roster tables a checkout's core owns) is refused with E_UNSUPPORTED and
// the CLI hint, nothing is written; a table the core does not own still
// accepts writes.
func TestGuardCoreOwned_RefusesCoreTablesOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newGuardedClient(t)
	refusals := []struct {
		name string
		do   func() error
		hint string
	}{
		{"project", func() error { _, err := client.Project.Create().SetName("p").Save(ctx); return err }, "lore project"},
		{"actor", func() error {
			_, err := client.Actor.Create().SetKind("human").SetDisplayName("d").SetStableKey("human:d@example.com").Save(ctx)
			return err
		}, "lore actor register"},
		{"tag", func() error { _, err := client.Tag.Create().SetName("t").Save(ctx); return err }, "lore tag"},
		{"memory bulk delete", func() error { _, err := client.Memory.Delete().Exec(ctx); return err }, "lore memory"},
		{"hotfix update", func() error { _, err := client.Hotfix.Update().SetTitle("x").Save(ctx); return err }, "lore hotfix"},
		{"task list delete", func() error { _, err := client.TaskList.Delete().Exec(ctx); return err }, "lore tasklist"},
		{"commit link delete", func() error { _, err := client.CommitLink.Delete().Exec(ctx); return err }, "lore link"},
	}
	for _, c := range refusals {
		err := c.do()
		var ce *errcodes.CLIError
		if !errors.As(err, &ce) || ce.Code != errcodes.Unsupported {
			t.Errorf("%s: want E_UNSUPPORTED, got %v", c.name, err)
			continue
		}
		if !strings.Contains(ce.Hint, c.hint) || !strings.Contains(ce.Message, "read-only") {
			t.Errorf("%s: message %q hint %q, want hint naming %q", c.name, ce.Message, ce.Hint, c.hint)
		}
	}
	if n := client.Project.Query().CountX(ctx); n != 0 {
		t.Fatalf("a refused create wrote %d project rows", n)
	}
	if _, err := client.EntityTag.Delete().Exec(ctx); err != nil {
		t.Fatalf("entity_tags is not core-owned and must stay writable: %v", err)
	}
	if _, err := client.Session.Delete().Exec(ctx); err != nil {
		t.Fatalf("a local table must stay writable: %v", err)
	}
}
