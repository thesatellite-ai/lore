package setup

// edit.go — setup.edit (ONBOARDING_DESIGN 7.1, 3.2): change the fields of
// one setups[] entry from flags; an empty flag keeps the field. Writes
// nothing when the result equals the entry (changed=false). --default
// clears default on the other setup first, so every write is a valid
// candidate list (3.2: at most one default).

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Store url forms personal_store accepts besides inherit and none (the
// RuleGitURL forms, ONB 2.2).
var storeURLPrefixes = []string{"git@", "ssh://", "https://", "file://"}

// Edit is setup.edit.
func (e engine) Edit(ctx context.Context, in EditIn) (EditOut, error) {
	name := setupName(ctx, in.Name)
	if err := requireName(name); err != nil {
		return EditOut{}, err
	}
	if in.Note != "" && in.NoNote {
		return EditOut{}, errs.New(errs.InvalidInput, "--note and --no-note contradict each other").WithHint("give one of them")
	}
	if in.Default && in.NoDefault {
		return EditOut{}, errs.New(errs.InvalidInput, "--default and --no-default contradict each other").WithHint("give one of them")
	}
	if err := checkPersonalStore(in.PersonalStore); err != nil {
		return EditOut{}, err
	}
	st, err := e.env.SetupStore(ctx)
	if err != nil {
		return EditOut{}, err
	}
	cfg, err := st.Config(ctx)
	if err != nil {
		return EditOut{}, err
	}
	before, ok := cfg.Setup(name)
	if !ok {
		return EditOut{}, notFound(name, cfg)
	}
	after := edited(before, in)
	if editsGitHub(in) {
		if rec, ok := st.(GitHubRecorder); ok {
			after.GitHub = rec.RecordGitHub(ctx, after.GitHub)
		}
	}
	_, repeated := uniqueFolders(in.Folders)
	warnings := folderRepeatWarnings(repeated)
	if reflect.DeepEqual(before, after) {
		return EditOut{Name: name, Warnings: warnings}, nil
	}
	if err := e.checkEditValues(ctx, st, after, in); err != nil {
		return EditOut{}, err
	}
	// Write the edit itself first, keeping its old default flag: a value
	// config.yaml refuses (a bad route, a relative folder) then fails
	// before the other setup's default is cleared, so a refused edit
	// writes nothing. The default moves afterwards.
	first := after
	first.Default = before.Default
	if err := putEdit(ctx, st, before, first); err != nil {
		// config.yaml was valid when read (st.Config), so a refusal here is
		// about the edited values: say nothing was saved, not "fix the
		// entries" of a file the edit never wrote.
		var le *errs.Error
		if errors.As(err, &le) && le.Code == errs.ConfigInvalid {
			return EditOut{}, le.WithHint("nothing was saved; run lore setup edit --name " + name + " again with a value the message allows")
		}
		return EditOut{}, err
	}
	// The default moves in this one write: PutSetup clears it on every
	// other setup under the config lock, so neither a stale read of the
	// others nor a default another process set meanwhile can leave two.
	if after.Default != first.Default {
		if err := putEdit(ctx, st, first, after); err != nil {
			return EditOut{}, err
		}
	}
	return EditOut{Name: name, Changed: true, Warnings: warnings}, nil
}

// GitHubValidator is the optional Store upgrade that judges the given
// values of a github block (empty fields are not judged) as the github
// step does: the validator every git and ssh call of the setup's route
// runs first (wire: probe.Route.Validate: a host alias or key path that
// ssh could read as an option, a GitHub login gh would not take), and a
// key file that exists (what github.ssh_key looks for: the file or its
// .pub). Without it, setup.edit checks the catalog's question rules only.
type GitHubValidator interface {
	ValidateGitHub(ctx context.Context, g onboard.SetupGitHub) error
}

// GitHubRecorder is the optional Store upgrade that fills what a GitHub
// save records and an older entry may lack (OWN7-5, owner 2026-11-10:
// the first unchanged GitHub save fills route and key_fingerprint): an
// empty route of an ssh block becomes ssh, and an empty key_fingerprint
// becomes the fingerprint of the key the route uses now. A recorded
// fingerprint is never replaced: it is what github.key_matches compares
// the alias's current key with, so refreshing it on every save would hide
// a swapped key. Only empty fields are filled, so a second unchanged save
// equals the entry and stays a no-op (changed=false, nothing written).
type GitHubRecorder interface {
	RecordGitHub(ctx context.Context, g onboard.SetupGitHub) onboard.SetupGitHub
}

// editsGitHub reports an edit that gives any github flag: a GitHub save,
// which records route and key_fingerprint (GitHubRecorder). An edit of
// only the note or folders leaves the github block as it is.
func editsGitHub(in EditIn) bool {
	return strings.TrimSpace(in.GitHubUser) != "" || strings.TrimSpace(in.HostAlias) != "" || strings.TrimSpace(in.SSHKey) != "" ||
		in.NoSSHKey || strings.TrimSpace(string(in.Route)) != "" || strings.TrimSpace(in.GHAccount) != ""
}

// editRefusedHint ends every refusal of an edit value: the edit wrote
// nothing.
func editRefusedHint(name, fix string) string {
	return fix + "; nothing was saved, run lore setup edit --name " + name + " again"
}

// checkEditValues refuses (E_INVALID_INPUT, nothing written) a value of
// --github-user, --host-alias, --ssh-key or --folder that the setup steps
// refuse when the setup is made: the question rules of the github and
// folders steps (logic/onboard catalog), then the route validator and the
// key file's existence (GitHubValidator). Only the flags this edit gives are judged, so an
// edit of the note never fails over an older value.
func (e engine) checkEditValues(ctx context.Context, st Store, after onboard.SetupEntry, in EditIn) error {
	given := map[string][]string{}
	add := func(id, v string) {
		if v = strings.TrimSpace(v); v != "" {
			given[id] = []string{v}
		}
	}
	add(onboard.QIDGitHubAccount, in.GitHubUser)
	add(onboard.QIDHostAlias, in.HostAlias)
	add(onboard.QIDSSHKey, in.SSHKey)
	if in.Folders != nil {
		given[onboard.QIDFolders] = in.Folders
	}
	if len(given) == 0 {
		return nil
	}
	flagOf := map[string]string{onboard.QIDGitHubAccount: "--github-user", onboard.QIDHostAlias: "--host-alias",
		onboard.QIDSSHKey: "--ssh-key", onboard.QIDFolders: "--folder"}
	for _, step := range e.cat.Plan(after.Kind) {
		if step.ID != onboard.StepGitHub && step.ID != onboard.StepFolders {
			continue
		}
		for _, q := range step.Questions {
			for _, v := range given[q.ID] {
				for _, r := range q.Rules {
					if why, ok := onboard.ValidateValue(r, v); !ok {
						return errs.New(errs.InvalidInput, flagOf[q.ID]+" "+quote(v)+": "+q.Label+" "+why).
							WithHint(editRefusedHint(after.Name, "give a value the message allows"))
					}
				}
			}
		}
	}
	if in.Folders != nil {
		if err := e.refuseFolderTie(ctx, st, after); err != nil {
			return err
		}
	}
	if in.GitHubUser != "" || in.HostAlias != "" || in.SSHKey != "" {
		if gv, ok := st.(GitHubValidator); ok {
			// Only the given values: an older saved value is not this
			// edit's to refuse.
			g := onboard.SetupGitHub{User: strings.TrimSpace(in.GitHubUser), HostAlias: strings.TrimSpace(in.HostAlias), SSHKey: strings.TrimSpace(in.SSHKey)}
			if err := gv.ValidateGitHub(ctx, g); err != nil {
				var le *errs.Error
				if errors.As(err, &le) {
					return errs.New(errs.InvalidInput, "setup edit: "+le.Message).WithHint(editRefusedHint(after.Name, le.Hint))
				}
				return err
			}
		}
	}
	return nil
}

// refuseFolderTie refuses (E_SETUP_AMBIGUOUS, nothing written) an edit
// whose new folders tie with another setup's on some folder: the same
// folders.overlap check the folders step's save gate runs at error level.
// Without it `lore setup edit --folder` saved a tie the step refuses, and
// every folder of the tie then matched no setup until the next edit
// (doctor --all: broken).
func (e engine) refuseFolderTie(ctx context.Context, st Store, after onboard.SetupEntry) error {
	s, err := e.load(ctx, st, after.Name, nil)
	if err != nil {
		return err
	}
	step, err := s.step(string(onboard.StepFolders))
	if err != nil {
		return nil // a kind without a folders step has no folders to tie
	}
	step.Checks = []onboard.CheckID{onboard.CheckFoldersOverlap}
	answers := onboard.Answers{onboard.QIDFolders: {List: after.Folders, IsList: true}}
	checks, err := e.probe(ctx, s, step, answers)
	if err != nil {
		return err
	}
	for _, c := range checks {
		if c.Check == onboard.CheckFoldersOverlap && c.Status == onboard.StatusFail {
			return errs.New(errs.SetupAmbiguous, "setup edit: --folder "+c.Detail).
				WithHint(editRefusedHint(after.Name, "make one folder glob more specific than the other setup's"))
		}
	}
	return nil
}

// edited is e with in's non-empty flags applied (lists replace).
func edited(e onboard.SetupEntry, in EditIn) onboard.SetupEntry {
	if in.Folders != nil {
		e.Folders, _ = uniqueFolders(in.Folders) // a repeated --folder is saved once (Edit warns)
	}
	switch {
	case in.Default:
		e.Default = true
	case in.NoDefault:
		e.Default = false
	}
	set := func(dst *string, v string) {
		if v = strings.TrimSpace(v); v != "" {
			*dst = v
		}
	}
	oldAlias, oldKey := e.GitHub.HostAlias, e.GitHub.SSHKey
	set(&e.GitHub.User, in.GitHubUser)
	set(&e.GitHub.HostAlias, in.HostAlias)
	set(&e.GitHub.SSHKey, in.SSHKey)
	if in.NoSSHKey {
		e.GitHub.SSHKey = ""
	}
	if e.GitHub.HostAlias != oldAlias || e.GitHub.SSHKey != oldKey {
		// The recorded fingerprint was of the old key: a new one is
		// recorded on save (GitHubRecorder). The same alias or key given
		// again keeps it, so an unchanged save never hides a key swapped
		// behind the same path (OWN7-5).
		e.GitHub.KeyFingerprint = ""
	}
	set(&e.GitHub.GHAccount, in.GHAccount)
	if v := onboard.GitRoute(strings.TrimSpace(string(in.Route))); v != "" {
		e.GitHub.Route = v
	}
	if in.Route == onboard.RouteHTTPS {
		e.GitHub.HostAlias, e.GitHub.SSHKey, e.GitHub.KeyFingerprint = "", "", ""
	}
	set(&e.PersonalStore, in.PersonalStore)
	set(&e.Secrets.Backend, in.Secrets)
	if in.Tools != nil {
		e.Tools = slices.Clone(in.Tools)
	}
	set(&e.Note, in.Note)
	if in.NoNote {
		e.Note = ""
	}
	return e
}

// checkPersonalStore refuses a personal_store that is neither inherit,
// none nor a git url, and an https url carrying credentials (a token in a
// url would land in config.yaml; ONB 3.2 keeps no secrets there).
func checkPersonalStore(v string) error {
	v = strings.TrimSpace(v)
	if v == "" || v == onboard.PersonalStoreInherit || v == onboard.PersonalStoreNone {
		return nil
	}
	ok := false
	for _, p := range storeURLPrefixes {
		if strings.HasPrefix(v, p) {
			ok = true
		}
	}
	if !ok {
		return errs.New(errs.InvalidInput, "personal_store must be inherit, none or a git url (git@host:owner/repo.git, ssh://, https://, file://)").
			WithHint("lore setup edit --name <name> --personal-store git@github.com:mira/lore-me.git")
	}
	if rest, found := strings.CutPrefix(v, "https://"); found {
		if host, _, _ := strings.Cut(rest, "/"); strings.Contains(host, "@") {
			return errs.New(errs.InvalidInput, "personal_store "+quote("https://…@…")+" carries credentials; config.yaml never holds a secret").
				WithHint("use an ssh url (git@host:owner/repo.git) or a credential helper")
		}
	}
	return nil
}

// putEdit saves the edit from before to after: only the changed keys when
// the store can (SetupPatcher: no lost update of a key another process
// wrote since the read), else the whole entry.
func putEdit(ctx context.Context, st Store, before, after onboard.SetupEntry) error {
	if p, ok := st.(SetupPatcher); ok {
		return p.PatchSetup(ctx, before, after)
	}
	_, err := st.PutSetup(ctx, after)
	return err
}
