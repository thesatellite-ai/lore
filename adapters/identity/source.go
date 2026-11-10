package identity

// source.go — one identity per setup is the source (SOURCE_OF_TRUTH row 1,
// owner 2026-11-09): the old top-level `identity:` of config.yaml is a
// copy to retire into the setup. Decide is the one rule every reader of
// that copy asks (the config write's migration, `lore identity migrate`,
// the doctor), so they never disagree.
//
//	top-level identity: ─┐
//	                     ├─ Decide ─► SourceNone      nothing at the top level
//	setup identity ──────┘           SourceSame      a leftover: the setup already says it all
//	                                 SourceFill      the setup lacks some of it: copy in, then remove
//	                                 SourceConflict  the two differ: ask, never pick
//
// A field differs only when both blocks set it and the values are not the
// same (keys compare without case, as e-mail addresses do); a field one
// side leaves empty is filled, never a conflict.

import "strings"

// SourceVerdict is what Decide found.
type SourceVerdict string

// The verdicts.
const (
	// SourceNone: there is no top-level identity.
	SourceNone SourceVerdict = "none"
	// SourceSame: the setup holds every field the top level sets, with
	// the same value; the top-level block is a leftover copy.
	SourceSame SourceVerdict = "same"
	// SourceFill: nothing differs, but the top level sets a field the
	// setup lacks (an empty setup, a key-less block); migrating copies
	// those fields in.
	SourceFill SourceVerdict = "fill"
	// SourceConflict: a field is set on both sides with different values.
	// Nothing may pick between them silently: the person chooses.
	SourceConflict SourceVerdict = "conflict"
)

// Keep names which side a person chose for a conflict.
type Keep string

// The two choices of `lore identity migrate --keep`.
const (
	KeepSetup Keep = "setup"
	KeepTop   Keep = "top"
)

// Keeps lists the choices, for messages and validation.
func Keeps() []Keep { return []Keep{KeepSetup, KeepTop} }

// IsZero reports an entry with no field set.
func (e Entry) IsZero() bool {
	return strings.TrimSpace(e.Key) == "" && strings.TrimSpace(e.Name) == "" && strings.TrimSpace(e.Handle) == ""
}

// String is "Name <key>" (or whichever part is set) for messages.
func (e Entry) String() string {
	name, key := strings.TrimSpace(e.Name), strings.TrimSpace(e.Key)
	switch {
	case name != "" && key != "":
		return name + " <" + key + ">"
	case key != "":
		return key
	case name != "":
		return name + " (no key)"
	}
	if h := strings.TrimSpace(e.Handle); h != "" {
		return "@" + h + " (no key)"
	}
	return "(empty)"
}

// Decide compares the top-level identity with a setup's identity.
func Decide(top, setup Entry) SourceVerdict {
	if top.IsZero() {
		return SourceNone
	}
	fill := false
	for _, f := range entryFields(top, setup) {
		switch {
		case f.top == "":
		case f.setup == "":
			fill = true
		case !f.same(f.top, f.setup):
			return SourceConflict
		}
	}
	if fill {
		return SourceFill
	}
	return SourceSame
}

// Merge is the one identity left after a migration: keep's side wins on
// every field it sets, the other side fills the fields it leaves empty.
func Merge(top, setup Entry, keep Keep) Entry {
	win, other := setup, top
	if keep == KeepTop {
		win, other = top, setup
	}
	pick := func(a, b string) string {
		if a = strings.TrimSpace(a); a != "" {
			return a
		}
		return strings.TrimSpace(b)
	}
	return Entry{Key: pick(win.Key, other.Key), Name: pick(win.Name, other.Name), Handle: pick(win.Handle, other.Handle)}
}

// field is one compared field of two entries.
type field struct {
	top, setup string
	same       func(a, b string) bool
}

// entryFields are the three fields Decide compares, trimmed.
func entryFields(top, setup Entry) []field {
	exact := func(a, b string) bool { return a == b }
	return []field{
		{strings.TrimSpace(top.Key), strings.TrimSpace(setup.Key), strings.EqualFold},
		{strings.TrimSpace(top.Name), strings.TrimSpace(setup.Name), exact},
		{strings.TrimSpace(top.Handle), strings.TrimSpace(setup.Handle), exact},
	}
}

// Source names the place in config.yaml that answered, so identity show
// and whoami point at the block to edit: the setup or org entry by name,
// else the step itself.
func (r Resolved) Source() string {
	switch {
	case r.Step == StepSetup && r.Setup != "":
		return "setup identity (config.yaml setups[" + r.Setup + "].identity)"
	case r.Step == StepOrg && r.Org != "":
		return "org entry (config.yaml orgs[" + r.Org + "].identity)"
	}
	return string(r.Step)
}
