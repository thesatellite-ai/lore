package onboard

// invite.go — the invite line and the accept decision (ONBOARDING_DESIGN
// 4.5, 4.6, 7.2; Q8). Pure: no store, no clock (today is passed in).
//
//	admin ─▶ team.invite ─▶ invites row (roles, projects, type, status, expires_on)
//	                     ─▶ EncodeInvite ─▶ "lore-invite:v1:<base64url JSON>"
//	member ─▶ DecodeInvite (where to look) ─▶ the invites row in the store
//	       ─▶ DecideInvite (row, today, who) ─▶ accept | already | E_INVITE_*
//
// The line holds no secret and grants nothing (4.5): it only says where the
// row is. Roles, projects and the period type are read from the row, so an
// edited line cannot give anyone more.

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// InvitePrefix starts every invite line (4.5); equal to setup.InvitePrefix
// (ops/setup is L5 and cannot be imported here).
const InvitePrefix = "lore-invite:v1:"

// InviteVersion is the JSON "v" of a v1 invite.
const InviteVersion = 1

// Invite is the JSON inside an invite line (4.5). Field names and json
// tags are those of setup.Invite, so either decodes the other's text.
type Invite struct {
	V         int    `json:"v"`
	ID        string `json:"id"`
	Org       string `json:"org"`
	Store     string `json:"store"`
	OrgFolder string `json:"org_folder"`
	Email     string `json:"email"`
	By        string `json:"by"`
	Expires   string `json:"expires"`
}

// invalidInviteMsg is the 7.2 message of E_INVITE_INVALID.
const invalidInviteMsg = "this is not a lore invite; copy the whole line starting with " + InvitePrefix

// invalidInvite is E_INVITE_INVALID naming what is wrong.
func invalidInvite(why string, cause error) *errs.Error {
	msg := invalidInviteMsg
	if why != "" {
		msg += " (" + why + ")"
	}
	if cause != nil {
		return errs.Wrap(errs.InviteInvalid, msg, cause).WithHint("ask the person who invited you to run lore team invite resend --ref <your-email>")
	}
	return errs.New(errs.InviteInvalid, msg).WithHint("ask the person who invited you to run lore team invite resend --ref <your-email>")
}

// check is the shape every v1 invite must have; why names the first gap.
func (inv Invite) check() string {
	switch {
	case inv.V != InviteVersion:
		return "version is not 1"
	case !strings.HasPrefix(inv.ID, string(ids.PrefixInvite)+"_"):
		return "id is not an inv_ id"
	case strings.TrimSpace(inv.Org) == "":
		return "no org"
	case strings.TrimSpace(inv.Store) == "":
		return "no store"
	case strings.TrimSpace(inv.Email) == "":
		return "no email"
	}
	if _, err := ids.ParseAny(inv.ID); err != nil {
		return "id is not an inv_ id"
	}
	if _, err := time.Parse(value.DateLayout, inv.Expires); err != nil {
		return "expires is not YYYY-MM-DD"
	}
	return ""
}

// EncodeInvite is the invite line of inv (V is set to 1). The JSON is
// encoded with the fields in declaration order, so the same invite always
// gives the same line (team.invite.resend prints the same text, 4.6).
func EncodeInvite(inv Invite) (string, error) {
	inv.V = InviteVersion
	if why := inv.check(); why != "" {
		return "", errs.New(errs.Internal, "invite "+inv.ID+" cannot be encoded: "+why)
	}
	b, err := json.Marshal(inv)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "invite "+inv.ID+" cannot be encoded", err)
	}
	return InvitePrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeInvite parses an invite line. Leading text up to the prefix (a
// pasted "lore setup join lore-invite:v1:…") and surrounding space are
// ignored; padding is accepted. Anything else that does not parse, or a
// payload missing a required field, is E_INVITE_INVALID (check
// invite.parse, 4.5).
func DecodeInvite(text string) (Invite, error) {
	text = strings.TrimSpace(text)
	i := strings.Index(text, InvitePrefix)
	if i < 0 {
		return Invite{}, invalidInvite("", nil)
	}
	payload := strings.TrimRight(strings.TrimSpace(text[i+len(InvitePrefix):]), "=")
	if payload == "" {
		return Invite{}, invalidInvite("nothing after the prefix", nil)
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Invite{}, invalidInvite("the text after the prefix is cut or changed", err)
	}
	var inv Invite
	if err := json.Unmarshal(b, &inv); err != nil {
		return Invite{}, invalidInvite("the text after the prefix is cut or changed", err)
	}
	if why := inv.check(); why != "" {
		return Invite{}, invalidInvite(why, nil)
	}
	return inv, nil
}

// InviteRecord is the part of an invites row the decision reads.
type InviteRecord struct {
	ID     string
	Email  string
	Status string
	// ExpiresOn is the last day the invite can be accepted (schema
	// FieldInviteExpiresOn), a calendar day at midnight UTC.
	ExpiresOn time.Time
	// AcceptedBy is the act_ id that accepted it ("" while pending).
	AcceptedBy string
}

// EffectiveInviteStatus is the status shown and decided on: a pending
// invite past its last day reads as expired without anyone writing it.
func EffectiveInviteStatus(rec InviteRecord, today time.Time) string {
	if rec.Status == schema.InviteStatusPending && Expired(rec.ExpiresOn, today) {
		return schema.InviteStatusExpired
	}
	return rec.Status
}

// Expired reports whether today is after lastDay (both compared as
// calendar days, so the last day itself still accepts).
func Expired(lastDay, today time.Time) bool {
	return teamprofile.Day(today).After(teamprofile.Day(lastDay))
}

// Acceptor is the person accepting: the writing actor and their identity
// email (the setup's identity, 4.2).
type Acceptor struct {
	Actor string
	Email string
	// Rerun: this machine already held the org's entry before this join
	// ran (a re-run of a join done here). Only then may an accepted invite
	// answer VerdictAlready. Why (J-9): on a fresh join the actor is found
	// by the invite's address, which anyone can type, so an accepted
	// invite would make a second person the invitee.
	Rerun bool
}

// InviteVerdict is DecideInvite's answer when the invite may be used.
type InviteVerdict string

const (
	// VerdictAccept: pending, in date, the right email — write the join.
	VerdictAccept InviteVerdict = "accept"
	// VerdictAlready: this same actor accepted it before — a re-run of the
	// join apply, nothing to write (single use, but idempotent for its
	// owner).
	VerdictAlready InviteVerdict = "already"
)

// DecideInvite is the accept decision of 4.5 (checks invite.status and
// invite.email), in this order: revoked, used (by someone else, or on a
// fresh join; the same actor re-running a join done on this machine is
// VerdictAlready), expired (status or past expires_on), wrong
// email. by is the inviter from the invite line, for the messages. Fail
// closed: an unknown status is E_INVITE_INVALID.
func DecideInvite(rec InviteRecord, by string, today time.Time, who Acceptor) (InviteVerdict, error) {
	ask := "ask " + orSomeone(by)
	switch rec.Status {
	case schema.InviteStatusRevoked:
		return "", errs.New(errs.InviteRevoked, "invite "+rec.ID+" was revoked; "+ask).
			WithHint(ask + " to run lore team invite --email " + rec.Email)
	case schema.InviteStatusAccepted:
		if who.Rerun && rec.AcceptedBy != "" && rec.AcceptedBy == who.Actor {
			return VerdictAlready, nil
		}
		return "", errs.New(errs.InviteUsed, "invite "+rec.ID+" was already used by "+orSomeone(rec.AcceptedBy)+"; "+ask+" for a new one").
			WithHint(ask + " to run lore team invite --email " + rec.Email)
	case schema.InviteStatusExpired, schema.InviteStatusPending:
	default:
		return "", errs.New(errs.InviteInvalid, "invite "+rec.ID+" has status "+rec.Status+", which this lore does not know").
			WithHint("lore upgrade, or " + ask + " for a new invite")
	}
	if rec.Status == schema.InviteStatusExpired || Expired(rec.ExpiresOn, today) {
		return "", errs.New(errs.InviteExpired, "invite "+rec.ID+" expired on "+rec.ExpiresOn.Format(value.DateLayout)+"; "+ask+" for a new one").
			WithHint(ask + " to run lore team invite --email " + rec.Email)
	}
	if !SameEmail(rec.Email, who.Email) {
		return "", errs.New(errs.InviteEmail, "invite "+rec.ID+" is for "+rec.Email+"; use that address or ask for a new invite").
			WithHint("lore setup edit --name <setup> with identity email " + rec.Email + ", or " + ask + " to invite " + orSomeone(who.Email))
	}
	return VerdictAccept, nil
}

// SameEmail compares two addresses as TEAM_PROFILES 6.1 normalises them
// (trim, lower-case); an address that does not normalise matches nothing.
func SameEmail(a, b string) bool {
	na, aerr := teamprofile.Normalize(schema.ContactKindEmail, a)
	nb, berr := teamprofile.Normalize(schema.ContactKindEmail, b)
	return aerr == nil && berr == nil && na == nb
}

// orSomeone is s, or a neutral word when it is empty (messages only).
func orSomeone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "the org admin"
	}
	return s
}
