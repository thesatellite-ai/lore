package coord

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dbent/schema"
)

// TestClaimLogSessionKeepsOnlyLoreRefs pins BUG-LAND-SESSION (P3-SEC-LAND-1):
// a claim_log entry is a synced row field, so it keeps a session only when
// it is lore's own ses_ id; a raw agent session (LORE_SESSION), a tool's
// UUID and a person's <stable_key>@<host> are dropped, on the new entry and
// on every older entry of the log.
func TestClaimLogSessionKeepsOnlyLoreRefs(t *testing.T) {
	ref := "ses_" + strings.Repeat("a", 32)
	for _, tc := range []struct {
		name, in, want string
	}{
		{"lore ses_ ref kept", ref, ref},
		{"raw LORE_SESSION dropped", "s-f30c2f0b", ""},
		{"tool uuid dropped", "019a7c2e-1111-4b1f-8a2b-0123456789ab", ""},
		{"person stable_key@host dropped", "mira@agency.example@devbox", ""},
		{"empty stays empty", "", ""},
		{"ses_ inside text dropped", "x-ses_" + strings.Repeat("b", 32), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Doc{}
			if err := d.AppendClaimLog(schema.ClaimLogEntry{At: t0, Event: schema.ClaimEventClaim, ActorID: "act_a", Session: tc.in, Epoch: 1}); err != nil {
				t.Fatal(err)
			}
			if got := d.ClaimLog()[0].Session; got != tc.want {
				t.Fatalf("session %q kept as %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	t.Run("older raw entries are cleaned on the next event", func(t *testing.T) {
		legacy, err := json.Marshal([]schema.ClaimLogEntry{
			{At: t0, Event: schema.ClaimEventClaim, ActorID: "act_a", Session: "s-legacy01", Epoch: 1},
			{At: t0, Event: schema.ClaimEventRelease, ActorID: "act_a", Session: ref, Epoch: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		d := Doc{keyClaimLog: json.RawMessage(legacy)}
		if err := d.AppendClaimLog(schema.ClaimLogEntry{At: t0, Event: schema.ClaimEventClaim, ActorID: "act_b", Session: "s-newer001", Epoch: 2}); err != nil {
			t.Fatal(err)
		}
		log := d.ClaimLog()
		if len(log) != 3 || log[0].Session != "" || log[1].Session != ref || log[2].Session != "" {
			t.Fatalf("log after append: %+v", log)
		}
	})
}

// TestClaimReleaseRowCarriesNoRawSession runs claim, a reclaim after expiry
// and a release on a real signals ref and checks the pushed row bytes: the
// holders' raw sessions sit on the signals ref only, never in the task row
// (the row a landing scans for known tool session ids).
func TestClaimReleaseRowCarriesNoRawSession(t *testing.T) {
	ctx := context.Background()
	wd := newWorld(t)
	wd.addTask("tsk_1", nil)
	a := wd.agent("sess-alpha")
	b := wd.agent("sess-bravo")
	res, claimErr := Claim(ctx, a.env, "tsk_1", ClaimOptions{})
	if claimErr != nil {
		t.Fatal(claimErr)
	}
	if res.Plan.Signal.Session != a.env.Caller.Session {
		t.Fatalf("the signal still names the holder's session: %q", res.Plan.Signal.Session)
	}
	// expiry needs a read that saw the lease, then TTL + grace (§7.4)
	b.clk.Set(t0.Add(time.Minute))
	if _, err := b.env.Signals.Read(ctx); err != nil {
		t.Fatal(err)
	}
	b.clk.Set(t0.Add(lease.TTL + lease.Grace + time.Minute))
	if _, err := Claim(ctx, b.env, "tsk_1", ClaimOptions{}); err != nil {
		t.Fatalf("reclaim after expiry: %v", err)
	}
	if err := Release(ctx, b.env, "tsk_1"); err != nil {
		t.Fatal(err)
	}
	d := b.row(t, "tsk_1")
	if n := len(d.ClaimLog()); n < 3 {
		t.Fatalf("claim, reclaim and release are logged: %d entries", n)
	}
	raw, encErr := json.Marshal(d)
	if encErr != nil {
		t.Fatal(encErr)
	}
	for _, s := range []string{a.env.Caller.Session, b.env.Caller.Session} {
		if strings.Contains(string(raw), s) {
			t.Fatalf("raw session %q reached the task row: %s", s, raw)
		}
	}
}
