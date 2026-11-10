package stores

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestStoreTextLines (G-CORE-4): each store operation prints one line
// saying what changed and where it was written (CLI_REFERENCE 3.21
// wording), with every variant the output can take.
func TestStoreTextLines(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"init new", InitOut{Name: "agency", URL: "git@h:a/n", ID: "sto_1"}.Text(),
			"✓ created store agency (sto_1) at git@h:a/n; recorded in .lore/stores.yaml; commit it to share\n"},
		{"init already", InitOut{Name: "agency", URL: "git@h:a/n", ID: "sto_1", Already: true}.Text(),
			"✓ git@h:a/n is already a lore store (sto_1); recorded as agency in .lore/stores.yaml; commit it to share\n"},
		{"init personal", InitOut{Name: "me", URL: "file:///h/.lore/stores/me", ID: "sto_2", Personal: true}.Text(),
			"✓ created store me (sto_2) at file:///h/.lore/stores/me; recorded in ~/.lore/config.yaml (personal.store, the target of --to me)\n"},
		{"init personal already", InitOut{Name: "me", URL: "file:///m", ID: "sto_2", Personal: true, Already: true}.Text(),
			"✓ file:///m is already a lore store (sto_2); recorded as me in ~/.lore/config.yaml (personal.store, the target of --to me)\n"},
		{"add git whole", AddOut{Name: "golang", URL: "git@github.com:a/g", Declared: "internal"}.Text(),
			"✓ attached store golang (whole store, git backend, internal) in .lore/stores.yaml; commit it to share\n"},
		{"add folder backend", AddOut{Name: "scratch", URL: "file:///tmp/s", Declared: "internal"}.Text(),
			"✓ attached store scratch (whole store, folder backend, internal) in .lore/stores.yaml; commit it to share\n"},
		{"add folder, branch, read-only, unknown visibility", AddOut{Name: "tips", URL: "https://h/a/n.git", Folder: "tips", Branch: "staging", ReadOnly: true}.Text(),
			"✓ attached store tips (folder tips, git backend, branch staging, read-only) in .lore/stores.yaml; commit it to share\n"},
		{"remove", RemoveOut{Name: "golang", URL: "git@h:a/g"}.Text(),
			"✓ detached store golang from .lore/stores.yaml; its rows stay in the store (`lore store forget` drops this machine's copy); commit the file to share\n"},
		{"remove a folder entry", RemoveOut{Name: "golang/tips", URL: "git@h:a/g", Folder: "tips"}.Text(),
			"✓ detached folder entry golang/tips from .lore/stores.yaml; its rows stay in the store (`lore store forget` drops this machine's copy); commit the file to share\n"},
		{"remove with folder entries", RemoveOut{Name: "golang", URL: "git@h:a/g", Folders: []string{"golang/tips"}}.Text(),
			"✓ detached store golang from .lore/stores.yaml; its rows stay in the store (`lore store forget` drops this machine's copy); commit the file to share\n  folder entries detached with it: golang/tips\n"},
		{"visibility unchanged", VisibilityOut{Name: "kit", Visibility: "internal", Previous: "internal"}.Text(),
			"kit is already internal; nothing changed\n"},
		{"visibility shareable", VisibilityOut{Name: "kit", Visibility: "shareable", Previous: "internal", Changed: true}.Text(),
			"✓ kit is shareable (was internal; lore-store.yaml written)\n"},
		{"visibility back to internal with repos and a note", VisibilityOut{Name: "kit", Visibility: "internal", Previous: "shareable", Changed: true, Repos: []string{"/r/a", "/r/b"}, Note: "x"}.Text(),
			"✓ kit is internal (was shareable; lore-store.yaml written)\n  re-render these repos (`lore render`): their committed files may hold its rows\n    /r/a\n    /r/b\n  note: x\n"},
		{"folder new", FolderAddOut{Store: "golang", Folder: "concurrency", ProjectID: "prj_1"}.Text(),
			"✓ created folder concurrency in golang (prj_1; shown only in repos that attach it)\n"},
		{"folder already", FolderAddOut{Store: "golang", Folder: "concurrency", ProjectID: "prj_1", Already: true}.Text(),
			"golang already has a folder concurrency (prj_1); nothing changed\n"},
		{"set-url changed", SetURLOut{Name: "agency", URL: "git@h:new", Previous: "git@h:old", ID: "sto_1", Changed: true}.Text(),
			"✓ store agency (sto_1) is now at git@h:new (was git@h:old); its rows stay\n"},
		{"set-url with folder entries", SetURLOut{Name: "notes", URL: "file:///n2", Previous: "file:///n", ID: "sto_1", Changed: true, Folders: []string{"notes/golang"}}.Text(),
			"✓ store notes (sto_1) is now at file:///n2 (was file:///n); its rows stay\n  folder entries re-pointed: notes/golang\n"},
		{"set-url repairs folder entries only", SetURLOut{Name: "notes", URL: "file:///n2", Previous: "file:///n2", ID: "sto_1", Changed: true, Folders: []string{"notes/golang"}}.Text(),
			"✓ store notes (sto_1) stays at file:///n2; its folder entries now point there too\n  folder entries re-pointed: notes/golang\n"},
		{"set-url unchanged with note", SetURLOut{Name: "agency", URL: "git@h:new", Note: "old address gone"}.Text(),
			"store agency is already at git@h:new; nothing changed\n  note: old address gone\n"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, c.got, c.want)
		}
	}
}

// TestCheckScheme (G-CORE-4): a scheme no 0.1.18 backend takes is refused
// with E_STORE_SCHEME naming it, before anything opens; every url a
// backend takes, and every form without a scheme (scp, a bare path, a
// v0.1.17 org store given by path), passes to the backends unchanged.
func TestCheckScheme(t *testing.T) {
	for _, u := range []string{"s3://bucket/x", "S3://bucket/x", "postgres://h/db", "lore://srv/x", "gs://b", "ftp://h/x"} {
		err := CheckScheme(u)
		if !errs.Has(err, errs.StoreScheme) {
			t.Errorf("%s: %v, want E_STORE_SCHEME", u, err)
			continue
		}
		var e *errs.Error
		if !errors.As(err, &e) || e.Hint != "nothing written" || !strings.Contains(e.Message, "no backend for") {
			t.Errorf("%s: message %v", u, err)
		}
	}
	for _, u := range []string{"file:///abs/x", "FILE:///abs/x", "ssh://git@h/a/n", "https://h/a/n.git", "http://h/a/n.git",
		"git@github.com:a/n.git", "/abs/path/store", "relative/store", "", "  https://h/a/n  ", "user@host:path:with:colons"} {
		if err := CheckScheme(u); err != nil {
			t.Errorf("%q: %v, want nil", u, err)
		}
	}
}

// statusFake is fakeEnv plus OrgLister, recording the url each status
// read saw (G-CORE-8: the status must be read from the real url).
type statusFake struct {
	fakeEnv
	org   []Entry
	quiet bool
	seen  map[string]string
}

func (s *statusFake) OrgStoreEntries(context.Context) ([]Entry, error) { return s.org, nil }
func (s *statusFake) QuietOutput() bool                                { return s.quiet }
func (s *statusFake) StoreStatus(_ context.Context, e Entry) Status {
	s.seen[e.Name] = e.URL
	if e.URL == redactedURL {
		return Status{Reach: ReachMissing}
	}
	return Status{Visibility: VisibilityInternal, Reach: ReachLocalCopy}
}

// TestListOrgRows (G-CORE-8): store.list adds the org's stores after
// stores.yaml (which wins a name) and before `me`, each with its source;
// in quiet output an org url is redacted only after its status was read
// from the real url, and stores.yaml / personal urls are never redacted.
func TestListOrgRows(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		f := &statusFake{quiet: quiet, seen: map[string]string{},
			fakeEnv: fakeEnv{stores: []Entry{
				{Name: "kit", URL: "file:///s/kit", Source: SourceStoresFile},
				{Name: "agency", URL: "file:///s/mine", Source: SourceStoresFile},
				{Name: "me", URL: "file:///s/me", Personal: true, Source: SourcePersonal},
			}},
			org: []Entry{
				{Name: "agency", URL: "https://h/agency/notes.git", Source: SourceOrg}, // shadowed by stores.yaml
				{Name: "agency/org", URL: "https://h/agency/notes.git", Folder: "org", Source: SourceOrg},
			}}
		raw, _, err := invoke(t, f, OpList, `{}`)
		if err != nil {
			t.Fatal(err)
		}
		order := []string{`"name":"kit"`, `"name":"agency","url":"file:///s/mine"`, `"name":"agency/org"`, `"name":"me"`}
		at := -1
		for _, k := range order {
			i := strings.Index(raw, k)
			if i <= at {
				t.Fatalf("quiet=%v: %s missing or out of order in %s", quiet, k, raw)
			}
			at = i
		}
		if strings.Count(raw, `"name":"agency"`) != 1 {
			t.Fatalf("quiet=%v: the stores.yaml name did not shadow the org one: %s", quiet, raw)
		}
		if f.seen["agency/org"] != "https://h/agency/notes.git" {
			t.Fatalf("quiet=%v: status read from %q, want the real url", quiet, f.seen["agency/org"])
		}
		if !strings.Contains(raw, `"folder":"org","source":"org","visibility":"internal","reach":"local_copy"`) {
			t.Fatalf("quiet=%v: org row status lost: %s", quiet, raw)
		}
		hidden := strings.Contains(raw, `"name":"agency/org","url":"\u003credacted\u003e"`) // JSON escapes < and >
		if hidden != quiet || quiet == strings.Contains(raw, "https://h/agency/notes.git") {
			t.Fatalf("quiet=%v: org url redaction wrong: %s", quiet, raw)
		}
		if !strings.Contains(raw, `"url":"file:///s/kit"`) || !strings.Contains(raw, `"url":"file:///s/me"`) {
			t.Fatalf("quiet=%v: a non-org url was redacted: %s", quiet, raw)
		}
	}
}

// openFake records the OpenIn store.open passed on.
type openFake struct {
	fakeEnv
	got OpenIn
	err error
}

func (o *openFake) OpenClone(_ context.Context, in OpenIn) (OpenOut, error) {
	o.got = in
	return OpenOut{Dir: "/w", Branch: in.Branch, Commit: "c"}, nil
}

func (o *openFake) ListStores(context.Context) ([]Entry, error) { return o.stores, o.err }

// TestStoreNamed (G-CORE-5): store open|forget take a store name this
// repo attaches (resolved to its url; open also to its pinned branch,
// unless --branch is given) or a url (passed as it is); an unknown name,
// or any name where the stores cannot be listed (outside a repo), is
// E_NOT_FOUND and nothing is opened.
func TestStoreNamed(t *testing.T) {
	stores := []Entry{{Name: "kit", URL: "file:///s/kit"}, {Name: "stage", URL: "git@h:a/n", Branch: "staging"}, {Name: "me", URL: "file:///s/me", Personal: true}}
	cases := []struct {
		name, body string
		listErr    error
		url        string
		branch     string
		code       errs.Code
	}{
		{"name", `{"url":"kit"}`, nil, "file:///s/kit", "", ""},
		{"name with pinned branch", `{"url":"stage"}`, nil, "git@h:a/n", "staging", ""},
		{"--branch wins over the pin", `{"url":"stage","branch":"main"}`, nil, "git@h:a/n", "main", ""},
		{"me", `{"url":"me"}`, nil, "file:///s/me", "", ""},
		{"url untouched", `{"url":"git@h:other/x"}`, nil, "git@h:other/x", "", ""},
		{"https url untouched", `{"url":"https://h/x.git"}`, nil, "https://h/x.git", "", ""},
		{"name with spaces", `{"url":"  kit "}`, nil, "file:///s/kit", "", ""},
		{"unknown name", `{"url":"nope"}`, nil, "", "", errs.NotFound},
		{"outside a repo", `{"url":"kit"}`, errs.New(errs.NotAStore, "no repo"), "", "", errs.NotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &openFake{fakeEnv: fakeEnv{stores: stores}, err: c.listErr}
			_, _, err := invoke(t, f, OpOpen, c.body)
			if c.code != "" {
				if !errs.Has(err, c.code) || f.got.URL != "" {
					t.Fatalf("err %v (want %s), opened %+v", err, c.code, f.got)
				}
				return
			}
			if err != nil || f.got.URL != c.url || f.got.Branch != c.branch {
				t.Fatalf("opened %+v err %v, want %s @%q", f.got, err, c.url, c.branch)
			}
		})
	}
}

// TestWithOrgStores: the org rows go after stores.yaml and before every
// personal row (BATCH 15: me stays last); a list without org rows is
// returned as it is.
func TestWithOrgStores(t *testing.T) {
	f := &statusFake{org: nil}
	in := []Entry{{Name: "me", Personal: true}, {Name: "kit"}}
	got, err := withOrgStores(context.Background(), f, in)
	if err != nil || len(got) != 2 || got[0].Name != "me" {
		t.Fatalf("no org rows: %+v %v", got, err)
	}
	f.org = []Entry{{Name: "a", Source: SourceOrg}, {Name: "kit", Source: SourceOrg}}
	got, err = withOrgStores(context.Background(), f, in)
	names := make([]string, len(got))
	for i, e := range got {
		names[i] = e.Name
	}
	if err != nil || strings.Join(names, ",") != "kit,a,me" {
		t.Fatalf("order %v %v, want kit,a,me", names, err)
	}
}
