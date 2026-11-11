package osrename

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// errBusy stands for a transient Windows refusal; errOther for a real error.
var (
	errBusy  = errors.New("busy")
	errOther = errors.New("other")
)

// fakeClock is a clock that sleep advances.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) sleep(d time.Duration)   { c.t = c.t.Add(d) }
func isBusy(err error) bool                  { return errors.Is(err, errBusy) }
func failing(n int, last error) func() error { return seq(n, errBusy, last) }

// seq fails n times with first, then returns last.
func seq(n int, first, last error) func() error {
	calls := 0
	return func() error {
		calls++
		if calls <= n {
			return first
		}
		return last
	}
}

// TestRetry: a transient error is retried until the op succeeds; a real
// error returns at once; a hold that outlasts the window returns the
// transient error after a bounded number of attempts.
func TestRetry(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		op      func() error
		want    error
		maxWait time.Duration
	}{
		{"ok first", failing(0, nil), nil, 0},
		{"busy twice then ok", failing(2, nil), nil, retryWindow},
		{"busy then real error", failing(1, errOther), errOther, retryWindow},
		{"real error at once", seq(1, errOther, nil), errOther, 0},
		{"busy forever", failing(1<<30, nil), errBusy, retryWindow + maxPause},
	} {
		clk := &fakeClock{t: time.Unix(0, 0)}
		err := retry(c.op, isBusy, clk.sleep, clk.now)
		if !errors.Is(err, c.want) || (c.want == nil && err != nil) {
			t.Errorf("%s: err %v, want %v", c.name, err, c.want)
		}
		if waited := clk.t.Sub(time.Unix(0, 0)); waited > c.maxWait {
			t.Errorf("%s: waited %v, want at most %v", c.name, waited, c.maxWait)
		}
	}
}

// TestRename replaces an existing file on this system.
func TestRename(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	from, to := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for p, s := range map[string]string{from: "new", to: "old"} {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Rename(from, to); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Clean(to)); err != nil || string(b) != "new" {
		t.Fatalf("%q %v", b, err)
	}
	if err := Rename(from, to); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source: %v", err)
	}
}

// TestReadFileReadsAndPassesNotExist: ReadFile returns the bytes, and a
// missing file's error unchanged (not transient: no retry).
func TestReadFileReadsAndPassesNotExist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("placeholder: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadFile(p); err != nil || string(b) != "placeholder: 1\n" {
		t.Fatalf("ReadFile = %q, %v", b, err)
	}
	start := time.Now()
	if _, err := ReadFile(p + ".gone"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	if time.Since(start) > retryWindow/2 {
		t.Fatal("a missing file was retried")
	}
}
