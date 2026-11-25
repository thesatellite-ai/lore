package ask

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/value"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

// linesEnv answers ask's statements from fixed lines, in the order perm
// gives them: the same rows a cache hands back in another insert order.
type linesEnv struct {
	typed, text [][]value.Value
	perm        func([][]value.Value) [][]value.Value
	at          time.Time
}

func (e *linesEnv) Now() time.Time { return e.at }

func (e *linesEnv) AskLens(_ context.Context, _ bool, fn func(q Query, keeps bool) error) error {
	return fn(func(_ context.Context, s string) (opssql.Result, error) {
		switch s {
		case typedSQL:
			return opssql.Result{Rows: e.perm(e.typed)}, nil
		case textSQL:
			return opssql.Result{Rows: e.perm(e.text)}, nil
		}
		return opssql.Result{}, nil
	}, false)
}

// orderStore is a store of many rows with one score: 60 notes holding the
// same words on one day (more than hybrid.Pool, so the pool cut falls
// inside the tie), two resources the question names by alias, and two
// open tasks written the same second.
func orderStore() (typed, text [][]value.Value) {
	txt := func(s string) value.Value { v, _ := value.TextOf(s); return v }
	line := func(cells ...string) []value.Value {
		out := make([]value.Value, len(cells))
		for i, c := range cells {
			out[i] = txt(c)
		}
		return out
	}
	typed = [][]value.Value{
		line("res", "res_aa01", "acme/eng", "service", "billing-gw", "", "", "2026-11-01T00:00:00Z", `["the payer"]`),
		line("res", "res_aa02", "acme/eng", "service", "ledger-svc", "", "", "2026-11-01T00:00:00Z", `["the books"]`),
		line("tsk", "tsk_aa01", "acme/eng", "todo", "retry the payer twice", "", "", "2026-11-02T10:00:00Z", ""),
		line("tsk", "tsk_aa02", "acme/eng", "todo", "retry the books twice", "", "", "2026-11-02T10:00:00Z", ""),
		line("ref", "tsk_aa01", "", "about", "res_aa01", "", "", "", ""),
		line("ref", "tsk_aa02", "", "about", "res_aa02", "", "", "", ""),
	}
	for i := range 60 {
		body := fmt.Sprintf("The gateway retries a failed charge twice before it gives up, step %c%c.", 'a'+i/26, 'a'+i%26)
		text = append(text, line("row", "memories", fmt.Sprintf("mem_ab%02d", i), "acme/eng", "", body,
			"2026-11-03T09:00:00.000000000Z", "0", "", "", " "+strings.ToLower(body), "2026-11-03T09:00:00.000000000Z", "2026-11-03T09:00:00.000000000Z"))
	}
	return typed, text
}

// TestAskSameAnswerAnyLineOrder (devco:D-979, bug hunt H-238): the same
// store and question print byte for byte the same answer whatever order
// the cache hands the lines in, on every run (a map's order changes per
// run) and with the clock moved by minutes and hours within its day. It
// failed while ties fell back to the cache's insert order and the alias
// matches came in map order.
func TestAskSameAnswerAnyLineOrder(t *testing.T) {
	t.Parallel()
	typed, text := orderStore()
	base := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewPCG(7, 9))
	perms := []func([][]value.Value) [][]value.Value{
		func(l [][]value.Value) [][]value.Value { return l },
		func(l [][]value.Value) [][]value.Value { out := slices.Clone(l); slices.Reverse(out); return out },
		func(l [][]value.Value) [][]value.Value {
			out := slices.Clone(l)
			rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
			return out
		},
	}
	for _, query := range []string{"how does the gateway retry a failed charge", "what about the payer and the books"} {
		var want string
		for run := range 60 {
			env := &linesEnv{typed: typed, text: text, perm: perms[run%len(perms)], at: base.Add(time.Duration(run) * 7 * time.Minute)}
			o, err := askRun(env, query)
			if err != nil {
				t.Fatal(err)
			}
			got := o.Text()
			if run == 0 {
				want = got
				continue
			}
			if got != want {
				t.Fatalf("%q: run %d (perm %d, clock +%dm) answered\n%s\nwant\n%s", query, run, run%len(perms), run*7, got, want)
			}
		}
	}
}

// askRun is run with the default caps.
func askRun(env Env, query string) (Out, error) {
	return run(context.Background(), env, RunIn{Query: query})
}
