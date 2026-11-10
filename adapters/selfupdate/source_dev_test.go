package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRunDevBuildNeverDownloads: a binary stamped with the bare "dev"
// version (an unstamped `go build ./cmd/lore`) is a source build: every
// form of `lore upgrade` prints the source line and asks the release server
// nothing. The release-stamped control run does ask it, which proves the
// counter would see a download.
func TestRunDevBuildNeverDownloads(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.NotFound(w, nil)
	}))
	t.Cleanup(srv.Close)
	updater := func(version string) Updater {
		return Updater{Version: version, Home: t.TempDir(), Dir: t.TempDir(), ReleaseBase: srv.URL,
			VersionProbe: func(context.Context, string) (string, error) {
				t.Fatal("a source build must never probe a new binary")
				return "", nil
			}}
	}
	for name, f := range map[string]Flags{
		"upgrade":              {},
		"upgrade --check":      {Check: true},
		"upgrade --version":    {Version: "0.1.20"},
		"upgrade --ignore-pin": {IgnorePin: true},
	} {
		t.Run(name, func(t *testing.T) {
			hits.Store(0)
			var out strings.Builder
			if err := updater("dev").Run(context.Background(), &out, f); err != nil {
				t.Fatalf("Run: %v", err)
			}
			want := "This " + BinaryName + " (dev) was built from source. Update it with: " + sourceUpdateHint + "\n"
			if out.String() != want {
				t.Fatalf("output = %q, want %q", out.String(), want)
			}
			if n := hits.Load(); n != 0 {
				t.Fatalf("a dev build asked the release server %d times", n)
			}
		})
	}
	t.Run("release-stamped control", func(t *testing.T) {
		hits.Store(0)
		var out strings.Builder
		// The server answers 404, so the run stops before any download.
		_ = updater("0.1.18").Run(context.Background(), &out, Flags{Check: true})
		if hits.Load() == 0 {
			t.Fatal("a release-stamped binary must ask the release server (the counter is blind otherwise)")
		}
	})
}
