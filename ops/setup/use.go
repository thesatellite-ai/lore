package setup

// use.go — setup.use (ONBOARDING_DESIGN 7.1, 3.3): pin this repo to a
// setup (--here: repo_setups, machine-only like repo_orgs), clear the pin
// (--here --clear), or give the shell line that picks it for one shell
// (LORE_SETUP). The shell scope writes nothing: an op cannot change its
// caller's environment.

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Use is setup.use.
func (e engine) Use(ctx context.Context, in UseIn) (UseOut, error) {
	if in.Clear && !in.Here {
		return UseOut{}, errs.New(errs.InvalidInput, "--clear removes this repo's pin and goes with --here").
			WithNext("lore", "setup", "use", "--here", "--clear")
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return UseOut{}, err
	}
	if in.Clear {
		if in.Name != "" {
			return UseOut{}, errs.New(errs.InvalidInput, "--here --clear takes no setup name").
				WithNext("lore", "setup", "use", "--here", "--clear")
		}
		if _, err := st.PinRepo(ctx, ""); err != nil {
			return UseOut{}, err
		}
		return UseOut{Scope: UseScopeRepo}, nil
	}
	if err := requireName(in.Name); err != nil {
		return UseOut{}, err
	}
	if _, err := e.entry(ctx, in.Name); err != nil {
		return UseOut{}, err
	}
	if in.Here {
		if _, err := st.PinRepo(ctx, in.Name); err != nil {
			return UseOut{}, err
		}
		return UseOut{Name: in.Name, Scope: UseScopeRepo}, nil
	}
	// requireName made the name shell-safe (a-z, 0-9, _ and -).
	return UseOut{Name: in.Name, Scope: UseScopeShell, Line: "export " + EnvSetup + "=" + in.Name}, nil
}

// Text is setup.use for a person: the shell scope prints only the line, so
// it can be pasted or run as eval "$(lore setup use <name>)"; a repo pin
// says what it did (an empty name was the cleared pin).
func (o UseOut) Text() string {
	switch {
	case o.Scope == UseScopeShell:
		return o.Line + "\n"
	case o.Name == "":
		return "cleared this repo's setup pin; lore setup list shows the setup lore picks here now\n"
	}
	return "pinned this repo to setup " + o.Name + " (this machine only; lore setup use --here --clear removes the pin)\n"
}
