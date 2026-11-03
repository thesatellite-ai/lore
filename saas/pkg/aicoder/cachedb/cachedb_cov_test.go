package cachedb

import (
	"os"
	"testing"
	"time"
)

// Built is false for a new cache and true once a Rebuild recorded a key;
// Touch moves last_used so `lore cache prune` keeps a cache in use.
func TestBuiltAndTouch(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir, "prj_x")
	d, err := Open(ctx(), path)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := d.Built(ctx()); err != nil || ok {
		t.Fatalf("a new cache is not built: %v %v", ok, err)
	}
	if err := d.Rebuild(ctx(), Key{Entry: "agency"}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.Built(ctx()); err != nil || !ok {
		t.Fatalf("an empty rebuild still counts as built: %v %v", ok, err)
	}
	later := t0.Add(30 * 24 * time.Hour)
	if err := d.Touch(ctx(), later); err != nil {
		t.Fatal(err)
	}
	if err := d.Touch(ctx(), later); err != nil {
		t.Fatalf("Touch is repeatable: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	render := t.TempDir()
	// Idle 30 days since the rebuild, but used just now: kept.
	res, err := Prune(ctx(), dir, render, later.Add(time.Hour), 7*24*time.Hour)
	if err != nil || res.Kept != 1 || len(res.Removed) != 0 {
		t.Fatalf("a touched cache is kept: %+v %v", res, err)
	}
	res, err = Prune(ctx(), dir, render, later.Add(8*24*time.Hour), 7*24*time.Hour)
	if err != nil || len(res.Removed) != 1 || res.Removed[0] != "prj_x" {
		t.Fatalf("idle past max: removed: %+v %v", res, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the pruned cache file is gone: %v", err)
	}
}

// Touch on a cache that was never built does not make it "built".
func TestTouchDoesNotMarkBuilt(t *testing.T) {
	d, err := Open(ctx(), Path(t.TempDir(), "prj_y"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Touch(ctx(), t0); err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.Built(ctx()); ok {
		t.Fatal("Touch alone must not mark the cache built")
	}
	if cur, _ := d.Current(ctx(), Key{Entry: "agency"}); cur {
		t.Fatal("a touched, never-built cache is not current")
	}
}
