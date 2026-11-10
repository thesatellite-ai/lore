package attach

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

const sample = `
stores:
  - name: agency
    url: git@github.com:agency/lore-company
    folder: acme-hub
  - name: agency_ui
    url: mem://ui
    read_only: true
`

func TestParse(t *testing.T) {
	t.Parallel()
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Stores) != 2 || c.Stores[0].Folder != "acme-hub" || !c.Stores[1].ReadOnly || c.Stores[0].Name != "agency" {
		t.Fatalf("config = %+v", c)
	}
	empty, err := Parse(nil)
	if err != nil || len(empty.Stores) != 0 {
		t.Fatalf("empty file: %+v %v", empty, err)
	}
}

func TestParseRefuses(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"reserved":    "stores:\n  - name: me\n    url: mem://x\n",
		"reserved 2":  "stores:\n  - name: company\n    url: mem://x\n",
		"not snake":   "stores:\n  - name: Agency-UI\n    url: mem://x\n",
		"duplicate":   "stores:\n  - name: a\n    url: mem://x\n  - name: a\n    url: mem://y\n",
		"no url":      "stores:\n  - name: a\n",
		"unknown key": "stores:\n  - name: a\n    url: mem://x\n    readonly: true\n",
		"not yaml":    "stores: [",
	}
	for name, doc := range cases {
		if _, err := Parse([]byte(doc)); !errs.Has(err, errs.ConfigInvalid) {
			t.Errorf("%s: err = %v, want E_CONFIG_INVALID", name, err)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	t.Parallel()
	got, err := NormalizeURL("git@github.com:agency/lore-company")
	if err != nil || got != "ssh://git@github.com/agency/lore-company" {
		t.Fatalf("scp form: %q %v", got, err)
	}
	secretURL := "https://bob:" + strings.Repeat("t", 12) + "@github.com/agency/x"
	_, err = NormalizeURL(secretURL)
	if !errs.Has(err, errs.URLUserinfo) || strings.Contains(err.Error(), strings.Repeat("t", 12)) {
		t.Fatalf("credential url: %v", err)
	}
	if _, err := NormalizeURL("no scheme here"); !errs.Has(err, errs.StoreScheme) {
		t.Fatalf("no scheme: %v", err)
	}
}

func TestOpen(t *testing.T) {
	t.Parallel()
	openers := store.NewOpeners()
	data := memstore.NewData()
	if err := openers.Register(memstore.Scheme, memstore.Opener(data)); err != nil {
		t.Fatal(err)
	}
	c, err := Parse([]byte("stores:\n  - name: scratch\n    url: mem://x\n    read_only: true\n    folder: f\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(context.Background(), c, openers, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].ReadOnly || got[0].Folder != "f" || got[0].Name != "scratch" {
		t.Fatalf("opened = %+v", got)
	}
	if err = got[0].Backend.Write(context.Background(), nil); err == nil {
		t.Fatal("read_only did not reach the backend")
	}

	closed := &closeRecorder{}
	if err = openers.Register("rec", func(context.Context, *url.URL, store.OpenOptions) (store.Backend, error) {
		return closed, nil
	}); err != nil {
		t.Fatal(err)
	}
	c, err = Parse([]byte("stores:\n  - name: first\n    url: rec://a\n  - name: second\n    url: s3://bucket\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), c, openers, store.OpenOptions{}); !errs.Has(err, errs.StoreScheme) {
		t.Fatalf("unknown scheme: %v", err)
	}
	if !closed.closed {
		t.Fatal("a store opened before the failure was left open")
	}
}

// closeRecorder is a Backend that records Close.
type closeRecorder struct {
	store.Backend
	closed bool
}

func (c *closeRecorder) Close() error { c.closed = true; return nil }
