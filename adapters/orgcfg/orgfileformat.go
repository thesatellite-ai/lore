package orgcfg

// orgfileformat.go — the format version of lore-org.yaml (devco:D-873),
// the org file's counterpart of lore-store.yaml's lore_store.
//
//	lore_org absent ─▶ version 1 (every file written before the key existed)
//	lore_org 1..OrgFileFormat ─▶ read; the next write through lore stamps
//	                             OrgFileFormat (a later format's upgrade
//	                             runs here, value for value)
//	lore_org > OrgFileFormat ─▶ E_OLD_LORE: a newer lore wrote it, this one
//	                            could misread a key, so it reads nothing
//	                            and says `lore upgrade`
//	lore_org < 1 or no number ─▶ E_CONFIG_INVALID naming the key
//
// The stability promise (docs/reference/config.md, lore-org.yaml): inside
// one format lore only adds optional keys, which every lore since 0.1.18
// ignores when it does not know them (yamlerr.Lenient); renaming,
// removing or changing the meaning of a key raises OrgFileFormat.

import (
	"errors"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/model/errs"
)

const (
	// OrgFileFormat is the lore-org.yaml format this lore writes and the
	// newest it reads. Raise it only when a key is renamed, removed or
	// changes meaning, and teach the readers the step from the old one.
	OrgFileFormat = 1
	// OrgFileFormatKey is the top-level key holding the format, first in
	// the file as lore_store is in lore-store.yaml.
	OrgFileFormatKey = "lore_org"
	// orgFileFormatUnset is the format of a file with no lore_org key:
	// the files written before the key existed, which are format 1.
	orgFileFormatUnset = 1
)

// orgFileFormatOf checks a file's lore_org value (0 = the key is absent)
// and returns the format it means; a newer one is E_OLD_LORE so no key
// of it is guessed at, a negative or zero one E_CONFIG_INVALID.
func orgFileFormatOf(v int, set bool) (int, error) {
	switch {
	case !set:
		return orgFileFormatUnset, nil
	case v < 1:
		return 0, errs.New(errs.ConfigInvalid, OrgFileFormatKey+" must be a positive version, not "+strconv.Itoa(v))
	case v > OrgFileFormat:
		return 0, errs.New(errs.OldLore, OrgFileFormatKey+": "+strconv.Itoa(v)+" is the format of a newer lore; this lore reads up to "+
			OrgFileFormatKey+": "+strconv.Itoa(OrgFileFormat)+" and leaves the file unread: run lore upgrade")
	}
	return v, nil
}

// orgFileError names the org file (path in store) in err: a lore error
// (a format refusal) keeps its code and reaches the person with the file
// in front (errs.Prefix), any other stays a wrapped cause as before.
func orgFileError(path, store string, err error) error {
	var le *errs.Error
	if errors.As(err, &le) {
		return errs.Prefix(path+" in "+store, err)
	}
	return fmt.Errorf("orgcfg: %s in %s: %w", path, store, err)
}

// CheckOrgFileFormat checks the lore_org value of a lore-org.yaml decoded
// into a reader's own struct (nil = the key is absent, format 1): a
// reader that reads only some keys still refuses a newer format.
func CheckOrgFileFormat(v *int) error {
	if v == nil {
		return nil
	}
	_, err := orgFileFormatOf(*v, true)
	return err
}

// orgFileFormatProbe reads only lore_org, as a pointer so an explicit
// `lore_org: 0` is told apart from an absent key.
type orgFileFormatProbe struct {
	Format *int `yaml:"lore_org"`
}

// checkDocFormat checks the format of raw, decoded into d, and sets
// d.Format to the one it means (an unversioned file reads as 1), so two
// versions of the file that differ only by the stamp compare equal.
func checkDocFormat(raw []byte, d *OrgFileDoc) error {
	var p orgFileFormatProbe
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return errs.Wrap(errs.ConfigInvalid, "not valid YAML", err)
	}
	v, err := orgFileFormatOf(d.Format, p.Format != nil)
	if err != nil {
		return err
	}
	d.Format = v
	return nil
}

// orgFileMapping decodes raw (a lore-org.yaml) for an edit that keeps
// every key's place: the document and its top mapping, with lore_org
// checked and set to OrgFileFormat (inserted first when absent). A file
// of a newer format is refused before anything is changed.
func orgFileMapping(raw []byte) (doc, top *yaml.Node, err error) {
	doc = &yaml.Node{}
	if err := yaml.Unmarshal(raw, doc); err != nil {
		return nil, nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, errs.New(errs.ConfigInvalid, "not a mapping")
	}
	top = doc.Content[0]
	if err := StampOrgFileFormat(top); err != nil {
		return nil, nil, err
	}
	return doc, top, nil
}

// StampOrgFileFormat sets lore_org in top (the mapping of a lore-org.yaml)
// to OrgFileFormat, inserting it as the first key when absent. It refuses
// a newer or a broken lore_org without changing top. Every writer of the
// file calls it, so the next write through lore is the file's migration
// to the current format (as a store's layout commit is lore-store.yaml's).
func StampOrgFileFormat(top *yaml.Node) error {
	want := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(OrgFileFormat)}
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != OrgFileFormatKey {
			continue
		}
		var v int
		if err := top.Content[i+1].Decode(&v); err != nil {
			return errs.New(errs.ConfigInvalid, OrgFileFormatKey+" must be a whole number, not "+strconv.Quote(top.Content[i+1].Value))
		}
		if _, err := orgFileFormatOf(v, true); err != nil {
			return err
		}
		top.Content[i+1] = want
		return nil
	}
	key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: OrgFileFormatKey}
	if len(top.Content) > 0 {
		// a comment above the first key is the file's own: it stays on top
		key.HeadComment, top.Content[0].HeadComment = top.Content[0].HeadComment, ""
	}
	top.Content = append([]*yaml.Node{key, want}, top.Content...)
	return nil
}
