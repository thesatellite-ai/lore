package memory_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// keyed decodes a memory add result.
func keyed(t *testing.T, h *opstest.H, input string) memory.WriteOut {
	t.Helper()
	var w memory.WriteOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(memory.OpAdd, input))), &w); err != nil {
		t.Fatal(err)
	}
	return w
}

// liveMemories counts the live memories of the repo store.
func liveMemories(t *testing.T, h *opstest.H) int {
	t.Helper()
	rows, err := h.App.List(context.Background(), store.SourceRepo, schema.TableMemories)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

// TestAddKeyIdempotent: memory add --key (CLEANUP Qs). The same key twice
// returns the first memory's id with existing:true and stores one row
// (even with another body: the retry is the same add); keys are trimmed;
// a unicode key works; the same key in another project is another memory;
// a different key is a new memory.
func TestAddKeyIdempotent(t *testing.T) {
	h := opstest.New(t)
	first := keyed(t, h, `{"body":"deploy steps","key":"run-42"}`)
	if first.Existing || first.ID == "" {
		t.Fatalf("first keyed add: %+v", first)
	}
	again := keyed(t, h, `{"body":"deploy steps (retry)","key":" run-42 "}`)
	if !again.Existing || again.ID != first.ID {
		t.Fatalf("retry: %+v, want existing %s", again, first.ID)
	}
	if !strings.Contains(opstest.Text(h.Must(memory.OpAdd, `{"body":"x","key":"run-42"}`)), "nothing written") {
		t.Fatal("the retry's text does not say nothing was written")
	}
	if n := liveMemories(t, h); n != 1 {
		t.Fatalf("%d memories after three keyed adds, want 1", n)
	}
	r := h.JSON(h.Must(memory.OpShow, `{"id":"`+first.ID+`"}`))
	if !strings.Contains(r, "deploy steps") || strings.Contains(r, "retry") {
		t.Fatalf("the retry changed the row: %s", r)
	}

	if u := keyed(t, h, `{"body":"ü","key":"ключ-🔑"}`); u.Existing || u.ID == first.ID {
		t.Fatalf("unicode key: %+v", u)
	}
	if u := keyed(t, h, `{"body":"ü","key":"ключ-🔑"}`); !u.Existing {
		t.Fatalf("unicode key retry: %+v", u)
	}
	if other := keyed(t, h, `{"body":"deploy steps","key":"run-43"}`); other.Existing || other.ID == first.ID {
		t.Fatalf("another key: %+v", other)
	}

	// a second project in the repo store: the same key is another memory
	name, err := value.TextOf("second")
	if err != nil {
		t.Fatal(err)
	}
	w, err := h.App.Create(context.Background(), store.SourceRepo, schema.TableProjects, map[string]value.Value{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	p2 := w.Row.ID.String()
	inP2 := keyed(t, h, `{"body":"deploy steps","key":"run-42","project":"`+p2+`"}`)
	if inP2.Existing || inP2.ID == first.ID {
		t.Fatalf("same key in another project: %+v", inP2)
	}
	if back := keyed(t, h, `{"body":"x","key":"run-42","project":"`+h.Project+`"}`); !back.Existing || back.ID != first.ID {
		t.Fatalf("first project again: %+v", back)
	}
}

// TestAddKeyRefusals: a key that is empty after trimming, holds a line
// break or is longer than 200 characters is E_INVALID_INPUT and writes
// nothing; exactly 200 is accepted. A memory deleted after a keyed add
// is never re-added silently under the same key.
func TestAddKeyRefusals(t *testing.T) {
	h := opstest.New(t)
	long := strings.Repeat("k", 201)
	for _, k := range []string{"   ", `a\nb`, `a\rb`, long, strings.Repeat("é", 201)} {
		h.Fails(memory.OpAdd, `{"body":"x","key":"`+k+`"}`, errs.InvalidInput)
	}
	if n := liveMemories(t, h); n != 0 {
		t.Fatalf("refused keys wrote %d memories", n)
	}
	keyed(t, h, `{"body":"x","key":"`+strings.Repeat("k", 200)+`"}`)

	gone := keyed(t, h, `{"body":"to delete","key":"once"}`)
	h.MustYes(memory.OpDelete, `{"id":"`+gone.ID+`"}`)
	_, err := h.Run(memory.OpAdd, `{"body":"to delete","key":"once"}`)
	if err == nil {
		t.Fatal("a deleted keyed memory was re-added under its key")
	}
	if !errs.Has(err, errs.InvalidInput) && !errs.Has(err, errs.InTrash) {
		t.Fatalf("deleted key: %v, want E_INVALID_INPUT or E_IN_TRASH", err)
	}
	if n := liveMemories(t, h); n != 1 {
		t.Fatalf("%d live memories, want 1 (the 200-char key)", n)
	}
}

// TestAddKeyConcurrent: parallel adds with one key store exactly one row
// and every caller gets that row's id (the race loser reads it back).
func TestAddKeyConcurrent(t *testing.T) {
	h := opstest.New(t)
	const n = 8
	got := make([]string, n)
	errsOut := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := h.Run(memory.OpAdd, `{"body":"same","key":"race"}`)
			if err != nil {
				errsOut[i] = err
				return
			}
			var w memory.WriteOut
			if jerr := json.Unmarshal([]byte(h.JSON(res)), &w); jerr != nil {
				errsOut[i] = jerr
				return
			}
			got[i] = w.ID
		}()
	}
	wg.Wait()
	for i := range n {
		if errsOut[i] != nil {
			t.Fatalf("caller %d: %v", i, errsOut[i])
		}
		if got[i] != got[0] {
			t.Fatalf("callers got different ids: %v", got)
		}
	}
	if c := liveMemories(t, h); c != 1 {
		t.Fatalf("%d memories after %d parallel keyed adds, want 1", c, n)
	}
}
