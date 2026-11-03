package personalplace

import (
	"fmt"
	"strings"

	"saas/pkg/aicoder/lorecfg"
)

// TargetKind is the shape of a `--to` value (§1 Flags, F14). `--to` only
// ever names a project — never an actor or a branch.
type TargetKind int

const (
	// TargetNone: no --to given; the write uses the kind's default (F14).
	TargetNone TargetKind = iota
	// TargetThisRepo: `--to repo`.
	TargetThisRepo
	// TargetOrg: `--to <org>` — the org's default linked project.
	TargetOrg
	// TargetOrgFolder: `--to <org>/<folder>`.
	TargetOrgFolder
	// TargetPersonal: `--to me` — the personal store, folder by F17.
	TargetPersonal
	// TargetPersonalOrg: `--to me/<org>` — only the resolved org.
	TargetPersonalOrg
)

// Target is a parsed `--to` value.
type Target struct {
	Kind   TargetKind
	Org    string // TargetOrg, TargetOrgFolder, TargetPersonalOrg
	Folder string // TargetOrgFolder
	Raw    string
}

// Explicit reports whether the caller chose a target (any --to). F10's
// fail-closed rule lets an explicit target through when links are unknown.
func (t Target) Explicit() bool { return t.Kind != TargetNone }

// IsPersonal reports `--to me` or `--to me/<org>`.
func (t Target) IsPersonal() bool { return t.Kind == TargetPersonal || t.Kind == TargetPersonalOrg }

// ParseTo parses a `--to` value. It checks shape only; CheckTo checks it
// against the resolved org.
func ParseTo(raw string) (Target, error) {
	s := strings.TrimSpace(raw)
	t := Target{Raw: s}
	if s == "" {
		return t, nil
	}
	head, tail, hasTail := strings.Cut(s, "/")
	switch {
	case s == lorecfg.TargetRepo:
		t.Kind = TargetThisRepo
	case s == lorecfg.TargetMe:
		t.Kind = TargetPersonal
	case head == lorecfg.TargetMe && hasTail:
		if strings.Contains(tail, "/") || tail == "" {
			return Target{}, fmt.Errorf("--to %s: use me or me/<org>", s)
		}
		t.Kind, t.Org = TargetPersonalOrg, tail
	case hasTail:
		if strings.Contains(tail, "/") {
			return Target{}, fmt.Errorf("--to %s: use <org>/<folder> with one folder name", s)
		}
		// <org>/. is the store root's project (`org_folder: .`, F13, §5.2,
		// cookbook 4); "." is the ONLY relative name accepted, ".." never.
		if tail != lorecfg.OrgFolderRoot {
			if err := lorecfg.ValidateFolderName(tail); err != nil {
				return Target{}, fmt.Errorf("--to %s: %w", s, err)
			}
		}
		t.Kind, t.Org, t.Folder = TargetOrgFolder, head, tail
	default:
		t.Kind, t.Org = TargetOrg, s
	}
	if t.Org == lorecfg.TargetRepo || t.Org == lorecfg.TargetMe {
		return Target{}, fmt.Errorf("--to %s: %q is not an org name", s, t.Org)
	}
	return t, nil
}

// CheckTo applies the org rules to a parsed target: <org>, <org>/<folder>
// and me/<org> must name the RESOLVED org (orgs never mix), and
// --everywhere needs --to me.
func CheckTo(t Target, resolvedOrg string, everywhere bool) error {
	if everywhere && t.Kind != TargetPersonal {
		return ErrEverywhereWithoutMe
	}
	switch t.Kind {
	case TargetOrg, TargetOrgFolder, TargetPersonalOrg:
		if resolvedOrg == "" {
			return fmt.Errorf("%w: --to %s, but no org applies in this repo", ErrOtherOrg, t.Raw)
		}
		if t.Org != resolvedOrg {
			return fmt.Errorf("%w: --to %s, but this repo resolved to %s", ErrOtherOrg, t.Raw, resolvedOrg)
		}
	}
	return nil
}
