package wire

// setupenv.go — *App implements ops/setup.Onboard (ONBOARDING_DESIGN 2.1,
// L7 wire row): the four ports the setup ops call, built over config.yaml,
// machine.db, adapters/probe and exec.
//
//	SetupStore     ─▶ setupStore     (setupstore.go)
//	SetupProber    ─▶ setupProber    (setupprobe.go, one probe.Prober per call)
//	SetupApplier   ─▶ setupApplier   (setupapply.go)
//	SetupFixRunner ─▶ setupFixRunner (setupfix.go)

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/ops/team"
)

var (
	_ opssetup.Onboard        = (*App)(nil)
	_ opsdoctor.SetupEnv      = (*App)(nil)
	_ opsdoctor.CacheEnv      = (*App)(nil)
	_ opssetup.HealthSource   = (*App)(nil)
	_ opssetup.HealthRecorder = (*App)(nil)
	_ team.Env                = (*App)(nil)
)

// probeTmpDir is the folder under the lore home that holds the write
// test's temp bare repo (ONB 4.4 "~/.lore/tmp/").
const probeTmpDir = "tmp"

// SetupStore implements opssetup.Onboard.
func (a *App) SetupStore(context.Context) (opssetup.Store, error) { return setupStore{a: a}, nil }

// SetupProber implements opssetup.Onboard. Each call builds one
// probe.Prober over the App's one limiter, so at most
// probe.DefaultConcurrency probes run at once in this process however
// many op calls overlap (ONB 6.1).
func (a *App) SetupProber(ctx context.Context) (opssetup.Prober, error) {
	p, err := a.onboardProbe(ctx)
	if err != nil {
		return nil, err
	}
	return setupProber{a: a, p: p}, nil
}

// SetupApplier implements opssetup.Onboard.
func (a *App) SetupApplier(context.Context) (opssetup.Applier, error) { return setupApplier{a: a}, nil }

// SetupFixRunner implements opssetup.Onboard.
func (a *App) SetupFixRunner(context.Context) (opssetup.FixRunner, error) {
	return setupFixRunner{lore: os.Executable}, nil
}

// onboardProbe returns a probe.Prober: the user's home (~/.ssh,
// tool folders), ~/.lore/tmp for the write test, and doctor.probe_timeout_s
// for git and ssh (7.3).
func (a *App) onboardProbe(ctx context.Context) (*probe.Prober, error) {
	home, err := a.SkillHome(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return nil, err
	}
	// writableHome: a read-only lore home (a CI runner, F-6) puts the
	// probe's temp repos in the scratch home, so the check still runs
	tmp := filepath.Join(writableHome(a.opts.Home), probeTmpDir)
	if err = os.MkdirAll(tmp, 0o700); err != nil {
		return nil, errs.FileIO("create the setup probe folder", tmp, err)
	}
	timeout := time.Duration(cfg.Settings.ProbeTimeoutS) * time.Second
	return probe.New(probe.Options{Home: home, TmpDir: tmp, GitTimeout: timeout, SSHTimeout: timeout, Limiter: a.probeLimiter}), nil
}

// withNamedSetup puts name in ctx as the --setup choice (ONB 3.3 step 1)
// when the caller named none and name is a setup in config.yaml: setup
// ops name their setup explicitly, so the write gate (an ambiguous or
// unknown active setup refuses writes) must judge that setup, not the
// folder's. A name not saved yet (a setup being made) changes nothing.
func (a *App) withNamedSetup(ctx context.Context, name string) context.Context {
	if name == "" || op.SetupOf(ctx) != "" {
		return ctx
	}
	cfg, err := ReadSetupsConfig(a.opts.Home)
	if err != nil {
		return ctx
	}
	if _, ok := cfg.Setup(name); !ok {
		return ctx
	}
	return op.WithSetup(ctx, name)
}

// TeamOrigin implements team.Env / team.OrgEntries: the store url and org
// folder of the orgs[] entry org (the invite line's store and org_folder,
// 4.5): its store holding the org folder, else its first store.
func (a *App) TeamOrigin(_ context.Context, org string) (team.Origin, error) {
	cfg, _, err := orgcfg.LoadConfig(a.opts.Home)
	if err != nil {
		return team.Origin{}, err
	}
	for _, o := range cfg.Orgs {
		if o.Name != org {
			continue
		}
		for _, st := range o.Stores {
			if st.OrgFolder != "" {
				return team.Origin{Store: st.URL, OrgFolder: st.OrgFolder}, nil
			}
		}
		if len(o.Stores) > 0 {
			return team.Origin{Store: o.Stores[0].URL}, nil
		}
		return team.Origin{}, errs.New(errs.NotFound, "org "+strconv.Quote(org)+" has no store in ~/.lore/"+ConfigFile).
			WithHint("lore org import --url <store-url> adds it")
	}
	return team.Origin{}, errs.New(errs.NotFound, "no org named "+strconv.Quote(org)+" in ~/.lore/"+ConfigFile).
		WithHint("lore org import --url <store-url> --as " + org)
}

// TeamGitHub implements team.Env: gh runs for invite.github_access and
// --grant (4.6).
func (a *App) TeamGitHub(context.Context) (team.GitHubRunner, error) { return ghRunner{}, nil }

// DoctorCache implements opsdoctor.CacheEnv: the result cache in
// machine.db that every lore process on this machine shares (ONB 6.1,
// 60 s; `--fresh` skips it; doctor_cache.go). A machine store that does not
// open falls back to the App's in-process cache, so the run still works.
// The machine.db handle is opened once per App and closed by App.Close:
// every call returns the same cache (TestSkillToolsFollowSetup), so a
// long-lived page server never opens one handle per doctor run.
func (a *App) DoctorCache(ctx context.Context) opsdoctor.Cache {
	a.doctorMu.Lock()
	defer a.doctorMu.Unlock()
	if a.machDoctor != nil {
		return a.machDoctor
	}
	c, err := openMachineDoctorCache(ctx, a.opts.Home, a.opts.Wall)
	if err != nil {
		return a.doctorCache
	}
	a.machDoctor = c
	return c
}

// HasOrg implements setup.OrgFinder: an orgs[] entry named org exists.
func (a *App) HasOrg(ctx context.Context, org string) bool {
	_, err := a.TeamOrigin(ctx, org)
	return err == nil
}
