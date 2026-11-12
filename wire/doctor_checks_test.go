package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opssettings "github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestDoctorTaskLeads (later 586, TASK 14.4, P3-DOC-2): a live task whose
// lead is missing from its assignees is one warning; --fix writes the lead
// back at the front of the list (the other assignee kept) and marks the
// finding fixed; a second run finds nothing; a task whose lead is listed,
// or that has no lead, is never reported.
func TestDoctorTaskLeads(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	ctx := background()
	addTask := func(title string) ids.AnyID {
		t.Helper()
		out, err := run(t, a, opstask.OpAdd, `{"title":"`+title+`"}`, true)
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err = json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatal(err)
		}
		id, err := ids.ParseAny(v.Data.ID)
		if err != nil {
			t.Fatalf("task add %s: %v", out, err)
		}
		return id
	}
	lead, err := identity.ActorID(testEmail)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	set := func(id ids.AnyID, withLead bool, assignees ...string) {
		t.Helper()
		items := make([]value.Value, 0, len(assignees))
		for _, s := range assignees {
			tv, terr := value.TextOf(s)
			if terr != nil {
				t.Fatal(terr)
			}
			items = append(items, tv)
		}
		l, lerr := value.ListOf(value.KindText, items...)
		if lerr != nil {
			t.Fatal(lerr)
		}
		patch := map[string]value.Value{schema.FieldTaskAssignees: l}
		if withLead {
			ref, rerr := value.RefOf(lead.Any())
			if rerr != nil {
				t.Fatal(rerr)
			}
			patch[fieldTaskLead] = ref
		}
		if _, uerr := a.Update(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TableTasks, ID: id}, patch); uerr != nil {
			t.Fatal(uerr)
		}
	}
	missing, listed, noLead := addTask("lead missing"), addTask("lead listed"), addTask("no lead")
	set(missing, true, other.String())
	set(listed, true, lead.String())
	set(noLead, false, other.String())

	got, err := a.DoctorTaskLeads(ctx, false)
	if err != nil || len(got) != 1 || got[0].Check != DoctorCheckTaskLead || got[0].Level != DoctorWarning || got[0].Fixed ||
		!strings.Contains(got[0].Message, "task "+missing.String()+": lead "+lead.String()+" is not in its assignees") || got[0].Fix != "lore doctor --fix" {
		t.Fatalf("find: %+v %v", got, err)
	}
	if got, err = a.DoctorTaskLeads(ctx, true); err != nil || len(got) != 1 || !got[0].Fixed {
		t.Fatalf("fix: %+v %v", got, err)
	}
	_, r, err := a.Get(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TableTasks, ID: missing})
	if err != nil {
		t.Fatal(err)
	}
	if as := value.ItemStrings(r.Fields[schema.FieldTaskAssignees]); strings.Join(as, ",") != lead.String()+","+other.String() {
		t.Fatalf("assignees after fix %v, want lead first then %s", as, other)
	}
	if got, err = a.DoctorTaskLeads(ctx, true); err != nil || len(got) != 0 {
		t.Fatalf("second fix: %+v %v", got, err)
	}
}

// TestDoctorTriageOwner (later 586, FB-TASK-3): unset is silent; a value
// matching no actor warns with the policy fix; the caller's own actor by
// id (with @ and case changed) is silent.
func TestDoctorTriageOwner(t *testing.T) {
	t.Parallel()
	a := openWith(t, gitRepo(t), Options{})
	withProject(t, a)
	ctx := background()
	if got, err := a.DoctorTriageOwner(ctx); err != nil || len(got) != 0 {
		t.Fatalf("unset: %+v %v", got, err)
	}
	me, err := identity.ActorID(testEmail)
	if err != nil {
		t.Fatal(err)
	}
	setOwner := func(v string) {
		t.Helper()
		if _, serr := run(t, a, opssettings.OpPolicySet, `{"key":"tasks.triage_owner","value":"`+v+`","to":"repo"}`, true); serr != nil {
			t.Fatal(serr)
		}
	}
	setOwner("@nobody-here")
	got, err := a.DoctorTriageOwner(ctx)
	if err != nil || len(got) != 1 || got[0].Level != DoctorWarning ||
		got[0].Message != "tasks.triage_owner is @nobody-here, which matches no actor; no inbox lists proposed tasks" ||
		!strings.HasPrefix(got[0].Fix, "lore policy set tasks.triage_owner ") {
		t.Fatalf("unknown owner: %+v %v", got, err)
	}
	setOwner("@" + strings.ToUpper(me.String()))
	if got, err = a.DoctorTriageOwner(ctx); err != nil || len(got) != 0 {
		t.Fatalf("own actor id: %+v %v", got, err)
	}
}

// TestDoctorStoreLocks (later 586, owner item 40): a held store lock names
// its holder (pid of the holder line) once, as a note while that process
// lives (a sibling lore command at work); a free lock is silent.
func TestDoctorStoreLocks(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	url := folderStoreAt(t, "sto_01a1a5aca7f57659be7320c5f4b72a01", rowfs.VisibilityInternal)
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+url+"\n")
	a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	ctx := background()
	if got, err := a.DoctorStoreLocks(ctx); err != nil || len(got) != 0 {
		t.Fatalf("free lock: %+v %v", got, err)
	}
	root, err := rowfs.Open(fileuri.LocalPath(url))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := root.Lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.DoctorStoreLocks(ctx)
	if rerr := lock.Release(); rerr != nil {
		t.Fatal(rerr)
	}
	if err != nil || len(got) != 1 || got[0].Store != "kb" || got[0].Level != DoctorNote ||
		!strings.Contains(got[0].Message, "is held by pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("held lock (live holder: a note, never a finding): %+v %v", got, err)
	}
	if got, err = a.DoctorStoreLocks(ctx); err != nil || len(got) != 0 {
		t.Fatalf("released lock: %+v %v", got, err)
	}
}

// TestEditAfterPurge (later 586, P3-DOC-3): a tombstone whose file history
// holds a live version with a higher HLC is one warning naming that commit;
// an older live version, or no history, finds nothing.
func TestEditAfterPurge(t *testing.T) {
	t.Parallel()
	dir := gitRepo(t)
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	stamp := func(min int) hlc.HLC {
		return hlc.NewClock(actor, func() time.Time { return base.Add(time.Duration(min) * time.Minute) }).Now()
	}
	rel := filepath.ToSlash(filepath.Join(".lore", "data", "memories", id.String()+".json"))
	git := func(args ...string) string {
		t.Helper()
		return gitIn(t, dir, nil, args...)
	}
	commit := func(r row.Row, msg string) string {
		t.Helper()
		b, eerr := row.Encode(r)
		if eerr != nil {
			t.Fatal(eerr)
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if merr := os.MkdirAll(filepath.Dir(p), 0o750); merr != nil {
			t.Fatal(merr)
		}
		if werr := os.WriteFile(p, b, 0o600); werr != nil {
			t.Fatal(werr)
		}
		git("add", rel)
		// its own identity: a machine with no global git identity (a fresh
		// CI runner) refuses the commit otherwise
		git("-c", "user.name=lore test", "-c", "user.email=test@example.com", "commit", "-q", "-m", msg)
		return git("rev-parse", "HEAD")
	}
	body, err := value.TextOf("x")
	if err != nil {
		t.Fatal(err)
	}
	liveAt := func(min int) row.Row {
		h := stamp(min)
		return row.Row{Table: schema.TableMemories, ID: id.Any(), HLC: h, CreatedAt: h.Wall, UpdatedAt: h.Wall, Fields: map[string]value.Value{"body": body}}
	}
	tomb := row.Row{Table: schema.TableMemories, ID: id.Any(), HLC: stamp(5), Deleted: true}

	if _, found := editAfterPurge(background(), dir, rel, tomb); found {
		t.Fatal("found an edit with no history")
	}
	commit(liveAt(1), "create")
	commit(tomb, "purge")
	if f, found := editAfterPurge(background(), dir, rel, tomb); found {
		t.Fatalf("an older edit reported: %+v", f)
	}
	later := commit(liveAt(9), "edit on another clone")
	commit(tomb, "merge keeps the purge")
	f, found := editAfterPurge(background(), dir, rel, tomb)
	if !found || f.Level != DoctorWarning || !strings.Contains(f.Message, "but commit "+later[:12]+" holds a later edit") ||
		!strings.HasSuffix(f.Fix, "git show "+later[:12]+":"+rel) {
		t.Fatalf("later edit: %+v %v", f, found)
	}
}

// TestStoreLockFinding (owner item 40): a held lock whose holder is a live
// process, or unnamed, is a note (counts toward nothing); one whose holder
// line names a process that has exited is a warning naming how to free it.
func TestStoreLockFinding(t *testing.T) {
	t.Parallel()
	since := time.Now().Add(-time.Minute)
	live := func(int) bool { return true }
	dead := func(int) bool { return false }
	ls := rowfs.LockState{Path: "/s/.git/lore/lock", Held: true, PID: 4242, Since: since}
	if f := storeLockFinding("kb", ls, live); f.Level != DoctorNote || f.Check != DoctorCheckStoreLock ||
		!strings.Contains(f.Message, "store kb: the lock /s/.git/lore/lock is held by pid 4242") {
		t.Fatalf("live holder: %+v", f)
	}
	if f := storeLockFinding("kb", rowfs.LockState{Path: ls.Path, Held: true}, dead); f.Level != DoctorNote ||
		!strings.Contains(f.Message, "a process that left no holder line") {
		t.Fatalf("unnamed holder: %+v", f)
	}
	f := storeLockFinding("kb", ls, dead)
	if f.Level != DoctorWarning || !strings.Contains(f.Message, "which has exited") || !strings.Contains(f.Fix, "lsof /s/.git/lore/lock") {
		t.Fatalf("dead holder: %+v", f)
	}
}
