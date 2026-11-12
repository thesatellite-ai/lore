package stores

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// fakeEnv is a full Manager (plus every optional upgrade) that records the
// calls each operation made and answers fixed data.
type fakeEnv struct {
	calls   []string
	stores  []Entry
	clones  ClonesOut
	removed string
	// checked is CheckStores' answer (StoreChecker).
	checked []StoreCheck
}

func (f *fakeEnv) CheckStores(context.Context) []StoreCheck { return f.checked }

func (f *fakeEnv) InitStore(_ context.Context, in InitIn) (InitOut, error) {
	f.calls = append(f.calls, "init "+in.Name)
	url := in.URL
	if url == "" {
		url = "file:///home/u/.lore/stores/_me"
	}
	return InitOut{Name: in.Name, URL: url, Personal: in.Name == PersonalName, ID: "sto_01a1a59b891c70b789144cd3e711fb92"}, nil
}

func (f *fakeEnv) AddStore(_ context.Context, in AddIn) (Entry, error) {
	f.calls = append(f.calls, "add "+in.Name)
	return Entry{Name: in.Name, URL: in.URL, Folder: in.Folder, ReadOnly: in.ReadOnly, Branch: in.Branch}, nil
}

func (f *fakeEnv) RemoveStore(_ context.Context, name string) (Entry, error) {
	f.calls, f.removed = append(f.calls, "remove "+name), name
	return Entry{Name: name, URL: "file:///s/" + name}, nil
}

func (f *fakeEnv) ListStores(context.Context) ([]Entry, error) {
	f.calls = append(f.calls, "list")
	return f.stores, nil
}

func (f *fakeEnv) StoreVisibility(_ context.Context, name string) (VisibilityOut, error) {
	return VisibilityOut{Name: name, Visibility: VisibilityInternal, Previous: VisibilityInternal}, nil
}

func (f *fakeEnv) SetVisibility(_ context.Context, in VisibilityIn) (VisibilityOut, error) {
	f.calls = append(f.calls, "visibility "+in.Name)
	return VisibilityOut{Name: in.Name, Visibility: in.Visibility, Previous: VisibilityInternal, Changed: true}, nil
}

func (f *fakeEnv) AddFolder(_ context.Context, in FolderAddIn) (FolderAddOut, error) {
	f.calls = append(f.calls, "folder "+in.Store+"/"+in.Folder)
	return FolderAddOut{Store: in.Store, Folder: in.Folder, ProjectID: "prj_01a1a59b891c70b789144cd3e711fb92"}, nil
}

func (f *fakeEnv) StoreStatus(_ context.Context, e Entry) Status {
	if strings.HasPrefix(e.URL, "file://") {
		return Status{Visibility: VisibilityInternal, Reach: ReachOK}
	}
	return Status{Reach: ReachNoLocalCopy}
}

func (f *fakeEnv) SetStoreURL(_ context.Context, in SetURLIn) (SetURLOut, error) {
	f.calls = append(f.calls, "set-url "+in.Name)
	return SetURLOut{Name: in.Name, URL: in.URL, Previous: "file:///old", ID: "sto_x", Changed: true}, nil
}

func (f *fakeEnv) ShowClones(context.Context, ShowIn) (ClonesOut, error) { return f.clones, nil }
func (f *fakeEnv) DoctorClones(context.Context) (ClonesOut, error)       { return f.clones, nil }

func (f *fakeEnv) OpenClone(_ context.Context, in OpenIn) (OpenOut, error) {
	return OpenOut{Dir: "/w/" + in.URL, Branch: "main", Commit: "0123456789abcdef0123"}, nil
}

func (f *fakeEnv) ForgetClone(_ context.Context, in ForgetIn) (ForgetOut, error) {
	if in.URL == "git@h:none" {
		return ForgetOut{URL: in.URL, Shown: in.URL}, nil
	}
	return ForgetOut{URL: in.URL, Removed: "/c/" + in.URL, Shown: in.URL}, nil
}

// invoke runs the named op on f and returns its JSON and its own text (""
// when the output has no Text). It calls the op directly, so the Runner's
// up-front ConfirmRule (remove's Always) is not in play here; the wire
// tests cover it through App.Run (remove asks itself, G-CORE-4).
func invoke(t *testing.T, f Manager, name, body string) (string, string, error) {
	t.Helper()
	for _, e := range Ops(f) {
		if e.Spec().Name != name {
			continue
		}
		in, err := e.Decode(op.JSONDecoder{Body: []byte(body)})
		if err != nil {
			return "", "", err
		}
		res, err := e.Invoke(context.Background(), in)
		if err != nil {
			return "", "", err
		}
		b, err := res.JSON()
		if err != nil {
			t.Fatal(err)
		}
		txt, _ := res.Text()
		return string(b), txt, nil
	}
	t.Fatalf("no op %s", name)
	return "", "", nil
}

// TestOpsMatrix (t2e-wire 225.2): every store operation over a fake Manager:
// what it calls, its JSON keys, its own text where it has one, and its
// input refusals (which reach the Manager never).
func TestOpsMatrix(t *testing.T) {
	cases := []struct {
		name, op, body string
		call           string   // the Manager call ("" for none)
		json           []string // substrings of the JSON
		text           string   // the exact Text ("" for no Texter)
		code           errs.Code
	}{
		{"init me", OpInit, `{"name":"me"}`, "init me", []string{`"name":"me"`, `"personal":true`, `"id":"sto_`}, "", ""},
		{"init named", OpInit, `{"name":"agency","url":"file:///s/a"}`, "init agency", []string{`"url":"file:///s/a"`}, "", ""},
		{"init named no url", OpInit, `{"name":"agency"}`, "", nil, "", errs.InvalidInput},
		{"init bad visibility", OpInit, `{"name":"me","visibility":"public"}`, "", nil, "", errs.InvalidInput},
		{"init reserved", OpInit, `{"name":"repo","url":"file:///x"}`, "", nil, "", errs.InvalidInput},
		{"add", OpAdd, `{"name":"tips","url":"git@github.com:a/n","folder":"tips","read_only":true,"branch":"staging"}`, "add tips",
			[]string{`"folder":"tips"`, `"read_only":true`, `"branch":"staging"`}, "", ""},
		{"add no url", OpAdd, `{"name":"tips"}`, "", nil, "", errs.InvalidInput},
		{"add me", OpAdd, `{"name":"me","url":"file:///x"}`, "", nil, "", errs.InvalidInput},
		// outside Run nothing is confirmed: remove reads the list, then asks
		// for an attached name and refuses an unknown one (G-CORE-4).
		{"remove asks", OpRemove, `{"name":"kit"}`, "", nil, "", errs.NeedsConfirm},
		{"remove unknown", OpRemove, `{"name":"tips"}`, "", nil, "", errs.NotFound},
		{"remove me", OpRemove, `{"name":"me"}`, "", nil, "", errs.InvalidInput},
		{"list", OpList, `{}`, "list", []string{`"count":2`, `"reach":"ok"`, `"reach":"no_local_copy"`, `"visibility":"internal"`}, "", ""},
		{"visibility shareable asks", OpVisibility, `{"name":"kit","visibility":"shareable"}`, "", nil, "", errs.NeedsConfirm},
		{"visibility back to internal", OpVisibility, `{"name":"kit","visibility":"internal"}`, "", []string{`"changed":false`, `"previous":"internal"`}, "", ""},
		{"folder add", OpFolderAdd, `{"store":"team","folder":"golang"}`, "folder team/golang", []string{`"project_id":"prj_`}, "", ""},
		{"folder add org", OpFolderAdd, `{"store":"kit","folder":"org"}`, "", nil, "", errs.InvalidInput},
		{"set-url", OpSetURL, `{"name":"kit","url":"file:///new"}`, "set-url kit", []string{`"previous":"file:///old"`, `"changed":true`}, "", ""},
		{"set-url no url", OpSetURL, `{"name":"kit"}`, "", nil, "", errs.InvalidInput},
		{"open", OpOpen, `{"url":"git@h:a/n"}`, "", []string{`"commit":"0123456789abcdef0123"`},
			"✓ /w/git@h:a/n on main (0123456789ab): the store's own clone, a plain git checkout to browse; lore commits and pulls here too\n", ""},
		{"forget", OpForget, `{"url":"git@h:u"}`, "", []string{`"removed":"/c/git@h:u"`}, "✓ forgot git@h:u (removed /c/git@h:u; the next command that needs it clones it again)\n", ""},
		{"forget none", OpForget, `{"url":"git@h:none"}`, "", nil, "no clone of git@h:none on this machine; nothing to forget\n", ""},
		// a store name resolves to its stores.yaml url (G-CORE-5); an unknown name is refused
		{"forget by name", OpForget, `{"url":"kit"}`, "", []string{`"removed":"/c/file:///s/kit"`}, "", ""},
		{"forget unknown name", OpForget, `{"url":"nope"}`, "", nil, "", errs.NotFound},
		{"show", OpShow, `{}`, "", []string{`"stores":[{`, `"unpushed":[]`, `"problems":[]`}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeEnv{stores: []Entry{{Name: "kit", URL: "file:///s/kit"}, {Name: "me", URL: "git@h:me/n", Personal: true}},
				clones: ClonesOut{Stores: []CloneView{{URL: "git@h:a/n", Entries: []string{"agency"}, Branches: []string{"main"}, Cloned: true, Clone: "/c/x", DefaultBranch: "main"}}}}
			raw, txt, err := invoke(t, f, c.op, c.body)
			if c.code != "" {
				if !errs.Has(err, c.code) || len(f.calls) > 1 || len(f.calls) == 1 && f.calls[0] != "list" {
					t.Fatalf("err %v (want %s), calls %v", err, c.code, f.calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.call != "" && (len(f.calls) != 1 || f.calls[0] != c.call) {
				t.Fatalf("calls %v, want [%s]", f.calls, c.call)
			}
			for _, k := range c.json {
				if !strings.Contains(raw, k) {
					t.Errorf("json lacks %s: %s", k, raw)
				}
			}
			if c.text != "" && txt != c.text {
				t.Errorf("text %q\nwant %q", txt, c.text)
			}
		})
	}
	// show text: one block per clone; doctor: problems make E_INVALID_INPUT.
	f := &fakeEnv{clones: ClonesOut{Stores: []CloneView{{URL: "git@h:a/n", Entries: []string{"agency", "kit"}, Branches: []string{"main"}, Cloned: true, Clone: "/c/x", DefaultBranch: "main", Unpushed: []string{"main"}}}}}
	_, txt, err := invoke(t, f, OpShow, `{}`)
	want := "git@h:a/n\n  entries   agency, kit   branches main\n  clone     /c/x   default branch main   last fetch never\n  unpushed  main\n"
	if err != nil || txt != want {
		t.Fatalf("show text %q %v\nwant %q", txt, err, want)
	}
	f.clones.Stores[0].Problems = []string{"marker missing"}
	if _, _, err = invoke(t, f, OpDoctor, `{}`); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "1 store(s) need attention: git@h:a/n: marker missing") {
		t.Fatalf("doctor with a problem: %v", err)
	}
	f.clones.Stores[0].Problems = nil
	if _, txt, err = invoke(t, f, OpDoctor, `{}`); err != nil || !strings.Contains(txt, "\n✓ git@h:a/n  (entries agency, kit; branches main)\n") {
		t.Fatalf("doctor clean: %q %v", txt, err)
	}
	if _, _, err = invoke(t, &fakeEnv{}, OpShow, `{}`); err != nil {
		t.Fatalf("show with no clones: %v", err)
	}
	if _, txt, _ = invoke(t, &fakeEnv{}, OpShow, `{}`); txt != "no stores configured (orgs[].stores in ~/.lore/config.yaml)\n" {
		t.Fatalf("empty show text %q", txt)
	}
}
