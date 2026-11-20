package wire

// orgsource_unknownaud.go — a store of an audience this lore does not know
// is said out loud (devco D-734). lore 0.1.18 met `audience: team:@web` in
// lore-org.yaml, read it as admins-only and left a member of @web without
// the team's store: no clone, no reads line in the repos linked to it, and
// whoami/doctor said nothing (doctor --all: 0 errors). An audience kind a
// newer lore adds is now kept as unknown (orgconf.Audience.Unknown): the
// store stays off a member's machine (fail safe) and whoami and doctor
// name it, with the audience and the upgrade to run.
//
//	lore-org.yaml store, audience X ─▶ ForPerson: skipped
//	   X known (admins)  ─▶ skippedStoresNote (a note: nothing to fix)
//	   X unknown         ─▶ unknownAudienceNote (a warning: lore upgrade)

import (
	"context"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// unknownAudienceFix is what a person with an older lore runs: the new
// binary reads the audience, and the next sync brings the store in when
// the person is in it.
const unknownAudienceFix = "lore upgrade"

// unknownAudienceStores is the stores of skipped whose audience this lore
// does not know (D-734), in order.
func unknownAudienceStores(skipped []orgconf.Store) []orgconf.Store {
	var out []orgconf.Store
	for _, s := range skipped {
		if s.Audience.Unknown() {
			out = append(out, s)
		}
	}
	return out
}

// knownSkippedStores is skipped without the unknown-audience stores: the
// admins-only ones skippedStoresNote explains.
func knownSkippedStores(skipped []orgconf.Store) []orgconf.Store {
	var out []orgconf.Store
	for _, s := range skipped {
		if !s.Audience.Unknown() {
			out = append(out, s)
		}
	}
	return out
}

// unknownAudienceNote is the sentence for one store of org whose audience
// this lore (version) does not know: which store, its audience, that this
// lore leaves it out, and the upgrade.
func unknownAudienceNote(org, version string, s orgconf.Store) string {
	v := strings.TrimSpace(version)
	if v == "" {
		v = "on this machine"
	}
	return fmt.Sprintf("%s's store %s is for %s (audience in lore-org.yaml); this lore %s cannot read that audience, so it does not clone or read the store: upgrade with `lore upgrade`, then run `lore sync`",
		org, s.ShortName(), s.Audience, v)
}

// unknownAudienceFindings is one doctor warning per store of org whose
// audience this lore does not know.
func unknownAudienceFindings(org, version string, skipped []orgconf.Store) []DoctorFinding {
	var out []DoctorFinding
	for _, s := range unknownAudienceStores(skipped) {
		out = append(out, DoctorFinding{Check: DoctorCheckOrgSource, Level: DoctorWarning, Message: unknownAudienceNote(org, version, s), Fix: unknownAudienceFix})
	}
	return out
}

// whoamiAudienceLabel labels whoami's rows of the unknown-audience stores.
const whoamiAudienceLabel = "audience"

// withUnknownAudienceRows puts one "audience" row per store of s's org
// whose audience this lore does not know after whoami's links row (at the
// end when there is none), so a member whose repo links to that store
// sees why it has no reads line.
func (w *standaloneWhoami) withUnknownAudienceRows(ctx context.Context, s *orgcfg.Situation, lines []string) []string {
	if s == nil || s.Result.Org == nil || s.Config == nil {
		return lines
	}
	o := s.Config.FindOrg(s.Result.Org.Name)
	if o == nil {
		return lines
	}
	sg, ok := orgSuggestionLocal(ctx, w.opts.Home, s.Config, o)
	if !ok {
		return lines
	}
	_, skipped := mineOf(ctx, s.Config, o, sg)
	unknown := unknownAudienceStores(skipped)
	if len(unknown) == 0 {
		return lines
	}
	rows := make([]string, 0, len(unknown))
	for i, st := range unknown {
		label := ""
		if i == 0 {
			label = whoamiAudienceLabel
		}
		rows = append(rows, fmt.Sprintf("%-*s %s", whoamiLabelWidth, label, repourl.MaskText(unknownAudienceNote(o.Name, w.opts.Version, st))))
	}
	at := len(lines)
	for i, l := range lines {
		if strings.HasPrefix(l, fmt.Sprintf("%-*s ", whoamiLabelWidth, "links")) {
			at = i + 1
			break
		}
	}
	return append(lines[:at:at], append(rows, lines[at:]...)...)
}
