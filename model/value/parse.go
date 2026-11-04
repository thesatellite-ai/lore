package value

import (
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/ids"
)

// Parse decodes raw JSON as a value of kind k. JSON null decodes to Null for
// every kind (a nullable field); whether null is allowed is the validator's
// call, not this package's. A list's items are inferred (Infer) and must all
// share one kind; use ParseList when the item kind is known.
func Parse(k Kind, raw []byte) (Value, error) {
	jk := canon.KindOf(raw)
	if jk == canon.KindInvalid {
		return nil, invalid(k, "invalid JSON")
	}
	if jk == canon.KindNull {
		return Null{}, nil
	}
	switch k {
	case KindText:
		return parseString(k, raw, func(s string) (Value, error) { return TextOf(s) })
	case KindNumber:
		if jk != canon.KindNumber {
			return nil, invalid(k, "want a JSON number")
		}
		c, err := canon.Compact(raw)
		if err != nil {
			return nil, invalid(k, "invalid JSON")
		}
		return NumberOf(string(c))
	case KindBool:
		switch jk {
		case canon.KindBool:
			c, err := canon.Compact(raw)
			if err != nil {
				return nil, invalid(k, "invalid JSON")
			}
			return BoolOf(string(c) == "true"), nil
		default:
			return nil, invalid(k, "want true or false")
		}
	case KindDate:
		return parseString(k, raw, func(s string) (Value, error) { return DateOf(s) })
	case KindDateTime:
		return parseString(k, raw, func(s string) (Value, error) { return DateTimeOf(s) })
	case KindDuration:
		return parseString(k, raw, func(s string) (Value, error) { return DurationOf(s) })
	case KindIP:
		return parseString(k, raw, func(s string) (Value, error) { return IPOf(s) })
	case KindCIDR:
		return parseString(k, raw, func(s string) (Value, error) { return CIDROf(s) })
	case KindDNS:
		return parseString(k, raw, func(s string) (Value, error) { return DNSOf(s) })
	case KindURL:
		return parseString(k, raw, func(s string) (Value, error) { return URLOf(s) })
	case KindEmail:
		return parseString(k, raw, func(s string) (Value, error) { return EmailOf(s) })
	case KindSecretRef:
		return parseString(k, raw, func(s string) (Value, error) { return SecretRefOf(s) })
	case KindJSON:
		return JSONOf(raw)
	case KindList:
		return parseInferredList(raw)
	case KindRef:
		return parseString(k, raw, func(s string) (Value, error) {
			id, err := ids.ParseAny(s)
			if err != nil {
				return nil, err
			}
			return RefOf(id)
		})
	case KindNull:
		return nil, invalid(k, "only null is a null")
	}
	return nil, invalid(k, "unknown kind")
}

// ParseList decodes a JSON array whose items are all of kind elem.
func ParseList(elem Kind, raw []byte) (List, error) {
	items, err := canon.Elements(raw)
	if err != nil {
		return List{}, invalid(KindList, "want a JSON array")
	}
	vals := make([]Value, len(items))
	for i, it := range items {
		v, err := Parse(elem, it)
		if err != nil {
			return List{}, err
		}
		vals[i] = v
	}
	return ListOf(elem, vals...)
}

// Infer decodes raw JSON with no schema at hand, by its JSON type alone:
// string → Text, number → Number, bool → Bool, null → Null, object → JSON,
// array → List when every item infers to one kind (an empty array is a list
// of text), otherwise JSON. Every inferred value re-marshals to the
// canonical form of raw, so decode → encode never changes bytes.
func Infer(raw []byte) (Value, error) {
	switch canon.KindOf(raw) {
	case canon.KindString:
		return Parse(KindText, raw)
	case canon.KindNumber:
		return Parse(KindNumber, raw)
	case canon.KindBool:
		return Parse(KindBool, raw)
	case canon.KindNull:
		return Null{}, nil
	case canon.KindObject:
		return JSONOf(raw)
	case canon.KindArray:
		l, err := parseInferredList(raw)
		if err != nil {
			return JSONOf(raw)
		}
		return l, nil
	case canon.KindInvalid:
	}
	return nil, invalid(KindJSON, "invalid JSON")
}

// parseInferredList infers each item and requires one shared kind.
func parseInferredList(raw []byte) (List, error) {
	items, err := canon.Elements(raw)
	if err != nil {
		return List{}, invalid(KindList, "want a JSON array")
	}
	if len(items) == 0 {
		return ListOf(KindText)
	}
	vals := make([]Value, len(items))
	for i, it := range items {
		v, err := Infer(it)
		if err != nil {
			return List{}, err
		}
		vals[i] = v
	}
	return ListOf(vals[0].Kind(), vals...)
}

// parseString decodes a JSON string and hands it to build.
func parseString(k Kind, raw []byte, build func(string) (Value, error)) (Value, error) {
	s, err := jsonString(k, raw)
	if err != nil {
		return nil, err
	}
	return build(s)
}
