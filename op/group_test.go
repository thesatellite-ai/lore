package op

import (
	"context"
	"slices"
	"testing"
)

// Repeated group inputs (group.go, P3-TASK-1): Define's tag checks, the
// JSON shape, routing without positions, and secret-scan coverage.

type gItem struct {
	Title string `json:"title" arg:"0"`
	Level string `json:"level,omitempty" flag:"level"`
}

type gIn struct {
	ID    string  `json:"id" arg:"0"`
	Items []gItem `json:"items,omitempty" arg:"1" rest:"true"`
	Level string  `json:"level,omitempty" flag:"level"`
}

func defineG[In any](In) {
	Define(Spec{Name: "thing.split"}, func(context.Context, In) (struct{}, error) { return struct{}{}, nil })
}

// TestGroupDefinePanics: every malformed group is a startup panic.
func TestGroupDefinePanics(t *testing.T) {
	t.Parallel()
	cases := map[string]func(){
		"no outer flag": func() {
			defineG(struct {
				Items []gItem `json:"items,omitempty" arg:"0" rest:"true"`
			}{})
		},
		"outer flag of another type": func() {
			defineG(struct {
				Items []gItem `json:"items,omitempty" arg:"0" rest:"true"`
				Level int     `json:"level,omitempty" flag:"level"`
			}{})
		},
		"not rest": func() {
			defineG(struct {
				Items []gItem `json:"items,omitempty" arg:"0"`
				Level string  `json:"level,omitempty" flag:"level"`
			}{})
		},
		"a flag, not a positional": func() {
			defineG(struct {
				Items []gItem `json:"items,omitempty" flag:"item"`
				Level string  `json:"level,omitempty" flag:"level"`
			}{})
		},
		"item without head": func() {
			defineG(struct {
				Items []struct {
					Level string `json:"level,omitempty" flag:"level"`
				} `json:"items,omitempty" arg:"0" rest:"true"`
				Level string `json:"level,omitempty" flag:"level"`
			}{})
		},
		"item field without flag": func() {
			defineG(struct {
				Items []struct {
					Title string `json:"title" arg:"0"`
					Note  string `json:"note,omitempty"`
				} `json:"items,omitempty" arg:"0" rest:"true"`
			}{})
		},
		"item head not a string": func() {
			defineG(struct {
				Items []struct {
					N int `json:"n" arg:"0"`
				} `json:"items,omitempty" arg:"0" rest:"true"`
			}{})
		},
	}
	for name, f := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Define did not panic", name)
				}
			}()
			f()
		}()
	}
	defineG(gIn{}) // the well-formed one does not panic
}

// TestGroupDecode: JSON items need no routing; ArgsDecoder without
// FlagPos keeps every flag on the outer input; with FlagPos a value after
// item n goes to item n; the item strings are scanned under "items.<f>".
func TestGroupDecode(t *testing.T) {
	t.Parallel()
	o := Define(Spec{Name: "thing.split"}, func(_ context.Context, in gIn) (gIn, error) { return in, nil })
	run := func(d Decoder) (gIn, Input) {
		t.Helper()
		in, err := o.Decode(d)
		if err != nil {
			t.Fatal(err)
		}
		v, ok := in.v.Interface().(gIn)
		if !ok {
			t.Fatal("input type")
		}
		return v, in
	}
	got, in := run(JSONDecoder{Body: []byte(`{"id":"p","level":"low","items":[{"title":"a","level":"high"},{"title":"b"}]}`)})
	if got.Level != "low" || len(got.Items) != 2 || got.Items[0].Level != "high" || got.Items[1].Title != "b" {
		t.Errorf("json: %+v", got)
	}
	var fields []string
	for _, tv := range in.Texts() {
		fields = append(fields, tv.Field+"="+tv.Text)
	}
	for _, want := range []string{"items.title=a", "items.level=high", "items.title=b", "level=low"} {
		if !slices.Contains(fields, want) {
			t.Errorf("texts %v lack %s", fields, want)
		}
	}
	got, _ = run(ArgsDecoder{Args: []string{"p", "a", "b"}, Flags: map[string][]string{"level": {"x"}}})
	if got.Level != "x" || got.Items[0].Level != "" || got.Items[1].Level != "" {
		t.Errorf("no positions: %+v", got)
	}
	got, _ = run(ArgsDecoder{Args: []string{"p", "a", "b"}, Flags: map[string][]string{"level": {"s", "x"}}, FlagPos: map[string][]int{"level": {1, 3}}})
	if got.Level != "s" || got.Items[0].Level != "" || got.Items[1].Level != "x" {
		t.Errorf("positions: %+v", got)
	}
	if _, err := o.Decode(ArgsDecoder{Args: []string{"p"}, Flags: map[string][]string{"level": {"s"}}, FlagPos: map[string][]int{}}); err == nil {
		t.Error("values without positions: want an error")
	}
}
