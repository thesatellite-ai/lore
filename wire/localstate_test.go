package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/store/localstate"
)

// TestLocalStatePath (later 53): the machine-local file is
// <home>/state/local.db, App.LocalState opens it there (creating the
// folder), and a value written through one handle is read by the next.
func TestLocalStatePath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if got, want := LocalStatePath(home), filepath.Join(home, "state", "local.db"); got != want {
		t.Fatalf("LocalStatePath = %q, want %q", got, want)
	}
	a := openWith(t, gitRepo(t), Options{Home: home})
	ctx := background()
	s, err := a.LocalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put(ctx, localstate.SecretChecks, "vault/x", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(LocalStatePath(home)); err != nil {
		t.Fatalf("the file is not at LocalStatePath: %v", err)
	}
	s, err = a.LocalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if e, ok, err := s.Get(ctx, localstate.SecretChecks, "vault/x"); err != nil || !ok || string(e.Doc) != `{"ok":true}` {
		t.Fatalf("second handle: %+v %v %v", e, ok, err)
	}
}
