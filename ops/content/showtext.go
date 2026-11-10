package content

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
)

// ShowText is the plain-text `show` of a knowledge row without its own
// layout: v0.1.17's "<badge>  <id>" first line (kept, so a script reading
// the first line still finds the id), then the title or name under its own
// label when the row has one, then the body after a blank line
// (INTEG-KNOWLEDGE-3: v0.1.17 printed only the first line, so a person
// reading `lore pattern show <id>` saw nothing of the pattern). label is
// "title" or "name"; an empty title or body is left out.
func ShowText(badge, id, label, title, body string) string {
	s := badge + "  " + id + "\n"
	if title != "" {
		s += "  " + label + ": " + title + "\n"
	}
	if body != "" {
		s += "\n" + body + "\n"
	}
	return s
}

// EditName is the new name of an edit on a kind whose add takes --title
// but whose rows store a name (playbook, prompt, behaviour, workflow,
// workspace): --title is the same flag as --name (INTEG-KNOWLEDGE-4), so
// the spelling add taught works on edit too. Both given with different
// values is refused; neither given is "" (the name is left as it is).
func EditName(noun, name, title string) (string, error) {
	switch {
	case title == "":
	case name == "" || name == title:
		name = title
	default:
		return "", errs.New(errs.InvalidInput, noun+" edit: --name and --title are the same flag and were given different values").
			WithHint("pass one of them")
	}
	// A name of only spaces is no name: add refuses it (title required),
	// and edit stored "   " as the new name (campaign knowledge
	// 2026-11-08). "" still means "not given".
	if name != "" && strings.TrimSpace(name) == "" {
		return "", errs.New(errs.InvalidInput, noun+" edit: the new name is blank").
			WithHint("give the new name, or leave --name / --title out to keep the old one")
	}
	return name, nil
}

// listTextMax bounds the title or body excerpt of one list line (bytes,
// cut on a character boundary), so a 10k body stays one readable line.
const listTextMax = 80

// ListLine is one plain-text `list` line of a knowledge row: v0.1.17's
// "<badge>  <id>" (kept first, so a script reading the second field still
// finds the id), then the title or name, or the body's first line for a
// kind without one, cut to listTextMax. v0.1.17 printed only "<badge>
// <id>", so `lore pattern list` showed a column of ids and nothing a
// person could recognise (campaign knowledge 2026-11-08, the list half of
// INTEG-KNOWLEDGE-3).
func ListLine(badge, id, text string) string {
	s := badge + "  " + id
	if t := ListText(text); t != "" {
		s += "  " + t
	}
	return s + "\n"
}

// ListText is text as one list line shows it: its first line, trimmed,
// cut to listTextMax on a character boundary ("" for blank text).
func ListText(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return textcut.Cut(strings.TrimSpace(first), listTextMax, listTextMax-len(textcut.Ellipsis))
}
