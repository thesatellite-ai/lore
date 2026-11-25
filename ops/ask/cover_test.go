package ask

import (
	"strings"
	"testing"
)

// coverWords is the words of "when does the orbit rollout open its
// window" a note holds: orbit and rollout in two sentences each, window
// in one.
var coverQuestion = []string{"orbit", "rollout", "window"}

// TestKeepSentencesCoversEveryWord (devco D-985, hunt H-244): with room
// for two sentences, the cut keeps the best one and then the one holding
// the question word it lacks (window), not a second sentence repeating
// the words the first already holds; best first alone left window out.
func TestKeepSentencesCoversEveryWord(t *testing.T) {
	text := "The orbit rollout starts on the first day of the month.\n\n" +
		"Filler about something else entirely goes here.\n\n" +
		"Every orbit rollout is announced in the team channel.\n\n" +
		"More filler that the question does not ask about.\n\n" +
		"The window opens at nine for one hour."
	out, ok := keepSentences(text, coverQuestion, 120, nil)
	if !ok {
		t.Fatal("keepSentences did not cut the text")
	}
	if !strings.Contains(out, "The window opens at nine") {
		t.Errorf("the only sentence holding window was left out:\n%s", out)
	}
	if !strings.Contains(out, "The orbit rollout starts") {
		t.Errorf("the best sentence was left out:\n%s", out)
	}
}

// coverNote is a long note whose two short sections hold orbit and
// rollout and whose long "Limits" section holds window in one sentence
// among filler: the section does not fit next to the two best ones.
func coverNote() string {
	var b strings.Builder
	b.WriteString("# Orbit notes\n\n## Rollout plan\n\nThe orbit rollout goes out region by region, smallest first.\n\n")
	b.WriteString("## Overview\n\nOrbit is the release tool; a rollout is one release to every region.\n\n")
	b.WriteString("## Limits\n\n")
	for i := 0; i < 12; i++ {
		b.WriteString("The team keeps notes on every past release and reviews them each quarter. ")
	}
	b.WriteString("The change window opens at nine.\n\n")
	b.WriteString("## Contacts\n\nAsk the release desk for anything else.\n")
	return b.String()
}

// TestBestSectionsFillsLeftOutWord (devco D-985, hunt H-244, owner
// decision 2026-11-25 item 2): when the best sections leave out a
// question word the row holds and their section does not fit, the cut
// adds the one sentence holding it under its section's path, and the
// cut note no longer names it as left out; before, window stayed in the
// part left out.
func TestBestSectionsFillsLeftOutWord(t *testing.T) {
	note := coverNote()
	out, ok := bestSections(note, coverQuestion, 400, nil)
	if !ok {
		t.Fatal("bestSections did not cut the note")
	}
	if !strings.HasPrefix(out, sectionMark+"Orbit notes > Rollout plan"+matchingMark) {
		t.Errorf("the best section does not lead:\n%s", out)
	}
	if !strings.Contains(out, sectionMark+"Orbit notes > Limits\n"+elision+"\nThe change window opens at nine.") {
		t.Errorf("the sentence holding window was not added under its section:\n%s", out)
	}
	if len(out) > 400 {
		t.Errorf("the cut is %d bytes, over its room of 400:\n%s", len(out), out)
	}
	q := question{words: coverQuestion}
	if lo := leftOutOf(q, coverQuestion, "Orbit notes", out, nil); len(lo) != 0 {
		t.Errorf("the cut still leaves out %v", lo)
	}
}

// TestFillKeepsListLeadIn (devco D-985): a list item added for a word
// the best sections left out comes after the sentence leading into its
// list, so it says what it is an item of.
func TestFillKeepsListLeadIn(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Orbit notes\n\n## Rollout plan\n\nThe orbit rollout goes out region by region, smallest first.\n\n")
	b.WriteString("## Tables\n\n")
	for i := 0; i < 10; i++ {
		b.WriteString("The team keeps notes on every past release and reviews them each quarter. ")
	}
	b.WriteString("\n\nThe tables of the tool are:\n- `orbit_runs`: one row per run.\n- `orbit_windows`: one row per change window.\n")
	out, ok := bestSections(b.String(), coverQuestion, 300, nil)
	if !ok {
		t.Fatal("bestSections did not cut the note")
	}
	if !strings.Contains(out, "The tables of the tool are:\n- `orbit_windows`: one row per change window.") {
		t.Errorf("the list item lost its lead-in:\n%s", out)
	}
}

// TestIdentWordsFindsCodeNames (devco D-985): the per-word identifier
// scan finds a question word the text writes as a code name (inner
// capital, underscore, digit, dotted path) and not a plain word or a part
// of a longer name.
func TestIdentWordsFindsCodeNames(t *testing.T) {
	text := "Call applyLevelDelta, then MAX_RUN caps it; jobs.Reconcile ends with stock_inbox. Apply is a word. v2Client too."
	got := identWords(text, []string{"applyleveldelta", "max_run", "jobs.reconcile", "stock_inbox", "apply", "v2client", "level", "caps"})
	for _, w := range []string{"applyleveldelta", "max_run", "jobs.reconcile", "stock_inbox", "v2client"} {
		if !got[w] {
			t.Errorf("%s is written as an identifier: %v", w, got)
		}
	}
	for _, w := range []string{"apply", "level", "caps"} {
		if got[w] {
			t.Errorf("%s is a plain word or only part of an identifier: %v", w, got)
		}
	}
}
