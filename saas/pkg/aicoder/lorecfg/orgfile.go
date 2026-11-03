package lorecfg

import (
	"context"
	"errors"
	"fmt"
)

// Committed org choice, `.lore/org.yaml` (Q-b, TEAM-023, F10 rule 3).
const (
	// OrgFileBase is the base name of the committed `.lore/org.yaml`.
	OrgFileBase = "org"
	// OrgFilePerm: the file is committed and read by the whole team.
	OrgFilePerm = 0o644
)

// OrgFile is `.lore/org.yaml`. It holds NAMES only — never a store URL, an
// identity or a pattern — so a repo can name its org without saying anything
// about where that org keeps its notes (TEAM-023). F10 accepts it only in
// repos matching the org's `own:` patterns and never linked as a client.
//
//	org: northwind
//	project: lore-tools   # the internal project folder, optional
type OrgFile struct {
	Org     string `yaml:"org"`
	Project string `yaml:"project,omitempty"`
}

// Validate implements cfgkit.Validator.
func (f *OrgFile) Validate() error {
	var errs []error
	if err := validateOrgName(f.Org); err != nil {
		errs = append(errs, fmt.Errorf("org %q: %w", f.Org, err))
	}
	if f.Project != "" {
		if err := ValidateFolderName(f.Project); err != nil {
			errs = append(errs, fmt.Errorf("project %q: %w", f.Project, err))
		}
	}
	return errors.Join(errs...)
}

// LoadOrgFile reads `<loreDir>/org.yaml`; nil with no error when absent.
func LoadOrgFile(ctx context.Context, loreDir string, o Options) (*OrgFile, error) {
	loc, err := Locate(ctx, loreDir, OrgFileBase, o)
	if err != nil {
		return nil, err
	}
	if !loc.Exists {
		return nil, nil
	}
	return Load[OrgFile](ctx, loc, o)
}

// WriteOrgFile validates f and writes `<loreDir>/org.yaml` atomically.
func WriteOrgFile(ctx context.Context, loreDir string, f *OrgFile, o Options) (string, error) {
	if err := f.Validate(); err != nil {
		return "", fmt.Errorf("lorecfg: %s%s: %w", OrgFileBase, ExtYAML, err)
	}
	return Write(ctx, loreDir, OrgFileBase, f, OrgFilePerm, o)
}
