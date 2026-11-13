package stores

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// TestCheckName: a store name is snake_case and not reserved (repo, me,
// team, company); `me` passes only where the caller allows the personal
// store (store init me).
func TestCheckName(t *testing.T) {
	for _, c := range []struct {
		name    string
		allowMe bool
		ok      bool
	}{
		{"agency", false, true}, {"acme_hub", false, true}, {"a1", false, true},
		{"me", true, true}, {"me", false, false},
		{"repo", true, false}, {"team", false, false}, {"company", false, false},
		{"", false, false}, {"Agency", false, false}, {"acme-hub", false, false}, {"agency/org", false, false},
		{"../x", false, false}, {"ägency", false, false}, {"a b", false, false}, {strings.Repeat("a", 300), false, false},
	} {
		err := CheckName(c.name, c.allowMe)
		if c.ok != (err == nil) {
			t.Errorf("CheckName(%q, %v) = %v, want ok=%v", c.name, c.allowMe, err, c.ok)
		}
		if err != nil && !errs.Has(err, errs.InvalidInput) {
			t.Errorf("CheckName(%q): code %s, want E_INVALID_INPUT", c.name, errs.CodeOf(err))
		}
	}
}

// BATCH2 #9: store init without a url is E_INVALID_INPUT naming the
// missing url and the usage; only `me` has a default url.
func TestMissingInitURL(t *testing.T) {
	err := MissingInitURL("agency")
	if !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("code %s, want E_INVALID_INPUT", errs.CodeOf(err))
	}
	for _, want := range []string{"store init agency", "url is missing", "lore store init --name <name> --url <url>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err.Error(), want)
		}
	}
}

// TestCheckFolderName: store folder add takes a snake_case name, never
// `org` (the org folder's name), E_INVALID_INPUT otherwise.
func TestCheckFolderName(t *testing.T) {
	for _, c := range []struct {
		name string
		ok   bool
	}{{"concurrency", true}, {"acme_hub", true}, {"org", false}, {"", false}, {"Acme", false}, {"a/b", false}, {"..", false}, {"a-b", false}} {
		err := CheckFolderName(c.name)
		if c.ok != (err == nil) || (err != nil && !errs.Has(err, errs.InvalidInput)) {
			t.Errorf("CheckFolderName(%q) = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// fakeVis is a Manager whose only live calls are the visibility ones.
type fakeVis struct {
	Manager
	cur string
	set []string
}

func (f *fakeVis) StoreVisibility(_ context.Context, name string) (VisibilityOut, error) {
	return VisibilityOut{Name: name, Visibility: f.cur, Previous: f.cur}, nil
}

func (f *fakeVis) SetVisibility(_ context.Context, in VisibilityIn) (VisibilityOut, error) {
	f.set = append(f.set, in.Visibility)
	return VisibilityOut{Name: in.Name, Visibility: in.Visibility, Previous: f.cur, Changed: true}, nil
}

// TestVisibilityConfirm (CFG4): shareable asks first (E_NEEDS_CONFIRM,
// nothing written) outside a confirmed run; internal never asks; the same
// visibility writes nothing; a bad value or name is E_INVALID_INPUT.
func TestVisibilityConfirm(t *testing.T) {
	var vis op.Entry
	run := func(m Manager, body string) error {
		for _, e := range Ops(m) {
			if e.Spec().Name == OpVisibility {
				vis = e
			}
		}
		in, err := vis.Decode(op.JSONDecoder{Body: []byte(body)})
		if err != nil {
			return err
		}
		_, err = vis.Invoke(context.Background(), in)
		return err
	}
	f := &fakeVis{cur: VisibilityInternal}
	if err := run(f, `{"name":"kit","visibility":"shareable"}`); !errs.Has(err, errs.NeedsConfirm) || len(f.set) != 0 {
		t.Fatalf("shareable: %v, writes %v", err, f.set)
	}
	if err := run(f, `{"name":"kit","visibility":"internal"}`); err != nil || len(f.set) != 0 {
		t.Fatalf("same visibility: %v, writes %v", err, f.set)
	}
	f = &fakeVis{cur: VisibilityShareable}
	if err := run(f, `{"name":"kit","visibility":"internal"}`); err != nil || len(f.set) != 1 {
		t.Fatalf("internal: %v, writes %v", err, f.set)
	}
	for _, body := range []string{`{"name":"kit","visibility":"public"}`, `{"name":"repo","visibility":"internal"}`} {
		if err := run(f, body); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: %v, want E_INVALID_INPUT", body, err)
		}
	}
}

// fakeRepos adds RepoUser to fakeVis.
type fakeRepos struct {
	*fakeVis
	repos []string
}

func (f fakeRepos) ReposUsingStore(context.Context, string) ([]string, error) { return f.repos, nil }

// TestVisibilityInternalListsRepos (CLI_REFERENCE 3.21): shareable →
// internal lists the repos that attach the store; an unchanged
// visibility lists none (to shareable asks first: TestVisibilityConfirm).
func TestVisibilityInternalListsRepos(t *testing.T) {
	run := func(m Manager, body string) string {
		for _, e := range Ops(m) {
			if e.Spec().Name != OpVisibility {
				continue
			}
			in, err := e.Decode(op.JSONDecoder{Body: []byte(body)})
			if err != nil {
				t.Fatal(err)
			}
			res, err := e.Invoke(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			b, err := res.JSON()
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		t.Fatal("no store.visibility op")
		return ""
	}
	repos := []string{"/w/client_a", "/w/own"}
	if got := run(fakeRepos{&fakeVis{cur: VisibilityShareable}, repos}, `{"name":"kit","visibility":"internal"}`); !strings.Contains(got, `"repos_holding_rows":["/w/client_a","/w/own"]`) {
		t.Errorf("back to internal: %s", got)
	}
	if got := run(fakeRepos{&fakeVis{cur: VisibilityInternal}, repos}, `{"name":"kit","visibility":"internal"}`); strings.Contains(got, "repos_holding_rows") {
		t.Errorf("unchanged lists repos: %s", got)
	}
}
