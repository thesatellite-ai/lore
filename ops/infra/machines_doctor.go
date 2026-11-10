package infra

// Doctor checks over the machine profiles (AGENT_IDENTITY_DESIGN 6.8, OQ7).
// Flow:
//
//	SharedMachineIDs(env, me)
//	  → ListAll(machines) in every attached store
//	  → per full mch_ id: the distinct non-empty hostnames across stores
//	    (+ this machine's current hostname for its own id)
//	  → two or more = one OS machine id on two hosts (a cloned VM image
//	    that kept /etc/machine-id), or a host renamed since that store's
//	    copy was written; doctor words it as both

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/schema"
)

// fieldHostname is the machines column doctor compares.
const fieldHostname = "hostname"

// SharedMachine is one machine id seen with more than one host name.
type SharedMachine struct {
	// ID is the full machine id (doctor shows mch_ + 8 hex).
	ID string
	// Hosts are the distinct host names, sorted.
	Hosts []string
	// This marks this machine's own id (its current host name is one of
	// Hosts).
	This bool
}

// SharedMachineIDs lists the machine ids whose profiles name two or more
// hosts (6.8: "a cloned VM image that kept its /etc/machine-id shows up as
// one machine whose host keeps changing"). The rows hold only the newest
// host per store, so this sees a clash across stores, or between this
// machine's own row and its current host name; a clone writing to one
// store only shows up from the other host. me may be the zero value (no
// machine id here): then only the cross-store check runs.
func SharedMachineIDs(ctx context.Context, env Env, me ThisMachine) ([]SharedMachine, error) {
	rows, err := work.ListAll(ctx, env, schema.TableMachines, "")
	if err != nil {
		return nil, err
	}
	seen := make([]idHost, 0, len(rows))
	for _, s := range rows {
		seen = append(seen, idHost{ID: work.Field(s.Row, schema.FieldMachineID), Host: work.Field(s.Row, fieldHostname)})
	}
	mine := idHost{}
	if !me.ID.IsZero() {
		mine = idHost{ID: me.ID.String(), Host: textField(me.Fields, fieldHostname)}
	}
	return sharedMachines(seen, mine), nil
}

// idHost is one profile's machine id and host name.
type idHost struct{ ID, Host string }

// sharedMachines folds the profiles per id; mine (this machine's id and
// current host) counts only when some store holds a profile for that id,
// so a machine that never wrote a profile is not compared with nothing.
func sharedMachines(seen []idHost, mine idHost) []SharedMachine {
	hosts := map[string][]string{}
	add := func(id, h string) {
		if id == "" || h == "" || slices.Contains(hosts[id], h) {
			return
		}
		hosts[id] = append(hosts[id], h)
	}
	known := false
	for _, p := range seen {
		add(p.ID, p.Host)
		known = known || (mine.ID != "" && p.ID == mine.ID)
	}
	if known {
		add(mine.ID, mine.Host)
	}
	var out []SharedMachine
	for id, hs := range hosts {
		if len(hs) < 2 {
			continue
		}
		slices.Sort(hs)
		out = append(out, SharedMachine{ID: id, Hosts: hs, This: id == mine.ID})
	}
	slices.SortFunc(out, func(a, b SharedMachine) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// textField is a text profile field as a string, "" when absent.
func textField(f map[string]value.Value, k string) string {
	if v, ok := f[k].(value.Text); ok {
		return v.String()
	}
	return ""
}
