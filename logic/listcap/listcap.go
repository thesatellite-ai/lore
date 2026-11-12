// Package listcap keeps a list of ids in human output to a few items
// (layer L1, pure: stdlib only).
//
// A doctor finding named every one of 84 --supersedes links on one
// physical line several thousand characters long. Text output (never
// --json, which keeps every item) now shows at most Shown items of such a
// list, then how many more and where the full list is:
//
//	(rule rul_a → rul_b, rule rul_c → rul_d, … 84 items …; each read as …)
//	        │ Text(line, "`lore doctor --json` lists all")
//	        ▼
//	(rule rul_a → rul_b, …, rule rul_i → rul_j … and 79 more (`lore doctor --json` lists all); each read as …)
//
// A list is a run of items joined by one separator ("; " or ", ") where
// every item names a lore id (three lower-case letters, "_", 32 hex
// digits: ids.AnyID's form). The rule lives in one place so every text
// surface (the CLI's output and error lines) applies it the same way, and
// a new doctor check that joins ids is capped without a change of its own.
package listcap

import (
	"regexp"
	"strconv"
	"strings"
)

// Shown is how many items of a list text output names.
const Shown = 5

// separators join the items of a list, tried in this order: "; " first,
// since an item of a "; " list may hold a ", " of its own (the actor
// copies check), while a ", " list's items hold no "; ".
var separators = []string{"; ", ", "}

// idWord matches a lore id (ids.AnyID's text form: prefix, "_", 32 hex).
var idWord = regexp.MustCompile(`\b[a-z]{3}_[0-9a-f]{32}\b`)

// More is the tail after the shown items: "… and N more (where)".
func More(n int, where string) string {
	s := "… and " + strconv.Itoa(n) + " more"
	if where != "" {
		s += " (" + where + ")"
	}
	return s
}

// Join joins items with sep, naming at most Shown of them and then
// More(rest, where); a list of Shown items or fewer is joined whole.
func Join(items []string, sep, where string) string {
	if len(items) <= Shown {
		return strings.Join(items, sep)
	}
	return strings.Join(items[:Shown], sep) + " " + More(len(items)-Shown, where)
}

// Text caps every list of ids in text (each line on its own) to Shown
// items, the rest named by More(n, where). Text with no line holding more
// than Shown ids is returned unchanged.
func Text(text, where string) string {
	if len(idWord.FindAllStringIndex(text, Shown+1)) <= Shown {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = line(l, where)
	}
	return strings.Join(lines, "\n")
}

// line caps the lists of one line, trying each separator in turn.
func line(l, where string) string {
	if len(idWord.FindAllStringIndex(l, Shown+1)) <= Shown {
		return l
	}
	for _, sep := range separators {
		l = capRuns(l, sep, where)
	}
	return l
}

// capRuns splits l at sep and caps each run of more than Shown parts that
// all name an id. The first part of a run keeps the text before its item
// (it is part of the sentence); of the last part only the item goes, and
// the text after it (from an itemEnd character on) stays.
func capRuns(l, sep, where string) string {
	parts := strings.Split(l, sep)
	if len(parts) <= Shown {
		return l
	}
	var out []string
	for i := 0; i < len(parts); {
		j := i
		for j < len(parts) && idWord.MatchString(parts[j]) {
			j++
		}
		if j-i <= Shown {
			if j == i {
				j++ // a part without an id
			}
			out = append(out, parts[i:j]...)
			i = j
			continue
		}
		head := strings.Join(parts[i:i+Shown], sep)
		out = append(out, head+" "+More(j-i-Shown, where)+afterItem(parts[j-1], parts[j-2]))
		i = j
	}
	return strings.Join(out, sep)
}

// afterItem is the text of a list's last part after its item, which the
// item before it (prev) shapes: the last item holds as many ids as prev
// (the part may name more after it: "… (task_lists.project_id) still
// point at prj_…"). Then:
//
//   - prev ends with its id ("rule rul_a → rul_b"): everything after the
//     item is the sentence going on and stays whole ("… are stale");
//   - prev ends with a bracket group ("tsk_… (tasks.project_id)"): the
//     item ends where its own first bracket group closes;
//   - else ("Mira (act_…) in kit has …") the item runs on to the first
//     ")" or "]" it did not open itself, a ";", or a "." ending the
//     sentence.
//
// The text from the item's end on stays ("" when the item ends the part).
func afterItem(part, prev string) string {
	n := len(idWord.FindAllStringIndex(prev, -1))
	_, prevRest := splitAtID(prev, n)
	item, rest := splitAtID(part, n)
	if prevRest == "" {
		return rest
	}
	depth := strings.Count(item, "(") + strings.Count(item, "[") - strings.Count(item, ")") - strings.Count(item, "]")
	groupEnds := strings.HasSuffix(strings.TrimSpace(prevRest), ")") || strings.HasSuffix(strings.TrimSpace(prevRest), "]")
	opened := false
	for k := 0; k < len(rest); k++ {
		switch rest[k] {
		case '(', '[':
			depth++
			opened = true
		case ')', ']':
			if depth <= 0 {
				return rest[k:]
			}
			depth--
			if groupEnds && opened && depth == 0 {
				return rest[k+1:]
			}
		case ';':
			if depth <= 0 {
				return rest[k:]
			}
		case '.':
			if depth <= 0 && (k == len(rest)-1 || rest[k+1] == ' ') {
				return rest[k:]
			}
		}
	}
	return ""
}

// splitAtID cuts part after its n-th id (its last one when it holds
// fewer): the text up to and with that id, and the rest.
func splitAtID(part string, n int) (upTo, rest string) {
	loc := idWord.FindAllStringIndex(part, -1)
	if n < 1 || n > len(loc) {
		n = len(loc)
	}
	end := loc[n-1][1]
	return part[:end], part[end:]
}
