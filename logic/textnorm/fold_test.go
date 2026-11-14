package textnorm

import "testing"

func TestSearchFold(t *testing.T) {
	cases := map[string]string{
		"ıspanak":    "ispanak",
		"diyarbakır": "diyarbakir",
		"Iğdır":      "Iğdir",
		"İSTANBUL":   "İSTANBUL",
		"plain":      "plain",
	}
	for in, want := range cases {
		if got := SearchFold(in); got != want {
			t.Errorf("SearchFold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestASCIIFold(t *testing.T) {
	cases := map[string]string{
		"chloé":   "chloe",
		"Chloé":   "Chloe",
		"Øyvind":  "Oyvind",
		"İlkay":   "Ilkay",
		"ılgaz":   "ilgaz",
		"straße":  "strasse",
		"Łukasz":  "Lukasz",
		"zoë-ann": "zoe-ann",
		"mira":    "mira",
		"山田":      "山田",
	}
	for in, want := range cases {
		if got := ASCIIFold(in); got != want {
			t.Errorf("ASCIIFold(%q) = %q, want %q", in, got, want)
		}
	}
}
