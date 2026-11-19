package team

// rosterperson.go — who the roster says a joining person is (devco
// D-487). `lore setup join` prefilled the identity step's name and the
// github step's account from global git (user.name), though the org's
// roster already holds the person's name and GitHub login, matched by the
// email they join with. The join lines now take both from the roster
// entry and fall back to git only when the roster has none.

import (
	"cmp"
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// RosterPerson is what the roster holds about one member: the name and
// GitHub login a join prefills, and the address that matched.
type RosterPerson struct {
	Email  string
	Name   string
	GitHub string
}

// FindRosterPerson is the member the primary email (emails[0]) names on
// the roster of profiles, with their name and GitHub login; ok false when
// none is a member (the join's own check then says why).
func FindRosterPerson(ctx context.Context, profiles profilestore.Store, emails []string) (RosterPerson, bool, error) {
	emails = memberEmails(emails)
	if len(emails) == 0 {
		return RosterPerson{}, false, nil
	}
	r, done, err := profiles.Roster(ctx, false)
	defer done()
	if err != nil {
		return RosterPerson{}, false, err
	}
	a, matched := findMember(ctx, r, emails)
	if matched == "" {
		return RosterPerson{}, false, nil
	}
	p := RosterPerson{Email: matched, Name: strings.TrimSpace(fieldText(a, actorFieldName))}
	contacts, err := r.List(ctx, schema.TableActorContacts)
	if err != nil {
		return RosterPerson{}, false, err
	}
	id := a.ID.String()
	for _, c := range contacts {
		if fieldText(c, schema.FieldContactActor) == id && fieldText(c, schema.FieldContactType) == teamprofile.ContactTypeGitHub {
			p.GitHub = strings.TrimSpace(cmp.Or(fieldText(c, schema.FieldContactValue), fieldText(c, schema.FieldContactValueNorm))) // the login as given
		}
	}
	return p, true, nil
}
