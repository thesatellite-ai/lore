package ids

import (
	"strings"
	"testing"
	"time"
)

func TestNew_FormatAndPrefix(t *testing.T) {
	id, err := New(PrefixMemory)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(id) != 36 {
		t.Errorf("expected len 36, got %d (%q)", len(id), id)
	}
	if !strings.HasPrefix(id, "mem_") {
		t.Errorf("expected mem_ prefix, got %q", id)
	}
	if !idPattern.MatchString(id) {
		t.Errorf("ID failed pattern: %q", id)
	}
}

func TestNew_AllPrefixes(t *testing.T) {
	for _, p := range AllPrefixes {
		id, err := New(p)
		if err != nil {
			t.Errorf("New(%q): %v", p, err)
			continue
		}
		if !strings.HasPrefix(id, p+"_") {
			t.Errorf("New(%q) produced %q (wrong prefix)", p, id)
		}
		if err := Validate(id, p); err != nil {
			t.Errorf("Validate(%q, %q): %v", id, p, err)
		}
	}
}

func TestNew_BadPrefix(t *testing.T) {
	cases := []string{"", "ab", "abcd", "AB1", "a_b", "ABC", " ab", "ab "}
	for _, p := range cases {
		if _, err := New(p); err == nil {
			t.Errorf("expected error for prefix %q", p)
		}
	}
}

func TestValidate_GoodIDs(t *testing.T) {
	id, _ := New(PrefixProject)
	if err := Validate(id, PrefixProject); err != nil {
		t.Errorf("expected valid: %v", err)
	}
}

func TestValidate_RejectsBadInputs(t *testing.T) {
	cases := []struct {
		id, prefix string
	}{
		{"", "mem"},
		{"mem", "mem"},       // too short
		{"mem_short", "mem"}, // too short
		{"MEM_018f3b2c9a8e7891b4f51234567890ab", "mem"},  // uppercase prefix
		{"mem-018f3b2c9a8e7891b4f51234567890ab", "mem"},  // wrong separator
		{"mem_018f3b2c9a8e7891b4f51234567890aZ", "mem"},  // uppercase in hex
		{"mem_018f3b2c9a8e7891b4f51234567890a", "mem"},   // 31 hex
		{"mem_018f3b2c9a8e7891b4f51234567890abc", "mem"}, // 33 hex
		{"rul_018f3b2c9a8e7891b4f51234567890ab", "mem"},  // wrong prefix
		{"mem_gggggggggggggggggggggggggggggggg", "mem"},  // non-hex
	}
	for _, c := range cases {
		if err := Validate(c.id, c.prefix); err == nil {
			t.Errorf("expected error for id=%q prefix=%q", c.id, c.prefix)
		}
	}
}

func TestValidate_RejectsNonV7(t *testing.T) {
	// A v4 UUID padded into mini format. Should fail version check.
	v4ID := "mem_550e8400e29b41d4a716446655440000"
	if err := Validate(v4ID, PrefixMemory); err == nil {
		t.Errorf("expected error for non-v7 UUID, got nil")
	}
}

func TestValidateAny(t *testing.T) {
	id, _ := New(PrefixDecision)
	if err := ValidateAny(id); err != nil {
		t.Errorf("ValidateAny: %v", err)
	}
	// unregistered prefix
	if err := ValidateAny("xyz_018f3b2c9a8e7891b4f51234567890ab"); err == nil {
		t.Errorf("expected unregistered-prefix error")
	}
}

func TestPrefixOf(t *testing.T) {
	id, _ := New(PrefixHotfix)
	if got := PrefixOf(id); got != "hfx" {
		t.Errorf("PrefixOf(%q) = %q, want hfx", id, got)
	}
	if got := PrefixOf("ab"); got != "" {
		t.Errorf("PrefixOf short = %q, want empty", got)
	}
}

func TestIDToTime_Roundtrip(t *testing.T) {
	before := time.Now()
	id, _ := New(PrefixRule)
	after := time.Now()

	got, err := IDToTime(id)
	if err != nil {
		t.Fatalf("IDToTime: %v", err)
	}

	// got should fall within [before-1ms, after+1ms]
	if got.Before(before.Add(-time.Millisecond)) || got.After(after.Add(time.Millisecond)) {
		t.Errorf("IDToTime out of range: got %v, before %v, after %v", got, before, after)
	}
}

func TestIDToTime_BadFormat(t *testing.T) {
	if _, err := IDToTime(""); err == nil {
		t.Error("expected error for empty")
	}
	if _, err := IDToTime("garbage"); err == nil {
		t.Error("expected error for garbage")
	}
}

func TestIDs_Sortable(t *testing.T) {
	// Generate a series of IDs and verify lexicographic sort = chronological.
	ids := make([]string, 100)
	for i := range ids {
		ids[i] = MustNew(PrefixMemory)
		time.Sleep(2 * time.Millisecond) // ensure ms tick separation
	}

	prev := ids[0]
	for _, id := range ids[1:] {
		if id < prev {
			t.Errorf("not chronologically sortable: %q < %q", id, prev)
		}
		prev = id
	}
}

func TestNew_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 10000)
	for i := 0; i < 10000; i++ {
		id := MustNew(PrefixMemory)
		if seen[id] {
			t.Fatalf("collision at iteration %d: %q", i, id)
		}
		seen[id] = true
	}
}
