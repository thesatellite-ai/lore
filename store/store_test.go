package store_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

func TestOpeners(t *testing.T) {
	t.Parallel()
	o := store.NewOpeners()
	d := memstore.NewData()
	if err := o.Register(memstore.Scheme, memstore.Opener(d)); err != nil {
		t.Fatal(err)
	}
	if err := o.Register(memstore.Scheme, memstore.Opener(d)); err == nil {
		t.Fatal("duplicate scheme accepted")
	}
	if got := o.Schemes(); len(got) != 1 || got[0] != memstore.Scheme {
		t.Fatalf("schemes %v", got)
	}
	b, err := o.Open(context.Background(), "mem://x", store.OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Write(context.Background(), nil); !errors.Is(err, store.ErrReadOnly) {
		t.Fatalf("read-only option lost: %v", err)
	}
	for _, u := range []string{"s3://bucket/x", "no-scheme", "https://tok:" + "secret@host/x"} {
		_, err := o.Open(context.Background(), u, store.OpenOptions{})
		if !errs.Has(err, errs.StoreScheme) {
			t.Errorf("%q: err = %v", u, err)
		}
		if err != nil && containsSecret(err.Error()) {
			t.Errorf("%q: error repeats the url credentials", u)
		}
	}
}

func containsSecret(s string) bool {
	for i := 0; i+len("secret") <= len(s); i++ {
		if s[i:i+len("secret")] == "secret" {
			return true
		}
	}
	return false
}

func TestFromRowAndDecode(t *testing.T) {
	t.Parallel()
	doc := `{"_v":2,"_table":"memories","id":"mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00","_hlc":"2026-11-04T10:42:07.123Z/0000/","_deleted":true}`
	r, err := row.Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if sr.Table != r.Table || sr.ID != r.ID || sr.HLC != r.HLC || !sr.Deleted || sr.Key() != (store.Key{Table: r.Table, ID: r.ID}) {
		t.Fatalf("header %+v", sr)
	}
	back, err := sr.Decode()
	if err != nil || back.ID != r.ID || !back.Deleted {
		t.Fatalf("decode %+v %v", back, err)
	}
	if !errs.Has(store.ErrStale, errs.Stale) || errs.CodeOf(store.ErrNotFound) != errs.NotFound {
		t.Fatal("sentinel codes")
	}
}

// TestOpenersDrivePath (B3-27): a Windows drive path, in either separator,
// opens through the PathScheme opener as the url path "/C:/…", never as a
// url with scheme "c".
func TestOpenersDrivePath(t *testing.T) {
	t.Parallel()
	o := store.NewOpeners()
	var got []string
	if err := o.Register(store.PathScheme, func(_ context.Context, u *url.URL, _ store.OpenOptions) (store.Backend, error) {
		got = append(got, u.Path)
		return nil, errs.New(errs.NotFound, "test opener")
	}); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{`C:\Users\nina\notes.git`, `C:/Users/nina/notes.git`} {
		if _, err := o.Open(context.Background(), in, store.OpenOptions{}); !errs.Has(err, errs.NotFound) {
			t.Fatalf("Open(%q) = %v, want the path opener's error", in, err)
		}
	}
	if len(got) != 2 || got[0] != "/C:/Users/nina/notes.git" || got[1] != got[0] {
		t.Fatalf("the path opener saw %q", got)
	}
}
