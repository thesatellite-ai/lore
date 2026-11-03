package xref

import (
	"errors"
	"testing"
)

// F15 direction matrix, every (from, to) pair.
func TestAllowedMatrix(t *testing.T) {
	cases := []struct {
		from, to Kind
		same     bool
		ok       bool
	}{
		{Client, Client, false, true},
		{Client, Internal, false, false},
		{Client, Personal, false, false},
		{Internal, Client, false, true},
		{Internal, Internal, false, true},
		{Internal, Personal, false, false},
		{Personal, Client, false, true},
		{Personal, Internal, false, true},
		{Personal, Personal, true, true},
		{Personal, Personal, false, false},
	}
	for _, c := range cases {
		err := Allowed(c.from, c.to, c.same)
		if (err == nil) != c.ok {
			t.Errorf("%s → %s (same=%v): err=%v want ok=%v", c.from, c.to, c.same, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInward) {
			t.Errorf("refusal must wrap ErrInward: %v", err)
		}
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]Ref{
		"tsk_01":             {RowID: "tsk_01"},
		"acme/app:tsk_01":    {ProjectID: "acme/app", RowID: "tsk_01"},
		"prj_x:mem_2":        {ProjectID: "prj_x", RowID: "mem_2"},
		" acme/app:tsk_01  ": {ProjectID: "acme/app", RowID: "tsk_01"},
	} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v", in, got, err)
		}
		if got.String() == "" {
			t.Error("String")
		}
	}
	for _, bad := range []string{"", ":tsk", "acme:", "a:b c"} {
		if _, err := Parse(bad); !errors.Is(err, ErrBadRef) {
			t.Errorf("Parse(%q) must fail", bad)
		}
	}
	if Unavailable(Ref{ProjectID: "acme/app", RowID: "tsk_01"}) != "→ acme/app:tsk_01 (not available here)" {
		t.Error("unavailable form")
	}
}
