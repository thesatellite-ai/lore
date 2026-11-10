package orgcmd

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// fakeHost asks on create/import/link like the real one: unconfirmed it
// returns the plan and a question, confirmed it "writes".
type fakeHost struct {
	writes int
	got    CreateIn
	link   LinkIn
}

func (f *fakeHost) plan(yes bool, what string) (Report, error) {
	if !yes {
		return Report{Lines: []string{"plan " + what}, Ask: what + "?"}, nil
	}
	f.writes++
	return Report{Lines: []string{"plan " + what, "✓ " + what}}, nil
}

func (f *fakeHost) Create(_ context.Context, in CreateIn, yes bool) (Report, error) {
	f.got = in
	return f.plan(yes, "create")
}
func (f *fakeHost) Import(_ context.Context, _ ImportIn, yes bool) (Report, error) {
	return f.plan(yes, "import")
}
func (f *fakeHost) Link(_ context.Context, in LinkIn, yes bool) (Report, error) {
	f.link = in
	if in.DryRun {
		return Report{Lines: []string{"plan link", "  --dry-run: nothing written"}}, nil
	}
	return f.plan(yes, "link")
}
func (f *fakeHost) Unlink(_ context.Context, in UnlinkIn, yes bool) (Report, error) {
	if in.DryRun {
		return Report{Lines: []string{"plan unlink", "  --dry-run: nothing written"}}, nil
	}
	return f.plan(yes, "unlink")
}
func (f *fakeHost) Rename(_ context.Context, in RenameIn) (RenameOut, error) {
	return RenameOut{From: in.Old, To: in.New, Lines: []string{"renamed"}}, nil
}
func (f *fakeHost) Set(context.Context, SetIn) (Report, error) { f.writes++; return Report{}, nil }
func (f *fakeHost) Use(context.Context, UseIn) (Report, error) { f.writes++; return Report{}, nil }

type env struct{ h Host }

func (e env) OrgHost(context.Context) (Host, bool) { return e.h, e.h != nil }

func run(t *testing.T, h Host, name, body string, yes bool) (op.Result, error) {
	t.Helper()
	for _, e := range Ops(env{h}) {
		if e.Spec().Name != name {
			continue
		}
		in, err := e.Decode(op.JSONDecoder{Body: []byte(body)})
		if err != nil {
			return op.Result{}, err
		}
		return e.Invoke(op.WithConfirmed(context.Background(), yes), in)
	}
	t.Fatalf("%s not defined", name)
	return op.Result{}, nil
}

// TestAskingVerbs: create, import and link write nothing unconfirmed and
// answer E_NEEDS_CONFIRM carrying the plan and the question; confirmed
// they write once; link --dry-run never asks and never writes.
func TestAskingVerbs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body string }{
		{OpCreate, `{"org":"agency","store":"agency","key":"human:m@a.example","name":"Mira"}`},
		{OpImport, `{"url":"git@h:agency/notes"}`},
		{OpLink, `{"org":"agency","project":"client"}`},
	} {
		h := &fakeHost{}
		_, err := run(t, h, tc.name, tc.body, false)
		if !errs.Has(err, errs.NeedsConfirm) || !strings.Contains(err.Error(), "plan ") || h.writes != 0 {
			t.Fatalf("%s unconfirmed: %v writes=%d", tc.name, err, h.writes)
		}
		res, err := run(t, h, tc.name, tc.body, true)
		if err != nil || h.writes != 1 || !strings.Contains(text(res), "✓") {
			t.Fatalf("%s confirmed: %v writes=%d %q", tc.name, err, h.writes, text(res))
		}
	}
	h := &fakeHost{}
	res, err := run(t, h, OpLink, `{"org":"agency","project":"client","dry_run":true}`, false)
	if err != nil || h.writes != 0 || !strings.Contains(text(res), "nothing written") {
		t.Fatalf("link --dry-run: %v writes=%d", err, h.writes)
	}
}

// TestRefusalsBeforeHost: the checks the operation makes itself, and the
// org folder default.
func TestRefusalsBeforeHost(t *testing.T) {
	t.Parallel()
	h := &fakeHost{}
	for _, tc := range []struct{ name, body, want string }{
		{OpCreate, `{"org":"a"}`, "--store"},
		{OpLink, `{"org":"a"}`, "--project"},
		{OpSet, `{"org":"a"}`, "--commit"},
		{OpUse, `{"entry":"a","clear":true}`, "not both"},
	} {
		if _, err := run(t, h, tc.name, tc.body, true); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s %s: %v", tc.name, tc.body, err)
		}
	}
	if h.writes != 0 {
		t.Fatalf("a refusal reached the host")
	}
	if _, err := run(t, h, OpCreate, `{"org":"a","store":"s"}`, true); err != nil || h.got.OrgFolder != DefaultOrgFolder {
		t.Fatalf("org folder default: %v %q", err, h.got.OrgFolder)
	}
	bodies := map[string]string{OpCreate: `{"org":"a","store":"s"}`, OpImport: `{"url":"u"}`, OpLink: `{"org":"a","project":"p"}`, OpUnlink: `{"org":"a"}`,
		OpRename: `{"old":"a","new":"b"}`, OpSet: `{"org":"a","commit":true}`, OpUse: `{"entry":"a"}`, OpRefresh: `{"org":"a"}`}
	for _, e := range Ops(env{nil}) {
		in, err := e.Decode(op.JSONDecoder{Body: []byte(bodies[e.Spec().Name])})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Invoke(context.Background(), in); !errs.Has(err, errs.Unsupported) {
			t.Fatalf("%s without a host: %v", e.Spec().Name, err)
		}
	}
}

// text is the result's human text.
func text(r op.Result) string {
	s, _ := r.Text()
	return s
}
