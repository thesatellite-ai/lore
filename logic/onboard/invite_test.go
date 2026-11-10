package onboard

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

const testInviteID = "inv_01890a5d8a2b7000a000000000000101"

func sampleInvite() Invite {
	return Invite{ID: testInviteID, Org: "agency", Store: "git@github.com:agency/lore-team.git",
		OrgFolder: "org", Email: "tomas@agency.example", By: "mira@agency.example", Expires: "2026-11-20"}
}

// TestInviteRoundTrip: encode then decode gives the same invite; the
// line is stable (resend prints the same text) and has the v1 prefix.
func TestInviteRoundTrip(t *testing.T) {
	line, err := EncodeInvite(sampleInvite())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, InvitePrefix) || strings.ContainsAny(line, "+/= \n") {
		t.Fatalf("line %q is not prefix + unpadded base64url", line)
	}
	again, _ := EncodeInvite(sampleInvite())
	if again != line {
		t.Errorf("encode is not stable: %q vs %q", line, again)
	}
	want := sampleInvite()
	want.V = InviteVersion
	for _, in := range []string{line, "  " + line + "\n", "lore setup join " + line, line + "=="} {
		got, err := DecodeInvite(in)
		if err != nil {
			t.Fatalf("decode %q: %v", in, err)
		}
		if got != want {
			t.Errorf("decode %q = %+v, want %+v", in, got, want)
		}
	}
}

func encodeRaw(t *testing.T, m map[string]json.RawMessage) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return InvitePrefix + base64.RawURLEncoding.EncodeToString(b)
}

// TestDecodeInviteInvalid: every malformed shape is E_INVITE_INVALID with
// the 7.2 text, never a panic or a half-filled invite.
func TestDecodeInviteInvalid(t *testing.T) {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	good := map[string]json.RawMessage{"v": raw(`1`), "id": raw(`"` + testInviteID + `"`), "org": raw(`"agency"`),
		"store": raw(`"file:///x"`), "org_folder": raw(`"org"`), "email": raw(`"tomas@agency.example"`),
		"by": raw(`"mira@agency.example"`), "expires": raw(`"2026-11-20"`)}
	// with replaces key k by the JSON text v ("" drops the key).
	with := func(k, v string) map[string]json.RawMessage {
		m := map[string]json.RawMessage{}
		for a, b := range good {
			m[a] = b
		}
		if v == "" {
			delete(m, k)
		} else {
			m[k] = raw(v)
		}
		return m
	}
	cases := map[string]string{
		"empty":         "",
		"no prefix":     "eyJ2IjoxfQ",
		"v2 prefix":     "lore-invite:v2:eyJ2IjoxfQ",
		"prefix only":   InvitePrefix,
		"bad base64":    InvitePrefix + "!!!***",
		"not json":      InvitePrefix + base64.RawURLEncoding.EncodeToString([]byte("hello")),
		"json array":    InvitePrefix + base64.RawURLEncoding.EncodeToString([]byte("[1,2]")),
		"version 2":     encodeRaw(t, with("v", `2`)),
		"no version":    encodeRaw(t, with("v", "")),
		"bad id":        encodeRaw(t, with("id", `"act_01890a5d8a2b7000a000000000000101"`)),
		"garbage id":    encodeRaw(t, with("id", `"inv_nope"`)),
		"no org":        encodeRaw(t, with("org", "")),
		"no store":      encodeRaw(t, with("store", `"  "`)),
		"no email":      encodeRaw(t, with("email", "")),
		"bad expires":   encodeRaw(t, with("expires", `"next week"`)),
		"wrong type v":  encodeRaw(t, with("v", `"1"`)),
		"truncated b64": encodeRaw(t, good)[:len(InvitePrefix)+20],
	}
	for name, in := range cases {
		_, err := DecodeInvite(in)
		if !errs.Has(err, errs.InviteInvalid) {
			t.Errorf("%s: got %v, want E_INVITE_INVALID", name, err)
			continue
		}
		if !strings.Contains(err.Error(), "copy the whole line starting with lore-invite:v1:") {
			t.Errorf("%s: message %q lacks the design text", name, err)
		}
	}
	if _, err := DecodeInvite(encodeRaw(t, good)); err != nil {
		t.Errorf("the good payload: %v", err)
	}
	if _, err := EncodeInvite(Invite{ID: "x"}); err == nil {
		t.Error("encoding an incomplete invite must fail")
	}
}

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestDecideInvite: the 4.5 status decision as a matrix of status ×
// expiry × accepting actor × email.
func TestDecideInvite(t *testing.T) {
	today := day("2026-11-06").Add(15 * time.Hour)
	const tomas, mira = "act_tomas", "act_mira"
	pending := InviteRecord{ID: testInviteID, Email: "tomas@agency.example", Status: schema.InviteStatusPending, ExpiresOn: day("2026-11-20")}
	at := func(r InviteRecord, f func(*InviteRecord)) InviteRecord { f(&r); return r }
	cases := []struct {
		name string
		rec  InviteRecord
		who  Acceptor
		want InviteVerdict
		code errs.Code
		text string
	}{
		{"pending in date", pending, Acceptor{tomas, "tomas@agency.example"}, VerdictAccept, "", ""},
		{"email case and space", pending, Acceptor{tomas, " Tomas@Agency.EXAMPLE "}, VerdictAccept, "", ""},
		{"last day still accepts", at(pending, func(r *InviteRecord) { r.ExpiresOn = day("2026-11-06") }), Acceptor{tomas, "tomas@agency.example"}, VerdictAccept, "", ""},
		{"expired yesterday", at(pending, func(r *InviteRecord) { r.ExpiresOn = day("2026-11-05") }), Acceptor{tomas, "tomas@agency.example"}, "", errs.InviteExpired, "expired on 2026-11-05; ask mira@agency.example for a new one"},
		{"status expired", at(pending, func(r *InviteRecord) { r.Status = schema.InviteStatusExpired }), Acceptor{tomas, "tomas@agency.example"}, "", errs.InviteExpired, "expired on 2026-11-20"},
		{"revoked", at(pending, func(r *InviteRecord) { r.Status = schema.InviteStatusRevoked }), Acceptor{tomas, "tomas@agency.example"}, "", errs.InviteRevoked, "was revoked; ask mira@agency.example"},
		{"revoked and expired is revoked", at(pending, func(r *InviteRecord) { r.Status = schema.InviteStatusRevoked; r.ExpiresOn = day("2026-01-01") }), Acceptor{tomas, "tomas@agency.example"}, "", errs.InviteRevoked, ""},
		{"used by another actor", at(pending, func(r *InviteRecord) { r.Status = schema.InviteStatusAccepted; r.AcceptedBy = tomas }), Acceptor{mira, "tomas@agency.example"}, "", errs.InviteUsed, "already used by act_tomas; ask mira@agency.example for a new one"},
		{"accepted by me again", at(pending, func(r *InviteRecord) { r.Status = schema.InviteStatusAccepted; r.AcceptedBy = tomas }), Acceptor{tomas, "tomas@agency.example"}, VerdictAlready, "", ""},
		{"accepted with no accepted_by", at(pending, func(r *InviteRecord) { r.Status = schema.InviteStatusAccepted }), Acceptor{"", "tomas@agency.example"}, "", errs.InviteUsed, ""},
		{"wrong email", pending, Acceptor{tomas, "mira@agency.example"}, "", errs.InviteEmail, "is for tomas@agency.example; use that address or ask for a new invite"},
		{"empty email", pending, Acceptor{tomas, ""}, "", errs.InviteEmail, ""},
		{"unknown status", at(pending, func(r *InviteRecord) { r.Status = "frozen" }), Acceptor{tomas, "tomas@agency.example"}, "", errs.InviteInvalid, ""},
	}
	for _, c := range cases {
		got, err := DecideInvite(c.rec, "mira@agency.example", today, c.who)
		if c.code != "" {
			if !errs.Has(err, c.code) {
				t.Errorf("%s: got %q %v, want %s", c.name, got, err, c.code)
			} else if c.text != "" && !strings.Contains(err.Error(), c.text) {
				t.Errorf("%s: message %q lacks %q", c.name, err, c.text)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %q %v, want %q", c.name, got, err, c.want)
		}
	}
}

// TestEffectiveInviteStatus: pending past its last day reads expired;
// every other status is shown as stored.
func TestEffectiveInviteStatus(t *testing.T) {
	today := day("2026-11-06")
	for _, c := range []struct {
		status  string
		expires string
		want    string
	}{
		{schema.InviteStatusPending, "2026-11-06", schema.InviteStatusPending},
		{schema.InviteStatusPending, "2026-11-05", schema.InviteStatusExpired},
		{schema.InviteStatusAccepted, "2026-01-01", schema.InviteStatusAccepted},
		{schema.InviteStatusRevoked, "2026-01-01", schema.InviteStatusRevoked},
	} {
		got := EffectiveInviteStatus(InviteRecord{Status: c.status, ExpiresOn: day(c.expires)}, today)
		if got != c.want {
			t.Errorf("%s until %s = %s, want %s", c.status, c.expires, got, c.want)
		}
	}
}
