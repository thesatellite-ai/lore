// doctor_machines.go — doctor's shared-machine-id check (AGENT_IDENTITY
// 6.8), called from doctor.go (owned by the coordinator):
//
//	doctorSharedMachineIDs → coreApp.SharedMachineIDs → one warning per id
package main

import (
	"context"
	"fmt"
	"strings"
)

// shortMachineLen is mch_ plus 8 hex, the display form (PROV4).
const shortMachineLen = len("mch_") + 8

// doctorSharedMachineIDs warns for each machine id whose profiles name two
// or more hosts: a cloned VM image that kept its OS machine id ("two
// machines share an OS machine id; give the clone a new one", 6.8), or a
// host renamed since a store's copy of the profile was written.
func doctorSharedMachineIDs(ctx context.Context, r *doctorReport) {
	if coreApp == nil {
		return
	}
	shared, err := coreApp.SharedMachineIDs(ctx)
	if err != nil {
		return // the machines read failing is reported by the store checks
	}
	for _, s := range shared {
		id := s.ID
		if len(id) > shortMachineLen {
			id = id[:shortMachineLen]
		}
		mine := ""
		if s.This {
			mine = " (this machine)"
		}
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"machine %s%s is reported by hosts %s: two machines share an OS machine id; give the clone a new one (regenerate /etc/machine-id). If one host was only renamed, its next write refreshes the profile",
			id, mine, strings.Join(s.Hosts, ", ")))
		degrade(r)
	}
}
