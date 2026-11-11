package folder

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/storetest"
)

// newStore makes an initialised, empty folder store.
func newStore(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store")
	id, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	if err := Init(dir, rowfs.Marker{ID: id}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fixture opens one store folder, through the Opener so the url path is
// what the contract exercises.
type fixture struct {
	url  string
	open store.Opener
}

func (f fixture) Open(t *testing.T, o store.OpenOptions) store.Backend {
	t.Helper()
	u, err := url.Parse(f.url)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.open(context.Background(), u, o)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fileURL(dir string) string { return fileuri.URL(dir, "") }

func TestContract(t *testing.T) {
	t.Parallel()
	storetest.Run(t, func(t *testing.T) storetest.Fixture {
		return fixture{url: fileURL(newStore(t)), open: NewOpener()}
	})
}

// TestContractOrgFolder runs the contract against the company-wide folder,
// proving a sub-folder Backend is a complete store too.
func TestContractOrgFolder(t *testing.T) {
	t.Parallel()
	storetest.Run(t, func(t *testing.T) storetest.Fixture {
		return fixture{url: fileURL(newStore(t)) + "?" + rowfs.FolderParam + "=" + rowfs.FolderOrg, open: NewOpener()}
	})
}

func TestPathOf(t *testing.T) {
	t.Parallel()
	// want "" = refused (E_STORE_SCHEME). An absolute path is the OS's own:
	// on Windows a drive path, where /srv/store has no drive and is refused.
	good := map[string]string{"file:///srv/store": "/srv/store", "file://localhost/srv/s": "/srv/s", "file:///srv/a/../b": ""}
	if runtime.GOOS == "windows" {
		good = map[string]string{
			"file:///C:/srv/store": `C:\srv\store`, "file://localhost/C:/srv/s": `C:\srv\s`, "file:///C:/srv/a/../b": "",
			// a drive written in the host place is that drive (fileuri.FoldDriveHost)
			"file://C:/srv/store": `C:\srv\store`, "file://c:/srv/s?folder=k": `c:\srv\s`,
			"file:///srv/store": "",
		}
	}
	for raw, want := range good {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		got, err := PathOf(u)
		if want == "" {
			if !errs.Has(err, errs.StoreScheme) {
				t.Errorf("PathOf(%q) = %q, %v; want E_STORE_SCHEME", raw, got, err)
			}
			continue
		}
		if err != nil || got != filepath.FromSlash(want) {
			t.Errorf("PathOf(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, bad := range []string{"file:relative/x", "file://host/srv/x", "file://user@/srv/x", "file://", "git://srv/x"} {
		u, err := url.Parse(bad)
		if err != nil {
			continue // unparseable is refused earlier by store.Openers
		}
		if _, err := PathOf(u); !errs.Has(err, errs.StoreScheme) {
			t.Errorf("PathOf(%q): err = %v, want E_STORE_SCHEME", bad, err)
		}
	}
}

func TestOpenerRefusals(t *testing.T) {
	t.Parallel()
	dir := newStore(t)
	cases := map[string]errs.Code{
		fileURL(dir) + "?token=x":                     errs.StoreScheme,
		fileURL(dir) + "?folder=../x":                 errs.BadPath,
		fileURL(dir) + "?folder=a&folder=b":           errs.StoreScheme,
		fileURL(t.TempDir()):                          errs.NotAStore,
		fileURL(filepath.Join(t.TempDir(), "absent")): errs.NotAStore,
	}
	for raw, code := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewOpener()(context.Background(), u, store.OpenOptions{}); !errs.Has(err, code) {
			t.Errorf("open %q: err = %v, want %s", raw, err, code)
		}
	}
}

func TestInitTwiceRefused(t *testing.T) {
	t.Parallel()
	dir := newStore(t)
	id, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	if err = Init(dir, rowfs.Marker{ID: id}); !errs.Has(err, errs.AlreadyInitialized) {
		t.Fatalf("second Init: %v", err)
	}
	b, err := Open(dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Marker().ID == id || b.Marker().Visibility != rowfs.VisibilityInternal {
		t.Fatalf("marker = %+v", b.Marker())
	}
}

// TestLockTimeout holds the store lock from a second root and checks a
// write gives up with E_STORE_LOCKED after the configured timeout.
func TestLockTimeout(t *testing.T) {
	t.Parallel()
	dir := newStore(t)
	b, err := Open(dir, rowfs.FolderRoot, store.OpenOptions{}, WithLockTimeout(30*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	other, err := rowfs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	held, err := other.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rerr := held.Release(); rerr != nil {
			t.Error(rerr)
		}
	}()
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	stamp := hlc.NewClock(actor, time.Now).Now()
	r, err := store.FromRow(row.Row{Table: "memories", ID: id.Any(), HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: actor, Fields: map[string]value.Value{"n": value.Int(1)}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = b.Write(context.Background(), []store.Change{{Row: r}})
	if !errors.Is(err, store.ErrLocked) {
		t.Fatalf("write under a held lock: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("the lock timeout was not honoured: %v", time.Since(start))
	}
}
