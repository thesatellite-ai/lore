package cachedb

import (
	"testing"
	"time"
)

// Built is false for a new cache and true once a Rebuild recorded a key;
// Touch is repeatable (age-based Prune went with FLAG3).
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
