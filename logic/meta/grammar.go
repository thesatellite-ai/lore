package meta

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Op is a grammar operator (3.6).
type Op string

// Operators, longest first where one is a prefix of another.
const (
	OpEq      Op = "="
	OpNe      Op = "!="
	OpLike    Op = "~="
	OpGt      Op = ">"
	OpGe      Op = ">="
	OpLt      Op = "<"
	OpLe      Op = "<="
	OpIn      Op = "<<"
	OpHas     Op = "?"
	OpMissing Op = "!?"
)

// opOrder is the match order at an operator position.
var opOrder = []Op{OpIn, OpGe, OpLe, OpNe, OpLike, OpMissing, OpEq, OpGt, OpLt, OpHas}

// Type is a value type: a kind, and the element kind of a list.
type Type struct {
	Kind value.Kind
	// Elem is the item kind of a list ("" = text).
	Elem value.Kind
}

// IsZero reports "no type given".
func (t Type) IsZero() bool { return t.Kind == "" }

// String is the type word ("list<number>", "text").
func (t Type) String() string {
	if t.Kind == value.KindList && t.Elem != "" && t.Elem != value.KindText {
		return "list<" + string(t.Elem) + ">"
	}
	if t.Kind == value.KindList {
		return "list<text>"
	}
	return string(t.Kind)
}

// scalarKinds are the kinds a scalar or list item may have.
var scalarKinds = map[value.Kind]bool{
	value.KindText: true, value.KindNumber: true, value.KindBool: true, value.KindDate: true,
	value.KindDateTime: true, value.KindDuration: true, value.KindDNS: true, value.KindIP: true,
	value.KindCIDR: true, value.KindURL: true, value.KindEmail: true,
}

// ParseType reads a type word: a scalar kind, list, list<elem>, object
// (stored as json, META-B7-OBJ) or json.
func ParseType(s string) (Type, error) {
	switch {
	case s == "json" || s == TypeObject:
		return Type{Kind: value.KindJSON}, nil
	case s == "list":
		return Type{Kind: value.KindList, Elem: value.KindText}, nil
	case strings.HasPrefix(s, "list<") && strings.HasSuffix(s, ">"):
		e := value.Kind(s[len("list<") : len(s)-1])
		if !scalarKinds[e] {
			return Type{}, errs.New(errs.MetaType, "meta: "+quote(s)+": a list holds one scalar type")
		}
		return Type{Kind: value.KindList, Elem: e}, nil
	case scalarKinds[value.Kind(s)]:
		return Type{Kind: value.Kind(s)}, nil
	}
	return Type{}, errs.New(errs.MetaType, "meta: "+quote(s)+" is not a metadata type").
		WithHint("text, number, bool, date, datetime, duration, dns, ip, cidr, url, email, list, list<elem>, object, json")
}

// Term is one parsed grammar argument.
type Term struct {
	// Level is "", repo, project or org; Place the full place name, if given.
	Level, Place string
	// Key is the dotted key name; empty when KeyID names the key.
	Key string
	// KeyID is set when the key part starts with a mky_ id; Fields are the
	// dot segments after it.
	KeyID  ids.ID[ids.MetaKey]
	Fields []string
	// Path is a json path after '$' (filters only).
	Path string
	Type Type
	Op   Op
	// Value is everything after the operator, as written.
	Value string
}

// Name is the key name as written (the id form prints the id).
func (t Term) Name() string {
	if !t.KeyID.IsZero() {
		return strings.Join(append([]string{t.KeyID.String()}, t.Fields...), ".")
	}
	return t.Key
}

// Parse reads one grammar argument (3.6): keypart op [value].
func Parse(arg string) (Term, error) {
	i, op, err := findOp(arg)
	if err != nil {
		return Term{}, err
	}
	t, err := parseKeyPart(arg[:i])
	if err != nil {
		return Term{}, err
	}
	t.Op = op
	t.Value = arg[i+len(op):]
	if (op == OpHas || op == OpMissing) && t.Value != "" {
		return Term{}, errs.New(errs.MetaOp, "meta: "+string(op)+" takes no value: "+quote(arg))
	}
	return t, nil
}

// ParseWrite reads a write argument: only key=value, no json path.
func ParseWrite(arg string) (Term, error) {
	t, err := Parse(arg)
	if err != nil {
		return Term{}, err
	}
	if t.Op != OpEq {
		return Term{}, errs.New(errs.MetaOp, "meta: a write is key=value; "+quote(arg)+" uses "+string(t.Op)).
			WithHint("--meta key=value, key:type=value, project:key=value")
	}
	if t.Path != "" {
		return Term{}, errs.New(errs.MetaOp, "meta: a json path filters; a write sets the whole value: "+quote(arg))
	}
	return t, nil
}

// findOp finds the first operator after a valid key part; a "list<…>"
// type word inside the key part is skipped.
func findOp(arg string) (int, Op, error) {
	for i := 0; i < len(arg); i++ {
		if strings.HasPrefix(arg[i:], ":list<") {
			if end := strings.IndexByte(arg[i:], '>'); end > 0 {
				i += end
				continue
			}
		}
		if strings.HasPrefix(arg[i:], "=>") || strings.HasPrefix(arg[i:], "=<") {
			return 0, "", errs.New(errs.MetaOp, "meta: "+quote(arg)+": did you mean "+string(arg[i+1])+"=?")
		}
		for _, op := range opOrder {
			if strings.HasPrefix(arg[i:], string(op)) {
				if i == 0 {
					return 0, "", errs.New(errs.MetaOp, "meta: "+quote(arg)+" has no key before "+string(op))
				}
				return i, op, nil
			}
		}
	}
	return 0, "", errs.New(errs.MetaOp, "meta: "+quote(arg)+" has no operator").WithHint("write key=value")
}

// parseKeyPart splits [scope ':'] key [':' type] (the colon rule of 3.6).
func parseKeyPart(kp string) (Term, error) {
	var t Term
	if k, p, ok := strings.Cut(kp, "$"); ok {
		kp, t.Path = k, "$"+p
	}
	parts := strings.Split(kp, ":")
	setType := func(s string) error {
		ty, err := ParseType(s)
		if err != nil {
			return err
		}
		t.Type = ty
		return nil
	}
	refuse := func() error {
		return errs.New(errs.MetaOp, "meta: unknown scope "+quote(parts[0])+" in "+quote(kp)).
			WithHint("use repo:<key>, project:<key>, org:<key> or <level>:<place>:<key>")
	}
	var key string
	if IsLevel(parts[0]) {
		t.Level = parts[0]
		switch len(parts) {
		case 2:
			key = parts[1]
		case 3:
			if IsTypeWord(parts[2]) {
				key = parts[1]
				if err := setType(parts[2]); err != nil {
					return Term{}, err
				}
			} else {
				t.Place, key = parts[1], parts[2]
			}
		case 4:
			t.Place, key = parts[1], parts[2]
			if err := setType(parts[3]); err != nil {
				return Term{}, err
			}
		default:
			return Term{}, refuse()
		}
	} else {
		switch len(parts) {
		case 1:
			key = parts[0]
		case 2:
			if !IsTypeWord(parts[1]) {
				if IsTypeWord(parts[0]) {
					return Term{}, errs.New(errs.MetaOp, "meta: "+quote(parts[0])+" is a type word, not a scope: "+quote(kp))
				}
				return Term{}, refuse()
			}
			key = parts[0]
			if err := setType(parts[1]); err != nil {
				return Term{}, err
			}
		default:
			return Term{}, refuse()
		}
	}
	if key == "" {
		return Term{}, errs.New(errs.MetaOp, "meta: "+quote(kp)+" has no key")
	}
	if strings.HasPrefix(key, string(ids.PrefixMetaKey)+"_") {
		segs := Segments(key)
		id, err := ids.Parse[ids.MetaKey](segs[0])
		if err != nil {
			return Term{}, errs.Wrap(errs.MetaKey, "meta: "+quote(segs[0])+" is not a key id", err)
		}
		t.KeyID, t.Fields = id, segs[1:]
		return t, nil
	}
	t.Key = key
	return t, nil
}

// maxQuoted bounds how much user text an error repeats.
const maxQuoted = 64

// quote renders user text for a message, truncated.
func quote(s string) string {
	r := []rune(s)
	if len(r) > maxQuoted {
		r = append(r[:maxQuoted], '…')
	}
	return `"` + string(r) + `"`
}
