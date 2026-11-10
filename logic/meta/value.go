package meta

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

// Coerced is one checked value: its canonical JSON and the notes that say
// how it was normalised (R12: never silent).
type Coerced struct {
	Raw   []byte
	Notes []string
}

// Coerce reads command-line text as a value of type t (MQ9, R12): text is
// trimmed and NFC-normalised, a list is split at commas only when the type
// is a list, nothing is guessed. Empty values are E_META_VALUE; a value
// that does not parse is E_META_TYPE.
func Coerce(t Type, raw string) (Coerced, error) {
	if t.IsZero() {
		t = Type{Kind: value.KindText}
	}
	switch t.Kind {
	case value.KindList:
		var items [][]byte
		var notes []string
		for p := range strings.SplitSeq(raw, ",") {
			c, err := scalar(elemOf(t), p)
			if err != nil {
				return Coerced{}, err
			}
			items = append(items, c.Raw)
			notes = append(notes, c.Notes...)
		}
		arr, err := jsonArray(items)
		if err != nil {
			return Coerced{}, errs.Wrap(errs.Internal, "meta: build a list", err)
		}
		return Coerced{Raw: arr, Notes: notes}, nil
	case value.KindJSON:
		return CoerceJSON(t, []byte(raw))
	case value.KindText, value.KindNumber, value.KindBool, value.KindDate, value.KindDateTime, value.KindDuration, value.KindIP, value.KindCIDR, value.KindDNS, value.KindURL, value.KindEmail, value.KindRef, value.KindSecretRef, value.KindNull:
	}
	return scalar(t.Kind, raw)
}

// elemOf is a list's item kind (text by default).
func elemOf(t Type) value.Kind {
	if t.Elem == "" {
		return value.KindText
	}
	return t.Elem
}

// scalar reads one scalar of kind k from text.
func scalar(k value.Kind, raw string) (Coerced, error) {
	if k == value.KindText {
		return text(raw)
	}
	s := strings.TrimSpace(raw)
	if s == "" {
		return Coerced{}, emptyErr()
	}
	var notes []string
	if s != raw {
		notes = append(notes, "trimmed spaces")
	}
	var j []byte
	switch k {
	case value.KindNumber:
		n, note, err := number(s)
		if err != nil {
			return Coerced{}, err
		}
		if note != "" {
			notes = append(notes, note)
		}
		j = []byte(n)
	case value.KindBool:
		if s != "true" && s != "false" {
			return Coerced{}, errs.New(errs.MetaType, "meta: "+quote(raw)+" is not a bool (true or false, lowercase)")
		}
		j = []byte(s)
	default:
		j = canon.String(s)
	}
	v, err := value.Parse(k, j)
	if err != nil {
		return Coerced{}, errs.Wrap(errs.MetaType, "meta: "+quote(raw)+" is not a "+string(k), err)
	}
	out, err := v.MarshalJSON()
	if err != nil {
		return Coerced{}, errs.Wrap(errs.Internal, "meta: encode", err)
	}
	if !jsonStringIs(out, s) && k != value.KindNumber && k != value.KindBool {
		notes = append(notes, "stored as "+string(out))
	}
	return Coerced{Raw: out, Notes: notes}, nil
}

// jsonStringIs reports whether raw is the JSON string of s.
func jsonStringIs(raw []byte, s string) bool { return string(raw) == string(canon.String(s)) }

// text is the text rule (R12 4): trim, NFC, control characters refused,
// case kept.
func text(raw string) (Coerced, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Coerced{}, emptyErr()
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return Coerced{}, errs.New(errs.MetaValue, "meta: a text value may not hold control characters")
		}
	}
	var notes []string
	if n := norm.NFC.String(s); n != s {
		s = n
		notes = append(notes, "normalised to Unicode NFC")
	}
	if s != strings.TrimSpace(raw) || len(s) != len(raw) {
		if strings.TrimSpace(raw) != raw {
			notes = append(notes, "trimmed spaces")
		}
	}
	return Coerced{Raw: canon.String(s), Notes: notes}, nil
}

// number is the number rule (R12 2): JSON grammar, finite, an integer fits
// int64, -0 is 0.
func number(s string) (string, string, error) {
	bad := func(why string) error {
		return errs.New(errs.MetaType, "meta: "+quote(s)+" is not a number ("+why+")").
			WithHint("plain digits, an optional sign, decimals and exponent: 3.5, -2, 1e3; no units or thousands separators")
	}
	if canon.KindOf([]byte(s)) != canon.KindNumber {
		return "", "", bad("not a JSON number")
	}
	if c, err := canon.Compact([]byte(s)); err != nil || string(c) != s {
		return "", "", bad("not a plain literal")
	}
	if !strings.ContainsAny(s, ".eE") {
		if _, err := strconv.ParseInt(s, 10, 64); err != nil {
			return "", "", bad("an integer outside int64; store it as text")
		}
	}
	if f, ok := new(big.Float).SetString(s); ok && f.Sign() == 0 && strings.HasPrefix(s, "-") {
		return "0", "stored as 0", nil
	}
	return s, "", nil
}

// emptyErr is the refusal of an empty value (R12 5).
func emptyErr() error {
	return errs.New(errs.MetaValue, "meta: an empty value is not a value").WithHint("lore meta unset removes a key")
}

// InferType is the type a --meta-json value gives a new key: string text,
// number, bool, a flat list of one scalar type, an object as json
// (META-B7-OBJ). A list of lists or objects, or a mixed list, is
// E_META_SHAPE; null, "", [] and {} are E_META_VALUE.
func InferType(raw []byte) (Type, error) {
	if err := notEmpty(raw); err != nil {
		return Type{}, err
	}
	switch canon.KindOf(raw) {
	case canon.KindString:
		return Type{Kind: value.KindText}, nil
	case canon.KindNumber:
		return Type{Kind: value.KindNumber}, nil
	case canon.KindBool:
		return Type{Kind: value.KindBool}, nil
	case canon.KindObject:
		return Type{Kind: value.KindJSON}, nil
	case canon.KindArray:
		items, err := canon.Elements(raw)
		if err != nil {
			return Type{}, errs.Wrap(errs.MetaValue, "meta: invalid json", err)
		}
		elem := value.Kind("")
		for _, it := range items {
			var k value.Kind
			switch canon.KindOf(it) {
			case canon.KindString:
				k = value.KindText
			case canon.KindNumber:
				k = value.KindNumber
			case canon.KindBool:
				k = value.KindBool
			default:
				return Type{}, shapeErr()
			}
			if elem != "" && k != elem {
				return Type{}, shapeErr()
			}
			elem = k
		}
		return Type{Kind: value.KindList, Elem: elem}, nil
	case canon.KindInvalid, canon.KindNull:
	}
	return Type{}, errs.New(errs.MetaValue, "meta: invalid json value")
}

// shapeErr is E_META_SHAPE.
func shapeErr() error {
	return errs.New(errs.MetaShape, "meta: a list holds scalars of one type (no lists or objects inside)").
		WithHint("use the json type for any other shape: --meta-json key:json=…")
}

// notEmpty refuses null, "", [] and {} (R12 5).
func notEmpty(raw []byte) error {
	c, err := canon.Compact(raw)
	if err != nil {
		return errs.Wrap(errs.MetaValue, "meta: invalid json", err)
	}
	switch string(c) {
	case "null", `""`, "[]", "{}":
		return errs.New(errs.MetaValue, "meta: "+string(c)+" is not a value").WithHint("lore meta unset removes a key")
	}
	return nil
}

// CoerceJSON checks a JSON value against type t: a scalar or list item
// must be the JSON type of its kind (dates and the network types as
// strings), json takes any non-empty value.
func CoerceJSON(t Type, raw []byte) (Coerced, error) {
	if err := notEmpty(raw); err != nil {
		return Coerced{}, err
	}
	c, err := canon.Compact(raw)
	if err != nil {
		return Coerced{}, errs.Wrap(errs.MetaValue, "meta: invalid json", err)
	}
	switch t.Kind {
	case value.KindJSON:
		return Coerced{Raw: c}, nil
	case value.KindList:
		if canon.KindOf(c) != canon.KindArray {
			return Coerced{}, errs.New(errs.MetaType, "meta: want a JSON array for "+t.String())
		}
		items, err := canon.Elements(c)
		if err != nil {
			return Coerced{}, errs.Wrap(errs.MetaValue, "meta: invalid json", err)
		}
		out := make([][]byte, 0, len(items))
		var notes []string
		for _, it := range items {
			if k := canon.KindOf(it); k == canon.KindArray || k == canon.KindObject {
				return Coerced{}, shapeErr()
			}
			one, cerr := CoerceJSON(Type{Kind: elemOf(t)}, it)
			if cerr != nil {
				return Coerced{}, cerr
			}
			out = append(out, one.Raw)
			notes = append(notes, one.Notes...)
		}
		arr, err := jsonArray(out)
		if err != nil {
			return Coerced{}, errs.Wrap(errs.Internal, "meta: build a list", err)
		}
		return Coerced{Raw: arr, Notes: notes}, nil
	case value.KindText, value.KindNumber, value.KindBool, value.KindDate, value.KindDateTime, value.KindDuration, value.KindIP, value.KindCIDR, value.KindDNS, value.KindURL, value.KindEmail, value.KindRef, value.KindSecretRef, value.KindNull:
	}
	switch canon.KindOf(c) {
	case canon.KindString:
		s, err := unquote(c)
		if err != nil {
			return Coerced{}, errs.Wrap(errs.MetaValue, "meta: invalid json string", err)
		}
		if t.Kind == value.KindNumber || t.Kind == value.KindBool {
			return Coerced{}, errs.New(errs.MetaType, "meta: "+quote(string(c))+" is a string; the key is "+string(t.Kind))
		}
		return scalar(t.Kind, s)
	case canon.KindNumber:
		if t.Kind != value.KindNumber {
			return Coerced{}, errs.New(errs.MetaType, "meta: "+string(c)+" is a number; the key is "+t.String())
		}
		return scalar(t.Kind, string(c))
	case canon.KindBool:
		if t.Kind != value.KindBool {
			return Coerced{}, errs.New(errs.MetaType, "meta: "+string(c)+" is a bool; the key is "+t.String())
		}
		return Coerced{Raw: c}, nil
	case canon.KindInvalid, canon.KindNull, canon.KindArray, canon.KindObject:
	}
	return Coerced{}, errs.New(errs.MetaType, "meta: a "+t.String()+" value cannot be "+string(c))
}

// jsonArray joins canonical items into a canonical array.
func jsonArray(items [][]byte) ([]byte, error) {
	return canon.Compact(append(append([]byte("["), bytes.Join(items, []byte(","))...), ']'))
}

// unquote decodes a JSON string (the stdlib decoder at the JSON boundary).
func unquote(raw []byte) (string, error) {
	var s string
	err := json.Unmarshal(raw, &s)
	return s, err
}
