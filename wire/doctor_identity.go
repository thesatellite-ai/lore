package wire

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/logic/identmatch"
)

// DoctorCheckIdentityContacts is the "identity contacts held by two
// actors" check of `lore doctor` (TEAM_PROFILES_DESIGN 6.4, TP-4).
const DoctorCheckIdentityContacts DoctorCheck = "identity_contacts"

// DoctorIdentityContacts finds every identity contact (type, value) held
// by two or more actors across the attached non-client stores: what two
// clones produce when each added the same login to a different person.
// While it stands, that identity resolves to nobody (identmatch fails
// closed), so writes by it keep the identity chain's own actor. One
// warning per key, naming the type, the value, the actors and the contact
// rows, with the fix: remove the contact from the wrong person.
func (a *App) DoctorIdentityContacts(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	in, err := readIdentityInputs(ctx, st, st.eng.Stores())
	if err != nil {
		return nil, err
	}
	var out []DoctorFinding
	for _, d := range identmatch.Duplicates(in.types, in.actors, in.contacts) {
		rows := make([]string, len(d.Contacts))
		for i, c := range d.Contacts {
			rows[i] = c.String()
		}
		out = append(out, DoctorFinding{Check: DoctorCheckIdentityContacts, Level: DoctorWarning,
			Message: "identity contact " + d.Key.String() + " is held by " + in.displays(d.Actors) + " (contacts " + strings.Join(rows, ", ") +
				"): it resolves to nobody until one person keeps it",
			Fix: "lore actor contact remove <acn_id> on the wrong person (lore actor show --id " + d.Key.String() + " lists them)"})
	}
	return out, nil
}
