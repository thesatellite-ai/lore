package textwidth

import "testing"

func TestWidthCountsTerminalCells(t *testing.T) {
	cases := map[string]int{
		"":                      0,
		"Ana":                   3,
		"李小龙":                   6,
		"Björn Þórsson":         13,
		"Zoë Ünal":            8, // combining diaeresis: zero cells
		"Ｆｕｌｌ":                  8,
		"🚀":                     2,
		"👩‍💻":                   4,
		"Jean-Luc O'Brien":      16,
		"Ngozi Okonkwo-Adeyemi": 21,
	}
	for in, want := range cases {
		if got := Width(in); got != want {
			t.Errorf("Width(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestPadRightPadsToCellsNeverCuts(t *testing.T) {
	if got := PadRight("李小龙", 8); got != "李小龙  " {
		t.Fatalf("PadRight CJK = %q", got)
	}
	if got := PadRight("Ana-María de la Peña", 5); got != "Ana-María de la Peña" {
		t.Fatalf("PadRight cut a long name: %q", got)
	}
}

func TestColumnsTakesTheWidestCell(t *testing.T) {
	got := Columns([][]string{{"a", "李小龙"}, {"abcd"}, {"", "x", "yz"}})
	want := []int{4, 6, 2}
	if len(got) != len(want) {
		t.Fatalf("Columns = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Columns = %v, want %v", got, want)
		}
	}
}
