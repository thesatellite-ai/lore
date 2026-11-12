package wire

// storemgr_pushstamp_test.go — withStoreDir (`lore store visibility`,
// `lore store set` on an org store, `lore store folder add`) commits and
// pushes the store's own files; when that commit goes out it is this
// clone's last push, so push_stamp is written with the app's clock and
// `lore store show` reports it (land2 2026-11-09: the stamp was missing
// and store show kept naming an older push; only a docs transcript
// locked it until this test).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

func TestWithStoreDirWritesPushStamp(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	url, bare := bareStore(t, hosts, "acme/stamp")
	at := time.Date(2026, 11, 9, 10, 1, 5, 0, time.UTC)
	a := openWith(t, gitRepo(t), Options{Wall: func() time.Time { return at }})
	if _, err := run(t, a, opsstores.OpInit, `{"name":"stamp","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	ctx := context.Background()
	src, err := a.findStore(ctx, "test", "stamp")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := a.storeDir(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	stampDir := gitbackend.StateDirOf(dir)
	// whatever init recorded is gone: only withStoreDir may write it now
	if err = os.Remove(filepath.Join(stampDir, pushStampFile)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	before := gitIn(t, bare, nil, "rev-parse", "main")
	at = at.Add(time.Minute)
	out, err := run(t, a, opsstores.OpVisibility, `{"name":"stamp","visibility":"shareable"}`, true)
	if err != nil {
		t.Fatalf("visibility: %v", err)
	}
	if !strings.Contains(out, `"changed":true`) {
		t.Fatalf("visibility changed nothing: %s", out)
	}
	if after := gitIn(t, bare, nil, "rev-parse", "main"); after == before {
		t.Fatalf("the visibility commit never reached the remote (main still %s)", strings.TrimSpace(after))
	}
	if got := readStamp(stampDir, pushStampFile); !got.Equal(at) {
		t.Fatalf("push_stamp after the pushed commit = %v, want the app clock %v", got, at)
	}
	// the same value commits nothing: no push, the stamp stays
	at = at.Add(time.Minute)
	if _, err = run(t, a, opsstores.OpVisibility, `{"name":"stamp","visibility":"shareable"}`, true); err != nil {
		t.Fatalf("visibility again: %v", err)
	}
	if got := readStamp(stampDir, pushStampFile); !got.Equal(at.Add(-time.Minute)) {
		t.Fatalf("push_stamp moved without a push: %v", got)
	}
}
