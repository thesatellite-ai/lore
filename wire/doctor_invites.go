package wire

// doctor_invites.go — `lore doctor` lists the org's pending invites with
// one hint (owner report 2026-11-12): staff are added with `lore member
// add`, which needs no line to paste, and the invites written before it
// convert in one step (`lore member add --from-invites`). An invite stays
// right for an outsider; the note only says how to convert.

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// DoctorCheckPendingInvites names the check.
const DoctorCheckPendingInvites DoctorCheck = "pending_invites"

// maxInvitesNamed caps the emails a note names.
const maxInvitesNamed = 5

// DoctorPendingInvites is the check (registered in doctor_op.go).
func (a *App) DoctorPendingInvites(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	today := a.opts.Wall().UTC()
	var out []DoctorFinding
	for _, src := range st.eng.Stores() {
		rows, lerr := st.eng.List(ctx, src, schema.TableInvites)
		if lerr != nil || len(rows) == 0 {
			continue
		}
		var emails []string
		for _, r := range rows {
			day, perr := time.Parse(value.DateLayout, fieldText(r, schema.FieldInviteExpiresOn))
			if r.Deleted || perr != nil {
				continue
			}
			rec := onboard.InviteRecord{ID: r.ID.String(), Email: fieldText(r, schema.FieldInviteEmail), Status: fieldText(r, schema.FieldInviteStatus), ExpiresOn: day}
			if onboard.EffectiveInviteStatus(rec, today) == schema.InviteStatusPending {
				emails = append(emails, rec.Email)
			}
		}
		if len(emails) == 0 {
			continue
		}
		slices.Sort(emails)
		named := emails
		more := ""
		if len(named) > maxInvitesNamed {
			named, more = named[:maxInvitesNamed], " and "+plural(len(emails)-maxInvitesNamed, "more")
		}
		out = append(out, DoctorFinding{Check: DoctorCheckPendingInvites, Level: DoctorNote, Store: src,
			Message: plural(len(emails), "pending invite") + " in " + a.StoreLabel(src) + " (" + strings.Join(named, ", ") + more + "): your own staff need no invite; lore member add --from-invites makes them members now (an outsider keeps the invite)",
			Fix:     "lore member add --from-invites"})
	}
	return out, nil
}
