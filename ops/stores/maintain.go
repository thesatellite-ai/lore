package stores

// `lore store maintain` (store growth SG-2, devco:D-996): pack this
// machine's store clones now. lore runs the same pass by itself at most
// once per store.maintenance_every (default a day) in a detached child
// after any command (`lore store maintain --all --due`), so nobody has to
// run it; the command is for "now" and for seeing what it did. Local: it
// reads and packs local clones only, never a row, never the network.
//
//	store.maintain ─► Maintainer.MaintainClones (wire: git.MaintainClone
//	                  per clone; a pass already running = skipped busy)

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpMaintain is the operation name of `lore store maintain`.
const OpMaintain = "store.maintain"

// Maintainer is the optional Manager upgrade behind store.maintain.
type Maintainer interface {
	// KnowsClone reports whether name is a clone this machine has, by
	// its folder under the stores folder (owner/repo), its path or its
	// url, before name is resolved as a store name.
	KnowsClone(ctx context.Context, name string) bool
	MaintainClones(ctx context.Context, in MaintainIn) (MaintainOut, error)
}

// MaintainIn is the input of store.maintain.
type MaintainIn struct {
	Store string `json:"store,omitempty" flag:"store" desc:"one clone: a store name, a store url, or its folder under the stores folder (owner/repo)"`
	All   bool   `json:"all,omitempty" flag:"all" desc:"every store clone on this machine"`
	// Due is the form lore's own background pass runs: only clones whose
	// last pass is older than store.maintenance_every, with one pack
	// thread.
	Due bool `json:"due,omitempty" flag:"due" desc:"only clones whose last pass is older than store.maintenance_every (what lore runs by itself once a day)"`
}

// MaintainSize is what a clone's .git folder holds.
type MaintainSize struct {
	GitBytes   int64 `json:"git_bytes"`
	Packs      int   `json:"packs"`
	PackBytes  int64 `json:"pack_bytes"`
	Loose      int   `json:"loose"`
	LooseBytes int64 `json:"loose_bytes"`
}

// MaintainView is one clone's pass.
type MaintainView struct {
	Store string `json:"store"`
	URL   string `json:"url,omitempty"`
	Clone string `json:"clone"`
	// Skipped is "" when the pass ran; "not_due" or "busy" otherwise.
	Skipped     string       `json:"skipped,omitempty"`
	Before      MaintainSize `json:"before"`
	After       MaintainSize `json:"after"`
	Repacked    bool         `json:"repacked"`
	CommitGraph bool         `json:"commit_graph"`
	// Refs is false when a command held the store lock, so pack-refs and
	// the backup refs wait for the next pass.
	Refs   bool   `json:"refs"`
	TookMS int64  `json:"took_ms"`
	Error  string `json:"error,omitempty"`
}

// MaintainOut is the result of store.maintain.
type MaintainOut struct {
	Clones []MaintainView `json:"clones"`
}

// Text renders one line per clone.
func (o MaintainOut) Text() string {
	if len(o.Clones) == 0 {
		return "no store clone on this machine needed a pass\n"
	}
	var b strings.Builder
	for _, c := range o.Clones {
		switch {
		case c.Error != "":
			fmt.Fprintf(&b, "✗ %s: %s\n", c.Store, c.Error)
		case c.Skipped == "busy":
			fmt.Fprintf(&b, "· %s: skipped, another maintenance pass is running on it\n", c.Store)
		case c.Skipped != "":
			fmt.Fprintf(&b, "· %s: skipped, its last pass is newer than store.maintenance_every\n", c.Store)
		default:
			fmt.Fprintf(&b, "✓ %s: .git %s → %s, %s loose object(s) → %s, %d pack(s) → %d", c.Store,
				HumanBytes(c.Before.GitBytes), HumanBytes(c.After.GitBytes),
				strconv.Itoa(c.Before.Loose), strconv.Itoa(c.After.Loose), c.Before.Packs, c.After.Packs)
			if c.CommitGraph {
				b.WriteString(", commit-graph written")
			}
			fmt.Fprintf(&b, " (%s)\n", (time.Duration(c.TookMS) * time.Millisecond).Round(10*time.Millisecond))
		}
	}
	return b.String()
}

// Failure makes a run where a clone failed exit non-zero after printing
// every line (op.Failer).
func (o MaintainOut) Failure() error {
	for _, c := range o.Clones {
		if c.Error != "" {
			return errs.New(errs.Internal, "store maintain: "+c.Store+": "+c.Error)
		}
	}
	return nil
}

// humanBytes prints n as B, KB, MB or GB (base 1024, one decimal).
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	v, i := float64(n)/unit, 0
	for v >= unit && i < 2 {
		v /= unit
		i++
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + []string{"KB", "MB", "GB"}[i]
}

// maintainOp is store.maintain bound to m.
func maintainOp(m Manager) op.Entry {
	return op.Define(op.Spec{
		Name:    OpMaintain,
		Summary: "Pack this machine's store clones now (lore does it by itself at most once a day): repack loose objects, prune old unreachable ones, write the commit-graph",
		// Read: it changes how git stores the clone's objects, never a row,
		// a ref of the store or anything on the remote.
		Permission: op.PermRead, Access: op.AccessRead, Errors: cloneErrors, Local: true,
		Examples: []op.Example{
			{Title: "every clone", CLI: "lore store maintain --all", Want: ""},
			{Title: "one store", CLI: "lore store maintain --store git@github.com:agency/notes", Want: ""},
		},
	}, func(ctx context.Context, in MaintainIn) (MaintainOut, error) {
		mt, ok := upgrade.As[Maintainer](m)
		if !ok {
			return MaintainOut{}, errs.New(errs.Unsupported, "store maintain: this build keeps no store clones")
		}
		in.Store = strings.TrimSpace(in.Store)
		switch {
		case in.Store == "" && !in.All:
			return MaintainOut{}, errs.New(errs.InvalidInput, "store maintain: name a store or ask for every clone").
				WithHint("lore store maintain --all, or lore store maintain --store <name>")
		case in.Store != "" && in.All:
			return MaintainOut{}, errs.New(errs.InvalidInput, "store maintain: --store and --all name different sets; give one").
				WithHint("lore store maintain --all, or lore store maintain --store " + in.Store)
		}
		if in.Store != "" && !mt.KnowsClone(ctx, in.Store) {
			e, named, err := storeNamed(ctx, m, "maintain", in.Store)
			if err != nil {
				return MaintainOut{}, err
			}
			if named {
				in.Store = e.URL
			}
		}
		out, err := mt.MaintainClones(ctx, in)
		if out.Clones == nil {
			out.Clones = []MaintainView{}
		}
		return out, err
	})
}
