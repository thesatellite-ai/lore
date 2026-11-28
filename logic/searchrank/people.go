package searchrank

import (
	"strings"
	"unicode"
)

// people.go — whether a question asks about people (devco:D-1034, bug
// hunt H-299): a long question about how something works is answered by
// knowledge rows, so the org's people and teams (cards) follow them in
// search; a question asking who owns, leads or maintains something, or
// about a team or a member, still ranks the cards by their words.

// LongQuestion is how many topic words make a question long: from it on,
// a card follows every knowledge row unless the question asks about
// people (AsksAboutPeople) or the card's name holds half its words.
const LongQuestion = 4

// peopleWords are the words of a question about people: who someone is,
// who owns or maintains something, a team or a member, how to reach them.
var peopleWords = map[string]bool{
	"who": true, "whom": true, "whose": true,
	"owner": true, "owners": true, "own": true, "owns": true, "owned": true, "ownership": true,
	"maintainer": true, "maintainers": true, "maintain": true, "maintains": true, "maintained": true,
	"team": true, "teams": true, "member": true, "members": true, "person": true, "people": true,
	"teammate": true, "teammates": true, "colleague": true, "colleagues": true, "responsible": true,
	"contact": true, "email": true, "emails": true,
}

// AsksAboutPeople reports whether text holds a word of a question about
// people (who, owner, owns, maintains, team, member, person, contact,
// email, …), in any case, as a whole word.
func AsksAboutPeople(text string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if peopleWords[w] {
			return true
		}
	}
	return false
}
