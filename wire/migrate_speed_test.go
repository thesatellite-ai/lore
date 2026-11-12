package wire

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/gitenv"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// TestMigrateSpeedManyV1Rows times migrate status, a dry run, the run and
// a second run over a repo of LORE_MIGSPEED_ROWS (default 5000) committed
// `_v: 1` memory files: the shape of a v0.1.17 notes repo. It is a
// measurement, not a gate: it runs only with LORE_MIGSPEED set, and prints
// each step's wall time.
func TestMigrateSpeedManyV1Rows(t *testing.T) {
	if os.Getenv("LORE_MIGSPEED") == "" {
		t.Skip("set LORE_MIGSPEED=1 to time migrate over many v1 rows")
	}
	n := 5000
	if s := os.Getenv("LORE_MIGSPEED_ROWS"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			t.Fatal(err)
		}
		n = v
	}
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	prj := projectID(t, a)
	def, ok := mustReg(t).Table(schema.TableMemories)
	if !ok {
		t.Fatal("no memories table")
	}
	body, _ := v1Body(t, def, prj)
	for i := range n {
		v1Row(t, repo, schema.TableMemories, fmt.Sprintf("mem_01a1a59b891c70b78914%012x", i), body)
	}
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "v1"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = gitenv.Clean(os.Environ())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	step := func(name, opName, input string) string {
		t.Helper()
		start := time.Now()
		raw, err := run(t, a, opName, input, true)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%-12s %6.2fs", name, time.Since(start).Seconds())
		return raw
	}
	step("status", opsmigrate.OpStatus, `{}`)
	if out := migrateOut(t, step("dry run", opsmigrate.OpRun, `{"dry_run":true}`)); len(out.Rows) < n {
		t.Fatalf("dry run lists %d rows, want %d", len(out.Rows), n)
	}
	if out := migrateOut(t, step("run", opsmigrate.OpRun, `{}`)); len(out.Rows) < n {
		t.Fatalf("run lists %d rows, want %d", len(out.Rows), n)
	}
	step("status2", opsmigrate.OpStatus, `{}`)
	if out := migrateOut(t, step("second run", opsmigrate.OpRun, `{}`)); len(out.Rows) != 0 {
		t.Fatalf("second run lists %d rows", len(out.Rows))
	}
}
