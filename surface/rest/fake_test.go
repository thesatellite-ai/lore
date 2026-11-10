package rest

// The fake registry and Runner shared by this package's tests. The same
// file lives in surface/cli (a test file cannot be imported across
// packages, and each folder has one owner).

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// opRuleAdd is the nested-noun fake operation.
const opRuleAdd = "secrets.rule.add"

// failPrefix in a body makes the fake operation return that error code.
const failPrefix = "fail:"

type addIn struct {
	Body  string        `arg:"0" json:"body" desc:"the memory text" ui:"widget=markdown"`
	Title string        `flag:"title" json:"title,omitempty" desc:"optional title"`
	Tags  []string      `flag:"tag" json:"tags,omitempty" desc:"a tag; repeat for more"`
	Limit int           `flag:"limit" json:"limit,omitempty" desc:"a number"`
	Wait  time.Duration `flag:"wait" json:"wait,omitempty" desc:"how long"`
	Force bool          `flag:"force" json:"force" desc:"ask first"`
}

type addOut struct {
	ID   string `json:"id"`
	Echo string `json:"echo"`
}

type listIn struct {
	Limit int `flag:"limit" json:"limit,omitempty" desc:"max rows"`
}

type listItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type listOut struct {
	Items []listItem `json:"items"`
}

type purgeIn struct {
	ID string `arg:"0" json:"id" desc:"the row"`
}

type purgeOut struct {
	Purged int `json:"purged"`
}

type ruleIn struct {
	Pattern string `flag:"pattern" json:"pattern" desc:"the regexp"`
}

type ruleOut struct {
	ID string `json:"id"`
}

// marker is the context key the fake Runner sets; a run function that does
// not see it was called by something other than the Runner.
type marker struct{}

// fake holds the registry and counts calls.
type fake struct {
	reg *op.Registry
	mu  sync.Mutex
	// viaRunner counts runner calls per operation; direct counts run
	// functions entered without the runner's context marker.
	viaRunner map[string]int
	direct    int
	callers   []op.Caller
}

func check(ctx context.Context, f *fake) {
	if ctx.Value(marker{}) == nil {
		f.mu.Lock()
		f.direct++
		f.mu.Unlock()
	}
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{viaRunner: map[string]int{}}
	add := op.Define(op.Spec{
		Name: "memory.add", Summary: "Add a memory", Permission: op.PermWrite, Access: op.AccessWrite,
		Errors:   []errs.Code{errs.EmptyBody, errs.SecretDetected},
		Confirm:  op.ConfirmRule{When: "force"},
		Examples: []op.Example{{Title: "Save a lesson", CLI: `lore memory add "Retry on 502"`, Want: "mem_x"}},
	}, func(ctx context.Context, in addIn) (addOut, error) {
		check(ctx, f)
		if code, ok := strings.CutPrefix(in.Body, failPrefix); ok {
			return addOut{}, errs.New(errs.Code(code), "forced failure").WithHint("do the thing")
		}
		echo := fmt.Sprintf("%s|%s|%s|%d|%s|%t", in.Body, in.Title, strings.Join(in.Tags, ","), in.Limit, in.Wait, in.Force)
		return addOut{ID: "mem_x", Echo: echo}, nil
	})
	list := op.Define(op.Spec{Name: "memory.list", Summary: "List memories", Permission: op.PermRead, Access: op.AccessRead},
		func(ctx context.Context, in listIn) (listOut, error) {
			check(ctx, f)
			out := listOut{Items: []listItem{{ID: "mem_a", Title: "first"}, {ID: "mem_b", Title: "second"}}}
			if in.Limit > 0 && in.Limit < len(out.Items) {
				out.Items = out.Items[:in.Limit]
			}
			return out, nil
		})
	purge := op.Define(op.Spec{Name: "trash.purge", Summary: "Purge a row", Permission: op.PermAdmin, Access: op.AccessWrite,
		Errors: []errs.Code{errs.NotFound, errs.NeedsConfirm}, Confirm: op.ConfirmRule{Always: true}},
		func(ctx context.Context, _ purgeIn) (purgeOut, error) {
			check(ctx, f)
			return purgeOut{Purged: 1}, nil
		})
	rule := op.Define(op.Spec{Name: opRuleAdd, Summary: "Add a secret rule", Permission: op.PermWrite, Access: op.AccessWrite},
		func(ctx context.Context, _ ruleIn) (ruleOut, error) {
			check(ctx, f)
			return ruleOut{ID: "sru_x"}, nil
		})
	hidden := op.Define(op.Spec{Name: "merge.driver", Summary: "Git merge driver", Permission: op.PermWrite, Access: op.AccessWrite, Hidden: true},
		func(ctx context.Context, _ struct{}) (struct{}, error) {
			check(ctx, f)
			return struct{}{}, nil
		})
	reg, err := op.NewRegistry(add, list, purge, rule, hidden)
	if err != nil {
		t.Fatal(err)
	}
	f.reg = reg
	return f
}

// Run implements op.Runner: decode, confirm, invoke under the marker.
func (f *fake) Run(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	f.mu.Lock()
	f.viaRunner[e.Spec().Name]++
	f.callers = append(f.callers, c)
	f.mu.Unlock()
	in, err := e.Decode(d)
	if err != nil {
		return op.Result{}, err
	}
	asks := e.Spec().Confirm.Always
	if w := e.Spec().Confirm.When; w != "" {
		v, _ := in.Bool(w)
		asks = asks || v
	}
	if asks && !c.Yes {
		return op.Result{}, errs.New(errs.NeedsConfirm, "really do "+e.Spec().Name+"?")
	}
	return e.Invoke(context.WithValue(ctx, marker{}, true), in)
}

func (f *fake) lastCaller() op.Caller {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callers[len(f.callers)-1]
}
