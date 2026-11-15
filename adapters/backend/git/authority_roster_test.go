package git

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// handStore is a plain clone of a store remote where made-up people
// commit row files by hand, as anyone with push access can.
type handStore struct {
	t      *testing.T
	remote string
	dir    string
}

func newHandStore(t *testing.T) *handStore {
	t.Helper()
	remote := newRemote(t, true)
	dir := filepath.Join(t.TempDir(), "hand")
	gitT(t, filepath.Dir(dir), "clone", "--quiet", remote, dir)
	return &handStore{t: t, remote: remote, dir: dir}
}

// commit writes files (path ─▶ JSON body, "" removes it) as author and pushes.
func (h *handStore) commit(author string, files map[string]string) {
	h.t.Helper()
	for p, body := range files {
		full := filepath.Join(h.dir, filepath.FromSlash(p))
		if body == "" {
			gitT(h.t, h.dir, "rm", "--quiet", p)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			h.t.Fatal(err)
		}
		gitT(h.t, h.dir, "add", p)
	}
	gitT(h.t, h.dir, "-c", "user.name=x", "-c", "user.email="+author, "commit", "--quiet", "--no-verify", "--author", "x <"+author+">", "-m", "edit")
	gitT(h.t, h.dir, "push", "--quiet", "origin", testBranch)
}

// hlcActor is the writer every hand-made test row's clock names.
var hlcActor = func() ids.ID[ids.Actor] {
	id, err := ids.New[ids.Actor]()
	if err != nil {
		panic(err)
	}
	return id
}()

// rowHLCTime is the wall time of every hand-made test row's clock
// (rowJSON); rowJSONAt takes another.
const rowHLCTime = "2026-01-01T00:00:00.000Z"

// rowJSON is a minimal row file of table with id and fields (raw JSON values).
func rowJSON(table string, id ids.AnyID, fields map[string]string) string {
	return rowJSONAt(table, id, rowHLCTime, fields)
}

// rowJSONAt is rowJSON with the clock's wall time at. Fields are written
// in sorted order: the same row is the same bytes on every run (devco
// D-334: map order made two writes of one row differ only sometimes, and
// a test's later commit of the same content then had nothing to commit).
func rowJSONAt(table string, id ids.AnyID, at string, fields map[string]string) string {
	var b strings.Builder
	b.WriteString(`{"_table":"` + table + `","_v":2,"_hlc":"` + at + `/0000/` + hlcActor.String() + `","id":"` + id.String() + `"`)
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		b.WriteString(`,"` + k + `":` + fields[k])
	}
	b.WriteString("}\n")
	return b.String()
}

func mustID[T ids.Tag](t *testing.T) ids.AnyID {
	t.Helper()
	id, err := ids.New[T]()
	if err != nil {
		t.Fatal(err)
	}
	return id.Any()
}

// orgPath is the org-folder path of a row.
func orgPath(table string, id ids.AnyID) string {
	return rowfs.FolderOrg + "/" + table + "/" + id.String() + ".json"
}

// person is one made-up roster entry.
type person struct {
	email         string
	actor, period ids.AnyID
}

func (p person) actorRow() string {
	return rowJSON("actors", p.actor, map[string]string{"stable_key": `"human:` + p.email + `"`, "status": `"active"`, "kind": `"human"`})
}

func (p person) periodRow(role, ended string) string {
	return p.periodRowAt(rowHLCTime, role, ended)
}

// periodRowAt is periodRow written at the clock's wall time at (a later
// write of the same stint, as lore stamps every write).
func (p person) periodRowAt(at, role, ended string) string {
	f := map[string]string{"actor_id": `"` + p.actor.String() + `"`, "type": `"employee"`, "started_on": `"2026-01-01"`, "org_role": `"` + role + `"`}
	if ended != "" {
		f["ended_on"] = `"` + ended + `"`
	}
	return rowJSONAt("actor_periods", p.period, at, f)
}

func newPerson(t *testing.T, email string) person {
	return person{email: email, actor: mustID[ids.Actor](t), period: mustID[ids.ActorPeriod](t)}
}

// orgFolderClone opens a lore clone of remote on the org folder.
func orgFolderClone(t *testing.T, remote string) *Backend {
	t.Helper()
	b, err := OpenClone(context.Background(), remote, filepath.Join(t.TempDir(), "clones", "host", "store"), rowfs.FolderOrg, store.OpenOptions{}, WithMerger(newestMerger{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// readField is field of the row id as a full read (lists, render) has it; "" when the read leaves the row out.
func readField(t *testing.T, b *Backend, id ids.AnyID, field string) (string, bool) {
	t.Helper()
	rows, _, err := b.Read(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == id {
			return jsonField(string(r.Doc), field), true
		}
	}
	return "", false
}

// jsonField is a string field of a row file ("" when absent or not a
// string); any layout, a canonical re-encode included.
func jsonField(doc, field string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(doc), &m) != nil {
		return ""
	}
	var v string
	if json.Unmarshal(m[field], &v) != nil {
		return ""
	}
	return v
}

// TestRosterChangeByMemberIsHeldOnRead: devco R-5 / R-6. A member who
// edits his own stint by hand to become admin, or ends a teammate's stint
// (an offboard made without lore), commits it past every lore check;
// every reader now reads the stint as the admin last left it and names
// the commit. An admin's later change of the same row is accepted.
func TestRosterChangeByMemberIsHeldOnRead(t *testing.T) {
	t.Parallel()
	h := newHandStore(t)
	boss, mallory, carol := newPerson(t, "boss@example.test"), newPerson(t, "mallory@example.test"), newPerson(t, "carol@example.test")
	h.commit(boss.email, map[string]string{
		orgPath("actors", boss.actor): boss.actorRow(), orgPath("actor_periods", boss.period): boss.periodRow("admin", ""),
	})
	h.commit(boss.email, map[string]string{
		orgPath("actors", mallory.actor): mallory.actorRow(), orgPath("actor_periods", mallory.period): mallory.periodRow("member", ""),
		orgPath("actors", carol.actor): carol.actorRow(), orgPath("actor_periods", carol.period): carol.periodRow("member", ""),
	})
	// the attack, by hand: self-promotion, then an offboard of a teammate
	h.commit(mallory.email, map[string]string{orgPath("actor_periods", mallory.period): mallory.periodRow("admin", "")})
	h.commit(mallory.email, map[string]string{orgPath("actor_periods", carol.period): carol.periodRow("member", "2026-02-01")})

	b := orgFolderClone(t, h.remote)
	if role, _ := readField(t, b, mallory.period, "org_role"); role != "member" {
		t.Errorf("mallory's org_role read as %q, want member (the self-promotion held back)", role)
	}
	if ended, _ := readField(t, b, carol.period, "ended_on"); ended != "" {
		t.Errorf("carol's stint read as ended on %q: a member's offboard was believed", ended)
	}
	got, err := b.Get(context.Background(), row.Table("actor_periods"), mallory.period)
	if err != nil || jsonField(string(got.Doc), "org_role") != "member" {
		t.Errorf("Get of mallory's stint = %q, %v; want org_role member", got.Doc, err)
	}
	held, err := b.Held(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 {
		t.Fatalf("held = %+v, want the two stints", held)
	}
	for _, hc := range held {
		if hc.Author != mallory.email || hc.Commit == "" || hc.Table != "actor_periods" || !strings.Contains(hc.Reason, "not an admin") {
			t.Errorf("held change %+v does not name the author, commit and cause", hc)
		}
	}
	// an admin makes the change (a later write, so its own clock): accepted, the hold ends
	h.commit(boss.email, map[string]string{orgPath("actor_periods", carol.period): carol.periodRowAt("2026-02-01T00:00:00.000Z", "member", "2026-02-01")})
	syncOK(t, b)
	if ended, _ := readField(t, b, carol.period, "ended_on"); ended != "2026-02-01" {
		t.Errorf("the admin's offboard of carol read as %q", ended)
	}
	if held, _ = b.Held(context.Background()); len(held) != 1 || held[0].ID != mallory.period.String() {
		t.Errorf("held after the admin's change = %+v, want only mallory's self-promotion", held)
	}
}

// TestWritesOfALeaverAreHeldOnRead: devco R-18. After an admin offboards
// a person, a row that person commits by hand (no lore to refuse it) is
// not read by anyone; what they wrote before the offboard stays.
func TestWritesOfALeaverAreHeldOnRead(t *testing.T) {
	t.Parallel()
	h := newHandStore(t)
	boss, kim := newPerson(t, "boss@example.test"), newPerson(t, "kim@example.test")
	before, after := mustID[ids.Memory](t), mustID[ids.Memory](t)
	h.commit(boss.email, map[string]string{
		orgPath("actors", boss.actor): boss.actorRow(), orgPath("actor_periods", boss.period): boss.periodRow("admin", ""),
		orgPath("actors", kim.actor): kim.actorRow(), orgPath("actor_periods", kim.period): kim.periodRow("member", ""),
	})
	h.commit(kim.email, map[string]string{orgPath("memories", before): rowJSON("memories", before, map[string]string{"body": `"written while here"`})})
	h.commit(boss.email, map[string]string{orgPath("actor_periods", kim.period): kim.periodRow("member", "2026-03-01")})
	h.commit(kim.email, map[string]string{orgPath("memories", after): rowJSON("memories", after, map[string]string{"body": `"written after leaving"`})})

	b := orgFolderClone(t, h.remote)
	if _, ok := readField(t, b, before, "body"); !ok {
		t.Error("the row written before the offboard is gone")
	}
	if body, ok := readField(t, b, after, "body"); ok {
		t.Errorf("the leaver's row written after the offboard was read: %q", body)
	}
	if _, err := b.Get(context.Background(), row.Table("memories"), after); err == nil {
		t.Error("Get of the leaver's later row found it")
	}
	held, err := b.Held(context.Background())
	if err != nil || len(held) != 1 || held[0].ID != after.String() || !strings.Contains(held[0].Reason, "marked as left") {
		t.Errorf("held = %+v, %v; want the later memory, named as a leaver's", held, err)
	}
}

// TestOwnRowsOfAMemberAreAccepted: a member's own actor row, own agent
// and own first stint (a join) are not roster changes only an admin
// makes; a store with no admin yet accepts everything.
func TestOwnRowsOfAMemberAreAccepted(t *testing.T) {
	t.Parallel()
	h := newHandStore(t)
	boss, new1 := newPerson(t, "boss@example.test"), newPerson(t, "new@example.test")
	agent := mustID[ids.Actor](t)
	h.commit(boss.email, map[string]string{
		orgPath("actors", boss.actor): boss.actorRow(), orgPath("actor_periods", boss.period): boss.periodRow("admin", ""),
	})
	h.commit(new1.email, map[string]string{
		orgPath("actors", new1.actor): new1.actorRow(), orgPath("actor_periods", new1.period): new1.periodRow("", ""),
		orgPath("actors", agent): rowJSON("actors", agent, map[string]string{"kind": `"agent"`, "owner_actor_id": `"` + new1.actor.String() + `"`}),
	})
	b := orgFolderClone(t, h.remote)
	if held, err := b.Held(context.Background()); err != nil || len(held) != 0 {
		t.Errorf("held = %+v, %v; a member's own join rows were held", held, err)
	}
}
