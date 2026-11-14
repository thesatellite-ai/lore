package doctor

// fleet.go — the repos `lore doctor --all` found in the setups' folders
// (K-10). The setup checks run over the known repos (reposBySetup); the
// folders can hold more: a repo on the lore 0.1.17 format no lore 0.1.18
// command ran in yet, a repo lore never ran in, repos with no lore data.
// The same discovery as lore fix --all (FleetEnv) names them, so a person
// can tell every repo was seen and what each needs next:
//
//	repos in the setups' folders: 3 with lore data (1 checked above), 1 on the lore 0.1.17 format, 8 without lore data
//	  <repo>  on the lore 0.1.17 format; to upgrade it here: cd <repo> && lore fix --migrate (…)
//	  <repo>  not checked: lore has not run in it on this machine; lore fix --all checks it
//	  8 repo(s) without lore data, left as they are (lore doctor --all --verbose lists them)
//
// The block is a note: it never changes the status (lore fix --all counts
// a 0.1.17-format repo apart, never as a failure).

import (
	"context"
	"fmt"
	"strings"
)

// FleetEnv is the optional upgrade of Env that finds the repos of the
// setups' folders as lore fix --all does (*wire.App implements it).
type FleetEnv interface {
	DoctorFleet(ctx context.Context) (Fleet, error)
}

// Fleet is what --all found in the setups' folders.
type Fleet struct {
	// Repos are the repos with lore data, sorted.
	Repos []FleetRepo `json:"repos"`
	// NoLore are the git repos with no lore data (never checked, never
	// written).
	NoLore []string `json:"no_lore,omitempty"`
}

// FleetRepo is one repo with lore data.
type FleetRepo struct {
	Path string `json:"path"`
	// Checked: the setup checks above ran over it (a known repo).
	Checked bool `json:"checked"`
	// FormatOld: it is on the lore 0.1.17 format; Next is its step.
	FormatOld bool `json:"format_old,omitempty"`
	// Next is what the person runs next ("" when checked and current).
	Next string `json:"next,omitempty"`
}

// fleetNotChecked is the step of a repo with lore data the setup checks
// did not cover.
const fleetNotChecked = "not checked: lore has not run in it on this machine; lore fix --all checks it (or run lore doctor in it)"

// fleetOf marks the repos the setup checks covered and gives every other
// one its next step.
func fleetOf(f Fleet, checked map[string]bool) Fleet {
	for i := range f.Repos {
		r := &f.Repos[i]
		r.Checked = checked[r.Path] && !r.FormatOld
		if !r.Checked && !r.FormatOld && r.Next == "" {
			r.Next = fleetNotChecked
		}
	}
	return f
}

// fleetText is the block of the --all text ("" when nothing was found).
func fleetText(f Fleet, verbose bool) string {
	if len(f.Repos) == 0 && len(f.NoLore) == 0 {
		return ""
	}
	checked, old := 0, 0
	for _, r := range f.Repos {
		switch {
		case r.FormatOld:
			old++
		case r.Checked:
			checked++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "repos in the setups' folders: %d with lore data (%d checked above)", len(f.Repos), checked)
	if old > 0 {
		fmt.Fprintf(&b, ", %d on the lore 0.1.17 format", old)
	}
	fmt.Fprintf(&b, ", %d without lore data\n", len(f.NoLore))
	for _, r := range f.Repos {
		switch {
		case r.FormatOld:
			fmt.Fprintf(&b, "  %s  on the lore 0.1.17 format; %s\n", r.Path, r.Next)
		case !r.Checked:
			fmt.Fprintf(&b, "  %s  %s\n", r.Path, r.Next)
		case verbose:
			fmt.Fprintf(&b, "  %s  checked\n", r.Path)
		}
	}
	switch {
	case len(f.NoLore) > 0 && verbose:
		for _, p := range f.NoLore {
			fmt.Fprintf(&b, "  %s  no lore data, left as it is\n", p)
		}
	case len(f.NoLore) > 0:
		fmt.Fprintf(&b, "  %d repo(s) without lore data, left as they are (lore doctor --all --verbose lists them)\n", len(f.NoLore))
	}
	return b.String()
}
