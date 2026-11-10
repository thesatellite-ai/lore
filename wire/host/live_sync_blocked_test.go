package host

// live_sync_blocked_test.go — G-SYNC-3: while the remote live ref was
// deleted or rewritten (session blocked), `lore sync` and `lore land`
// refuse with the same E_READ_ONLY as every live write, naming the local
// backup and `lore live restore`; sync never answers "· already in sync"
// then (it used to, silently).

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// TestLiveSyncRefusesWhileRewritten: matrix over the verb (sync, land
// --all, land <id>, land --dry-run) with the session blocked, with and
// without a backup ref named in the cause; and unblocked sync answers.
func TestLiveSyncRefusesWhileRewritten(t *testing.T) {
	backup := live.RefBackupPrefix + "20261106T000000"
	causes := []struct {
		name, want string
		err        error
	}{
		{"backup named", backup, fmt.Errorf("%w (%s%s)", live.ErrRemoteRewritten, backupKeptAs, backup)},
		{"no backup named", live.RefBackupPrefix, live.ErrRemoteRewritten},
	}
	verbs := []struct {
		name string
		run  func(l liveSync, f ltFixture) error
	}{
		{"sync", func(l liveSync, f ltFixture) error { _, err := l.SyncRun(f.ctx, opssync.RunIn{}); return err }},
		{"land --all", func(l liveSync, f ltFixture) error { _, err := l.Land(f.ctx, opssync.LandIn{All: true}); return err }},
		{"land id", func(l liveSync, f ltFixture) error { _, err := l.Land(f.ctx, opssync.LandIn{ID: ltTaskA}); return err }},
	}
	for _, c := range causes {
		for _, v := range verbs {
			t.Run(c.name+"/"+v.name, func(t *testing.T) {
				f := newLiveFixture(t)
				f.s.blocked = c.err
				err := v.run(liveSync{h: f.s.h}, f)
				if !errs.Has(err, errs.ReadOnly) {
					t.Fatalf("err = %v, want E_READ_ONLY", err)
				}
				var e *errs.Error
				msg := err.Error()
				if !strings.Contains(msg, "deleted or rewritten") || !strings.Contains(msg, c.want) {
					t.Fatalf("message %q lacks the reason or %q", msg, c.want)
				}
				if !errors.As(err, &e) || !strings.Contains(e.Hint, "lore live restore") {
					t.Fatalf("hint of %v", err)
				}
			})
		}
	}
	t.Run("not blocked: sync answers", func(t *testing.T) {
		f := newLiveFixture(t)
		if _, err := (liveSync{h: f.s.h}).SyncRun(f.ctx, opssync.RunIn{}); err != nil {
			t.Fatal(err)
		}
	})
}
