package wire

// doctor_local.go — three `lore doctor` rows over what only this machine
// holds, moved from the old binary (saas/cmd/cli doctor_local.go) into the
// core so cmd/lore prints them (later 15.1; FB-DOC-2..4). Lines are the
// v0.1.17 text word for word (DoctorFinding.Plain):
//
//	DoctorTimeOutbox          run time queued in local.db time_outbox (TIME 4.3, 11.9): warning
//	DoctorPurgedCopies        v0.1.17 safety copies of rows _purged.json lists (DELETION 3.10): warning
//	DoctorUnclassifiedSettings v0.1.17 settings left in place (CFG12): note
//
// All three only read: doctor never writes the outbox, drops a copy or
// moves a setting, and it never creates local.db just to look. Like the
// machine rows, they run only in the binary StandaloneOptionsOrg built
// (the old binary prints them from its own doctor host).

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/thesatellite-ai/lore/store/localstate"
)

// The checks of this file (the JSON "check" names).
const (
	DoctorCheckTimeOutbox   DoctorCheck = "time_outbox"
	DoctorCheckPurgedCopies DoctorCheck = "purged_copies"
	DoctorCheckOldSettings  DoctorCheck = "unclassified_settings"
	doctorAgoHourThreshold              = time.Hour
	doctorPurgedCopiesNext              = "lore trash purge --local --purged --confirm"
	doctorOldSettingSuffix              = " (it has no effect in 0.1.18; set the new key with lore config set or lore policy set)"
)

// DoctorTimeOutbox warns when run time spans wait in this machine's
// time_outbox: they exist nowhere else until a write reaches their store,
// so a lost machine loses them. No local.db: no line (never created here).
func (a *App) DoctorTimeOutbox(ctx context.Context) ([]DoctorFinding, error) {
	if !a.coreOwnsMachineRows() {
		return nil, nil
	}
	if _, err := os.Stat(LocalStatePath(a.opts.Home)); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	warn := func(msg string) []DoctorFinding {
		return []DoctorFinding{{Check: DoctorCheckTimeOutbox, Level: DoctorWarning, Plain: true, Message: msg}}
	}
	ls, err := a.LocalState(ctx)
	if err != nil {
		return warn("time_outbox: this machine's local state could not be opened: " + err.Error()), nil
	}
	defer func() { _ = ls.Close() }() // read only; a close error loses nothing
	entries, err := ls.List(ctx, localstate.TimeOutbox, "")
	if err != nil {
		return warn("time_outbox: the queued run time could not be read: " + err.Error()), nil
	}
	return timeOutboxFindings(entries, time.Now()), nil
}

// timeOutboxFindings is the warning for entries queued as of now (none
// for an empty outbox).
func timeOutboxFindings(entries []localstate.Entry, now time.Time) []DoctorFinding {
	if len(entries) == 0 {
		return nil
	}
	oldest := entries[0].UpdatedAt
	for _, e := range entries[1:] {
		if e.UpdatedAt.Before(oldest) {
			oldest = e.UpdatedAt
		}
	}
	return []DoctorFinding{{Check: DoctorCheckTimeOutbox, Level: DoctorWarning, Plain: true, Message: fmt.Sprintf(
		"time_outbox: %d run time span(s) wait on this machine only (oldest queued %s ago): their task's store was unreachable; run lore sync to write them",
		len(entries), agoText(now.Sub(oldest)))}}
}

// DoctorPurgedCopies warns when this checkout's v0.1.17 safety copies
// (lore.db _lore_sync_trash) still hold rows the repo's _purged.json
// lists: the purged content survives here. A dry run; an unreadable
// _purged.json is a warning, never "nothing purged". No repo, or no
// lore.db: no line.
func (a *App) DoctorPurgedCopies(ctx context.Context) ([]DoctorFinding, error) {
	if !a.coreOwnsMachineRows() {
		return nil, nil
	}
	top, err := a.repoTop(ctx)
	if err != nil || top == "" {
		return nil, nil // no repo: no checkout copies
	}
	copies, err := readLocalTrash(ctx, filepath.Join(top, filepath.FromSlash(v017LoreDB)))
	if err == nil && len(copies) == 0 {
		return nil, nil
	}
	var set map[string]bool
	if err == nil {
		set, err = a.RepoPurged(ctx)
	}
	if err != nil {
		return []DoctorFinding{{Check: DoctorCheckPurgedCopies, Level: DoctorWarning, Plain: true,
			Message: "local copies of purged rows could not be checked: " + err.Error()}}, nil
	}
	n := 0
	for _, c := range copies {
		if set[c.RowID] {
			n++
		}
	}
	if n == 0 {
		return nil, nil
	}
	return []DoctorFinding{{Check: DoctorCheckPurgedCopies, Level: DoctorWarning, Plain: true,
		Message: fmt.Sprintf("%d local cop(ies) of purged rows on this machine; drop them with %s", n, doctorPurgedCopiesNext)}}, nil
}

// DoctorUnclassifiedSettings notes each v0.1.17 setting lore cannot
// classify (CFG12: reported, left in place, never guessed). A failing read
// is a note too; the store checks report the cause.
func (a *App) DoctorUnclassifiedSettings(ctx context.Context) ([]DoctorFinding, error) {
	if !a.coreOwnsMachineRows() {
		return nil, nil
	}
	lines, err := a.UnclassifiedSettings(ctx)
	if err != nil {
		return []DoctorFinding{{Check: DoctorCheckOldSettings, Level: DoctorNote, Plain: true, Message: "v0.1.17 settings not checked: " + err.Error()}}, nil
	}
	out := make([]DoctorFinding, 0, len(lines))
	for _, l := range lines {
		out = append(out, DoctorFinding{Check: DoctorCheckOldSettings, Level: DoctorNote, Plain: true,
			Message: "v0.1.17 setting not moved: " + l + doctorOldSettingSuffix})
	}
	return out, nil
}
