package infra

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/store"
)

// storesOf maps each row id to the names of the stores holding a live copy
// of it, in store precedence order (rows come from work.ListAll in that
// order): the STORES column of a list that shows one row per id although
// several stores hold it (actors, machines; FB-ACTOR-STORES).
func storesOf(rows []work.Sourced) map[string][]string {
	out := map[string][]string{}
	for _, s := range rows {
		id, name := s.Row.ID.String(), storeName(s.Source)
		if !slices.Contains(out[id], name) {
			out[id] = append(out[id], name)
		}
	}
	return out
}

// storeName is a store's name as the lists print it ("repo" for the repo).
func storeName(src store.Source) string { return string(src) }

// storeColumn is a merged list's text: each head padded to the widest, the
// stores holding the row after it, then countLine. A list read from one
// store only keeps its v0.1.17 lines, no column and no count: nothing was
// merged, so there is nothing to tell apart.
func storeColumn(heads []string, stores [][]string, one, many string) string {
	if !merged(stores) {
		return strings.Join(heads, "\n") + "\n"
	}
	w := 0
	for _, h := range heads {
		w = max(w, utf8.RuneCountInString(h))
	}
	var b strings.Builder
	for i, h := range heads {
		line := h
		if len(stores[i]) > 0 {
			line += strings.Repeat(" ", w-utf8.RuneCountInString(h)) + "  " + strings.Join(stores[i], ", ")
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(countLine(len(heads), stores, one, many))
	return b.String()
}

// merged reports whether the rows came from two or more stores.
func merged(stores [][]string) bool {
	var first string
	for _, s := range stores {
		for _, name := range s {
			if first == "" {
				first = name
			} else if name != first {
				return true
			}
		}
	}
	return false
}

// countLine is "2 actors across 3 stores": the rows listed and the stores
// they were merged from.
func countLine(n int, stores [][]string, one, many string) string {
	var all []string
	for _, s := range stores {
		for _, name := range s {
			if !slices.Contains(all, name) {
				all = append(all, name)
			}
		}
	}
	noun := many
	if n == 1 {
		noun = one
	}
	where := "stores"
	if len(all) == 1 {
		where = "store"
	}
	if len(all) == 0 {
		return fmt.Sprintf("%d %s\n", n, noun)
	}
	return fmt.Sprintf("%d %s across %d %s\n", n, noun, len(all), where)
}
