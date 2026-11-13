package signals

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/clock"
)

// TestViewSavesOnlyWhatChanged (S-2): a reader records its observations
// only when they changed. A clone with no claims and no observation file
// (a repo with no lore data, where `lore task list` reads the board) gets
// no file at all; a board that drops a claim the file still holds is
// saved.
func TestViewSavesOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lore", "signals-seen.json")
	s := &Store{Ref: CodeRepoRef, Seen: SeenStore{Path: path}, Clock: clock.NewFake(t0)}

	s.view(ctx, map[string][]byte{})
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("an empty board with no observations wrote %s (err %v)", filepath.Dir(path), err)
	}

	held := Observations{"_root/tsk_1": {Token: "ct_a_1", Seq: 2, Since: t0}}
	if err := s.Seen.Save(ctx, CodeRepoRef, held); err != nil {
		t.Fatal(err)
	}
	s.view(ctx, map[string][]byte{})
	if got := s.Seen.Load(ctx, CodeRepoRef); len(got) != 0 {
		t.Fatalf("the claim left the board but the file still holds it: %v", got)
	}
}

// TestObservationsSame compares paths, tokens, sequences and instants.
func TestObservationsSame(t *testing.T) {
	t.Parallel()
	a := Observations{"p": {Token: "x", Seq: 1, Since: t0}}
	for _, c := range []struct {
		other Observations
		want  bool
	}{
		{Observations{"p": {Token: "x", Seq: 1, Since: t0.In(time.FixedZone("z", 3600))}}, true},
		{Observations{"p": {Token: "y", Seq: 1, Since: t0}}, false},
		{Observations{"p": {Token: "x", Seq: 2, Since: t0}}, false},
		{Observations{"p": {Token: "x", Seq: 1, Since: t0.Add(time.Second)}}, false},
		{Observations{"q": {Token: "x", Seq: 1, Since: t0}}, false},
		{Observations{}, false},
	} {
		if got := a.Same(c.other); got != c.want {
			t.Errorf("Same(%v) = %v, want %v", c.other, got, c.want)
		}
	}
	if !(Observations{}).Same(nil) {
		t.Error("an empty set differs from a nil one")
	}
}
