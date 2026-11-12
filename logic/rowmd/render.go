// Package rowmd is the Markdown view of one row that `lore edit` opens in
// the person's editor, and the parser that reads the edited view back
// (D2 / D-i of the plain-files round): YAML frontmatter with every field
// of the row except its main long text field, which is the Markdown body.
//
//	Render(def, row, where) ─► "---\n# help lines\nid: …\ntitle: …\n---\n<body>\n"
//	Parse(def, cur, file, text) ─► Result{Patch, Changed} | error with file:line
//
// The view is a convenience over the row file, never a second format: the
// row stays a JSON file (D2), Parse only builds the patch that the normal
// write path (engine.Update: schema, references, secret scan, policy, new
// HLC, updated_by/updated_at) writes. A value that does not parse, a
// changed lore-owned key (id, table, _hlc, created_*, updated_*), an
// unknown key or a removed key is refused with the file and line it is on,
// so the person fixes that line and tries again.
//
// Layer L1: pure (model, schema, logic/validate, yaml.v3); no I/O.
package rowmd

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Delimiter is the line that opens and closes the frontmatter.
const Delimiter = "---"

// The lore-owned keys every view shows first. They are written by lore on
// every write (or once, at create) and never change by hand.
const (
	KeyID        = row.KeyID
	KeyTable     = "table"
	KeyHLC       = row.KeyHLC
	KeyCreatedAt = row.KeyCreatedAt
	KeyCreatedBy = row.KeyCreatedBy
	KeyUpdatedAt = row.KeyUpdatedAt
	KeyUpdatedBy = row.KeyUpdatedBy
)

// systemKeys are the lore-owned keys in view order.
var systemKeys = []string{KeyID, KeyTable, KeyHLC, KeyCreatedAt, KeyCreatedBy, KeyUpdatedAt, KeyUpdatedBy}

// systemWhy is why each lore-owned key cannot change: the refusal says it.
var systemWhy = map[string]string{
	KeyID:        "the id names the row's file and every link to it, so it never changes",
	KeyTable:     "the table comes from the id's prefix; to put the row in another store use lore move",
	KeyHLC:       "_hlc is lore's write clock; lore stamps a new one on every write",
	KeyCreatedAt: "created_at is written once, when the row was made",
	KeyCreatedBy: "created_by is written once, when the row was made",
	KeyUpdatedAt: "updated_at is stamped by lore on every write (this edit sets it)",
	KeyUpdatedBy: "updated_by is stamped by lore on every write (this edit sets it to you)",
}

// IsSystemKey reports whether key is one of the lore-owned keys of a view.
func IsSystemKey(key string) bool { return slices.Contains(systemKeys, key) }

// bodyPreference is the order the main long text field is chosen in: the
// first one the table declares as text. Most tables call it body; a few
// call it content, description or message.
var bodyPreference = []string{"body", "content"}

// textFallback are the plain text fields used as the body when the table
// declares no markdown field (tech_docs.description).
var textFallback = []string{"description", "message", "notes"}

// BodyField is the table's main long text field, written as the Markdown
// body of the view: body or content when declared, else the first field
// whose widget is markdown (reminders.message, actors.bio), else a plain
// description/message/notes text field; "" when the table has none (the
// whole row is frontmatter).
func BodyField(def schema.TableDef) string {
	isText := func(name string) bool {
		f, ok := def.Field(name)
		return ok && f.Kind == value.KindText && !f.Sensitive
	}
	for _, n := range bodyPreference {
		if isText(n) {
			return n
		}
	}
	for _, f := range def.Fields {
		if f.UI.Widget == schema.WidgetMarkdown && f.Kind == value.KindText && !f.Sensitive {
			return f.Name
		}
	}
	for _, n := range textFallback {
		if isText(n) {
			return n
		}
	}
	return ""
}

// Where says where the row lives, for the view's help lines.
type Where struct {
	// Store is the attached store name (repo, me, an org store).
	Store string
	// Folder is the store folder ("." for the root; "" for a code repo).
	Folder string
	// Path is the row file's absolute path ("" when it has none yet).
	Path string
	// Live, when set, names the live ref copy the row is read from
	// ("refs/lore/live: _root/tasks/tsk_….json").
	Live string
}

// Render is the Markdown view of r. Every declared field is in the
// frontmatter (an absent one as an empty value, so a person can fill it),
// in the table's order, after the lore-owned keys; fields the row holds
// that this lore version does not declare follow them, unchangeable.
func Render(def schema.TableDef, r row.Row, w Where) ([]byte, error) {
	body := BodyField(def)
	doc := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, v *yaml.Node) {
		doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: key}, v)
	}
	for _, k := range systemKeys {
		add(k, systemNode(systemValue(r, k)))
	}
	// Fields with a value first, then the empty ones under a comment, so
	// what the row holds is not lost among the fields it does not use.
	var empty []schema.FieldDef
	for _, f := range def.Fields {
		if f.Name == body {
			continue
		}
		if blank(r.Fields[f.Name]) {
			empty = append(empty, f)
			continue
		}
		n, err := fieldNode(f, r.Fields[f.Name])
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "rowmd: render "+r.ID.String()+" field "+f.Name, err)
		}
		add(f.Name, n)
	}
	for i, f := range empty {
		n, err := fieldNode(f, r.Fields[f.Name])
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "rowmd: render "+r.ID.String()+" field "+f.Name, err)
		}
		add(f.Name, n)
		if i == 0 {
			doc.Content[len(doc.Content)-2].HeadComment = "Not set (give one a value to set it):"
		}
	}
	for _, k := range undeclared(def, r) {
		n, err := jsonNode(r.Fields[k])
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "rowmd: render "+r.ID.String()+" field "+k, err)
		}
		add(k, n)
	}
	var fm bytes.Buffer
	enc := yaml.NewEncoder(&fm)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, errs.Wrap(errs.Internal, "rowmd: render "+r.ID.String(), err)
	}
	if err := enc.Close(); err != nil {
		return nil, errs.Wrap(errs.Internal, "rowmd: render "+r.ID.String(), err)
	}
	var b bytes.Buffer
	b.WriteString(Delimiter + "\n")
	for _, l := range helpLines(def, r, w, body) {
		b.WriteString("# " + l + "\n")
	}
	b.Write(fm.Bytes())
	b.WriteString(Delimiter + "\n")
	if body != "" {
		if t := textOf(r.Fields[body]); t != "" {
			b.WriteString(t)
			b.WriteString("\n")
		}
	}
	return b.Bytes(), nil
}

// helpLines are the YAML comment lines at the top of the frontmatter: what
// the file is and how to change it. Comments are ignored when it is read.
func helpLines(def schema.TableDef, r row.Row, w Where, body string) []string {
	out := []string{"lore edit: " + r.ID.String() + " (" + string(def.Name) + ") as Markdown. Change values, save and close the editor, and lore checks the row and writes it."}
	place := "store " + w.Store
	if w.Folder != "" && w.Folder != "." {
		place += ", folder " + w.Folder
	}
	switch {
	case w.Live != "":
		out = append(out, place+"; a live row, read from "+w.Live)
	case w.Path != "":
		out = append(out, place+"; file "+w.Path)
	default:
		out = append(out, place)
	}
	if body != "" {
		out = append(out, "The text below the closing --- is the "+body+" field (Markdown).")
	} else {
		out = append(out, string(def.Name)+" has no long text field: keep the part below the closing --- empty.")
	}
	out = append(out, "An empty value (key:) clears an optional field. id, table, _hlc, created_* and updated_* are lore's own and cannot change.")
	return out
}

// undeclared lists the row's fields the table does not declare, sorted.
func undeclared(def schema.TableDef, r row.Row) []string {
	var out []string
	for k := range r.Fields {
		if _, ok := def.Field(k); !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// YAML core tags.
const (
	tagStr   = "!!str"
	tagNull  = "!!null"
	tagBool  = "!!bool"
	tagInt   = "!!int"
	tagFloat = "!!float"
	tagMap   = "!!map"
	tagSeq   = "!!seq"
)

// systemValue is the text of a lore-owned key ("" when absent).
func systemValue(r row.Row, key string) string {
	switch key {
	case KeyID:
		return r.ID.String()
	case KeyTable:
		return string(r.Table)
	case KeyHLC:
		if r.HLC.IsZero() {
			return ""
		}
		return r.HLC.String()
	case KeyCreatedAt:
		return stamp(r.CreatedAt)
	case KeyCreatedBy:
		if r.CreatedBy.IsZero() {
			return ""
		}
		return r.CreatedBy.String()
	case KeyUpdatedAt:
		return stamp(r.UpdatedAt)
	case KeyUpdatedBy:
		if r.UpdatedBy.IsZero() {
			return ""
		}
		return r.UpdatedBy.String()
	}
	return ""
}

// stamp is an audit time as the row file writes it ("" when absent).
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// systemNode is a lore-owned value: text, or empty when absent.
func systemNode(s string) *yaml.Node {
	if s == "" {
		return nullNode()
	}
	return strNode(s)
}

func nullNode() *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagNull, Value: ""} }

// strNode is a text scalar; multi-line text is a literal block (|) so it
// reads as written. A value yaml cannot keep exactly in a block (leading
// spaces on its first line, trailing spaces, tabs, control characters) is
// double-quoted instead (roundTrips decides; TestEditEveryTextField
// covers the strings that need it).
func strNode(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: s}
	if strings.Contains(s, "\n") {
		n.Style = yaml.LiteralStyle
		if !roundTrips(n) {
			n.Style = yaml.DoubleQuotedStyle
		}
	}
	return n // one line: yaml's encoder quotes it whenever plain would read differently
}

// roundTrips reports whether n encodes and decodes back to the same text.
func roundTrips(n *yaml.Node) bool {
	doc := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: tagStr, Value: "k"}, n}}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return false
	}
	var back struct {
		K *string `yaml:"k"`
	}
	if yaml.Unmarshal(raw, &back) != nil || back.K == nil {
		return false
	}
	return *back.K == n.Value
}

// textOf is a text field's string ("" when absent or null).
func textOf(v value.Value) string {
	if v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// fieldNode renders one declared field by its declared kind. The stored
// value may be in its inferred form (a ref read back is Text, a list of
// refs a List of Text), so each kind reads it through the form helpers.
func fieldNode(f schema.FieldDef, v value.Value) (*yaml.Node, error) {
	if v == nil || v.Kind() == value.KindNull {
		return nullNode(), nil
	}
	switch f.Kind {
	case value.KindList:
		items := value.ItemStrings(v)
		if _, isList := v.(value.List); !isList && !isJSONArrayOfScalars(v) {
			return jsonNode(v) // an odd stored shape: shown as it is
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq, Style: yaml.FlowStyle}
		for _, it := range items {
			seq.Content = append(seq.Content, scalarItem(f.Elem, it))
		}
		return seq, nil
	case value.KindJSON:
		return jsonNode(v)
	case value.KindNumber:
		if n, ok := v.(value.Number); ok {
			return numberNode(n.String()), nil
		}
	case value.KindBool:
		if b, ok := v.(value.Bool); ok {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagBool, Value: b.String()}, nil
		}
	case value.KindText, value.KindDate, value.KindDateTime, value.KindDuration, value.KindIP, value.KindCIDR,
		value.KindDNS, value.KindURL, value.KindEmail, value.KindRef, value.KindSecretRef, value.KindNull:
	}
	if v.Kind() == value.KindJSON { // a json value under a scalar kind: shown as JSON
		return jsonNode(v)
	}
	return strNode(v.String()), nil
}

// isJSONArrayOfScalars: v is a JSON array whose items are all scalars, so
// a list field stored as JSON still renders as a flow list.
func isJSONArrayOfScalars(v value.Value) bool {
	raw, ok := value.JSONForm(v)
	if !ok || len(raw) == 0 || raw[0] != '[' {
		return false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return false
	}
	for _, it := range items {
		if t := bytes.TrimSpace(it); len(t) > 0 && (t[0] == '{' || t[0] == '[') {
			return false
		}
	}
	return true
}

// scalarItem is one list item: a number or bool item as itself, anything
// else as text.
func scalarItem(elem value.Kind, s string) *yaml.Node {
	switch elem {
	case value.KindNumber:
		return numberNode(s)
	case value.KindBool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagBool, Value: s}
	case value.KindText, value.KindDate, value.KindDateTime, value.KindDuration, value.KindIP, value.KindCIDR,
		value.KindDNS, value.KindURL, value.KindEmail, value.KindJSON, value.KindList, value.KindRef,
		value.KindSecretRef, value.KindNull:
	}
	return strNode(s)
}

// numberNode is a number scalar, its literal as stored.
func numberNode(lit string) *yaml.Node {
	tag := tagInt
	if strings.ContainsAny(lit, ".eE") {
		tag = tagFloat
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: lit}
}

// jsonNode renders any value's JSON as YAML: objects as blocks, arrays of
// scalars as flow lists, scalars as themselves.
func jsonNode(v value.Value) (*yaml.Node, error) {
	raw, ok := value.JSONForm(v)
	if !ok {
		return nullNode(), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return jsonToNode(dec)
}

// jsonToNode reads one JSON value from dec as a YAML node.
func jsonToNode(dec *json.Decoder) (*yaml.Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
			for dec.More() {
				kt, kerr := dec.Token()
				if kerr != nil {
					return nil, kerr
				}
				key, _ := kt.(string)
				val, verr := jsonToNode(dec)
				if verr != nil {
					return nil, verr
				}
				m.Content = append(m.Content, strNode(key), val)
			}
			if _, err := dec.Token(); err != nil { // '}'
				return nil, err
			}
			if len(m.Content) == 0 {
				m.Style = yaml.FlowStyle
			}
			return m, nil
		case '[':
			s := &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq, Style: yaml.FlowStyle}
			for dec.More() {
				it, ierr := jsonToNode(dec)
				if ierr != nil {
					return nil, ierr
				}
				if it.Kind != yaml.ScalarNode || strings.Contains(it.Value, "\n") {
					s.Style = 0 // a list of objects, or of long text: one item per line
				}
				s.Content = append(s.Content, it)
			}
			if _, err := dec.Token(); err != nil { // ']'
				return nil, err
			}
			return s, nil
		}
	case string:
		return strNode(t), nil
	case json.Number:
		return numberNode(t.String()), nil
	case bool:
		if t {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagBool, Value: "true"}, nil
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagBool, Value: "false"}, nil
	case nil:
		return nullNode(), nil
	}
	return nil, io.ErrUnexpectedEOF
}
