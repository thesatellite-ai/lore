package app

// pickupread_test.go — docs bug B3 (plain files round, 2026-11-12; D-aa):
// a read that picks up a hand edit in a store clone commits it and says
// what it committed, but never pushes it: the commit waits for the next
// write or `lore sync`, and the read says so. It printed "lore: pushed
// store notes: commit … to branch main" from `lore memory list`, for a
// hand edit and for a hand delete alike.

import (
	"os"
	"strings"
	"testing"
)

func TestReadPickUpNeverPushes(t *testing.T) {
	t.Parallel()
	w, repo, clone, bare, memID := pfStoreWorld(t)
	w.mustLore(t, repo, "memory", "add", "--body", "the ledger keeps integer cents", "--to", "notes")
	out := w.mustLore(t, repo, "memory", "list", "--store", "notes", "--json")
	var second string
	for _, id := range pfMemoryID.FindAllString(out, -1) {
		if id != memID {
			second = id
		}
	}
	if second == "" {
		t.Fatalf("setup: no second memory in:\n%s", out)
	}
	remoteTip := func() string { return strings.TrimSpace(w.git(t, bare, "rev-parse", "main")) }

	cases := []struct {
		name string
		hand func()
		read []string
	}{
		{"hand edit", func() {
			f := rowFileOf(t, clone, memID)
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f, []byte(strings.Replace(string(b), "the widget cache warms at boot", "the widget cache warms after the first request", 1)), 0o600); err != nil {
				t.Fatal(err)
			}
		}, []string{"memory", "list", "--store", "notes"}},
		{"hand delete", func() {
			if err := os.Remove(rowFileOf(t, clone, second)); err != nil {
				t.Fatal(err)
			}
		}, []string{"memory", "list"}},
	}
	for _, c := range cases {
		before, head := remoteTip(), strings.TrimSpace(w.git(t, clone, "rev-parse", "HEAD"))
		c.hand()
		code, stdout, errOut := w.lore(t, repo, c.read...)
		if code != 0 {
			t.Fatalf("%s: lore %v: exit %d\n%s\n%s", c.name, c.read, code, stdout, errOut)
		}
		if strings.Contains(errOut, "lore: pushed") {
			t.Fatalf("%s: the read lore %v pushed the pick-up commit:\n%s", c.name, c.read, errOut)
		}
		if now := remoteTip(); now != before {
			t.Fatalf("%s: the read moved the remote %s → %s\n%s", c.name, before, now, errOut)
		}
		if now := strings.TrimSpace(w.git(t, clone, "rev-parse", "HEAD")); now == head {
			t.Fatalf("%s: the read did not commit the hand change (D-aa):\n%s", c.name, errOut)
		}
		if !strings.Contains(errOut, "wait to be pushed; a read never pushes") {
			t.Fatalf("%s: the read does not say the commit waits for `lore sync`:\n%s", c.name, errOut)
		}
	}

	// lore sync sends them
	code, stdout, errOut := w.lore(t, repo, "sync")
	if code != 0 {
		t.Fatalf("sync: exit %d\n%s\n%s", code, stdout, errOut)
	}
	if tip, local := remoteTip(), strings.TrimSpace(w.git(t, clone, "rev-parse", "HEAD")); tip != local || pfLastPush(errOut) == "" {
		t.Fatalf("lore sync did not push the picked-up commits (remote %s, clone %s):\n%s", tip, local, errOut)
	}
}
