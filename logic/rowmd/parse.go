package rowmd

// parse.go — reading an edited view back into a patch.
//
//	text ─► split at the two --- lines ─► frontmatter (yaml.Node, lines kept) + body
//	     ─► each key: lore-owned? compare │ declared field? parse + validate + compare
//	        │ held but undeclared? compare │ else unknown key
//	     ─► keys that had a value and are gone ─► refused
//	     ─► Result{Patch: changed fields only}
//
// Every problem is one line "<file>:<line>: <what> (<why / what works>)",
// all of them at once, so a person fixes every line in one go.

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/validate"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// Result is what an edited view changes.
type Result struct {
	// Patch holds the changed fields only, typed by their declared kind; a
	// cleared optional field is value.Null. Empty when nothing changed.
	Patch map[string]value.Value
	// Changed lists the changed field names, sorted.
	Changed []string
}

// NoChange reports whether the view changes nothing.
func (r Result) NoChange() bool { return len(r.Patch) == 0 }

// problems collects the file:line lines of one parse.
type problems struct {
	file  string
	lines []string
}

func (p *problems) add(line int, msg string) {
	p.lines = append(p.lines, p.file+":"+strconv.Itoa(line)+": "+msg)
}

// err is the refusal holding every problem (nil when there is none).
func (p *problems) err() error {
	if len(p.lines) == 0 {
		return nil
	}
	return errs.New(errs.InvalidInput, "the edited row has "+count(len(p.lines))+":\n"+strings.Join(p.lines, "\n")).
		WithHint("fix the lines named above (an empty value `key:` clears an optional field), then run the command again")
}

func count(n int) string {
	if n == 1 {
		return "1 problem"
	}
	return strconv.Itoa(n) + " problems"
}

// split cuts text into the frontmatter (without the delimiter lines), the
// file line the frontmatter starts on, the body and the line the body
// starts on. CRLF line ends (a Windows editor) read as LF.
func split(p *problems, text []byte) (fm []byte, body string, bodyLine int, ok bool) {
	s := strings.ReplaceAll(string(text), "\r\n", "\n")
	s = strings.TrimPrefix(s, "\uFEFF") // a byte order mark some editors add
	lines := strings.SplitAfter(s, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t\n") != Delimiter {
		p.add(1, "the view must start with a line `---` (the frontmatter opens there); run the command again to get a fresh view")
		return nil, "", 0, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\n") == Delimiter {
			fm = []byte(strings.Join(lines[1:i], ""))
			body = strings.Join(lines[i+1:], "")
			return fm, body, i + 2, true
		}
	}
	p.add(1, "the frontmatter is not closed: a line `---` must follow the last key (the body comes after it)")
	return nil, "", 0, false
}

// fmLine turns a frontmatter line (yaml.v3 counts from 1 at the first line
// after the opening ---) into a file line.
func fmLine(n int) int { return n + 1 }

// yamlLine finds yaml.v3's "line N:" in a syntax error.
var yamlLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// Parse reads an edited view of cur (the row as it is now) and returns the
// fields it changes. file names the view in every refusal. id is the row
// the command was asked to edit; a view of another row is refused.
func Parse(def schema.TableDef, cur row.Row, file string, text []byte) (Result, error) {
	p := &problems{file: file}
	fm, body, bodyLine, ok := split(p, text)
	if !ok {
		return Result{}, p.err()
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		for msg := range strings.SplitSeq(strings.TrimPrefix(err.Error(), "yaml: unmarshal errors:\n"), "\n") {
			msg = strings.TrimSpace(msg)
			if m := yamlLine.FindStringSubmatch(msg); m != nil {
				n, _ := strconv.Atoi(m[1])
				p.add(fmLine(n), "not valid YAML: "+m[2]+" (quote a value that holds `: ` or starts with #, -, |, >, [ or {)")
				continue
			}
			p.add(1, "not valid YAML: "+strings.TrimPrefix(msg, "yaml: "))
		}
		return Result{}, p.err()
	}
	var m *yaml.Node
	switch {
	case doc.Kind == 0: // only comments: every key was removed
		m = &yaml.Node{Kind: yaml.MappingNode}
	case len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode:
		m = doc.Content[0]
	default:
		p.add(2, "the frontmatter must be `key: value` lines, one per field")
		return Result{}, p.err()
	}
	r := reader{def: def, cur: cur, p: p, body: BodyField(def), patch: map[string]value.Value{}}
	// A changed _hlc means the row was written since the view was made (or
	// the line was changed by hand): every other compare would then report
	// the newer write as the person's change, so it is the only answer.
	if hn := valueOf(m, KeyHLC); hn != nil && scalarText(hn) != systemValue(cur, KeyHLC) {
		return Result{}, errs.New(errs.Stale, file+":"+strconv.Itoa(fmLine(hn.Line))+": "+cur.ID.String()+
			" was written since this view was made (_hlc is "+systemValue(cur, KeyHLC)+" now, the view has "+scalarText(hn)+
			"), or the _hlc line was changed by hand; "+systemWhy[KeyHLC]).
			WithHint("open the row again (lore edit " + cur.ID.String() + ") and redo the change on the current version")
	}
	seen := map[string]int{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			p.add(fmLine(k.Line), "a key must be a plain name")
			continue
		}
		if first, dup := seen[k.Value]; dup {
			p.add(fmLine(k.Line), "the key "+k.Value+" appears twice (first on line "+strconv.Itoa(first)+"); keep one")
			continue
		}
		seen[k.Value] = fmLine(k.Line)
		r.key(k.Value, k, v)
	}
	r.removed(seen)
	r.readBody(body, bodyLine)
	if err := p.err(); err != nil {
		return Result{}, err
	}
	out := Result{Patch: r.patch}
	for k := range r.patch {
		out.Changed = append(out.Changed, k)
	}
	slices.Sort(out.Changed)
	return out, nil
}

// reader holds one parse.
type reader struct {
	def   schema.TableDef
	cur   row.Row
	p     *problems
	body  string
	patch map[string]value.Value
}

// key checks one frontmatter key and its value.
func (r *reader) key(name string, k, v *yaml.Node) {
	line := fmLine(k.Line)
	vline := fmLine(v.Line)
	switch {
	case IsSystemKey(name):
		if got, want := scalarText(v), systemValue(r.cur, name); got != want || v.Kind != yaml.ScalarNode {
			r.p.add(vline, name+" cannot change: "+systemWhy[name]+"; put back "+orEmpty(want))
		}
	case name == r.body && r.body != "":
		r.p.add(line, name+" is the text below the closing ---, not a frontmatter key; move the text there and remove this line")
	default:
		f, declared := r.def.Field(name)
		if !declared {
			if held, ok := r.cur.Fields[name]; ok {
				r.undeclared(name, held, v, vline)
				return
			}
			r.p.add(line, "unknown key "+name+": "+string(r.def.Name)+" has no field "+name+" (its fields: "+strings.Join(fieldNames(r.def, r.body), ", ")+")")
			return
		}
		r.field(f, v, vline)
	}
}

// orEmpty names an empty value in a refusal.
func orEmpty(s string) string {
	if s == "" {
		return "an empty value"
	}
	return s
}

// undeclared checks a field the row holds that this lore version does not
// declare: lore keeps it as written, so it cannot change here.
func (r *reader) undeclared(name string, held value.Value, v *yaml.Node, line int) {
	want, err := jsonNode(held)
	if err == nil && sameNode(want, v) {
		return
	}
	r.p.add(line, name+" cannot change: "+string(r.def.Name)+" in this lore version has no field "+name+", so lore keeps the stored value as it is; put it back")
}

// field reads one declared field and adds it to the patch when it changed.
func (r *reader) field(f schema.FieldDef, v *yaml.Node, line int) {
	old := r.cur.Fields[f.Name]
	if want, err := fieldNode(f, old); err == nil && sameNode(want, v) {
		return // as rendered: unchanged, whatever its stored shape
	}
	if f.LoreRecord {
		r.p.add(line, f.Name+" cannot change: lore writes this record itself (put the old value back and use the "+string(r.def.Name)+" commands)")
		return
	}
	nv, msg := nodeValue(f, v)
	if msg != "" {
		r.p.add(line, f.Name+": "+msg)
		return
	}
	r.compare(f, old, nv, line)
}

// compare validates nv as f (typing and cleaning it as every write does)
// and adds it to the patch when it differs from old.
func (r *reader) compare(f schema.FieldDef, old, nv value.Value, line int) {
	typed, err := validate.Fields(r.def, map[string]value.Value{f.Name: nv}, validate.Patch)
	if err != nil {
		r.p.add(line, fieldProblem(r.def, f, err))
		return
	}
	got := typed[f.Name]
	was := old
	if was == nil {
		was = value.Null{}
	} else if t, terr := validate.Fields(r.def, map[string]value.Value{f.Name: old}, validate.Patch); terr == nil {
		was = t[f.Name]
	}
	if blank(got) && blank(was) || value.Equal(got, was) {
		return
	}
	if blank(got) {
		got = value.Null{} // an emptied optional field is cleared
		if f.Required {
			r.p.add(line, f.Name+" is required and cannot be empty")
			return
		}
	}
	r.patch[f.Name] = got
}

// blank: absent, null or empty text.
func blank(v value.Value) bool {
	if v == nil || v.Kind() == value.KindNull {
		return true
	}
	t, isText := v.(value.Text)
	return isText && t.String() == ""
}

// fieldProblem is validate's refusal for one field, without the table
// prefix, plus the form the kind takes.
func fieldProblem(def schema.TableDef, f schema.FieldDef, err error) string {
	msg := errs.EnvelopeOf(err).Error.Message
	msg = strings.TrimPrefix(msg, string(def.Name)+": ")
	msg = strings.TrimPrefix(msg, "field ")
	if form := value.Form(f.Kind); form != "" {
		msg += " (" + form + ")"
	}
	if len(f.Values) > 0 && !strings.Contains(msg, "one of") {
		msg += " (one of: " + strings.Join(f.Values, ", ") + ")"
	}
	return msg
}

// readBody reads the Markdown body into the body field. A table without
// one takes no body text.
func (r *reader) readBody(body string, line int) {
	if r.body == "" {
		if strings.TrimSpace(body) != "" {
			r.p.add(line, string(r.def.Name)+" has no long text field, so the part below the closing --- must stay empty; put the text in a frontmatter key")
		}
		return
	}
	body = strings.TrimSuffix(body, "\n") // the line end Render (and most editors) add
	f, _ := r.def.Field(r.body)
	old := r.cur.Fields[r.body]
	if body == textOf(old) {
		return
	}
	r.compare(f, old, textValue(body), line)
}

// removed refuses a key the view had with a value and the edit dropped:
// was it meant to be cleared, or deleted by mistake? An empty value says
// "clear" without doubt. Keys that were empty may go.
func (r *reader) removed(seen map[string]int) {
	var gone []string
	for _, k := range systemKeys {
		if _, ok := seen[k]; !ok && systemValue(r.cur, k) != "" {
			gone = append(gone, k)
		}
	}
	for _, f := range r.def.Fields {
		if f.Name == r.body {
			continue
		}
		if _, ok := seen[f.Name]; !ok && !blank(r.cur.Fields[f.Name]) {
			gone = append(gone, f.Name)
		}
	}
	for _, k := range undeclared(r.def, r.cur) {
		if _, ok := seen[k]; !ok {
			gone = append(gone, k)
		}
	}
	for _, k := range gone {
		r.p.add(1, "the key "+k+" was removed; put it back (to clear an optional field keep the key with an empty value: `"+k+":`)")
	}
}

// fieldNames lists the frontmatter field names of def (the body field is
// the Markdown body).
func fieldNames(def schema.TableDef, body string) []string {
	var out []string
	for _, f := range def.Fields {
		if f.Name != body {
			out = append(out, f.Name)
		}
	}
	return out
}

// valueOf is the value node of key in mapping m, nil when absent.
func valueOf(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// scalarText is a scalar's text; "" for null and for anything else.
func scalarText(n *yaml.Node) string {
	if n.Kind != yaml.ScalarNode || n.Tag == tagNull {
		return ""
	}
	return n.Value
}

// textValue is s as a text value (Text accepts any string).
func textValue(s string) value.Value {
	t, err := value.TextOf(s)
	if err != nil {
		return value.Null{}
	}
	return t
}

// nodeValue turns one YAML value into the field's input value; msg says
// what is wrong with its shape ("" when it parsed). Scalars become text,
// which validate converts to the declared kind exactly as a CLI flag is.
func nodeValue(f schema.FieldDef, n *yaml.Node) (value.Value, string) {
	if n.Kind == yaml.AliasNode {
		return nil, "YAML anchors and aliases (&, *) are not supported; write the value out"
	}
	if n.Kind == yaml.ScalarNode && n.Tag == tagNull {
		return value.Null{}, ""
	}
	switch f.Kind {
	case value.KindJSON:
		raw, msg := nodeJSON(n)
		if msg != "" {
			return nil, msg
		}
		j, err := value.JSONOf(raw)
		if err != nil {
			return nil, "not a JSON value"
		}
		return j, ""
	case value.KindList:
		if n.Kind != yaml.SequenceNode {
			return nil, "want a list of " + string(f.Elem) + " values, like [a, b] (an empty list is [])"
		}
		items := make([]value.Value, 0, len(n.Content))
		for _, it := range n.Content {
			if it.Kind != yaml.ScalarNode || it.Tag == tagNull {
				return nil, "each list item must be one " + string(f.Elem) + " value"
			}
			items = append(items, textValue(it.Value))
		}
		l, err := value.ListOf(value.KindText, items...)
		if err != nil {
			return nil, "not a list"
		}
		return l, ""
	case value.KindText, value.KindNumber, value.KindBool, value.KindDate, value.KindDateTime, value.KindDuration,
		value.KindIP, value.KindCIDR, value.KindDNS, value.KindURL, value.KindEmail, value.KindRef,
		value.KindSecretRef, value.KindNull:
	}
	if n.Kind != yaml.ScalarNode {
		return nil, "want one " + string(f.Kind) + " value, not a list or a mapping (quote text that starts with [ or {)"
	}
	return textValue(n.Value), ""
}

// nodeJSON converts a YAML value of a json field to JSON. A plain scalar
// that reads as a JSON number, true, false or null is that; a quoted one is
// always text.
func nodeJSON(n *yaml.Node) ([]byte, string) {
	switch n.Kind {
	case yaml.AliasNode:
		return nil, "YAML anchors and aliases (&, *) are not supported; write the value out"
	case yaml.DocumentNode:
		if len(n.Content) == 1 {
			return nodeJSON(n.Content[0])
		}
	case yaml.MappingNode:
		var b bytes.Buffer
		b.WriteByte('{')
		for i := 0; i+1 < len(n.Content); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			k, err := json.Marshal(n.Content[i].Value)
			if err != nil {
				return nil, "a key is not text"
			}
			b.Write(k)
			b.WriteByte(':')
			v, msg := nodeJSON(n.Content[i+1])
			if msg != "" {
				return nil, msg
			}
			b.Write(v)
		}
		b.WriteByte('}')
		return b.Bytes(), ""
	case yaml.SequenceNode:
		var b bytes.Buffer
		b.WriteByte('[')
		for i, it := range n.Content {
			if i > 0 {
				b.WriteByte(',')
			}
			v, msg := nodeJSON(it)
			if msg != "" {
				return nil, msg
			}
			b.Write(v)
		}
		b.WriteByte(']')
		return b.Bytes(), ""
	case yaml.ScalarNode:
		return scalarJSON(n), ""
	}
	return nil, "not a value lore can store"
}

// scalarJSON is one scalar as JSON.
func scalarJSON(n *yaml.Node) []byte {
	if n.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) == 0 {
		switch {
		case n.Tag == tagNull:
			return []byte("null")
		case n.Tag == tagBool && (n.Value == "true" || n.Value == "false"):
			return []byte(n.Value)
		case canon.KindOf([]byte(n.Value)) == canon.KindNumber:
			return []byte(n.Value)
		case n.Tag == tagInt || n.Tag == tagFloat:
			if f, err := strconv.ParseFloat(strings.ReplaceAll(n.Value, "_", ""), 64); err == nil {
				return []byte(strconv.FormatFloat(f, 'g', -1, 64))
			}
		}
	}
	s, _ := json.Marshal(n.Value)
	return s
}

// sameNode reports whether a (as rendered) and b (as read back) hold the
// same values, whatever their style: the shape and every scalar's text.
func sameNode(a, b *yaml.Node) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case yaml.ScalarNode:
		an, bn := a.Tag == tagNull, b.Tag == tagNull
		if an || bn {
			return an && bn
		}
		return a.Value == b.Value && quotedLike(a) == quotedLike(b)
	case yaml.MappingNode, yaml.SequenceNode:
		if len(a.Content) != len(b.Content) {
			return false
		}
		for i := range a.Content {
			if !sameNode(a.Content[i], b.Content[i]) {
				return false
			}
		}
		return true
	case yaml.DocumentNode, yaml.AliasNode:
	}
	return false
}

// quotedLike: a scalar that is text (a string tag) rather than a number,
// bool or null; `"1"` and `1` differ for a json field.
func quotedLike(n *yaml.Node) bool {
	return n.Tag == tagStr || n.Tag == "" && n.Style != 0
}
