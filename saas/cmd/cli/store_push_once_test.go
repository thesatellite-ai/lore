package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestStorePushNoteOnce (FLAG4): one command that writes twice to a store
// whose push fails (the actor row, then the row) prints the "not pushed
// yet" note once and does not retry the push; `lore sync` pushes both.
func TestStorePushNoteOnce(t *testing.T) {
	f := &fakeStoreBackend{caps: store.Capabilities{Remote: true}, syncErr: errors.New("boom")}
	var notes bytes.Buffer
	b := &storePushBackend{inner: f, name: "agency", autopush: func(context.Context) bool { return true }, notes: &notes}
	for range 2 {
		if err := b.Write(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if f.writes != 2 || f.syncs != 1 {
		t.Fatalf("writes=%d syncs=%d, want 2/1", f.writes, f.syncs)
	}
	if n := strings.Count(notes.String(), "pushing failed"); n != 1 {
		t.Fatalf("note printed %d times:\n%s", n, notes.String())
	}
}
