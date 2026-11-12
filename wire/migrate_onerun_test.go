package wire

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// oneRunLiveMem is the id of the live-only memory TestMigrateRunOneRun
// brings in.
const oneRunLiveMem = "mem_01a1a59b891c70b789144cd3e711fb03"

// TestMigrateRunOneRun (F-MIGRATE-1): on a fresh machine (empty machine
// store), a v0.1.17 repo whose rows a teammate wrote (the O6 question is
// open), whose lore.db holds a machine-local row and whose live ref holds
// a live-only row converts in ONE `lore migrate run`: no E_NEEDS_CONFIRM
// from the machine-local copy (its audited write creates this process's
// session row, which the O6 guard used to refuse), the live row gets its
// created_by/updated_by in the same run, and a second run is a no-op (no
// row, no step, no backup ref). A plain write on the same
// repo before the run still asks: the guard is lifted for the run's own
// writes only.
func TestMigrateRunOneRun(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	t.Setenv(identity.EnvSession, "f-migrate-1") // a session row applies whatever the host agent is
	repo := gitRepo(t)
	seed := newTestApp(t, repo)
	withProject(t, seed)
	project := projectID(t, seed)
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	removeV2Projects(t, repo, project)
	writeV1(t, repo, v1Memory(project, otherActor))
	const at = "'2026-01-02 03:04:05.123456789+00:00'"
	writeV017DB(t, filepath.Join(repo, filepath.FromSlash(v017LoreDB)), v017RenderHistoriesDDL,
		`INSERT INTO render_histories VALUES ('`+copyRender+`', `+at+`, `+at+`, '`+project+`', NULL, '.lore/LORE.md', '# lore',
			'abc123', 6, NULL, '{"included":{"rules":1},"cut_summary":{}}', NULL)`)
	writeLiveRef(t, repo, liveRefLocal, map[string]string{
		livePath(oneRunLiveMem): liveMemory(oneRunLiveMem, project, "live only", liveNewer),
	})

	// cmd/lore's audit sink (the chain): its first audited write creates
	// this process's session row in the machine store, the write the O6
	// guard used to refuse mid-run.
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	a, err := New(Options{RepoDir: repo, Home: home, Actor: actor, Audit: NewChainAudit(home, repo, time.Now)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() }) // a failed close is the other tests' report, not this one's
	// (a write here would now run the migration itself, owner item 11:
	// wire/fixproject.go; this test is about the explicit run)
	raw, err := run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := migrateOut(t, raw)
	if !slices.ContainsFunc(first.Steps, func(s string) bool {
		return strings.HasPrefix(s, StepMachineLocal+": copied 1 render_histories")
	}) {
		t.Fatalf("first run did not copy the machine-local row: %s", raw)
	}
	live := rowFile(t, memPath(repo, oneRunLiveMem))
	if fieldString(t, live, row.KeyCreatedBy) == "" || fieldString(t, live, row.KeyUpdatedBy) == "" {
		t.Fatalf("live row without created_by/updated_by after the first run: %v", live)
	}

	if raw, err = run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if again := migrateOut(t, raw); again.Count != 0 || len(again.Rows) != 0 || len(again.Steps) != 0 || again.BackupRef != "" {
		t.Fatalf("second run is not a no-op: %s", raw)
	}
}
