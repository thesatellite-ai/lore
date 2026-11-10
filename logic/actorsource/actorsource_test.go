package actorsource

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
)

func at(sec int64) hlc.HLC { return hlc.HLC{Wall: time.Unix(sec, 0).UTC()} }

func team(name string) Source {
	return Source{Known: true, Reachable: true, Origin: "sto_1", Where: "acme/org", Values: Values{Name: name, Kind: "human"}, Version: "v2"}
}

func TestStateOf(t *testing.T) {
	t.Parallel()
	src := team("Pat Org Edited")
	cases := []struct {
		name string
		src  Source
		c    Copy
		want State
	}{
		{"the roster row", src, Copy{IsSource: true}, StateSource},
		{"same version", src, Copy{OriginStore: "sto_1", OriginVersion: "v2"}, StateCurrent},
		{"older version", src, Copy{OriginStore: "sto_1", OriginVersion: "v1"}, StateStale},
		{"another origin, same version text", src, Copy{OriginStore: "setup:solo", OriginVersion: "v2"}, StateStale},
		{"no stamp", src, Copy{Values: Values{Name: "Pat Org Edited"}}, StateUnstamped},
		{"source unreadable", Source{Known: true}, Copy{OriginStore: "sto_1", OriginVersion: "v1"}, StateUnreachable},
		{"source unreadable, no stamp", Source{Known: true}, Copy{}, StateUnstamped},
	}
	for _, c := range cases {
		if got := StateOf(c.src, c.c); got != c.want {
			t.Errorf("%s: StateOf = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestShownOf is the winner rule: the source when read, else the newest
// copy, marked.
func TestShownOf(t *testing.T) {
	t.Parallel()
	copies := []Copy{
		{Store: "repo", Values: Values{Name: "pat"}, HLC: at(10)},
		{Store: "me", Values: Values{Name: "Pat One"}, HLC: at(30)},
		{Store: "acme/org", Values: Values{Name: "Pat Two"}, HLC: at(20)},
	}
	if s := ShownOf(team("Pat Three"), copies); s.Values.Name != "Pat Three" || s.Mark != "" || s.From != "" {
		t.Errorf("reachable source: %+v, want the source's Pat Three unmarked", s)
	}
	s := ShownOf(Source{Known: true}, copies)
	if s.Values.Name != "Pat One" || s.From != "me" || s.Mark != "(copy from me, source unreachable)" {
		t.Errorf("unreachable source: %+v, want the newest copy (me, Pat One) marked", s)
	}
	same := []Copy{{Store: "repo", Values: Values{Name: "codex"}, HLC: at(1)}, {Store: "me", Values: Values{Name: "codex"}, HLC: at(2)}}
	if s := ShownOf(Source{}, same); s.Mark != "" || s.Values.Name != "codex" {
		t.Errorf("no source, copies agree: %+v, want unmarked", s)
	}
	if s := ShownOf(Source{}, copies); s.Mark == "" {
		t.Errorf("no source, copies differ: %+v, want marked", s)
	}
}

func TestNeedsRefreshAndPatch(t *testing.T) {
	t.Parallel()
	src := team("Pat New")
	src.Values.StableKey, src.Values.Handle = "human:pat@example.test", "pat"
	stale := Copy{Values: Values{Name: "Pat Old", Kind: "human"}, OriginStore: "sto_1", OriginVersion: "v1"}
	if !NeedsRefresh(src, stale) {
		t.Fatal("a stale copy needs no refresh")
	}
	p := Patch(src, stale)
	want := map[string]string{FieldDisplayName: "Pat New", FieldOriginVersion: "v2"}
	if len(p) != len(want) || p[FieldDisplayName] != want[FieldDisplayName] || p[FieldOriginVersion] != want[FieldOriginVersion] {
		t.Errorf("Patch of a client copy = %v, want %v (no stable_key or handle added)", p, want)
	}
	full := stale
	full.Values.StableKey, full.Values.Handle = "human:pat@example.test", "pold"
	if p := Patch(src, full); p[FieldHandle] != "pat" || p[FieldStableKey] != "" {
		t.Errorf("Patch of a full copy = %v, want the handle changed and the key left (same)", p)
	}
	current := Copy{OriginStore: "sto_1", OriginVersion: "v2"}
	if NeedsRefresh(src, current) || NeedsRefresh(src, Copy{IsSource: true}) || NeedsRefresh(Source{Known: true}, stale) {
		t.Error("refresh asked for a current copy, the source row or an unreadable source")
	}
	if !NeedsRefresh(src, Copy{Values: Values{Name: "Pat New"}}) {
		t.Error("an unstamped copy is not stamped on the next write")
	}
}

// TestProjectedCopy: a client copy (the F18 projection) is judged by its
// values and never gains the origin stamp, which would put a store id and
// the writer's HLC into the client's committed files.
func TestProjectedCopy(t *testing.T) {
	t.Parallel()
	src := team("Pat New")
	src.Values.StableKey = "human:pat@example.test"
	same := Copy{Projected: true, Values: Values{Name: "Pat New", Kind: "human"}}
	if got := StateOf(src, same); got != StateCurrent || NeedsRefresh(src, same) {
		t.Errorf("a projected copy with the source's values: %q (refresh %v), want current and no refresh", got, NeedsRefresh(src, same))
	}
	old := Copy{Projected: true, Values: Values{Name: "Pat Old", Kind: "human"}}
	if got := StateOf(src, old); got != StateStale || !NeedsRefresh(src, old) {
		t.Errorf("a projected copy with an old name: %q, want stale and a refresh", got)
	}
	p := Patch(src, old)
	if len(p) != 1 || p[FieldDisplayName] != "Pat New" {
		t.Errorf("Patch of a projected copy = %v, want only the display name (no stamp, no key)", p)
	}
	if got := StateOf(Source{Known: true}, old); got != StateUnreachable {
		t.Errorf("a projected copy with an unreadable source: %q, want %q", got, StateUnreachable)
	}
}

func TestIdentityVersion(t *testing.T) {
	t.Parallel()
	a := IdentityVersion(Values{Name: "Pat One", Kind: "human", StableKey: "human:pat@example.test"})
	if len(a) != versionHexLen || a != IdentityVersion(Values{Name: "Pat One", Kind: "human", StableKey: "human:pat@example.test"}) {
		t.Fatalf("IdentityVersion %q is not a stable %d-hex hash", a, versionHexLen)
	}
	if a == IdentityVersion(Values{Name: "Pat Two", Kind: "human", StableKey: "human:pat@example.test"}) {
		t.Error("a new name gives the same version")
	}
}

func TestCountsOf(t *testing.T) {
	t.Parallel()
	src := team("x")
	n := CountsOf(src, []Copy{{IsSource: true}, {OriginStore: "sto_1", OriginVersion: "v2"}, {OriginStore: "sto_1", OriginVersion: "v1"}, {}})
	if n != (Counts{Current: 1, Stale: 1, Unstamped: 1}) {
		t.Errorf("CountsOf = %+v", n)
	}
}

func TestSettle(t *testing.T) {
	t.Parallel()
	copies := []Copy{{Store: "repo", OriginStore: "setup:solo"}, {Store: "app", OriginStore: "sto_9", OriginVersion: "v1"}}
	s := Settle(Source{}, copies)
	if !s.Known || s.Reachable || s.Origin != "sto_9" {
		t.Errorf("Settle = %+v, want a known, unreachable source sto_9", s)
	}
	if s := Settle(Source{}, copies[:1]); s.Known {
		t.Errorf("a setup stamp alone made a known source: %+v", s)
	}
	r := Report{Source: team("n"), Copies: []Copy{{OriginStore: "sto_1", OriginVersion: "v2"}, {OriginStore: "sto_1", OriginVersion: "v1", Store: "x"}}}
	if st := r.Stale(); len(st) != 1 || st[0].Store != "x" {
		t.Errorf("Stale = %+v", st)
	}
}

// TestEmailChangePatch: after an email change the source names the new
// key and the old one as an alias. A full copy gets both (and is stale until
// then); the F18 projection gets neither, so no email reaches a repo copy.
// An identity with no aliases keeps the version it had before aliases.
func TestEmailChangePatch(t *testing.T) {
	t.Parallel()
	v := Values{Name: "Pat", Kind: "human", StableKey: "human:pat@new.test", Aliases: JoinAliases([]string{"human:pat@old.test"})}
	src := Source{Known: true, Reachable: true, Origin: "setup:solo", Values: v, Version: IdentityVersion(v)}
	full := Copy{Values: Values{Name: "Pat", Kind: "human", StableKey: "human:pat@old.test"}, OriginStore: "setup:solo",
		OriginVersion: IdentityVersion(Values{Name: "Pat", Kind: "human", StableKey: "human:pat@old.test"})}
	if StateOf(src, full) != StateStale {
		t.Fatalf("a full copy with the old key is %q, want stale", StateOf(src, full))
	}
	p := Patch(src, full)
	if p[FieldStableKey] != "human:pat@new.test" || p[FieldKeyAliases] != "human:pat@old.test" {
		t.Errorf("Patch of a full copy = %v, want the new key and the old one as an alias", p)
	}
	proj := Copy{Projected: true, Values: Values{Name: "Pat", Kind: "human"}}
	if p := Patch(src, proj); len(p) != 0 || StateOf(src, proj) != StateCurrent {
		t.Errorf("Patch of a projected copy = %v (%q), want nothing (no email in a repo copy) and current", p, StateOf(src, proj))
	}
	plain := Values{Name: "Pat", Kind: "human", StableKey: "human:pat@old.test"}
	sum := sha256.Sum256([]byte(strings.Join([]string{plain.Name, plain.Kind, plain.StableKey, plain.Handle}, "\x00")))
	if IdentityVersion(plain) != hex.EncodeToString(sum[:])[:versionHexLen] {
		t.Error("an identity with no aliases changed its version (every existing copy would read stale)")
	}
	if got := SplitAliases(JoinAliases([]string{"human:a@x", " ", "human:b@y"})); len(got) != 2 || got[0] != "human:a@x" || got[1] != "human:b@y" {
		t.Errorf("SplitAliases(JoinAliases) = %v", got)
	}
}
