package setup

// rename.go — setup.rename (owner report 2026-11-08: a setup opened from
// the list is edited, and a new name on its kind step is a rename, not a
// second setup). The setups[] entry takes the new name with everything
// else it holds, its repo pins follow, and its setup_progress rows (an
// unfinished setup) move with it. Never touches a store, a repo or orgs[].
//
//	from == to ─▶ unchanged
//	no setup from ─▶ E_SETUP_NOT_FOUND
//	bad name ─▶ E_INVALID_INPUT
//	a setup named to, or progress rows left under to (a trashed setup) ─▶ E_SETUP_NAME_TAKEN
//	SetupRenamer.RenameSetup (config.yaml) ─▶ progress rows moved

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// OpRename is setup.rename (CLI and REST; not on the page allowlist:
// the page renames through the kind step's setup.apply).
const OpRename = "setup.rename"

// SetupRenamer is the optional Store upgrade that renames a setups[]
// entry in config.yaml and moves its repo pins (wire's store has it).
// found=false when there is no such setup; E_SETUP_NAME_TAKEN when to is
// in use.
type SetupRenamer interface {
	RenameSetup(ctx context.Context, from, to string) (found bool, err error)
}

// RenameIn is setup.rename's input.
type RenameIn struct {
	Name string `json:"name" arg:"0" desc:"the setup name now"`
	To   string `json:"to" arg:"1" desc:"the new name (snake_case or kebab-case)"`
}

// RenameOut is setup.rename's output.
type RenameOut struct {
	From string `json:"from"`
	Name string `json:"name"`
	// Changed is false when the new name is the old one.
	Changed bool `json:"changed"`
}

// Text implements op.Texter.
func (o RenameOut) Text() string {
	if !o.Changed {
		return "setup " + o.Name + " already has that name\n"
	}
	return "renamed setup " + o.From + " to " + o.Name + "; its repo pins and saved progress follow it\n"
}

// Rename is setup.rename.
func (e engine) Rename(ctx context.Context, in RenameIn) (RenameOut, error) {
	from := setupName(ctx, in.Name)
	if err := requireName(from); err != nil {
		return RenameOut{}, err
	}
	to := strings.TrimSpace(in.To)
	if err := requireName(to); err != nil {
		return RenameOut{}, err
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return RenameOut{}, err
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return RenameOut{}, err
	}
	if _, ok := cfg.Setup(from); !ok {
		return RenameOut{}, notFound(from, cfg)
	}
	if from == to {
		return RenameOut{From: from, Name: to}, nil
	}
	if err := e.renameSetup(ctx, st, from, to); err != nil {
		return RenameOut{}, err
	}
	return RenameOut{From: from, Name: to, Changed: true}, nil
}

// renameSetup is the rename that setup.rename and the kind step's save (a
// new name for a setup that exists) both run: the same checks, then the
// config entry and its pins (when the entry is written yet: a member or
// admin setup being made has only progress rows), then the progress rows.
// The caller has made sure from differs from to.
func (e engine) renameSetup(ctx context.Context, st Store, from, to string) error {
	rn, ok := st.(SetupRenamer)
	if !ok {
		return errs.New(errs.Unsupported, "this lore cannot rename a setup").WithHint("edit the name in ~/.lore/config.yaml by hand")
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return err
	}
	if _, taken := cfg.Setup(to); taken {
		return nameTakenErr(to)
	}
	stale, err := st.Progress(ctx, to)
	if err != nil {
		return err
	}
	if len(stale) > 0 {
		// A trashed setup of that name keeps its progress rows until the
		// purge; taking its name would hand them to this setup.
		return errs.New(errs.SetupNameTaken, "setup "+quote(to)+" is in the trash with saved progress; renaming onto it would mix the two").
			WithHint("pick another name, or lore setup trash purge --name "+to+" --confirm first").
			WithNext("lore", "setup", "trash", "list")
	}
	rows, err := st.Progress(ctx, from)
	if err != nil {
		return err
	}
	if _, entry := cfg.Setup(from); entry {
		if _, err := rn.RenameSetup(ctx, from, to); err != nil {
			return err
		}
	} else if len(rows) == 0 {
		// Neither an entry nor saved progress: nothing has that name.
		return notFound(from, cfg)
	}
	for _, r := range rows {
		r.Setup = to
		if err := st.PutProgress(ctx, r); err != nil {
			return err
		}
	}
	if len(rows) > 0 {
		return st.DeleteProgress(ctx, from)
	}
	return nil
}

// nameTakenErr is E_SETUP_NAME_TAKEN for a name another setup holds: the
// one rule the kind step's save, setup.rename and the setup.name_free
// check share (a setup's own name is never a clash).
func nameTakenErr(name string) error {
	return errs.New(errs.SetupNameTaken, "another setup is already named "+quote(name)).
		WithHint("pick another name; lore setup list shows the names in use").
		WithNext("lore", "setup", "list")
}
