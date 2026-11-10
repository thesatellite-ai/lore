package wire

// doctor_identitysource.go — the doctor side of SOURCE_OF_TRUTH row 1
// (identity_source.go holds the writes).
//
//	lore doctor            ─► DoctorIdentitySource: key-less setup blocks, and the
//	                          top-level identity: against the active solo setup
//	lore doctor --all /    ─► identity.top_level       (Run it: --keep setup)
//	setup page Doctor         identity.top_level_keep  (Run it: --keep top; a conflict only)
//
// With two different identities neither is picked: the finding names both
// and offers one command per value.

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// DoctorCheckIdentitySource is the `lore doctor` check of who-you-are
// copies in config.yaml.
const DoctorCheckIdentitySource DoctorCheck = "identity_source"

// topLevelState is the top-level identity measured against one setup.
type topLevelState struct {
	verdict  identity.SourceVerdict
	top, own identity.Entry
	// heldBy is another enabled solo setup whose identity equals the top
	// level exactly: the copy is that setup's leftover, not this one's
	// conflict ("" when none, or when this setup already holds it).
	heldBy string
}

// topLevelOf compares c's top-level identity with solo setup s.
func topLevelOf(c onboard.Config, s onboard.SetupEntry) topLevelState {
	st := topLevelState{top: entryOf(c.TopIdentity), own: entryOf(s.Identity)}
	st.verdict = identity.Decide(st.top, st.own)
	if st.verdict == identity.SourceSame || st.verdict == identity.SourceNone {
		return st
	}
	for _, o := range c.Setups {
		if o.Name != s.Name && o.Kind == onboard.KindSolo && !o.Disabled && identity.Decide(st.top, entryOf(o.Identity)) == identity.SourceSame {
			st.heldBy = o.Name
			break
		}
	}
	return st
}

// DoctorIdentitySource reports every solo setup identity block without a
// key, and the top-level identity: beside the active solo setup: a
// leftover copy (one fix), or two different identities (two fixes, one
// per value; never picked).
func (a *App) DoctorIdentitySource(ctx context.Context) ([]DoctorFinding, error) {
	c, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return nil, err
	}
	var out []DoctorFinding
	for _, p := range keylessSetupProblems(c.Setups) {
		out = append(out, DoctorFinding{Check: DoctorCheckIdentitySource, Level: DoctorWarning, Message: p})
	}
	res, err := a.SetupResolution(ctx, "")
	if err != nil {
		return out, err
	}
	if res.Active.Name == "" || res.Entry.Kind != onboard.KindSolo {
		return out, nil
	}
	if f, ok := topLevelFinding(c, res.Entry); ok {
		out = append(out, f)
	}
	return out, nil
}

// topLevelFinding is the doctor line for the top-level identity beside
// solo setup s; false when there is none to report.
func topLevelFinding(c onboard.Config, s onboard.SetupEntry) (DoctorFinding, bool) {
	st := topLevelOf(c, s)
	f := DoctorFinding{Check: DoctorCheckIdentitySource, Level: DoctorWarning}
	switch {
	case st.verdict == identity.SourceNone:
		return f, false
	case st.heldBy != "":
		f.Message = "the top-level identity: (" + st.top.String() + ") is an old copy of setup " + st.heldBy + "'s identity; the setup is the one source of who you are now"
		f.Fix = migrateCmd(identity.KeepSetup, st.heldBy)
	case st.verdict == identity.SourceSame:
		f.Message = "the top-level identity: (" + st.top.String() + ") is an old copy of setup " + s.Name + "'s identity; the setup is the one source of who you are now"
		f.Fix = migrateCmd(identity.KeepSetup, s.Name)
	case st.verdict == identity.SourceFill:
		f.Message = "the top-level identity: (" + st.top.String() + ") still holds what setup " + s.Name + "'s identity (" + st.own.String() + ") lacks; the setup is the one source of who you are now"
		f.Fix = migrateCmd(identity.KeepSetup, s.Name)
	default:
		f.Message = "the top-level identity: says " + st.top.String() + " and setup " + s.Name + " says " + st.own.String() +
			"; lore uses the setup's and never picks for you: keep one"
		f.Fix = migrateCmd(identity.KeepSetup, s.Name) + " (keeps " + st.own.String() + "), or " +
			migrateCmd(identity.KeepTop, s.Name) + " (keeps " + st.top.String() + ")"
	}
	return f, true
}

// Probe details of the two catalog checks.
const (
	detailNoTopIdentity  = "no top-level identity: in ~/.lore/config.yaml"
	detailNoDisagreement = "the top-level identity: and this setup's do not disagree"
	detailTeamIdentity   = "a member or admin setup's identity is its org entry's"
)

// topLevelIdentity probes identity.top_level and identity.top_level_keep
// for r.Setup (solo only; the catalog limits both to solo).
func (sp setupProber) topLevelIdentity(r opssetup.ProbeRequest) opssetup.ProbeResult {
	if r.Setup.Kind != onboard.KindSolo {
		return skip(detailTeamIdentity)
	}
	c, err := ReadSetupsConfig(sp.a.opts.Home)
	if err != nil {
		return result(onboard.StatusError, err.Error(), nil)
	}
	s := r.Setup
	if cur, ok := c.Setup(s.Name); ok {
		s = cur // the file as it is now (a Run it just changed it)
	}
	st := topLevelOf(c, s)
	vars := map[string]string{onboard.VarTopIdentity: st.top.String(), onboard.VarSetupIdentity: st.own.String()}
	conflict := st.verdict == identity.SourceConflict && st.heldBy == ""
	if r.Check.ID == onboard.CheckIdentityTopLevelKeep {
		if !conflict {
			return skip(detailNoDisagreement)
		}
		return result(onboard.StatusFail, "the top-level identity: says "+st.top.String()+", this setup says "+st.own.String(), vars)
	}
	switch {
	case st.verdict == identity.SourceNone:
		return result(onboard.StatusOK, detailNoTopIdentity, nil)
	case st.heldBy != "":
		return result(onboard.StatusOK, "the top-level identity: is setup "+st.heldBy+"'s; its own check offers the fix", nil)
	case conflict:
		vars[onboard.VarFixVariant] = string(onboard.FixIdentityConflict)
		return result(onboard.StatusFail, "the top-level identity: says "+st.top.String()+", this setup says "+st.own.String(), vars)
	}
	return result(onboard.StatusFail, "the top-level identity: ("+st.top.String()+") is an old copy beside this setup's", vars)
}

// topIdentityStamp is the top-level identity as the doctor cache stamp
// sees it: a keep fix or a migrating write changes it, so the cached
// identity.top_level results are not served after it.
func topIdentityStamp(home string) string {
	mc, err := readMachineConfig(home)
	if err != nil {
		return stampUnreadable
	}
	return mc.Identity.Key + "\x00" + mc.Identity.Name + "\x00" + mc.Identity.Handle
}
