package ask

import (
	"strings"
	"testing"
)

// subjectNote is a long as-built note whose "Workers" section repeats
// the question's common words (first, run, still, running, return) and
// whose "Caller API" section holds its subject (duplicate, key) once.
const subjectNote = `# As built: job queue

## Overview

The job queue runs every background job of the app. Each attempt of a job is a run. Runs are kept for thirty days.

## Workers

Workers claim runs with a lease of one minute and renew it while the run is still running. The first run of a job and every retry run go through the same claim, and the worker returns the run to the queue when it shuts down while the run is still running.

## Caller API

The caller API takes the job type, the payload and an optional key. A duplicate key while the earlier attempt is non-terminal gives back that attempt id and writes nothing.

## Operations

Operators watch the queue depth on the dashboard. Pausing a job type leaves its runs in the queue.`

// subjectQuestion is the words of "what does the job queue return for a
// duplicate key while the first run is still running" the note holds.
var subjectQuestion = []string{"return", "duplicate", "key", "while", "first", "run", "still", "running"}

// subjectStore is the spread of those words over ten scored rows: the
// common ones most rows hold, duplicate and key one row each.
func subjectStore() []scored {
	all := make([]scored, 10)
	for i := range all {
		all[i].matched = []string{"return", "while", "first", "run", "still", "running"}
	}
	all[0].matched = append(all[0].matched, "duplicate", "key")
	return all
}

// TestBestSectionsPicksSubjectSection (devco D-973, bug hunt H-232,
// S6-01): weighed by the store's spread, the section holding the
// question's rare words leads the cut, and a section holding only its
// common words is not printed under it; without the store's spread (nil)
// the section repeating the common words led and the subject section was
// left out.
func TestBestSectionsPicksSubjectSection(t *testing.T) {
	st := subjectTermsOf(subjectStore())
	if st == nil || !st.isRare("duplicate") || st.isRare("run") || st.of("key") <= st.of("first") {
		t.Fatalf("subject terms: %+v", st)
	}
	out, ok := bestSections(subjectNote, subjectQuestion, 400, st)
	if !ok {
		t.Fatal("bestSections did not cut the note")
	}
	if !strings.HasPrefix(out, sectionMark+"As built: job queue > Caller API"+matchingMark) || !strings.Contains(out, "A duplicate key while the earlier attempt is non-terminal") {
		t.Errorf("the subject section does not lead the cut:\n%s", out)
	}
	if strings.Contains(out, "> Workers") {
		t.Errorf("a section holding only the question's common words was printed under the subject section:\n%s", out)
	}
	// with no store spread (fewer than rareFloor scored rows) the note's
	// own spread decides, as before
	if subjectTermsOf(subjectStore()[:rareFloor-1]) != nil {
		t.Error("subject terms below rareFloor scored rows")
	}
}

// TestKeepSentencesSubjectSentence (devco D-973, S6-01 at the sentence
// cut): a section cut to its sentences keeps the one holding the rare
// words over the one repeating the common ones.
func TestKeepSentencesSubjectSentence(t *testing.T) {
	text := "Workers renew the lease while the run is still running, and the first run returns to the queue while it is still running. A duplicate key gives back the first attempt id. Operators watch the queue."
	out, ok := keepSentences(text, subjectQuestion, 60, subjectTermsOf(subjectStore()))
	if !ok || !strings.Contains(out, "A duplicate key gives back the first attempt id.") {
		t.Errorf("the sentence holding the subject was dropped:\n%s", out)
	}
}

// TestKeepSentencesListItem (devco D-973, S6-26): a numbered list item
// stays one sentence with its number, and a kept item comes with the line
// that introduces the list.
func TestKeepSentencesListItem(t *testing.T) {
	if got := splitSentences("6. The bulk importer writes rows directly. It skips the queue."); len(got) != 2 || got[0] != "6. The bulk importer writes rows directly." {
		t.Errorf("splitSentences split off the item number: %q", got)
	}
	text := "The catalog has six write paths:\n1. The editor saves one product.\n2. The feed import writes in batches.\n3. The price job writes prices.\n4. The stock sync writes stock.\n5. The translation job writes texts.\n6. The bulk importer writes rows directly, bypassing validation.\n\nEvery write path records an audit row."
	out, ok := keepSentences(text, []string{"bulk", "importer", "write", "paths"}, 140, nil)
	if !ok || !strings.Contains(out, "6. The bulk importer writes rows directly") || !strings.Contains(out, "The catalog has six write paths:") {
		t.Errorf("the list item or its lead-in was dropped:\n%s", out)
	}
}
