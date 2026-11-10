package wire

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitremote"
	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsperiod "github.com/thesatellite-ai/lore/ops/period"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestTaskReaderOpenTasksClaims (TEAM_PROFILES 5.1 step 3, TP-17): an open
// task the leaving person is neither lead nor assignee of is listed when
// they hold a claim this clone reads as active (as `task list
// --claimed-by` does); someone else's claim and a claimed done task are
// not; lead and assignee win over the claim. (Expiry is the signals
// view's rule, observation-based, and is tested there.)
// Break-verified: with claimHeldBy answering false the claim rows vanish.
func TestTaskReaderOpenTasksClaims(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := orgPeriodsAppIn(t, repo)
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	prj, err := ids.ParseAny(projectID(t, a))
	if err != nil {
		t.Fatal(err)
	}
	mira, err := ids.ParseAny(periodsHere)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ids.ParseAny(periodsBot)
	if err != nil {
		t.Fatal(err)
	}
	add := func(title, status string, lead ids.AnyID) string {
		f := textValues(t, map[string]string{"title": title, "status": status,
			"priority": schema.TaskPriorities()[0], "commitment": schema.TaskCommitments()[0]})
		pv, perr := value.RefOf(prj)
		if perr != nil {
			t.Fatal(perr)
		}
		lv, lerr := value.RefOf(lead)
		if lerr != nil {
			t.Fatal(lerr)
		}
		f["project_id"], f[fieldTaskLead] = pv, lv
		w, cerr := eng.Create(background(), store.SourceRepo, schema.TableTasks, f)
		if cerr != nil {
			t.Fatal(cerr)
		}
		return w.Row.ID.String()
	}
	claimed := add("claimed by mira", schema.TaskStatusInProgress, other)
	theirs := add("claimed by the bot", schema.TaskStatusTodo, other)
	done := add("claimed but done", schema.TaskStatusDone, other)
	led := add("mira leads and claims", schema.TaskStatusTodo, mira)

	now := time.Now().UTC()
	files := map[string]string{}
	for i, c := range []struct {
		task, holder string
		expires      time.Time
	}{
		{claimed, periodsHere, now.Add(time.Hour)},
		{theirs, periodsBot, now.Add(time.Hour)},
		{done, periodsHere, now.Add(time.Hour)},
		{led, periodsHere, now.Add(time.Hour)},
	} {
		b, eerr := signals.Encode(signals.Signal{Kind: signals.KindClaim, Task: c.task, Holder: c.holder,
			Session: "s-1", ClaimToken: "tok-" + string(rune('a'+i)), Since: now.Add(-time.Minute), Expires: c.expires})
		if eerr != nil {
			t.Fatal(eerr)
		}
		files[signals.ClaimPath(signalsRootFolder, c.task)] = string(b)
	}
	tracking, ok := gitremote.TrackingRef("origin", signals.CodeRepoRef)
	if !ok {
		t.Fatal("no tracking ref for the signals ref")
	}
	writeLiveRef(t, repo, tracking, files)

	got, err := a.OpenTasks(background(), mira)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{claimed: opsperiod.TaskOnClaim, led: opsperiod.TaskOnLead}
	if len(got) != len(want) {
		t.Fatalf("open tasks %+v, want %v", got, want)
	}
	for _, x := range got {
		if want[x.ID] != x.How {
			t.Fatalf("open tasks %+v, want %v", got, want)
		}
	}
}
