package whoami

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

type fakeHost struct {
	label    *string
	whoamied bool
}

func (f *fakeHost) Whoami(context.Context, ShowIn) (Out, error) {
	f.whoamied = true
	return Out{Data: Data{Org: "agency", Remotes: []string{}}, Lines: []string{"org        agency"}}, nil
}

func (f *fakeHost) SetMachineLabel(_ context.Context, l string) (MachineLabel, []string, error) {
	f.label = &l
	return MachineLabel{ID: "mch_1", Label: l, Changed: true}, []string{"✓ label"}, nil
}

type env struct{ h Host }

func (e env) WhoamiHost(context.Context) (Host, bool) { return e.h, e.h != nil }

func run(t *testing.T, h Host, body string) (op.Result, error) {
	t.Helper()
	e := Ops(env{h})[0]
	in, err := e.Decode(op.JSONDecoder{Body: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	return e.Invoke(context.Background(), in)
}

// TestShowRoutes: no --machine-label runs Whoami and keeps the v0.1.17
// envelope; a present label, even "", sets it (clears) instead; a label
// with --explain is refused; no host is E_UNSUPPORTED.
func TestShowRoutes(t *testing.T) {
	t.Parallel()
	h := &fakeHost{}
	res, err := run(t, h, `{}`)
	if err != nil || !h.whoamied || h.label != nil {
		t.Fatalf("plain whoami: %v %+v", err, h)
	}
	b, _ := res.JSON()
	if !strings.Contains(string(b), `"kind":"whoami"`) || !strings.Contains(string(b), `"schema_version":1`) || strings.Contains(string(b), `"label"`) {
		t.Fatalf("envelope %s", b)
	}
	h = &fakeHost{}
	if _, err := run(t, h, `{"machine_label":""}`); err != nil || h.label == nil || *h.label != "" || h.whoamied {
		t.Fatalf("clear label: %v %+v", err, h)
	}
	if _, err := run(t, &fakeHost{}, `{"machine_label":"x","explain":true}`); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("label+explain: %v", err)
	}
	if _, err := run(t, nil, `{}`); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("no host: %v", err)
	}
	var l Label
	if err := json.Unmarshal([]byte(`"desk"`), &l); err != nil || !l.Set || l.Value != "desk" {
		t.Fatalf("label json: %+v %v", l, err)
	}
}
