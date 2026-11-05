package wire

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
)

// resurrectedMark is the words of doctor's resurrected-row finding.
const resurrectedMark = "is a live copy of a purged row"

// soundCache writes a real SQLite file of several pages at p.
func soundCache(t *testing.T, p string) {
	t.Helper()
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE TABLE t(a TEXT)", "CREATE INDEX ta ON t(a)",
		"WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 3000) INSERT INTO t SELECT hex(randomblob(40)) FROM n"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
}

// damagePastHeader overwrites p's middle pages, keeping page 1 (the
// header) and the tail, so the file still opens and only quick_check sees
// the damage.
func damagePastHeader(t *testing.T, p string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		t.Fatal(err)
	}
	for i := 8192; i < len(b)-4096; i++ {
		b[i] = 0x5a
	}
	root, err := os.OpenRoot(filepath.Dir(p)) // confine the write to the test's temp dir
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }() // a directory handle; nothing to flush
	if err := root.WriteFile(filepath.Base(p), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCacheFinding (B11 port of saas/cmd/cli doctor_cache_test.go): an
// absent and a sound cache are no finding; a cache damaged past its first
// pages (it opens, only quick_check sees it) is named with the fix.
func TestCacheFinding(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "c.db")
	if got := cacheFinding(ctx, p); got != "" {
		t.Fatalf("absent: %q", got)
	}
	soundCache(t, p)
	if got := cacheFinding(ctx, p); got != "" {
		t.Fatalf("sound: %q", got)
	}
	damagePastHeader(t, p)
	if got := cacheFinding(ctx, p); !strings.Contains(got, "cache "+p+" is damaged; delete it") || !strings.Contains(got, "rebuilds it from .lore/data") {
		t.Fatalf("damaged: %q", got)
	}
}

// TestDoctorCachesBreaksDoctor: through the doctor operation, a damaged
// cache under <home>/cache is one error line (v0.1.17 text), status
// broken, exit 2; a sound one beside it is not named.
func TestDoctorCachesBreaksDoctor(t *testing.T) {
	ctx := context.Background()
	repo := gitRepo(t)
	home := t.TempDir()
	actor, aerr := ids.Parse[ids.Actor](testActor)
	if aerr != nil {
		t.Fatal(aerr)
	}
	a, nerr := New(Options{RepoDir: repo, Home: home, Actor: actor})
	if nerr != nil {
		t.Fatal(nerr)
	}
	t.Cleanup(func() { _ = a.Close() }) // test teardown; Close errors are covered elsewhere
	withProject(t, a)
	dir := filepath.Join(home, cacheDir, "extra")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	bad, good := filepath.Join(dir, "bad.db"), filepath.Join(dir, "good.db")
	soundCache(t, bad)
	soundCache(t, good)
	if r, err := opsdoctor.Run(ctx, a, opsdoctor.RunIn{}); err != nil || len(r.Errors) != 0 {
		t.Fatalf("control: %+v %v", r.Errors, err)
	}
	damagePastHeader(t, bad)
	r, err := opsdoctor.Run(ctx, a, opsdoctor.RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Errors) != 1 || !strings.HasPrefix(r.Errors[0], "cache "+bad+" is damaged; delete it") || r.Status != opsdoctor.StatusBroken || r.Exit != 2 {
		t.Fatalf("errors %q status %s exit %d", r.Errors, r.Status, r.Exit)
	}
	if strings.Contains(strings.Join(r.Errors, "\n"), good) {
		t.Fatal("a sound cache was named")
	}
}

// TestDoctorNamesResurrectedRows (B11 port of doctor_resurrected_test.go
// and doctor_stores_test.go TestE2E_DoctorNamesResurrectedPurgedRow): a
// purged row whose live file comes back (an old clone) is named by
// doctor with its path and the fix EXACTLY ONCE and degrades it; an
// archived (not purged) row is never named; once the file is gone the
// finding goes away.
func TestDoctorNamesResurrectedRows(t *testing.T) {
	ctx := context.Background()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	out, err := run(t, a, opsmemory.OpAdd, `{"text":"an old note to purge"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	id := idOf(t, out)
	file := memPath(repo, id)
	live, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		t.Fatal(err)
	}
	count := func() (int, opsdoctor.Report) {
		t.Helper()
		r, rerr := opsdoctor.Run(ctx, a, opsdoctor.RunIn{})
		if rerr != nil {
			t.Fatal(rerr)
		}
		return strings.Count(strings.Join(append(append([]string{}, r.Warnings...), r.Notes...), "\n"), resurrectedMark), r
	}
	if _, err = run(t, a, opsmemory.OpArchive, `{"id":"`+id+`"}`, false); err != nil {
		t.Fatal(err)
	}
	if n, _ := count(); n != 0 {
		t.Fatal("an archived row is not a resurrected one")
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eng.Purge(ctx, engine.Ref{Table: row.Table(schema.TableMemories), ID: aid}); err != nil {
		t.Fatal(err)
	}
	if n, _ := count(); n != 0 {
		t.Fatal("control: nothing resurrected yet")
	}
	rt, err := os.OpenRoot(repo) // confine the write to the checkout
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }() // a directory handle; nothing to flush
	rel, err := filepath.Rel(repo, file)
	if err != nil {
		t.Fatal(err)
	}
	if err = rt.WriteFile(rel, live, 0o600); err != nil { // an old clone's copy comes back
		t.Fatal(err)
	}
	n, r := count()
	want := "store repo: .lore/data/memories/" + id + ".json " + resurrectedMark
	if n != 1 || !strings.Contains(strings.Join(r.Warnings, "\n"), want) || !strings.Contains(strings.Join(r.Warnings, "\n"), "remove the file") || r.Exit == 0 {
		t.Fatalf("doctor must name the resurrected file once, with the fix: %d time(s), exit %d\n%q", n, r.Exit, r.Warnings)
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if n, _ = count(); n != 0 {
		t.Fatal("a removed file is no longer reported")
	}
}

// TestDoctorRemovedEnv (B11 port of doctor_secrets_test.go, D3): a
// leftover LORE_SYNC_ALLOW_SECRETS is one warning naming the replacement
// that degrades health; unset, nothing.
func TestDoctorRemovedEnv(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, gitRepo(t))
	t.Setenv(EnvRemovedSyncAllow, "")
	if f, err := a.DoctorRemovedEnv(ctx); err != nil || len(f) != 0 {
		t.Fatalf("unset env warned: %v %v", f, err)
	}
	t.Setenv(EnvRemovedSyncAllow, "1")
	f, err := a.DoctorRemovedEnv(ctx)
	if err != nil || len(f) != 1 || f[0].Level != DoctorWarning || !strings.HasPrefix(f[0].Line(), EnvRemovedSyncAllow+" is set but does nothing") || !strings.Contains(f[0].Line(), "lore secrets rule off") {
		t.Fatalf("findings %+v %v", f, err)
	}
	if r := runDoctorOp(t, a); r.Status == opsdoctor.StatusHealthy {
		t.Fatalf("the warning must degrade: %+v", r)
	}
}

// runDoctorOp runs the doctor operation over a.
func runDoctorOp(t *testing.T, a *App) opsdoctor.Report {
	t.Helper()
	r, err := opsdoctor.Run(context.Background(), a, opsdoctor.RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
