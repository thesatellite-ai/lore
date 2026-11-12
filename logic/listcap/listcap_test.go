package listcap

import (
	"fmt"
	"strings"
	"testing"
)

// id is the n-th made-up lore id of prefix p.
func id(p string, n int) string { return fmt.Sprintf("%s_%032x", p, n) }

const where = "`lore doctor --json` lists all"

// supersedeLine is the doctor finding for n reversed --supersedes links,
// worded as wire's supersedeWarning words it.
func supersedeLine(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "rule " + id("rul", 2*i) + " → " + id("rul", 2*i+1)
	}
	return fmt.Sprintf("%d --supersedes link(s) written by lore 0.1.16 or older point from the new row to the old one (%s; each read as old → new, the row that replaces it); "+
		"run `lore doctor --fix` to move each link onto the old row", n, strings.Join(parts, ", "))
}

// TestTextCapsA50ItemFinding: a finding naming 50 links prints the first
// 5, "… and 45 more (`lore doctor --json` lists all)", and the rest of its
// sentence; the 6th link and later are not printed.
func TestTextCapsA50ItemFinding(t *testing.T) {
	got := Text(supersedeLine(50), where)
	want := "50 --supersedes link(s) written by lore 0.1.16 or older point from the new row to the old one (" +
		"rule " + id("rul", 0) + " → " + id("rul", 1) + ", rule " + id("rul", 2) + " → " + id("rul", 3) + ", rule " + id("rul", 4) + " → " + id("rul", 5) +
		", rule " + id("rul", 6) + " → " + id("rul", 7) + ", rule " + id("rul", 8) + " → " + id("rul", 9) +
		" … and 45 more (`lore doctor --json` lists all); each read as old → new, the row that replaces it); run `lore doctor --fix` to move each link onto the old row"
	if got != want {
		t.Fatalf("got\n %s\nwant\n %s", got, want)
	}
	if strings.Contains(got, id("rul", 10)) || strings.Contains(got, id("rul", 99)) {
		t.Fatal("an item after the 5th was printed")
	}
}

// TestTextKeepsShortLists: 5 items or fewer, a list of names without ids,
// and several lines each with a short list stay as they are.
func TestTextKeepsShortLists(t *testing.T) {
	for _, in := range []string{
		supersedeLine(5),
		supersedeLine(1),
		".gitignore does not list lore's local files (a, b, c, d, e, f, g, h)",
		"a " + id("tsk", 1) + ", " + id("tsk", 2) + "\nb " + id("tsk", 3) + ", " + id("tsk", 4) + "\nc " + id("tsk", 5) + ", " + id("tsk", 6),
		"",
	} {
		if got := Text(in, where); got != in {
			t.Errorf("Text changed %q to %q", in, got)
		}
	}
}

// TestTextShapes: the separators, the end of the last item, and lines.
func TestTextShapes(t *testing.T) {
	ids := func(p string, n int, sep string) string {
		s := make([]string, n)
		for i := range s {
			s[i] = id(p, i)
		}
		return strings.Join(s, sep)
	}
	first5 := func(p, sep string) string { return ids(p, 5, sep) }
	for _, c := range []struct{ name, in, want string }{
		{"comma list ending the line", "rows: " + ids("tsk", 8, ", "), "rows: " + first5("tsk", ", ") + " … and 3 more (" + where + ")"},
		{"semicolon list", "fix: lore x " + ids("act", 7, "; lore x "), "fix: lore x " + ids("act", 5, "; lore x ") + " … and 2 more (" + where + ")"},
		{"sentence end", "held by " + ids("tsk", 6, ", ") + ". Run lore sync.", "held by " + first5("tsk", ", ") + " … and 1 more (" + where + "). Run lore sync."},
		{"file names keep no suffix", "files " + ids("tsk", 6, ".yaml, ") + ".yaml", "files " + ids("tsk", 5, ".yaml, ") + ".yaml … and 1 more (" + where + ")"},
		{"sentence goes on after the last id", "rows " + ids("tsk", 7, ", ") + " are stale", "rows " + first5("tsk", ", ") + " … and 2 more (" + where + ") are stale"},
		{"items with words after their id", "copies: " + copies(7) + "; run lore actor refresh", "copies: " + copies(5) + " … and 2 more (" + where + "); run lore actor refresh"},
		{"items in a bracket group, an id after the list", "delete: " + deps(6) + " still point at " + id("prj", 9), "delete: " + deps(5) + " … and 1 more (" + where + ") still point at " + id("prj", 9)},
		{"two lines", "a\n" + ids("tsk", 6, ", "), "a\n" + first5("tsk", ", ") + " … and 1 more (" + where + ")"},
		{"no where", "rows: " + ids("tsk", 6, ", "), "rows: " + first5("tsk", ", ") + " … and 1 more"},
	} {
		w := where
		if c.name == "no where" {
			w = ""
		}
		if got := Text(c.in, w); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// copies is n stale-copy items as the actor copies check words them
// (words after the id, a ", " inside each item), "; " joined.
func copies(n int) string {
	s := make([]string, n)
	for i := range s {
		s[i] = fmt.Sprintf("Mira (%s) in kit has %q, the roster says %q", id("act", i), "old", "new")
	}
	return strings.Join(s, "; ")
}

// deps is n "id (table.field)" items, ", " joined, as E_HAS_DEPENDENTS
// words them.
func deps(n int) string {
	s := make([]string, n)
	for i := range s {
		s[i] = id("tsk", i) + " (tasks.project_id)"
	}
	return strings.Join(s, ", ")
}

// TestJoin caps a list given as items.
func TestJoin(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e", "f", "g"}
	if got := Join(items, ", ", where); got != "a, b, c, d, e … and 2 more ("+where+")" {
		t.Errorf("Join = %q", got)
	}
	if got := Join(items[:5], ", ", where); got != "a, b, c, d, e" {
		t.Errorf("Join of 5 = %q", got)
	}
}
