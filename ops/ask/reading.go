package ask

// reading.go — devco D-821 (hunt H-77): an empty answer says how the
// question was read. `lore ask` answering nothing printed one fixed
// sentence, so an agent could not tell a question read wrong (a name taken
// for a filler word, a date not seen) from a store that holds nothing
// about it, and asked again blindly. Read exposes the same reading run
// uses (parseQuestion), for the note the composition root adds to every
// empty search (wire/emptysearch.go).

import (
	"strings"
	"time"
)

// The question types Reading.Type names, in the order run tries them.
const (
	TypeIOwe     = "what I owe"
	TypeOwesMe   = "what is owed to me"
	TypeDate     = "what happened on a date"
	TypeIdentity = "who is"
	TypeWords    = "words"
)

// Reading is how ask reads a question before it looks at any row: the
// question type, the content words it matches names and text with, the
// topics it asks about and the dates it names.
type Reading struct {
	// Type is one of the Type* constants, or the topics it names joined by
	// " and " ("decision", "status") when it names some.
	Type string
	// Words are the content words (question words and filler left out).
	Words []string
	// Dates is the date window as read ("2026-11-19 (yesterday)"); "" for
	// none.
	Dates string
}

// Read reads q against now as run does.
func Read(q string, now time.Time) Reading {
	p := parseQuestion(q, now)
	r := Reading{Words: p.words, Dates: p.when}
	var topics []string
	for _, t := range p.topics {
		topics = append(topics, t.name)
	}
	switch {
	case p.owe == oweIOwe:
		r.Type = TypeIOwe
	case p.owe == oweOwesMe:
		r.Type = TypeOwesMe
	case !p.window.IsZero():
		r.Type = TypeDate
	case isIdentity(p.text):
		r.Type = TypeIdentity
	case len(topics) > 0:
		r.Type = strings.Join(topics, " and ")
	default:
		r.Type = TypeWords
	}
	return r
}
