package task_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"
)

// fakeTeam is a host roster: handles to people or teams, and one claims
// board (or a board read error).
type fakeTeam struct {
	people   map[string]string
	teams    map[string]string
	board    *fakeBoard
	boardErr error
}

func (f *fakeTeam) ResolveHandle(_ context.Context, handle string, allowTeam bool) (opstask.Assignee, error) {
	if id, ok := f.people[handle]; ok {
		return opstask.Assignee{ActorID: id}, nil
	}
	if id, ok := f.teams[handle]; ok && allowTeam {
		return opstask.Assignee{TeamID: id}, nil
	}
	return opstask.Assignee{}, errs.New(errs.NotFound, "no "+handle+" on the roster").WithHint("lore team list")
}

func (f *fakeTeam) Claims(context.Context) (opstask.ClaimBoard, error) {
	if f.boardErr != nil {
		return nil, f.boardErr
	}
	return f.board, nil
}

// claim is one task's claim on the fake board.
type claim struct {
	holder string
	active bool
}

// fakeBoard answers Claim from a map and State from a map.
type fakeBoard struct {
	me     string
	claims map[string]claim
	states map[string]string
}

func (b *fakeBoard) Me() string { return b.me }

func (b *fakeBoard) Claim(id string) (string, bool, bool) {
	c, ok := b.claims[id]
	return c.holder, ok, c.active
}

func (b *fakeBoard) State(t opstask.View) string { return b.states[t.ID] }

// listIDs runs task.list under ctx and returns the ids.
func listIDs(t *testing.T, h *opstest.H, ctx context.Context, in string) []string {
	t.Helper()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(mustCtx(h, ctx, opstask.OpList, in))), &out); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range out.Data {
		got = append(got, d.ID)
	}
	return sortedCopy(got)
}

// TestAssignHandle: --assign @person sets the assignee and clears the
// team, --assign @team the reverse; a handle on no roster is E_NOT_FOUND
// with the roster's hint; mission/plan approvers are the mission
// package's. Internal repo, so TEAM-030 does not apply.
func TestAssignHandle(t *testing.T) {
	h := opstest.New(t, repoClient(false))
	ann := newActor(h, "Ann")
	team := newTeam(h, "@core", ann)
	ft := &fakeTeam{people: map[string]string{"@ann": ann}, teams: map[string]string{"@core": team}, board: &fakeBoard{}}
	ctx := opstask.WithTeam(context.Background(), ft)

	id := addID(h, mustCtx(h, ctx, opstask.OpAdd, `{"title":"t","assign":["@ann"]}`))
	_, r := get(h, schema.TableTasks, id)
	if field(r, "assigned_to_actor_id") != ann || field(r, "assigned_team") != "" {
		t.Errorf("person: %v", r.Fields)
	}
	mustCtx(h, ctx, opstask.OpEdit, `{"id":"`+id+`","assign":["@core"]}`)
	_, r = get(h, schema.TableTasks, id)
	if field(r, "assigned_team") != team || field(r, "assigned_to_actor_id") != "" {
		t.Errorf("team clears person: %v", r.Fields)
	}
	mustCtx(h, ctx, opstask.OpEdit, `{"id":"`+id+`","assign":["@ann"]}`)
	_, r = get(h, schema.TableTasks, id)
	if field(r, "assigned_to_actor_id") != ann || field(r, "assigned_team") != "" {
		t.Errorf("person clears team: %v", r.Fields)
	}
	teamTask := addID(h, mustCtx(h, ctx, opstask.OpAdd, `{"title":"t2","assign":["@core"]}`))
	if _, r = get(h, schema.TableTasks, teamTask); field(r, "assigned_team") != team || field(r, "assigned_to_actor_id") != "" {
		t.Errorf("add team: %v", r.Fields)
	}
	before := h.JSON(h.Must("tasklist.list", ``))
	_, err := runCtx(h, ctx, opstask.OpAdd, `{"title":"t3","assign":["@nobody"]}`)
	if !errs.Has(err, errs.NotFound) || hintOf(err) != "lore team list" {
		t.Errorf("unknown handle: %v", err)
	}
	if h.JSON(h.Must("tasklist.list", ``)) != before {
		t.Error("a refused --assign wrote a tasklist")
	}
	// Without the test's Team, App.Run supplies the standalone one
	// (P3-COORD-3, P3-COORD-3a): it resolves on the repo roster, where Ann
	// has no handle, so the handle is refused, never guessed.
	h.Fails(opstask.OpAdd, `{"title":"t4","assign":["@ann"]}`, errs.NotFound)
	h.Fails(opstask.OpEdit, `{"id":"`+id+`","assign":["@ann"]}`, errs.NotFound)
	// A plain actor reference needs no host Team.
	h.Must(opstask.OpEdit, `{"id":"`+id+`","assign":["Ann"]}`)
}

// TestTeam030Matrix is TEAM-030: a team is never assigned on a task in a
// client repo's repo store. Matrix: add/edit × target (default, repo,
// org store, personal) × repo class (client, internal, no hook = client,
// failing hook = client). A person is always allowed.
func TestTeam030Matrix(t *testing.T) {
	type repoKind struct {
		name   string
		opt    func(*wire.Options)
		client bool
	}
	kinds := []repoKind{
		{"client", repoClient(true), true},
		{"internal", repoClient(false), false},
		{"no hook", func(*wire.Options) {}, true},
		{"hook fails", repoClientErr(), true},
	}
	targets := []struct {
		name, to string
		repoSide bool
	}{
		{"default", "", true},
		{"repo", "repo", true},
		{"org", orgStore, false},
		{"me", "me", false},
	}
	for _, k := range kinds {
		h := opstest.New(t, k.opt, withOrg(t), withPersonal(t))
		ann := newActor(h, "Ann")
		team := newTeam(h, "@core", ann)
		ctx := opstask.WithTeam(context.Background(), &fakeTeam{people: map[string]string{"@ann": ann}, teams: map[string]string{"@core": team}, board: &fakeBoard{}})
		for _, tg := range targets {
			to := ""
			if tg.to != "" {
				to = `,"to":"` + tg.to + `"`
			}
			wantRefused := k.client && tg.repoSide
			t.Run(k.name+"/"+tg.name, func(t *testing.T) {
				_, err := runCtx(h, ctx, opstask.OpAdd, `{"title":"team task"`+to+`,"assign":["@core"]}`)
				checkTeam030(t, "add", err, wantRefused)
				plain := addID(h, mustCtx(h, ctx, opstask.OpAdd, `{"title":"plain"`+to+`}`))
				_, err = runCtx(h, ctx, opstask.OpEdit, `{"id":"`+plain+`","assign":["@core"]}`)
				checkTeam030(t, "edit", err, wantRefused)
				if _, r := get(h, schema.TableTasks, plain); wantRefused && field(r, "assigned_team") != "" {
					t.Error("refused edit still wrote the team")
				}
				if _, err := runCtx(h, ctx, opstask.OpEdit, `{"id":"`+plain+`","assign":["@ann"]}`); err != nil {
					t.Errorf("person refused: %v", err)
				}
			})
		}
	}
}

// checkTeam030 checks one TEAM-030 outcome: v0.1.17's message and hint
// when refused, no error otherwise.
func checkTeam030(t *testing.T, step string, err error, refused bool) {
	t.Helper()
	switch {
	case refused && (!errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "a team cannot be assigned in a linked client repo: team names are internal (TEAM-030)") ||
		!strings.Contains(hintOf(err), "--to <org>")):
		t.Errorf("%s: %v, want the TEAM-030 refusal", step, err)
	case !refused && err != nil:
		t.Errorf("%s: %v, want allowed", step, err)
	}
}

// TestMineAndClaimedBy: --mine keeps tasks assigned to me or my team that
// are unclaimed, expired or mine; --claimed-by keeps active claims of the
// holder (@handle or actor reference); both need a host Team, and an
// unreadable board fails them closed while a plain list still works.
func TestMineAndClaimedBy(t *testing.T) {
	h := opstest.New(t, repoClient(false))
	// The board's caller ("me") is an actor of the roster.
	me := newActor(h, "Me")
	bob := newActor(h, "Bob")
	myTeam := newTeam(h, "@mine", me)
	otherTeam := newTeam(h, "@other", bob)
	mineFree := add(h, "mine free", `"assigned_to":"`+me+`"`)
	mineClaimedByMe := add(h, "mine claimed by me", `"assigned_to":"`+me+`"`)
	mineClaimedByBob := add(h, "mine claimed by bob", `"assigned_to":"`+me+`"`)
	mineExpired := add(h, "mine expired", `"assigned_to":"`+me+`"`)
	viaTeam := addID(h, mustCtx(h, opstask.WithTeam(context.Background(), &fakeTeam{teams: map[string]string{"@mine": myTeam}}), opstask.OpAdd, `{"title":"via team","assign":["@mine"]}`))
	otherTeamTask := addID(h, mustCtx(h, opstask.WithTeam(context.Background(), &fakeTeam{teams: map[string]string{"@other": otherTeam}}), opstask.OpAdd, `{"title":"other team","assign":["@other"]}`))
	bobs := add(h, "bobs", `"assigned_to":"`+bob+`"`)
	board := &fakeBoard{me: me, claims: map[string]claim{
		mineClaimedByMe:  {me, true},
		mineClaimedByBob: {bob, true},
		mineExpired:      {bob, false},
		bobs:             {bob, true},
		otherTeamTask:    {bob, false},
	}}
	ctx := opstask.WithTeam(context.Background(), &fakeTeam{people: map[string]string{"@bob": bob}, board: board})

	if got, want := listIDs(t, h, ctx, `{"mine":true}`), sortedCopy([]string{mineFree, mineClaimedByMe, mineExpired, viaTeam}); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("--mine: got %v want %v", got, want)
	}
	// The same answer from a cold process: team members decoded from the
	// team row file (value.ItemStrings), not the in-memory list.
	cold := h.ReopenCold(repoClient(false))
	if got, want := listIDs(t, cold, ctx, `{"mine":true}`), sortedCopy([]string{mineFree, mineClaimedByMe, mineExpired, viaTeam}); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("--mine, cold: got %v want %v", got, want)
	}
	want := sortedCopy([]string{mineClaimedByBob, bobs})
	for _, by := range []string{"@bob", bob, "Bob", "human:bob"} {
		if got := listIDs(t, h, ctx, `{"claimed_by":"`+by+`"}`); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("--claimed-by %s: got %v want %v", by, got, want)
		}
	}
	if _, err := runCtx(h, ctx, opstask.OpList, `{"claimed_by":"@nobody"}`); !errs.Has(err, errs.NotFound) {
		t.Errorf("unknown holder: %v", err)
	}
	// Without the test's Team, App.Run supplies the standalone one
	// (P3-COORD-3, P3-COORD-3a): --mine runs against the repo's own board
	// (no signals ref here, so no claims), and --claimed-by resolves the
	// handle on the repo roster, where Bob has none: refused, never guessed.
	h.Must(opstask.OpList, `{"mine":true}`)
	h.Fails(opstask.OpList, `{"claimed_by":"@bob"}`, errs.InvalidInput)
	// An unreadable board: a filter fails closed, a plain list shows the
	// rows without a STATE column.
	broken := opstask.WithTeam(context.Background(), &fakeTeam{boardErr: errs.New(errs.StoreUnreachable, "signals ref unreadable")})
	if _, err := runCtx(h, broken, opstask.OpList, `{"mine":true}`); !errs.Has(err, errs.StoreUnreachable) {
		t.Errorf("--mine on a broken board: %v", err)
	}
	if got := opstest.Text(mustCtx(h, broken, opstask.OpList, ``)); strings.Contains(got, "  working") {
		t.Errorf("states without a board: %q", got)
	}
}

// TestStateColumn: the host board's state goes after the line's own
// fields and before the store label (v0.1.17 "… due:2026-12-01  ✓ done ·
// not merged yet"); human output only, and no column without a host
// Team.
func TestStateColumn(t *testing.T) {
	h := opstest.New(t, repoClient(false), withOrg(t))
	done := add(h, "Second", `"due":"2026-12-01"`)
	h.Must(opstask.OpDone, `{"id":"`+done+`","no_lesson":true}`)
	working := add(h, "Working", "")
	org := addID(h, h.Must(opstask.OpAdd, `{"title":"Org","to":"`+orgStore+`"}`))
	plain := add(h, "Plain", "")
	board := &fakeBoard{states: map[string]string{done: "✓ done · not merged yet", working: "▶ working (Ann, 5m left)", org: "EXPIRED (Bob)"}}
	ctx := opstask.WithTeam(context.Background(), &fakeTeam{board: board})
	res := mustCtx(h, ctx, opstask.OpList, `{"all":true}`)
	want := "✓ tsk_ID Second [medium] due:2026-12-01  ✓ done · not merged yet\n" +
		"○ tsk_ID Working [medium]  ▶ working (Ann, 5m left)\n" +
		"○ tsk_ID Plain [medium]\n" +
		"○ tsk_ID Org [medium]  EXPIRED (Bob)  [" + orgStore + "]\n"
	if got := lines(opstest.Text(res)); got != want {
		t.Errorf("STATE column:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(h.JSON(res), "working") || strings.Contains(h.JSON(res), "EXPIRED") {
		t.Errorf("state leaked into --json: %s", h.JSON(res))
	}
	if got := opstest.Text(h.Must(opstask.OpList, `{"all":true}`)); strings.Contains(got, "EXPIRED") {
		t.Errorf("state without a host Team: %q", got)
	}
	_ = plain
}

// TestRefsF15: --ref pointers follow F15 at write time (refused
// E_LEAK_BLOCKED from a client repo row to an internal or personal row,
// nothing written), are allowed from internal/personal to repo and from
// personal to internal is refused per the direction table; re-adding a
// pointer writes nothing; show prints refs and "(not available here)" for
// a qualified ref outside this checkout.
func TestRefsF15(t *testing.T) {
	h := opstest.New(t, repoClient(true), withOrg(t), withPersonal(t))
	repoMem := h.ID(h.Must("memory.add", `{"body":"repo note"}`))
	orgMem := h.ID(h.Must("memory.add", `{"body":"org note","to":"`+orgStore+`"}`))
	meMem := h.ID(h.Must("memory.add", `{"body":"my note","to":"me"}`))
	if repoMem == "" || orgMem == "" || meMem == "" {
		t.Fatal("memory.add gave no id")
	}
	refused := []struct{ to, target string }{
		{"", orgMem}, {"repo", orgMem}, {"", meMem}, {orgStore, meMem},
	}
	for _, c := range refused {
		before := h.JSON(h.Must(opstask.OpList, `{"all":true}`))
		_, err := h.Run(opstask.OpAdd, `{"title":"r","to":"`+c.to+`","ref":["`+c.target+`"]}`)
		if !errs.Has(err, errs.LeakBlocked) {
			t.Errorf("add to %q ref %s: %v, want E_LEAK_BLOCKED", c.to, c.target, err)
		}
		if h.JSON(h.Must(opstask.OpList, `{"all":true}`)) != before {
			t.Errorf("refused add to %q wrote a task", c.to)
		}
	}
	allowed := []struct{ to, target string }{
		{"", repoMem}, {orgStore, repoMem}, {orgStore, orgMem}, {"me", orgMem}, {"me", repoMem}, {"me", meMem},
	}
	for _, c := range allowed {
		if _, err := h.Run(opstask.OpAdd, `{"title":"a","to":"`+c.to+`","ref":["`+c.target+`"]}`); err != nil {
			t.Errorf("add to %q ref %s: %v", c.to, c.target, err)
		}
	}
	// Edit checks against the store holding the task.
	repoTask := add(h, "repo task", "")
	h.Fails(opstask.OpEdit, `{"id":"`+repoTask+`","ref":["`+orgMem+`"]}`, errs.LeakBlocked)
	orgTask := addID(h, h.Must(opstask.OpAdd, `{"title":"org task","to":"`+orgStore+`"}`))
	h.Must(opstask.OpEdit, `{"id":"`+orgTask+`","ref":["`+orgMem+`","commit:abc123"]}`)
	h.Must(opstask.OpEdit, `{"id":"`+orgTask+`","ref":["`+orgMem+`","commit:abc123"]}`)
	// A qualified pointer to a row this checkout does not hold. (A
	// project outside the checkout is refused today: knowledge_refs'
	// dst_project relation lacks AllowDangling, reported to schema's
	// owner; this case keeps the loaded project.)
	other := h.Project + ":mem_01a1a5aca7f57659be7320c5f4b70098"
	h.Must(opstask.OpEdit, `{"id":"`+orgTask+`","ref":["`+other+`"]}`)
	show := h.Must(opstask.OpShow, `{"id":"`+orgTask+`"}`)
	got := lines(opstest.Text(show))
	for _, want := range []string{
		"  ref:      memories/mem_ID\n",
		"  ref:      → prj_ID:mem_ID (not available here)\n",
		"  ref:      commits/abc123\n",
	} {
		if strings.Count(got, want) != 1 {
			t.Errorf("show refs: want one %q in\n%s", want, got)
		}
	}
	if !strings.Contains(h.JSON(show), `"unavailable":true`) {
		t.Errorf("json unavailable flag: %s", h.JSON(show))
	}
	_ = store.SourceRepo
}
