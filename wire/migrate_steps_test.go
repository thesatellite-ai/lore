package wire

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Fixture ids: tagA sorts before tagB byte-wise, so tagA is the keeper.
const (
	snakeTagA  = "tag_01a1a59b891c70b789144cd3e711fe01"
	snakeTagB  = "tag_01a1a59b891c70b789144cd3e711fe02"
	snakeTagC  = "tag_01a1a59b891c70b789144cd3e711fe03"
	snakeMem   = "mem_01a1a59b891c70b789144cd3e711fe04"
	settingRT  = "pcf_01a1a59b891c70b789144cd3e711fe05"
	settingFTS = "pcf_01a1a59b891c70b789144cd3e711fe06"
)

// TestMigrateTagsSnakeCase (step 7, R13): dash names become snake_case;
// a collision keeps the byte-wise smallest id, moves the other tag's
// bindings to it and deletes the other; a dry run writes nothing; a second
// run finds nothing to do.
func TestMigrateTagsSnakeCase(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	prj := projectID(t, a)
	v1Row(t, repo, schema.TableTags, snakeTagA, `"project_id":"`+prj+`","name":"cert-rotation"`)
	v1Row(t, repo, schema.TableTags, snakeTagB, `"project_id":"`+prj+`","name":"cert_rotation"`)
	v1Row(t, repo, schema.TableTags, snakeTagC, `"project_id":"`+prj+`","name":"On-Call"`)
	v1Row(t, repo, schema.TableMemories, snakeMem, `"project_id":"`+prj+`","body":"b","kind":"core","source_kind":"manual","tag_ids":["`+snakeTagB+`"]`)

	dry, _, err := a.migrateTagsSnakeCase(background(), true)
	if err != nil || len(dry) != 3 {
		t.Fatalf("dry run: %v %v", dry, err)
	}
	if got := tagIDsOf(t, a, snakeMem); !slices.Equal(got, []string{snakeTagB}) {
		t.Fatalf("a dry run rebound the memory: %v", got)
	}
	steps, _, err := a.migrateTagsSnakeCase(background(), false)
	if err != nil || len(steps) != len(dry) {
		t.Fatalf("run: %v %v (dry %v)", steps, err, dry)
	}
	if got := tagIDsOf(t, a, snakeMem); !slices.Equal(got, []string{snakeTagA}) {
		t.Fatalf("memory tag_ids %v, want the keeper %s", got, snakeTagA)
	}
	names := map[string]string{}
	rows, err := engineList(t, a, schema.TableTags)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if !r.Deleted {
			nm, _ := r.Fields[tagFieldName].(value.Text)
			names[r.ID.String()] = nm.String()
		}
	}
	if names[snakeTagA] != "cert_rotation" || names[snakeTagC] != "on_call" || names[snakeTagB] != "" {
		t.Fatalf("tags after the step: %v", names)
	}
	again, _, err := a.migrateTagsSnakeCase(background(), false)
	if err != nil || len(again) != 0 {
		t.Fatalf("second run: %v %v", again, err)
	}
}

// engineList lists a repo table through the engine.
func engineList(t *testing.T, a *App, table row.Table) ([]row.Row, error) {
	t.Helper()
	eng, err := a.Engine(background())
	if err != nil {
		return nil, err
	}
	return eng.List(background(), store.SourceRepo, table)
}

// TestMigrateDBSettings (step 12, CFG12): render_targets naming built-in
// pointer files becomes one context.targets policy row; a key with no
// 0.1.18 home is reported and left in place; a second run writes no
// second row.
func TestMigrateDBSettings(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	prj := projectID(t, a)
	v1Row(t, repo, projectConfigsTable, settingRT, `"project_id":"`+prj+`","key":"render_targets","value":"[\"CLAUDE.md\",\"AGENTS.md\"]"`)
	v1Row(t, repo, projectConfigsTable, settingFTS, `"project_id":"`+prj+`","key":"fts_mode","value":"trigram"`)

	raw, err := run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(migrateOut(t, raw).Steps, "\n")
	if !strings.Contains(all, "context.targets = claude_code,codex") || !strings.Contains(all, "fts_mode has no 0.1.18 home") {
		t.Fatalf("steps: %s", all)
	}
	count := func() int {
		rows, lerr := engineList(t, a, schema.TablePolicies)
		if lerr != nil {
			t.Fatal(lerr)
		}
		n := 0
		for _, r := range rows {
			if k, ok := r.Fields[policyFieldKey].(value.Text); ok && !r.Deleted && k.String() == string(policy.KeyContextTargets) {
				n++
			}
		}
		return n
	}
	if n := count(); n != 1 {
		t.Fatalf("%d context.targets rows, want 1", n)
	}
	if _, err := run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("second run: %d context.targets rows, want 1", n)
	}
}
