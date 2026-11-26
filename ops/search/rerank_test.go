package search

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/searchrank"
)

// rerankOrder ranks hits as GlobalSearch's merge does (Rerank, then
// Before) and returns their ids in order.
func rerankOrder(hits []Hit, df []int) []string {
	Rerank(hits, len(df), searchrank.Share(df))
	slices.SortStableFunc(hits, Before)
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	return ids
}

// TestCardNeverLeadsOnCommonWords (devco:D-1007, set 8 ask 3): a team
// whose handle holds "admin" and whose description holds the common
// words "store" and "company" matched three words where the knowledge
// row about the admin page matched two, and ranked first. Its
// description words now count by their rarity, so the knowledge row
// leads; a card that names the asked words still leads.
func TestCardNeverLeadsOnCommonWords(t *testing.T) {
	// words: admin, page, store, company; store and company are common
	df := []int{6, 3, 60, 45}
	n := len(df)
	team := Hit{Entity: EntityTeam, ID: "team", Score: searchrank.Score(3, n, false, 0.25, 0),
		Held: []bool{true, false, true, true}, NameHeld: []bool{true, false, false, false}}
	note := Hit{Entity: "memory", ID: "note", Score: searchrank.Score(2, n, false, 0, 0),
		Held: []bool{true, true, false, false}}
	if got := rerankOrder([]Hit{team, note}, df); got[0] != "note" {
		t.Errorf("order %v, want the knowledge row before the team", got)
	}

	// the same team named by its handle words keeps the first place
	named := team
	named.NameHeld = []bool{true, false, true, true}
	if got := rerankOrder([]Hit{note, named}, df); got[0] != "team" {
		t.Errorf("order %v, want the team its name holds every word of first", got)
	}

	// a resource whose fields hold the rarest word counts it fully
	res := []Hit{{Entity: EntityResource, ID: "res", Score: searchrank.Score(1, n, false, 0, 0),
		Held: []bool{false, true, false, false}, NameHeld: make([]bool, n)}}
	Rerank(res, n, searchrank.Share(df))
	if RankScore(res[0]) != res[0].Score {
		t.Errorf("rank %v, want the score %v kept for the rarest word", RankScore(res[0]), res[0].Score)
	}
}

// TestCardAfterKnowledgeOnATie (devco:D-1007): a person (kind member,
// sorted before memory) of the same score as a knowledge row comes
// after it.
func TestCardAfterKnowledgeOnATie(t *testing.T) {
	s := searchrank.Score(1, 1, false, 1, 0)
	person := Hit{Entity: EntityMember, ID: "person", Score: s}
	note := Hit{Entity: "memory", ID: "note", Score: s}
	if got := rerankOrder([]Hit{person, note}, nil); got[0] != "note" {
		t.Errorf("order %v, want the knowledge row first", got)
	}
}

// TestStaleOffTopicBelowFreshRare (devco:D-1007, set 8 ask 6): a stale
// fact holding three of the question's common words ranked above a
// fresh row holding its rare word and one more (0.9 of its score was not
// enough); its words now count by rarity.
func TestStaleOffTopicBelowFreshRare(t *testing.T) {
	df := []int{2, 40, 40, 40} // quokka is rare
	n := len(df)
	stale := Hit{Entity: "memory", ID: "stale", Stale: "stale: evidence changed", Score: searchrank.Score(3, n, false, 0, 0),
		Held: []bool{false, true, true, true}}
	fresh := Hit{Entity: "memory", ID: "fresh", Score: searchrank.Score(2, n, false, 0, 1),
		Held: []bool{true, true, false, false}}
	if got := rerankOrder([]Hit{stale, fresh}, df); got[0] != "fresh" {
		t.Errorf("order %v, want the fresh row holding the rare word first", got)
	}
	// a stale row holding the rare word too is still found above weaker rows
	onTopic := stale
	onTopic.Held = []bool{true, true, true, false}
	weak := Hit{Entity: "memory", ID: "weak", Score: searchrank.Score(1, n, false, 0, 1), Held: []bool{false, true, false, false}}
	if got := rerankOrder([]Hit{weak, onTopic}, df); got[0] != "stale" {
		t.Errorf("order %v, want the on-topic stale row above a row holding one common word", got)
	}
}
