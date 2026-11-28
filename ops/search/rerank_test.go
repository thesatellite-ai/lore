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

// TestCardsFollowLongQuestions (devco:D-1034, bug hunt H-299): in a long
// question about how something works, a team and a person holding a word
// or two of it (the team's handle, the company name in an email) ranked
// above the notes holding fewer of its words; they now follow every
// knowledge row. A question about people, or one the card's name answers,
// keeps the order by score; a short query is not touched.
func TestCardsFollowLongQuestions(t *testing.T) {
	df := []int{1, 2, 3, 3, 4, 6} // billing, page, rounds, totals, nightly, export
	n := len(df)
	order := func(people bool, hits ...Hit) []string {
		Rerank(hits, n, searchrank.Share(df))
		CardsFollow(hits, n, people)
		slices.SortStableFunc(hits, Before)
		// GroupComments re-sorts the groups as Before does (wire's merge)
		hits = GroupComments(hits, func(CommentOn) (Hit, bool, bool) { return Hit{}, false, false })
		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = h.ID
		}
		return ids
	}
	team := Hit{Entity: EntityTeam, ID: "team", Score: searchrank.Score(3, n, true, 0.5, 0),
		Held: []bool{true, false, false, false, true, true}, NameHeld: []bool{true, false, false, false, false, false}}
	person := Hit{Entity: EntityMember, ID: "person", Score: searchrank.Score(2, n, false, 0, 0),
		Held: []bool{true, true, false, false, false, false}, NameHeld: []bool{true, true, false, false, false, false}}
	note := Hit{Entity: "memory", ID: "note", Score: searchrank.Score(1, n, false, 0, 1),
		Held: []bool{false, false, false, true, false, false}}
	if got := order(false, team, person, note); !slices.Equal(got, []string{"note", "team", "person"}) {
		t.Errorf("long question: order %v, want the knowledge row before every card", got)
	}
	// a question about people ranks the cards by score
	if got := order(true, team, person, note); got[0] == "note" {
		t.Errorf("question about people: order %v, want a card first", got)
	}
	// a card whose name holds half the words is what the question names
	named := team
	named.NameHeld = []bool{true, false, false, false, true, true}
	if got := order(false, note, named); got[0] != "team" {
		t.Errorf("card named by the question: order %v, want it first", got)
	}
	// a short query (under searchrank.LongQuestion words) is not touched
	short := []Hit{{Entity: EntityTeam, ID: "team", Score: 0.9, Held: []bool{true, true}, NameHeld: []bool{true, false}},
		{Entity: "memory", ID: "note", Score: 0.5, Held: []bool{true, false}}}
	CardsFollow(short, 2, false)
	if slices.SortStableFunc(short, Before); short[0].ID != "team" {
		t.Errorf("short query: order %v, want the team by its score", short)
	}
}
