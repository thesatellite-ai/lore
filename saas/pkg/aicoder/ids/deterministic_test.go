package ids

import "testing"

func TestDeterministic_StableAndValid(t *testing.T) {
	t.Parallel()
	a, err := Deterministic(PrefixTag, "prj_x", "auth")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Deterministic(PrefixTag, "prj_x", "auth")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("same input gave different ids: %s vs %s", a, b)
	}
	if err := Validate(a, PrefixTag); err != nil {
		t.Fatalf("deterministic id must validate as UUIDv7: %v", err)
	}
	if err := ValidateAny(a); err != nil {
		t.Fatalf("ValidateAny: %v", err)
	}
}

func TestDeterministic_DistinguishesParts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		x, y []string
	}{
		{"different value", []string{"p", "auth"}, []string{"p", "authz"}},
		{"boundary shift", []string{"ab", "c"}, []string{"a", "bc"}},
		{"order matters", []string{"a", "b"}, []string{"b", "a"}},
		{"arity matters", []string{"a"}, []string{"a", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			x, err := Deterministic(PrefixRepo, tc.x...)
			if err != nil {
				t.Fatal(err)
			}
			y, err := Deterministic(PrefixRepo, tc.y...)
			if err != nil {
				t.Fatal(err)
			}
			if x == y {
				t.Fatalf("%v and %v collided: %s", tc.x, tc.y, x)
			}
		})
	}
}

func TestDeterministic_Errors(t *testing.T) {
	t.Parallel()
	if _, err := Deterministic("TAG", "a"); err == nil {
		t.Fatal("uppercase prefix must be rejected")
	}
	if _, err := Deterministic(PrefixTag); err == nil {
		t.Fatal("zero parts must be rejected")
	}
}
