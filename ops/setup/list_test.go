package setup

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// problemEnv is fakeEnv with config.yaml problems, and a store whose
// Config fails when cfgErr is set (a file that does not decode).
type problemEnv struct {
	fakeEnv
	problems []ConfigProblem
	cfgErr   error
}

func (p problemEnv) ConfigProblems(context.Context) []ConfigProblem { return p.problems }

func (p problemEnv) SetupStore(ctx context.Context) (Store, error) {
	if p.cfgErr != nil {
		return failingConfigStore{fakeStore: p.store, err: p.cfgErr}, nil
	}
	return p.fakeEnv.SetupStore(ctx)
}

// failingConfigStore answers Config with err.
type failingConfigStore struct {
	*fakeStore
	err error
}

func (f failingConfigStore) Config(context.Context) (onboard.Config, error) {
	return onboard.Config{}, f.err
}

// setup.list with an invalid config.yaml (realmac #23): problems ride
// along with the setups; a file that does not decode gives an empty list
// with the problems instead of an error; any other failure stays an error.
func TestListConfigProblems(t *testing.T) {
	bad := []ConfigProblem{{File: "~/.lore/config.yaml", Line: 2, Key: "identity.key", Problem: "must be human:<email>", Fix: "edit line 2"}}
	personal := onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, GitHub: onboard.SetupGitHub{User: "someone"}}
	invalid := errs.New(errs.ConfigInvalid, "config.yaml is not valid YAML")
	other := errs.New(errs.Internal, "disk")
	for _, c := range []struct {
		name       string
		problems   []ConfigProblem
		cfgErr     error
		wantErr    bool
		wantSetups int
		wantProbs  int
	}{
		{"valid", nil, nil, false, 1, 0},
		{"problems, setups still read", bad, nil, false, 1, 1},
		{"does not decode", bad, invalid, false, 0, 1},
		{"invalid but no located problem", nil, invalid, true, 0, 0},
		{"another failure", bad, other, true, 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := problemEnv{fakeEnv: newFakeEnv(personal), problems: c.problems, cfgErr: c.cfgErr}
			out, err := newEngine(env, onboardCatalog{}).List(bg, ListIn{})
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, want error %v", err, c.wantErr)
			}
			if err != nil {
				return
			}
			if len(out.Setups) != c.wantSetups || len(out.ConfigProblems) != c.wantProbs || out.Setups == nil {
				t.Fatalf("setups %d (nil %v), problems %d; want %d, %d", len(out.Setups), out.Setups == nil, len(out.ConfigProblems), c.wantSetups, c.wantProbs)
			}
		})
	}
}

// unfinishedSteps (realmac #22): an entry without a GitHub account (an
// imported one) has the GitHub step unfinished; with one, nothing.
func TestUnfinishedSteps(t *testing.T) {
	for _, c := range []struct {
		name string
		gh   onboard.SetupGitHub
		want int
	}{
		{"no github block", onboard.SetupGitHub{}, 1},
		{"alias only (import from a store url)", onboard.SetupGitHub{HostAlias: "github-acme"}, 1},
		{"blank user", onboard.SetupGitHub{User: "  "}, 1},
		{"account set", onboard.SetupGitHub{User: "someone"}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := unfinishedSteps(onboard.SetupEntry{Name: "x", Kind: onboard.KindSolo, GitHub: c.gh})
			if len(got) != c.want || (c.want == 1 && got[0] != onboard.StepGitHub) {
				t.Fatalf("unfinished = %v, want %d (github)", got, c.want)
			}
		})
	}
}
