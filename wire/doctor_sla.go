package wire

import (
	"context"
	"strconv"
	"strings"

	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// DoctorCheckSLA is the SLA line of `lore doctor` (TASK 18.4: SLA state is
// computed on read in list, show, inbox, mission status and doctor).
const DoctorCheckSLA DoctorCheck = "sla"

// DoctorSLA lists, per attached store, its open tasks whose SLA (the
// store's tasks.sla.<priority> policy) is breached or at risk: one note
// each, never a warning (a late task is work, not an unhealthy store).
// Nothing when no store sets an SLA.
func (a *App) DoctorSLA(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	now := a.Now()
	var out []DoctorFinding
	for _, src := range a.Stores() {
		set, perr := st.eng.Policy(src)
		if perr != nil {
			return nil, perr
		}
		hours := opstask.SLAHours(set)
		if len(hours) == 0 {
			continue
		}
		rows, lerr := a.List(ctx, src, schema.TableTasks)
		if lerr != nil {
			return nil, lerr
		}
		atRisk, breached := opstask.SLALate(rows, hours, now)
		for _, g := range []struct {
			ids  []string
			what string
		}{{breached, "breached their SLA"}, {atRisk, "are at risk of their SLA (80% of the time used)"}} {
			if len(g.ids) == 0 {
				continue
			}
			out = append(out, DoctorFinding{Check: DoctorCheckSLA, Level: DoctorNote, Store: src,
				Message: strconv.Itoa(len(g.ids)) + " open task(s) " + g.what + ": " + strings.Join(g.ids, ", "), // text output caps the list (listcap), --json keeps it whole
				Fix:     "lore task list --where 'sla:at_risk,breached'"})
		}
	}
	return out, nil
}
