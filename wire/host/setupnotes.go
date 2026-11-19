package host

// setupnotes.go — a write's "set up git for lore's row files" line is said
// only when the write goes through (devco:S-11). The first write in a clone
// sets git up before the operation runs (gitwire.go), and a refused write
// puts that set-up back (refusalundo.go). Both used to be printed before
// the refusal: the set-up line, then "the command was refused, so what it
// changed first is undone (…)", and only then the error an agent parses
// first. Now the Runner collects the set-up lines of a write, prints them
// when it succeeds and drops them when it is refused, whose undo leaves
// the clone as it was; the error is the first line.

import (
	"context"
	"sync"
)

// setupNotes holds the set-up lines of one write until it ends.
type setupNotes struct {
	mu    sync.Mutex
	lines []heldNote
}

// heldNote is one held line and the printer it was meant for.
type heldNote struct {
	text  string
	print func(string)
}

type setupNotesKey struct{}

// withSetupNotes returns ctx whose set-up lines are held (holdSetupNote)
// in the returned collector instead of printed.
func withSetupNotes(ctx context.Context) (context.Context, *setupNotes) {
	n := &setupNotes{}
	return context.WithValue(ctx, setupNotesKey{}, n), n
}

// holdSetupNote keeps text for the write's end when ctx carries a
// collector, and reports whether it did; the caller prints it otherwise.
func holdSetupNote(ctx context.Context, text string, print func(string)) bool {
	n, ok := ctx.Value(setupNotesKey{}).(*setupNotes)
	if !ok || n == nil {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lines = append(n.lines, heldNote{text: text, print: print})
	return true
}

// flush prints the held lines in order (the write went through); a nil
// collector prints nothing.
func (n *setupNotes) flush() {
	if n == nil {
		return
	}
	n.mu.Lock()
	lines := n.lines
	n.lines = nil
	n.mu.Unlock()
	for _, l := range lines {
		l.print(l.text)
	}
}

// flushSetupNotes prints the set-up lines ctx holds (a write that went
// through, before the lines naming the files it wrote: the order the
// first write printed before S-11).
func flushSetupNotes(ctx context.Context) {
	if n, ok := ctx.Value(setupNotesKey{}).(*setupNotes); ok {
		n.flush()
	}
}

// drop forgets the held lines (the write was refused and its set-up undone).
func (n *setupNotes) drop() {
	if n == nil {
		return
	}
	n.mu.Lock()
	n.lines = nil
	n.mu.Unlock()
}
