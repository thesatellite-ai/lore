package wire

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmission "github.com/thesatellite-ai/lore/ops/mission"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// getFault is a repo-store backend whose Get of the ids in fail answers
// that id's error (a read error, or store.ErrNotFound for a missing row).
type getFault struct {
	store.Backend
	mu   *sync.Mutex
	fail map[string]error
}

// Unwrap implements wire.Unwrapper.
func (g getFault) Unwrap() store.Backend { return g.Backend }

func (g getFault) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	g.mu.Lock()
	err := g.fail[id.String()]
	g.mu.Unlock()
	if err != nil {
		return store.Row{}, err
	}
	return g.Backend.Get(ctx, table, id)
}

// TestCheckApproverFailsClosed: gate approve's approvers check refuses
// when the task or its mission cannot be read (the list may name someone
// else), refuses an approver not on a mission's list, and passes a task
// with no mission, a mission with no list, an approver on the list and a
// mission row that does not exist (no list to check).
func TestCheckApproverFailsClosed(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	fail := map[string]error{}
	a := openWith(t, gitRepo(t), Options{
		RepoIsClient: func(context.Context) (bool, error) { return false, nil },
		WrapBackend: func(_ context.Context, name store.Source, _ string, b store.Backend) store.Backend {
			if name != store.SourceRepo {
				return b
			}
			return getFault{Backend: b, mu: &mu, fail: fail}
		},
	})
	withProject(t, a)
	ctx := background()
	me, err := a.Actor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := a.Engine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mission := func(approvers ...string) string {
		t.Helper()
		out, rerr := run(t, a, opsmission.OpAdd, `{"title":"ship"}`, true)
		if rerr != nil {
			t.Fatal(rerr)
		}
		i := strings.Index(out, "msn_")
		if i < 0 || len(out) < i+36 {
			t.Fatalf("mission add output has no id: %s", out)
		}
		id := out[i : i+36]
		if len(approvers) == 0 {
			return id
		}
		items := make([]value.Value, 0, len(approvers))
		for _, ap := range approvers {
			v, verr := value.TextOf(ap)
			if verr != nil {
				t.Fatal(verr)
			}
			items = append(items, v)
		}
		l, lerr := value.ListOf(value.KindText, items...)
		if lerr != nil {
			t.Fatal(lerr)
		}
		aid, perr := ids.ParseAny(id)
		if perr != nil {
			t.Fatal(perr)
		}
		if _, uerr := eng.Update(ctx, engine.Ref{Table: schema.TableMissions, ID: aid}, map[string]value.Value{fieldApprovers: l}); uerr != nil {
			t.Fatal(uerr)
		}
		return id
	}
	task := func(mis string) string {
		t.Helper()
		in := `{"title":"step"`
		if mis != "" {
			in += `,"mission":"` + mis + `"`
		}
		out, rerr := run(t, a, opstask.OpAdd, in+`}`, true)
		if rerr != nil {
			t.Fatal(rerr)
		}
		i := strings.Index(out, "tsk_")
		if i < 0 || len(out) < i+36 {
			t.Fatalf("task add output has no id: %s", out)
		}
		return out[i : i+36]
	}
	other := "act_0123456789abcdef0123456789abcdef"
	readErr := errors.New("disk read failed")
	h := standaloneCoord{a: a}
	noMission := task("")
	open := mission()
	onList := mission(other, me.String())
	offList := mission(other)
	tOpen, tOn, tOff := task(open), task(onList), task(offList)
	missing := mission(me.String())
	tMissing := task(missing)

	cases := []struct {
		name    string
		task    string
		fails   map[string]error
		wantErr errs.Code
	}{
		{"no mission", noMission, nil, ""},
		{"mission without approvers", tOpen, nil, ""},
		{"approver on the list", tOn, nil, ""},
		{"approver not on the list", tOff, nil, errs.InvalidInput},
		{"mission row missing", tMissing, map[string]error{missing: store.ErrNotFound}, ""},
		{"mission unreadable", tOn, map[string]error{onList: readErr}, errs.Internal},
		{"mission unreadable, would be refused anyway", tOff, map[string]error{offList: readErr}, errs.Internal},
		{"task unreadable", tOn, map[string]error{tOn: readErr}, errs.Internal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mu.Lock()
			clear(fail)
			for k, v := range c.fails {
				fail[k] = v
			}
			mu.Unlock()
			defer func() { mu.Lock(); clear(fail); mu.Unlock() }()
			err := h.checkApprover(ctx, c.task, me.String())
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("want allowed, got %v", err)
				}
				return
			}
			if errs.CodeOf(err) != c.wantErr {
				t.Fatalf("got %v, want %s", err, c.wantErr)
			}
		})
	}
}
