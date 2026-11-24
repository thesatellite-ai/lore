package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	"github.com/thesatellite-ai/lore/store"
)

// staleTestClone is a store clone of a bare remote under root, with the
// remote's path; the caller moves the remote away to make it unreachable.
func staleTestClone(t *testing.T, root string) (*gitbackend.Backend, string) {
	t.Helper()
	ctx := background()
	bare := filepath.Join(root, "notes.git")
	seed := filepath.Join(root, "seed")
	spGit(t, root, "init", "-q", "--bare", "-b", "main", bare)
	spGit(t, root, "init", "-q", "-b", "main", seed)
	id, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	marker, err := rowfs.Marker{ID: id}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, rowfs.MarkerFile), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	spGit(t, seed, "add", rowfs.MarkerFile)
	spGit(t, seed, "commit", "-q", "-m", "init")
	spGit(t, seed, "push", "-q", bare, "main")
	clone, err := gitbackend.OpenClone(ctx, bare, filepath.Join(root, "ann", "clones", "host", "notes"), rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clone.Close() })
	return clone, bare
}

// TestStaleCopyReadNamesUnreachableStore (devco:D-853, hunt H-110): a
// read whose store remote does not answer takes the local copy and
// records the store with the copy's last good fetch, both when its own
// fetch fails and when it backs off after a failure a moment ago; a
// store whose fetch is recent records nothing. Before, the read took the
// copy without a word (search and ask listed the store as searched).
// Break-verified by dropping b.noteStale in refresh.
func TestStaleCopyReadNamesUnreachableStore(t *testing.T) {
	t.Parallel()
	good := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name      string
		failed    time.Duration // a failed fetch this long ago (0 = none)
		goodAgo   time.Duration // the last good fetch this long before the subtest runs (0 = the fixed 3 h ago)
		wantStale bool
	}{
		{"the fetch fails now", 0, 0, true},
		{"backing off after a failure", 10 * time.Second, 0, true},
		// the age is taken when the subtest runs, not when the table is built: parallel subtests of a
		// loaded -race run start late, and "a moment ago" must not drift past the fetch interval
		{"fetched a moment ago", 0, time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			clone, bare := staleTestClone(t, root)
			if err := os.Rename(bare, bare+".gone"); err != nil {
				t.Fatal(err)
			}
			state := t.TempDir()
			lastGood := good
			if tc.goodAgo > 0 {
				lastGood = time.Now().Add(-tc.goodAgo)
			}
			if err := live.WriteFetchStamp(state, lastGood); err != nil {
				t.Fatal(err)
			}
			if tc.failed > 0 {
				if err := live.WriteFetchFailed(state, time.Now().Add(-tc.failed)); err != nil {
					t.Fatal(err)
				}
			}
			b := &storePush{inner: clone, name: "notes/core", res: pushRes(t), stateDir: state, notes: &strings.Builder{}}
			ctx := withStaleCopies(WithNoCreate(background()))
			for range 2 {
				if _, _, err := b.Read(ctx, ""); err != nil {
					t.Fatal(err)
				}
			}
			got := staleCopiesOf(ctx)
			if !tc.wantStale {
				if len(got) != 0 {
					t.Fatalf("a store fetched a moment ago was named stale: %+v", got)
				}
				return
			}
			want := []staleCopy{{Store: "notes", CopyOf: good.Format(time.RFC3339)}}
			if len(got) != 1 || got[0] != want[0] {
				t.Fatalf("stale copies = %+v, want %+v", got, want)
			}
			if tc.failed == 0 && live.FetchFailed(state).IsZero() {
				t.Fatal("a read whose fetch failed left no failure stamp: the next reads wait for the network again (devco:U-6)")
			}
		})
	}
}

// staleTextIn and staleTextOut are a test op's input and text output.
type (
	staleTextIn  struct{}
	staleTextOut struct {
		Body string `json:"body"`
	}
)

// Text implements op.Texter.
func (o staleTextOut) Text() string { return o.Body }

// staleTextResult is an op.Result whose text is body.
func staleTextResult(t *testing.T, body string) op.Result {
	t.Helper()
	e := op.Define(op.Spec{Name: "test.stale", Summary: "s", Permission: op.PermRead, Access: op.AccessRead},
		func(context.Context, staleTextIn) (staleTextOut, error) { return staleTextOut{Body: body}, nil })
	in, err := e.Decode(op.JSONDecoder{Body: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.Invoke(background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestStaleCopyNoteOnSearchAnswers (devco:D-853): a search's answer
// names each stale store under its rows and in the --json list
// "unreachable"; an empty answer names it under its searched line, once;
// a read that lists no searched stores is left alone.
func TestStaleCopyNoteOnSearchAnswers(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC)
	ctx := withStaleCopies(background())
	noteStaleCopy(ctx, "notes", at)
	line := "notes: unreachable, results from the copy of 2026-03-04 05:06 UTC"
	search := op.Spec{Name: opssearch.OpRun, Access: op.AccessRead}

	res := noteStaleRead(ctx, search, staleTextResult(t, "memory  mem_1  the vendor contract\n"), false)
	text, _ := res.Text()
	if !strings.Contains(text, line) {
		t.Fatalf("answer %q does not name the stale store (%q)", text, line)
	}
	raw, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Unreachable []staleCopy `json:"unreachable"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Unreachable) != 1 || out.Unreachable[0].Store != "notes" || out.Unreachable[0].CopyOf != at.Format(time.RFC3339) {
		t.Fatalf("--json unreachable = %+v (%s)", out.Unreachable, raw)
	}

	empty := withEmptyNote(ctx, staleTextResult(t, "(no matches)\n"), emptySearched{Stores: []string{"repo", "notes"}, Kinds: []string{everyKind}, Words: []string{"vendor"}}, nil)
	empty = noteStaleRead(ctx, search, empty, true)
	text, _ = empty.Text()
	if strings.Count(text, line) != 1 {
		t.Fatalf("empty answer names the stale store %d times, want once under searched:\n%s", strings.Count(text, line), text)
	}
	if i, j := strings.Index(text, "searched:"), strings.Index(text, line); j < i || strings.Contains(text[i:j], "read as:") {
		t.Fatalf("the stale line is not right under searched:\n%s", text)
	}

	// an empty answer that did not search the stale store does not name it
	elsewhere := withEmptyNote(ctx, staleTextResult(t, "(no matches)\n"), emptySearched{Stores: []string{"me"}, Kinds: []string{everyKind}, Words: []string{"vendor"}}, nil)
	if text, _ := noteStaleRead(ctx, search, elsewhere, true).Text(); strings.Contains(text, "unreachable") {
		t.Fatalf("an empty answer that read only me names notes as read from its copy:\n%s", text)
	}

	other := noteStaleRead(ctx, op.Spec{Name: "memory.list", Access: op.AccessRead}, staleTextResult(t, "x\n"), false)
	if text, _ := other.Text(); strings.Contains(text, "unreachable") {
		t.Fatalf("a read that lists no searched stores got the note: %q", text)
	}

	never := withStaleCopies(background())
	noteStaleCopy(never, "fresh", time.Time{})
	if l := staleCopyLine(staleCopiesOf(never)[0]); !strings.Contains(l, "never fetched here") {
		t.Fatalf("a copy never fetched reads %q", l)
	}
}
