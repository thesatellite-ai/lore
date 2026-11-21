//go:build journeys && !windows

package journeys

// upgrade_stamps_test.go — devco:D-865 (public issue #95): each folder
// cache has an open stamp beside it (<cache>.stamp) that lets a read skip
// parsing the cache's whole schema. The stamp names the binary that wrote
// it, and a read never wrote one, so after an upgrade or reinstall (a new
// file at the same path) no stamp was current and every read paid the full
// open of every folder cache until each folder got a write. Folders
// nothing changes stayed slow for good. Now the first read after the
// upgrade stamps each unchanged cache for the new binary.

import (
	"encoding/json"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestJourneyReadAfterUpgradeStampsCaches(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	// lore installed at one path of this machine, replaced on upgrade
	bin := filepath.Join(t.TempDir(), "bin", "lore")
	install := func(at time.Time) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(bin)
		src, err := os.Open(loreBin)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = src.Close() }()
		dst, err := os.OpenFile(bin, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(dst, src); err != nil {
			t.Fatal(err)
		}
		if err := dst.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(bin, at, at); err != nil {
			t.Fatal(err)
		}
	}
	install(time.Now().Add(-time.Hour))
	w.Bin = bin

	uma := w.Persona("Uma Reyes", "uma@acme-co.example")
	lap := uma.Laptop()
	me := w.Remote("github.com/uma-reyes/lore-me")
	folders := []string{"work", "archive"}

	// stamps maps each cache stamp under ~/.lore/cache to the binary it
	// names and its raw bytes.
	stamps := func() (map[string]string, map[string]string) {
		t.Helper()
		exe, raw := map[string]string{}, map[string]string{}
		err := filepath.WalkDir(lap.LorePath("cache"), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".db.stamp") {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			var st struct {
				Exe string `json:"exe"`
			}
			if err := json.Unmarshal(b, &st); err != nil {
				t.Fatalf("stamp %s does not decode: %v", p, err)
			}
			exe[p], raw[p] = st.Exe, string(b)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return exe, raw
	}
	// installed names bin the way lore's open stamp does: path|size|mtime
	installed := func() string {
		t.Helper()
		real, err := filepath.EvalSymlinks(bin)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(real)
		if err != nil {
			t.Fatal(err)
		}
		return real + "|" + strconv.FormatInt(fi.Size(), 10) + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
	}

	w.Step("day1/notes-in-two-folders", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		for _, f := range folders {
			lap.Lore(lap.Home, "store", "folder", "add", "me", f).OK(t)
			lap.Lore(lap.Home, "memory", "add", "--body", "Dana reviews the deploy checklist on Mondays ("+f+").", "--to", "me/"+f, "--yes").OK(t)
		}
		exe, _ := stamps()
		if len(exe) < len(folders) {
			t.Fatalf("want a stamp per folder cache after the writes, have %d", len(exe))
		}
		// Days pass: the row files are old now. A read of files written
		// inside the racy window (rowfs.RacyWindow, 2 s) commits to the
		// cache and so stamps it as any write does, which a real upgrade,
		// days after the last write, never gets. One read settles the
		// caches on the old times; the next one changes no stamp.
		old := time.Now().Add(-48 * time.Hour)
		err := filepath.WalkDir(lap.LorePath("stores"), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.Name() == ".git" {
				if err == nil {
					return filepath.SkipDir
				}
				return err
			}
			if d.Type().IsRegular() {
				return os.Chtimes(p, old, old)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		lap.Lore(lap.Home, "ask", "--store", "me", "--query", "who reviews the deploy checklist").OK(t)
		_, settled := stamps()
		lap.Lore(lap.Home, "ask", "--store", "me", "--query", "who reviews the deploy checklist").OK(t)
		if _, again := stamps(); !maps.Equal(settled, again) {
			t.Fatal("a read of unchanged old files rewrote a current stamp")
		}
	})

	w.Step("day2/upgrade-then-read", func() {
		before := installed()
		install(time.Now())
		now := installed()
		if now == before {
			t.Fatal("the reinstall did not change the binary's stamp name")
		}
		exe, _ := stamps()
		for p, e := range exe {
			if e == now {
				t.Fatalf("stamp %s already names the new binary before any run", p)
			}
		}
		lap.Lore(lap.Home, "ask", "--store", "me", "--query", "who reviews the deploy checklist").OK(t).Says(t, "Dana reviews the deploy checklist")
		exe, raw := stamps()
		for p, e := range exe {
			if e != now {
				t.Errorf("after the first read since the upgrade, stamp %s still names %q, not the installed lore: every later read pays the full cache open until a write", filepath.Base(p), e)
			}
		}
		// the next read finds them current and rewrites none
		lap.Lore(lap.Home, "ask", "--store", "me", "--query", "who reviews the deploy checklist").OK(t).Says(t, "Dana reviews the deploy checklist")
		if _, again := stamps(); len(again) != len(raw) {
			t.Errorf("the second read changed the set of stamps: %d -> %d", len(raw), len(again))
		} else {
			for p, b := range again {
				if raw[p] != b {
					t.Errorf("the second read rewrote the current stamp %s", filepath.Base(p))
				}
			}
		}
	})
}
