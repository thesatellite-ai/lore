package wire

// trash_doctor.go — the trash row of `lore doctor` on cmd/lore (DELETION
// 3.8, 3.10; opstrash.Doctor): rows past trash.retention_days, the trash
// and tombstone counts, and this machine's safety copies of rows the repo
// lists as purged. The old binary prints the same lines from its own
// doctor (saas doctorTrash), so the core check runs only in the binary
// StandaloneOptionsOrg built (cmd/lore), as doctor_untrusted.go does.

import (
	"context"

	opstrash "github.com/thesatellite-ai/lore/ops/trash"
)

// DoctorCheckTrash is the trash check's name.
const DoctorCheckTrash DoctorCheck = "trash"

// DoctorTrash runs opstrash.Doctor; its lines keep their "trash: " words.
func (a *App) DoctorTrash(ctx context.Context) ([]DoctorFinding, error) {
	if _, standalone := a.opts.OrgCommands.(*standaloneOrg); !standalone {
		return nil, nil // the old binary's own doctor prints this row
	}
	warnings, notes, err := opstrash.Doctor(ctx, a)
	if err != nil {
		return nil, err
	}
	out := make([]DoctorFinding, 0, len(warnings)+len(notes))
	for _, w := range warnings {
		out = append(out, DoctorFinding{Check: DoctorCheckTrash, Level: DoctorWarning, Message: w, Plain: true})
	}
	for _, n := range notes {
		out = append(out, DoctorFinding{Check: DoctorCheckTrash, Level: DoctorNote, Message: n, Plain: true})
	}
	return out, nil
}
