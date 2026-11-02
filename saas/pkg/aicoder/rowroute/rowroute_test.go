package rowroute

import (
	"context"
	"errors"
	"testing"
)

var defaults = map[string]bool{"tasks": true, "missions": true, "handoffs": true, "comments": true, "rules": false, "decisions": false, "memories": false}

// TestDecideMatrix walks the F1 table (live / branch / both / neither) ×
// flag (none, --live, --no-live, flag for another kind) × new / edit /
// delete × kind default live / not live.
func TestDecideMatrix(t *testing.T) {
	type tc struct {
		name  string
		req   Request
		row   Row
		place Place
		why   string
	}
	task, rule := "tasks", "rules"
	cases := []tc{
		{"live row, no flag", Request{Defaults: defaults}, Row{Table: task, InLive: true}, PlaceLive, ReasonLiveWins},
		{"live row, --no-live still live (rule 4)", Request{Choice: ChoiceNoLive, ChoiceTable: task, Defaults: defaults}, Row{Table: task, InLive: true}, PlaceLive, ReasonLiveWins},
		{"live and branch row edits live", Request{Defaults: defaults}, Row{Table: task, InLive: true, InBranch: true}, PlaceLive, ReasonLiveWins},
		{"live row deleted → tombstone", Request{Defaults: defaults}, Row{Table: rule, InLive: true, Deleted: true}, PlaceLive, ReasonLiveWins},
		{"branch row, no flag stays branch", Request{Defaults: defaults}, Row{Table: task, InBranch: true}, PlaceBranch, ReasonBranchRow},
		{"branch row, --live moves to live", Request{Choice: ChoiceLive, ChoiceTable: task, Defaults: defaults}, Row{Table: task, InBranch: true}, PlaceLive, ReasonFlagLive},
		{"branch row, --live for another kind", Request{Choice: ChoiceLive, ChoiceTable: rule, Defaults: defaults}, Row{Table: task, InBranch: true}, PlaceBranch, ReasonBranchRow},
		{"branch row deleted", Request{Defaults: defaults}, Row{Table: task, InBranch: true, Deleted: true}, PlaceBranch, ReasonDeleteBranch},
		{"new task default live", Request{Defaults: defaults}, Row{Table: task}, PlaceLive, ReasonDefaultLive},
		{"new task --no-live", Request{Choice: ChoiceNoLive, ChoiceTable: task, Defaults: defaults}, Row{Table: task}, PlaceBranch, ReasonFlagNoLive},
		{"new rule default with the code", Request{Defaults: defaults}, Row{Table: rule}, PlaceBranch, ReasonDefaultLocal},
		{"new rule --live", Request{Choice: ChoiceLive, ChoiceTable: rule, Defaults: defaults}, Row{Table: rule}, PlaceLive, ReasonFlagLive},
		{"new actor (no default) with the code", Request{Defaults: defaults}, Row{Table: "actors"}, PlaceBranch, ReasonDefaultLocal},
		{"tasks off in config", Request{Defaults: map[string]bool{"tasks": false}}, Row{Table: task}, PlaceBranch, ReasonDefaultLocal},
		{"nil defaults", Request{}, Row{Table: task}, PlaceBranch, ReasonDefaultLocal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(c.req, c.row)
			if got.Place != c.place || got.Reason != c.why {
				t.Fatalf("Decide = %+v, want %s (%s)", got, c.place, c.why)
			}
		})
	}
}

// TestDecideAllCompanions: a supporting row created by a command follows
// its live content rows, and only then.
func TestDecideAllCompanions(t *testing.T) {
	actor := Row{Table: "actors", ID: "act_1"}
	cases := []struct {
		name string
		req  Request
		rows []Row
		want []Place
	}{
		{"new task live → new actor live", Request{Defaults: defaults}, []Row{actor, {Table: "tasks"}}, []Place{PlaceLive, PlaceLive}},
		{"new memory branch → actor branch", Request{Defaults: defaults}, []Row{actor, {Table: "memories"}}, []Place{PlaceBranch, PlaceBranch}},
		{"--no-live task → actor branch", Request{Choice: ChoiceNoLive, ChoiceTable: "tasks", Defaults: defaults}, []Row{actor, {Table: "tasks"}}, []Place{PlaceBranch, PlaceBranch}},
		{"existing actor file stays branch", Request{Defaults: defaults}, []Row{{Table: "actors", InBranch: true}, {Table: "tasks"}}, []Place{PlaceBranch, PlaceLive}},
		{"live task edit pulls a new actor", Request{Defaults: defaults}, []Row{actor, {Table: "tasks", InLive: true}}, []Place{PlaceLive, PlaceLive}},
		{"a rule (content, own default) never follows", Request{Defaults: defaults}, []Row{{Table: "rules"}, {Table: "tasks"}}, []Place{PlaceBranch, PlaceLive}},
		{"deleted supporting row never follows", Request{Defaults: defaults}, []Row{{Table: "actors", Deleted: true}, {Table: "tasks"}}, []Place{PlaceBranch, PlaceLive}},
		{"only supporting rows: default", Request{Defaults: defaults}, []Row{actor}, []Place{PlaceBranch}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DecideAll(c.req, c.rows)
			for i := range got {
				if got[i].Place != c.want[i] {
					t.Fatalf("row %d (%s): %+v, want %s", i, c.rows[i].Table, got[i], c.want[i])
				}
			}
		})
	}
}

type recordDest struct {
	got []Write
	err error
}

func (d *recordDest) Apply(_ context.Context, ws []Write) ([]Outcome, error) {
	d.got = append(d.got, ws...)
	out := make([]Outcome, len(ws))
	for i, w := range ws {
		out[i] = Outcome{Table: w.Table, ID: w.ID, Where: string(w.Target.Kind)}
	}
	return out, d.err
}

func TestRouterDispatchesByTargetKind(t *testing.T) {
	var r Router
	repo, store := &recordDest{}, &recordDest{}
	r.Register(TargetRepo, repo)
	r.Register(TargetStore, store)
	ws := []Write{
		{Target: Target{Kind: TargetStore, Org: "northwind", Folder: "acme"}, Table: "memories", ID: "mem_1"},
		{Target: Target{Kind: TargetRepo}, Table: "tasks", ID: "tsk_1"},
	}
	out, err := r.Apply(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.got) != 1 || repo.got[0].ID != "tsk_1" || len(store.got) != 1 || store.got[0].ID != "mem_1" {
		t.Fatalf("dispatch: repo=%v store=%v", repo.got, store.got)
	}
	if out[0].ID != "tsk_1" || out[1].ID != "mem_1" {
		t.Fatalf("outcomes must come in target order repo, store: %+v", out)
	}
	if kinds := r.Registered(); len(kinds) != 2 || kinds[0] != TargetRepo {
		t.Fatalf("registered: %v", kinds)
	}
}

// A write for a kind nobody registered fails before ANY destination runs.
func TestRouterRefusesUnknownTargetBeforeWriting(t *testing.T) {
	var r Router
	repo := &recordDest{}
	r.Register(TargetRepo, repo)
	_, err := r.Apply(context.Background(), []Write{
		{Target: Target{Kind: TargetRepo}, Table: "tasks", ID: "tsk_1"},
		{Target: Target{Kind: TargetPersonal}, Table: "memories", ID: "mem_1"},
	})
	if !errors.Is(err, ErrNoDestination) {
		t.Fatalf("want ErrNoDestination, got %v", err)
	}
	if len(repo.got) != 0 {
		t.Fatal("nothing may be written when one target has no destination")
	}
}

func TestChoiceString(t *testing.T) {
	for c, want := range map[Choice]string{ChoiceDefault: "default", ChoiceLive: "--live", ChoiceNoLive: "--no-live"} {
		if c.String() != want {
			t.Fatalf("%d: %q", c, c.String())
		}
	}
}
