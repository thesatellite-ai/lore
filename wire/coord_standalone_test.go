package wire

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
	opscoord "github.com/thesatellite-ai/lore/ops/coord"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// standaloneTask opens an App over a fresh git repo with a project and one
// task, returning both.
func standaloneTask(t *testing.T) (*App, string, string) {
	t.Helper()
	repo := gitRepo(t)
	notClient := func(context.Context) (bool, error) { return false, nil } // a plain repo of no org (cmd/lore's org resolution)
	a := openWith(t, repo, Options{RepoIsClient: notClient})
	withProject(t, a)
	out, err := run(t, a, opstask.OpAdd, `{"title":"fix rounding"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &shown); err != nil || shown.Data.ID == "" {
		t.Fatalf("task add output has no id: %s (%v)", out, err)
	}
	return a, repo, shown.Data.ID
}

// noteLines runs task.note and returns its report lines.
func noteLines(t *testing.T, a *App, input string) ([]string, error) {
	t.Helper()
	out, err := run(t, a, opscoord.OpTaskNote, input, true)
	if err != nil {
		return nil, err
	}
	var r opscoord.Report
	if jerr := json.Unmarshal([]byte(out), &r); jerr != nil {
		t.Fatal(jerr)
	}
	return r.Lines, nil
}

// TestStandaloneNote (P3-COORD-3): without host coordination a note is a
// comment row on the task ("✓ note cmt_… on tsk_…"); an empty note, a
// non-task id, a missing task and --to anything but repo are refused with
// nothing written; --to repo is the one deliberate share (one more comment).
func TestStandaloneNote(t *testing.T) {
	a, _, id := standaloneTask(t)
	lines, err := noteLines(t, a, `{"id":"`+id+`","text":["route","done"]}`)
	if err != nil || len(lines) != 1 || !strings.HasPrefix(lines[0], "✓ note cmt_") || !strings.HasSuffix(lines[0], " on "+id) {
		t.Fatalf("note = %q, %v", lines, err)
	}
	if n := countRows(t, a, schema.TableComments); n != 1 {
		t.Fatalf("comments after one note = %d", n)
	}
	refusals := []struct {
		name, input string
		code        errs.Code
	}{
		{"empty", `{"id":"` + id + `","text":["  "]}`, errs.EmptyBody},
		{"not a task id", `{"id":"mem_01a1ac2f60977e82abe757a9387b48bc","text":["x"]}`, errs.InvalidInput},
		{"no such task", `{"id":"tsk_01a1ac2f60977e82abe757a9387b48bc","text":["x"]}`, errs.NotFound},
		{"shared with --to another place", `{"id":"` + id + `","text":["x"],"share_to":"acme"}`, errs.Unsupported},
		{"from another place", `{"id":"` + id + `","text":["x"],"from":"acme"}`, errs.Unsupported},
	}
	for _, c := range refusals {
		if _, err := noteLines(t, a, c.input); !errs.Has(err, c.code) {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
	}
	if n := countRows(t, a, schema.TableComments); n != 1 {
		t.Fatalf("a refused note wrote a row: %d comments", n)
	}
	if lines, err = noteLines(t, a, `{"id":"`+id+`","text":["shared"],"share_to":"repo"}`); err != nil || len(lines) != 1 || !strings.HasPrefix(lines[0], "✓ note cmt_") {
		t.Fatalf("--to repo = %q, %v", lines, err)
	}
	if n := countRows(t, a, schema.TableComments); n != 2 {
		t.Fatalf("--to repo writes one comment: %d comments", n)
	}
	// Claims are taken here now (P3-SIGCOORD-2); a repo without a remote
	// cannot confirm one, so the write fails closed and nothing is queued.
	for name, input := range map[string]string{opscoord.OpTaskClaim: `{"id":"` + id + `"}`, opscoord.OpTaskRelease: `{"id":"` + id + `"}`, opscoord.OpHoldRelease: `{"name":"suite"}`} {
		if _, err := run(t, a, name, input, true); !errs.Has(err, errs.Offline) {
			t.Errorf("%s: %v, want E_OFFLINE", name, err)
		}
	}
}

// writeTrackedClaim puts one claim on task id held by holder into the
// tracking copy of refs/lore/signals (what a fetch by the release binary
// leaves), expiring in 15 minutes.
func writeTrackedClaim(t *testing.T, repo, id, holder, session string) {
	t.Helper()
	r, err := gitref.Open(background(), repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	git := func(stdin string, args ...string) string {
		out, gerr := r.Run(background(), gitref.Invocation{Args: args, Stdin: []byte(stdin)})
		if gerr != nil {
			t.Fatalf("git %v: %v", args, gerr)
		}
		return strings.TrimSpace(string(out))
	}
	now := time.Now().UTC().Truncate(time.Second)
	sig := `{"kind":"claim","task":"` + id + `","holder":"` + holder + `","session":"` + session + `","claim_token":"tok","since":"` +
		now.Add(-5*time.Minute).Format(time.RFC3339) + `","expires":"` + now.Add(15*time.Minute).Format(time.RFC3339) + `","renewed_seq":1}`
	blob := git(sig, "hash-object", "-w", "--stdin")
	claims := git("100644 blob "+blob+"\t"+id+".json\n", "mktree")
	folder := git("040000 tree "+claims+"\tclaims\n", "mktree")
	root := git("040000 tree "+folder+"\t_root\n", "mktree")
	commit := git("", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit-tree", root, "-m", "lore: signals")
	git("", "update-ref", "refs/lore-remote/origin/signals", commit)
}

// TestStandaloneNoteFencedForHolder: a note by the task's holder (this
// actor in this session, v0.1.17 readClaim) is a fenced write on the
// signals ref (P3-SIGCOORD-6); in a repo without a remote it cannot be
// confirmed, so it fails closed and writes nothing. Someone else's claim
// does not stop the note. The board also fills the STATE column and Claim
// answers.
func TestStandaloneNoteFencedForHolder(t *testing.T) {
	a, repo, id := standaloneTask(t)
	me, err := a.Actor(background())
	if err != nil {
		t.Fatal(err)
	}
	who, err := a.Who(background())
	if err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname() // the session's host part; empty is still one session
	writeTrackedClaim(t, repo, id, me.String(), who.StableKey+humanSessionSeparator+host)
	if _, nerr := noteLines(t, a, `{"id":"`+id+`","text":["held"]}`); !errs.Has(nerr, errs.Offline) {
		t.Fatalf("note by the holder: %v, want E_OFFLINE (fenced, no remote to confirm it)", nerr)
	}
	if n := countRows(t, a, schema.TableComments); n != 0 {
		t.Fatalf("a fenced note wrote %d comments", n)
	}
	other := "act_00000000000000000000000000000001"
	writeTrackedClaim(t, repo, id, other, "ses_x")
	if _, nerr := noteLines(t, a, `{"id":"`+id+`","text":["unheld"]}`); nerr != nil {
		t.Fatalf("note on someone else's claim: %v", nerr)
	}
	b, err := standaloneTeam{a: a}.Claims(background())
	if err != nil {
		t.Fatal(err)
	}
	if h, claimed, active := b.Claim(id); h != other || !claimed || !active {
		t.Fatalf("Claim = %q %v %v", h, claimed, active)
	}
	if s := b.State(opstask.View{ID: id, Status: "todo"}); !strings.HasPrefix(s, "▶ working: "+other) {
		t.Fatalf("State = %q", s)
	}
}

// TestStandaloneResolveHandle: @h is an active person of the repo roster,
// else (allowTeam) a miss names both; without allowTeam the miss is the
// roster's; an empty handle is invalid. A host with its own Coord keeps
// its context as is.
func TestStandaloneResolveHandle(t *testing.T) {
	a, _, _ := standaloneTask(t)
	ctx := background()
	id, _, err := a.RosterRegister(ctx, store.SourceRepo, RosterActor{Kind: schema.ActorKindHuman, StableKey: "mailto:dev3@acme.example", Name: "dev3", Handle: "dev3", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	team := standaloneTeam{a: a}
	if got, rerr := team.ResolveHandle(ctx, "@dev3", true); rerr != nil || got.ActorID != id {
		t.Fatalf("@dev3 = %+v %v, want %s", got, rerr, id)
	}
	if _, rerr := team.ResolveHandle(ctx, "@nobody", true); !errs.Has(rerr, errs.NotFound) {
		t.Fatalf("@nobody (teams too): %v, want E_NOT_FOUND", rerr)
	}
	if _, rerr := team.ResolveHandle(ctx, "@nobody", false); !errs.Has(rerr, errs.InvalidInput) {
		t.Fatalf("@nobody (people only): %v, want E_INVALID_INPUT", rerr)
	}
	if _, rerr := team.ResolveHandle(ctx, "@", true); !errs.Has(rerr, errs.InvalidInput) {
		t.Fatalf("empty handle: %v", rerr)
	}
	b, err := team.Claims(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, _ := b.Claim("tsk_01a1ac2f60977e82abe757a9387b48bc"); claimed {
		t.Fatal("a repo with no signals ref has no claims")
	}
}
