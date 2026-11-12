package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// commitLineMemIDs are fixture memory ids.
const (
	commitLineMemA = "mem_01a1a59b891c70b789144cd3e711fd11"
	commitLineMemB = "mem_01a1a59b891c70b789144cd3e711fd12"
)

// TestMigrateEndsWithCommitLine (owner item C2): a run in a code repo
// leaves the upgraded .lore/data files for the person to commit; the run
// counts them (Uncommitted) and its text ends with the one commit line,
// while the branch journal line no longer carries the hint. The doctor
// judges the working tree: the upgraded, uncommitted files are migrated,
// so row_format finds nothing (lore fix stayed DEGRADED, exit 1, on a
// successful run).
func TestMigrateEndsWithCommitLine(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	v1Row(t, repo, schema.TableMemories, commitLineMemA, `"project_id":"`+projectID(t, a)+`","body":"one"`)
	v1Row(t, repo, schema.TableMemories, commitLineMemB, `"project_id":"`+projectID(t, a)+`","body":"two"`)
	raw, err := run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	out := migrateOut(t, raw)
	if out.Uncommitted < 2 {
		t.Fatalf("uncommitted = %d, want >= 2: %+v", out.Uncommitted, out)
	}
	text := out.Text()
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if last := lines[len(lines)-1]; last != opsmigrate.CommitLine(out.Uncommitted) {
		t.Fatalf("last line %q, want the commit line; text:\n%s", last, text)
	}
	if strings.Count(text, "git add .lore/data") != 1 {
		t.Fatalf("the commit hint should appear once:\n%s", text)
	}
	if !strings.Contains(text, "memories: 2 rows (") {
		t.Fatalf("rows not summarised per table:\n%s", text)
	}
	got, err := a.DoctorRowFormat(background())
	if err != nil || len(got) != 0 {
		t.Fatalf("row_format after a run, files not committed: %+v %v", got, err)
	}
}

// TestRowFormatUnfinishedMigrationIsANote (owner item C2): v1 row files
// beside a raised min_lore_version are "an older lore still writes here"
// (warning) only when this checkout's migration finished; while its
// journal holds one that did not (interrupted, rolled back) they are the
// migration's own leftovers: a note naming lore fix.
func TestRowFormatUnfinishedMigrationIsANote(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	v1Row(t, repo, schema.TableMemories, commitLineMemA, `"project_id":"`+projectID(t, a)+`","body":"one"`)
	if _, err := run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	v1Row(t, repo, schema.TableMemories, commitLineMemB, `"project_id":"`+projectID(t, a)+`","body":"two"`)
	a.wrote(nil) // the file was written behind the engine
	// a 0.1.18 feature raised min_lore_version (the fixture's repair did)
	meta := filepath.Join(repo, filepath.FromSlash(rowfs.RepoMetaPath))
	if err := os.WriteFile(meta, []byte(`{"_v": 1, "features": ["hlc"], "min_lore_version": "0.1.18"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setJournalPhase(t, repo, phaseRunning)
	got, err := a.DoctorRowFormat(background())
	if err != nil || len(got) != 1 || got[0].Level != DoctorNote || !strings.Contains(got[0].Message, "did not finish") || got[0].Fix != rowFormatBeforeFix {
		t.Fatalf("interrupted migration: %+v %v", got, err)
	}
	setJournalPhase(t, repo, phaseDone)
	got, err = a.DoctorRowFormat(background())
	if err != nil || len(got) != 1 || got[0].Level != DoctorWarning {
		t.Fatalf("finished migration, then a v1 file: %+v %v", got, err)
	}
}

// setJournalPhase rewrites the phase of repo's migration journal.
func setJournalPhase(t *testing.T, repo, phase string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(MigrateJournalFile))
	var j map[string]json.RawMessage
	if err := json.Unmarshal(mustRead(t, path), &j); err != nil {
		t.Fatal(err)
	}
	j["phase"] = json.RawMessage(`"` + phase + `"`)
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestFixMigrateStepNextAndRows (owner item C2): lore fix's migrate step
// carries the commit line as its Next (printed once, last) and every row
// in Rows (--json), while its Detail names a count per table.
func TestFixMigrateStepNextAndRows(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	v1Row(t, repo, schema.TableMemories, commitLineMemA, `"project_id":"`+projectID(t, a)+`","body":"one"`)
	st := a.fixMigrate(background(), repo, false, false)
	if st.Failed || !st.Changed {
		t.Fatalf("migrate step: %+v", st)
	}
	if !strings.HasPrefix(st.Next, "1 upgraded file") && !strings.Contains(st.Next, "upgraded files in .lore/data wait for your commit") {
		t.Fatalf("Next = %q", st.Next)
	}
	if len(st.Rows) != 1 || st.Rows[0] != "memories/"+commitLineMemA {
		t.Fatalf("Rows = %q", st.Rows)
	}
	for _, d := range st.Detail {
		if strings.Contains(d, "git add") {
			t.Fatalf("detail carries the commit hint: %q", st.Detail)
		}
	}
	if got := opsdoctor.NextLines([]opsdoctor.FixStep{st}); len(got) != 1 {
		t.Fatalf("NextLines = %q", got)
	}
}
