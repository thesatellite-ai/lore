package wire

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/store/localstate"
)

// Fixture ids for the purged-copies rows: purgedRowA is listed in
// _purged.json, trashedRowB only has a safety copy.
const (
	purgedRowA  = "mem_01a1a59b891c70b789144cd3e711fe0a"
	trashedRowB = "mem_01a1a59b891c70b789144cd3e711fe0b"
)

// TestTimeOutboxFindings (later 15.1, TIME 4.3): an empty outbox gives no
// line; otherwise one warning counting every span, the age taken from the
// oldest entry wherever it sits in the list, in minutes under an hour and
// hours from an hour on.
func TestTimeOutboxFindings(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) localstate.Entry { return localstate.Entry{Key: "k", UpdatedAt: now.Add(-d)} }
	cases := []struct {
		name    string
		entries []localstate.Entry
		want    string
	}{
		{"nil", nil, ""},
		{"empty", []localstate.Entry{}, ""},
		{"one fresh", []localstate.Entry{at(0)}, "time_outbox: 1 run time span(s) wait on this machine only (oldest queued 0 min ago)"},
		{"one under an hour", []localstate.Entry{at(59 * time.Minute)}, "(oldest queued 59 min ago)"},
		{"exactly an hour", []localstate.Entry{at(time.Hour)}, "(oldest queued 1 h ago)"},
		{"oldest in the middle", []localstate.Entry{at(time.Minute), at(5*time.Hour + 30*time.Minute), at(2 * time.Hour)}, "time_outbox: 3 run time span(s) wait on this machine only (oldest queued 5 h ago)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := timeOutboxFindings(c.entries, now)
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("findings for an empty outbox: %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("findings %+v, want one", got)
			}
			f := got[0]
			if f.Check != DoctorCheckTimeOutbox || f.Level != DoctorWarning || !f.Plain || !strings.Contains(f.Line(), c.want) ||
				!strings.HasSuffix(f.Line(), "their task's store was unreachable; run lore sync to write them") {
				t.Fatalf("finding %+v (line %q), want %q", f, f.Line(), c.want)
			}
		})
	}
}

// plantV017Trash writes a checkout lore.db holding _lore_sync_trash copies
// of rowIDs (table false: a lore.db without the table).
func plantV017Trash(t *testing.T, repo string, table bool, rowIDs ...string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(v017LoreDB))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	db, err := sql.Open(sqliteDriver, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if !table {
		if _, err := db.Exec(`CREATE TABLE config (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := db.Exec(`CREATE TABLE ` + v017SyncTrashTable + ` (id INTEGER PRIMARY KEY AUTOINCREMENT, table_name TEXT NOT NULL, row_id TEXT NOT NULL, doc TEXT NOT NULL, reason TEXT NOT NULL, at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range rowIDs {
		if _, err := db.Exec(`INSERT INTO `+v017SyncTrashTable+` (table_name, row_id, doc, reason, at) VALUES ('memories', ?, '{}', 'file deleted', '2026-10-01T10:00:00Z')`, id); err != nil {
			t.Fatal(err)
		}
	}
}

// writePurged writes the checkout's root _purged.json with body.
func writePurged(t *testing.T, repo, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, ".lore", "data", "_purged.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorLocalRows (later 15.1, FB-DOC-2..4): the three machine-only
// doctor rows on cmd/lore. time_outbox: no local.db gives no line and does
// not create one; a queued span warns. purged_copies: no lore.db, a lore.db
// without the table, or copies of rows not purged give no line; copies of
// a purged row are counted (each copy) with the drop command; an unreadable
// _purged.json warns instead of saying nothing is purged. Settings: a
// v0.1.17 key with no 0.1.18 home is one note. DoctorChecks runs all
// three, and the old binary (no standalone org) prints none of them.
func TestDoctorLocalRows(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	home := t.TempDir()
	a := openWith(t, repo, Options{Home: home, Version: "0.1.18", OrgCommands: &standaloneOrg{home: home}})
	withProject(t, a)
	ctx := background()
	mustNone := func(what string, got []DoctorFinding, err error) {
		t.Helper()
		if err != nil || len(got) != 0 {
			t.Fatalf("%s: %+v %v", what, got, err)
		}
	}
	mustOne := func(what string, got []DoctorFinding, err error, check DoctorCheck, level DoctorLevel, want string) {
		t.Helper()
		if err != nil || len(got) != 1 || got[0].Check != check || got[0].Level != level || !got[0].Plain || got[0].Line() != want {
			t.Fatalf("%s: %+v %v\nwant %q", what, got, err, want)
		}
	}

	// time_outbox
	got, err := a.DoctorTimeOutbox(ctx)
	mustNone("nothing queued", got, err)
	if _, serr := os.Stat(oldLocalStatePath(home)); !errors.Is(serr, fs.ErrNotExist) {
		t.Fatalf("doctor created the old state/local.db: %v", serr)
	}
	ls, err := a.LocalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err = a.DoctorTimeOutbox(ctx)
	mustNone("empty outbox", got, err)
	if err = ls.Put(ctx, localstate.TimeOutbox, "span-1", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err = ls.Close(); err != nil {
		t.Fatal(err)
	}
	got, err = a.DoctorTimeOutbox(ctx)
	mustOne("one span", got, err, DoctorCheckTimeOutbox, DoctorWarning,
		"time_outbox: 1 run time span(s) wait on this machine only (oldest queued 0 min ago): their task's store was unreachable; run lore sync to write them")

	// purged_copies
	got, err = a.DoctorPurgedCopies(ctx)
	mustNone("no lore.db", got, err)
	plantV017Trash(t, repo, false)
	writePurged(t, repo, `{"_v":1,"ids":["`+purgedRowA+`"]}`)
	got, err = a.DoctorPurgedCopies(ctx)
	mustNone("lore.db without the table", got, err)
	plantV017Trash(t, repo, true, trashedRowB)
	got, err = a.DoctorPurgedCopies(ctx)
	mustNone("copies of rows not purged", got, err)
	plantV017Trash(t, repo, true, purgedRowA, trashedRowB, purgedRowA)
	got, err = a.DoctorPurgedCopies(ctx)
	mustOne("copies of a purged row", got, err, DoctorCheckPurgedCopies, DoctorWarning,
		"2 local cop(ies) of purged rows on this machine; drop them with lore trash purge --local --purged --confirm")
	for _, bad := range []string{`not json`, `{"_v":99,"ids":[]}`} {
		writePurged(t, repo, bad)
		got, err = a.DoctorPurgedCopies(ctx)
		if err != nil || len(got) != 1 || got[0].Level != DoctorWarning ||
			!strings.HasPrefix(got[0].Line(), "local copies of purged rows could not be checked: ") {
			t.Fatalf("unreadable _purged.json %q: %+v %v", bad, got, err)
		}
	}
	writePurged(t, repo, `{"_v":1,"ids":[]}`)
	got, err = a.DoctorPurgedCopies(ctx)
	mustNone("nothing purged", got, err)
	writePurged(t, repo, `{"_v":1,"ids":["`+purgedRowA+`"]}`)

	// unclassified settings
	got, err = a.DoctorUnclassifiedSettings(ctx)
	mustNone("no v0.1.17 settings", got, err)
	v1Row(t, repo, projectConfigsTable, settingFTS, `"project_id":"`+projectID(t, a)+`","key":"fts_mode","value":"trigram"`)
	got, err = a.DoctorUnclassifiedSettings(ctx)
	if err != nil || len(got) != 1 || got[0].Check != DoctorCheckOldSettings || got[0].Level != DoctorNote || !got[0].Plain ||
		!strings.HasPrefix(got[0].Line(), "v0.1.17 setting not moved: ") || !strings.Contains(got[0].Line(), "fts_mode has no 0.1.18 home") ||
		!strings.HasSuffix(got[0].Line(), doctorOldSettingSuffix) {
		t.Fatalf("unclassified setting: %+v %v", got, err)
	}
	if got, err = a.DoctorUnclassifiedSettings(ctx); err != nil || len(got) != 1 {
		t.Fatalf("doctor moved the setting (second run): %+v %v", got, err)
	}

	names := map[string]bool{}
	for _, r := range a.DoctorChecks(ctx, false) {
		names[r.Check] = true
	}
	for _, c := range []DoctorCheck{DoctorCheckTimeOutbox, DoctorCheckPurgedCopies, DoctorCheckOldSettings} {
		if !names[string(c)] {
			t.Fatalf("DoctorChecks misses %s: %v", c, names)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	old := openWith(t, repo, Options{Home: home, Version: "0.1.18"})
	for name, f := range map[string]func() ([]DoctorFinding, error){
		"outbox":   func() ([]DoctorFinding, error) { return old.DoctorTimeOutbox(ctx) },
		"purged":   func() ([]DoctorFinding, error) { return old.DoctorPurgedCopies(ctx) },
		"settings": func() ([]DoctorFinding, error) { return old.DoctorUnclassifiedSettings(ctx) },
	} {
		got, err := f()
		mustNone("old binary "+name, got, err)
	}
}
