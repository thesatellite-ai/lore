package ask

// abstain.go — "not in memory" without a model (devco D-897, bug hunt
// H-150).
//
//	"how long have I lived in Shinjuku" ─▶ names: shinjuku
//	  ─▶ no row read holds it ─▶ hint line first:
//	     not in memory: no row holds shinjuku; the rows below are the nearest
//
// A hint, never a refusal: the rows still print. Why only a name (a word
// written with a capital, not the first word, a month, weekday or
// holiday): on LongMemEval_S the hint is right on 6 of its 30 questions
// about things never mentioned and wrong on 3 of 470 others, while "any
// question word in no row" caught 13 of 30 but was wrong on 52. The rows
// read are every row holding a question word (loadRows), so a name no
// row read holds is in no row of the stores read.
//
// Held means what score means by it (devco D-974, bug hunt H-233, Sync
// benchmark set 6 S6-01): a code identifier the question writes
// (Queue.Enqueue, EnqueueOptions.DedupKey) is held by a row holding each
// of its parts (identWord), as score matches it. Before, the hint said
// "no row holds queue.enqueue" above the very row that answered, and a
// word starting the question's second sentence ("… run? Explain …") read
// as a name. The hint is read from each row's whole text, never from the
// excerpt the answer prints: a long row cut to its best sections still
// holds what was cut (the cut note names the question's words left out,
// Row.LeftOut).

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Abstain is the "not in memory" verdict of an answer.
type Abstain struct {
	NotInMemory bool     `json:"not_in_memory" desc:"true: the question names something no row holds, so the rows are the nearest the memory holds, not the answer"`
	Missing     []string `json:"missing" desc:"the names (lower case) the question writes with a capital that no row holds"`
}

// line is the hint the text answer starts with, "" when the memory holds
// every name the question asks about.
func (a Abstain) line() string {
	if !a.NotInMemory {
		return ""
	}
	return "not in memory: no row holds " + strings.Join(a.Missing, ", ") + "; the rows below are the nearest\n"
}

// abstainOf reads whether rows hold the names q asks about: each word of
// q written with a capital (capitalised) that no row holds as score
// reads a word (hasWord) is missing. A question naming a resource is
// answered by it and never abstains.
func abstainOf(q question, ms []match, rows []textRow) Abstain {
	if len(ms) > 0 {
		return Abstain{}
	}
	names := capitalised(q.text)
	var missing []string
	for _, w := range q.words {
		if !names[w] {
			continue
		}
		if !heldByAny(q, rows, w) {
			missing = append(missing, w)
		}
	}
	if len(missing) == 0 {
		return Abstain{}
	}
	return Abstain{NotInMemory: true, Missing: missing}
}

// heldByAny reports whether a row of rows holds name w, read leniently
// (a false "not in memory" would make an agent deny what the memory
// holds): the word or a form of it (hasWord), each part of it when q
// writes it as a code identifier (identWord), a typo of it within the
// budget resource names get (typoBudget: "Kyto" for Kyoto), or its
// letters and digits inside the row's ("Wi-Fi" in "wifi", "eu-west" in
// "euwest").
func heldByAny(q question, rows []textRow, w string) bool {
	bare := squeeze(w)
	for i := range rows {
		toks := rows[i].tokens()
		if hasWord(toks, w) || identWord(q, toks, w) {
			return true
		}
		for t := range toks {
			if len(t) >= 4 && !isStemKey(t) && osa(t, w) <= typoBudget(w) {
				return true
			}
		}
		if bare != "" && strings.Contains(squeeze(strings.ToLower(rows[i].Title+" "+rows[i].Body)), bare) {
			return true
		}
	}
	return false
}

// squeeze is s with only its letters and digits.
func squeeze(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

// notNames are words written with a capital that name no thing asked
// about: months, weekdays, holidays.
var notNames = map[string]bool{
	"january": true, "february": true, "march": true, "april": true, "may": true, "june": true, "july": true, "august": true,
	"september": true, "october": true, "november": true, "december": true, "monday": true, "tuesday": true, "wednesday": true,
	"thursday": true, "friday": true, "saturday": true, "sunday": true, "valentine": true, "christmas": true, "halloween": true,
	"thanksgiving": true, "easter": true,
}

// capitalised is the set of words (lower case) text writes with a
// capital letter, the first word of each sentence and dates left out:
// the names it asks about.
func capitalised(text string) map[string]bool {
	out := map[string]bool{}
	for _, at := range reWord.FindAllStringIndex(text, -1) {
		w := strings.TrimSuffix(strings.TrimSuffix(text[at[0]:at[1]], "'s"), "’s")
		low := strings.ToLower(w)
		if low == w || notNames[low] || low == "i" || startsSentence(text[:at[0]]) {
			continue
		}
		out[low] = true
	}
	return out
}

// startsSentence reports whether a word after before starts a sentence,
// so its capital says nothing: nothing before it, or a line break, ? or !
// before it, or a full stop after a word of three or more letters ("…
// not terminal. Explain"; never "Dr. Okafor" or "e.g. Lisbon").
func startsSentence(before string) bool {
	t := strings.TrimRightFunc(before, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '"' || r == '\'' || r == '(' || r == '“' || r == '‘'
	})
	if t == "" || strings.ContainsRune("?!\n\r", rune(t[len(t)-1])) {
		return true
	}
	if !strings.HasSuffix(t, ".") {
		return false
	}
	prev := strings.TrimSuffix(t, ".")
	word := prev[strings.LastIndexFunc(prev, func(r rune) bool { return !unicode.IsLetter(r) })+1:]
	return utf8.RuneCountInString(word) >= 3
}
