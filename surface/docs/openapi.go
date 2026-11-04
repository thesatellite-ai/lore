package docs

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
)

// OpenAPI document constants.
const (
	// OpenAPIVersion is the OpenAPI dialect written (spec 9.6).
	OpenAPIVersion = "3.1.0"
	// APITitle names the API in the document.
	APITitle = "lore api"
	// APIVersion is the REST contract version, the /v1 prefix. It is not the
	// lore release, so the written openapi.json changes only when the API
	// does, never on every release.
	APIVersion = "v1"
	// SecurityScheme is the name of the bearer-token scheme every operation
	// requires (closed by default, spec 9.7).
	SecurityScheme = "bearer"
	// ErrorSchema is the component holding the spec 9.4 error object.
	ErrorSchema = "Error"
	// KindSchemaPrefix prefixes the component of one dynamic kind
	// ("kind.server"); only the served /openapi.json has them.
	KindSchemaPrefix = "kind."
	// YesField is the body field (or query parameter) that repeats a
	// request after E_NEEDS_CONFIRM (spec 9.7). It is not part of any
	// operation's input; the REST surface strips it before decoding.
	YesField = "yes"
	// IDParam is the path parameter of show, edit, delete and custom verbs
	// on one row.
	IDParam = "id"
)

// JSON schema type and format words.
const (
	jsonString  = "string"
	jsonBool    = "boolean"
	jsonInteger = "integer"
	jsonNumber  = "number"
	jsonArray   = "array"
	jsonObject  = "object"

	formatDurationNS = "duration-ns"
	formatDate       = "date"
	formatDateTime   = "date-time"
	formatEmail      = "email"
	formatURI        = "uri"

	mediaJSON   = "application/json"
	inQuery     = "query"
	inPath      = "path"
	refSchemas  = "#/components/schemas/"
	schemeHTTP  = "http"
	jsonIndent  = "  "
	codesJoiner = ", "
)

// alwaysErrors are the codes every route can answer besides its own: bad
// input (decode), no token, an internal failure.
var alwaysErrors = []errs.Code{errs.InvalidInput, errs.Unauthorized, errs.Internal}

// bodyErrors are added to every route that reads a request body.
var bodyErrors = []errs.Code{errs.BodyTooLarge}

// document is the OpenAPI 3.1 document, typed field by field so nothing
// untyped is ever built. encoding/json sorts map keys, so the bytes are
// deterministic.
type document struct {
	OpenAPI    string                           `json:"openapi"`
	Info       info                             `json:"info"`
	Security   []map[string][]string            `json:"security"`
	Paths      map[string]map[string]*operation `json:"paths"`
	Components components                       `json:"components"`
}

type info struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type components struct {
	Schemas         map[string]*jsonSchema    `json:"schemas"`
	SecuritySchemes map[string]securityScheme `json:"securitySchemes"`
}

type securityScheme struct {
	Type   string `json:"type"`
	Scheme string `json:"scheme"`
}

type operation struct {
	OperationID string               `json:"operationId"`
	Summary     string               `json:"summary"`
	Tags        []string             `json:"tags"`
	Parameters  []parameter          `json:"parameters,omitempty"`
	RequestBody *requestBody         `json:"requestBody,omitempty"`
	Responses   map[string]*response `json:"responses"`
}

type parameter struct {
	Name        string      `json:"name"`
	In          string      `json:"in"`
	Required    bool        `json:"required"`
	Description string      `json:"description,omitempty"`
	Schema      *jsonSchema `json:"schema"`
}

type requestBody struct {
	Required bool                 `json:"required"`
	Content  map[string]mediaType `json:"content"`
}

type response struct {
	Description string               `json:"description"`
	Content     map[string]mediaType `json:"content,omitempty"`
}

type mediaType struct {
	Schema *jsonSchema `json:"schema"`
}

// jsonSchema is the subset of JSON Schema 2020-12 lore needs.
type jsonSchema struct {
	Ref                  string                 `json:"$ref,omitempty"`
	Type                 string                 `json:"type,omitempty"`
	Format               string                 `json:"format,omitempty"`
	Description          string                 `json:"description,omitempty"`
	Enum                 []string               `json:"enum,omitempty"`
	Minimum              *int                   `json:"minimum,omitempty"`
	Items                *jsonSchema            `json:"items,omitempty"`
	Properties           map[string]*jsonSchema `json:"properties,omitempty"`
	Required             []string               `json:"required,omitempty"`
	AdditionalProperties *additional            `json:"additionalProperties,omitempty"`
}

// additional is JSON Schema's additionalProperties: either `false` (closed
// object: an input refuses unknown fields) or a schema (a string map).
type additional struct {
	closed bool
	schema *jsonSchema
}

// MarshalJSON writes false or the schema.
func (a additional) MarshalJSON() ([]byte, error) {
	if a.closed || a.schema == nil {
		return []byte("false"), nil
	}
	return json.Marshal(a.schema)
}

// OpenAPI returns the OpenAPI 3.1 document of every non-hidden operation in
// reg, as indented JSON ending in a newline. kinds adds one component schema
// per dynamic kind: nil for the written openapi.json (fixed operations
// only), the attached stores' kinds for the document `lore api` serves.
//
// Invariant: deterministic: the same registry and kinds give the same bytes
// (maps are sorted by encoding/json, slices follow registry order).
func OpenAPI(reg *op.Registry, kinds []schema.TableDef) ([]byte, error) {
	doc := document{
		OpenAPI:  OpenAPIVersion,
		Info:     info{Title: APITitle, Version: APIVersion},
		Security: []map[string][]string{{SecurityScheme: {}}},
		Paths:    map[string]map[string]*operation{},
		Components: components{
			Schemas:         map[string]*jsonSchema{ErrorSchema: errorSchema()},
			SecuritySchemes: map[string]securityScheme{SecurityScheme: {Type: schemeHTTP, Scheme: SecurityScheme}},
		},
	}
	for _, e := range reg.Entries() {
		rt, ok := op.RouteOf(e.Spec(), e.InputFields())
		if !ok {
			continue
		}
		if doc.Paths[rt.Path] == nil {
			doc.Paths[rt.Path] = map[string]*operation{}
		}
		doc.Paths[rt.Path][strings.ToLower(rt.Method)] = operationOf(e, rt)
	}
	for _, k := range kinds {
		doc.Components.Schemas[KindSchemaPrefix+string(k.Name)] = kindSchema(k)
	}
	b, err := json.MarshalIndent(doc, "", jsonIndent)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "docs: encode openapi", err)
	}
	return append(b, '\n'), nil
}

// QueryInput reports whether a route takes its input from the query string
// (GET, DELETE) rather than a JSON body (POST, PATCH). The REST surface uses
// the same rule, so the document and the server agree.
func QueryInput(method string) bool {
	return method == http.MethodGet || method == http.MethodDelete
}

// PathHasID reports whether a route path carries the {id} parameter.
func PathHasID(path string) bool { return strings.Contains(path, "{"+IDParam+"}") }

// AsksFirst reports whether an operation can answer E_NEEDS_CONFIRM, so its
// route accepts the "yes" field.
func AsksFirst(s op.Spec) bool { return s.Confirm.Always || s.Confirm.When != "" }

// operationOf describes one route.
func operationOf(e op.Entry, rt op.Route) *operation {
	s := e.Spec()
	o := &operation{
		OperationID: s.Name,
		Summary:     s.Summary,
		Tags:        []string{op.CLIPath(s.Name)[0]},
		Responses:   map[string]*response{},
	}
	inPathID := PathHasID(rt.Path)
	var fields []op.FieldInfo
	for _, f := range e.InputFields() {
		if inPathID && f.Name == IDParam {
			o.Parameters = append(o.Parameters, parameter{Name: IDParam, In: inPath, Required: true, Description: f.Desc, Schema: fieldSchema(f)})
			continue
		}
		fields = append(fields, f)
	}
	codes := append(append([]errs.Code(nil), alwaysErrors...), s.Errors...)
	if AsksFirst(s) {
		codes = append(codes, errs.NeedsConfirm)
		fields = append(fields, op.FieldInfo{Name: YesField, Type: op.TypeBool, Desc: "repeat after E_NEEDS_CONFIRM once the person said yes"})
	}
	if QueryInput(rt.Method) {
		for _, f := range fields {
			o.Parameters = append(o.Parameters, parameter{Name: f.Name, In: inQuery, Required: f.Required, Description: f.Desc, Schema: fieldSchema(f)})
		}
	} else {
		codes = append(codes, bodyErrors...)
		o.RequestBody = &requestBody{
			Required: hasRequired(fields),
			Content:  map[string]mediaType{mediaJSON: {Schema: objectSchema(fields, true)}},
		}
	}
	o.Responses[strconv.Itoa(rt.Status)] = &response{
		Description: http.StatusText(rt.Status),
		Content:     map[string]mediaType{mediaJSON: {Schema: objectSchema(e.OutputFields(), false)}},
	}
	for status, list := range byStatus(codes) {
		o.Responses[strconv.Itoa(status)] = &response{
			Description: strings.Join(list, codesJoiner),
			Content:     map[string]mediaType{mediaJSON: {Schema: &jsonSchema{Ref: refSchemas + ErrorSchema}}},
		}
	}
	return o
}

// byStatus groups codes by the HTTP status of their class (never a status
// per code: O3/O5), each list sorted and deduplicated.
func byStatus(codes []errs.Code) map[int][]string {
	out := map[int][]string{}
	for _, c := range codes {
		d := errs.DefOf(c)
		if !slices.Contains(out[d.HTTP], string(c)) {
			out[d.HTTP] = append(out[d.HTTP], string(c))
		}
	}
	for k := range maps.Keys(out) {
		slices.Sort(out[k])
	}
	return out
}

func hasRequired(fields []op.FieldInfo) bool {
	return slices.ContainsFunc(fields, func(f op.FieldInfo) bool { return f.Required })
}

// objectSchema is the schema of a struct; closed refuses unknown fields
// (inputs: op.JSONDecoder refuses them).
func objectSchema(fields []op.FieldInfo, closed bool) *jsonSchema {
	s := &jsonSchema{Type: jsonObject, Properties: map[string]*jsonSchema{}}
	for _, f := range fields {
		s.Properties[f.Name] = fieldSchema(f)
		if f.Required {
			s.Required = append(s.Required, f.Name)
		}
	}
	if closed {
		s.AdditionalProperties = &additional{closed: true}
	}
	return s
}

// fieldSchema maps one operation field type to JSON Schema.
func fieldSchema(f op.FieldInfo) *jsonSchema {
	s := &jsonSchema{Description: f.Desc}
	switch f.Type {
	case op.TypeString, op.TypeText:
		s.Type = jsonString
	case op.TypeBool:
		s.Type = jsonBool
	case op.TypeInt:
		s.Type = jsonInteger
	case op.TypeUint:
		zero := 0
		s.Type, s.Minimum = jsonInteger, &zero
	case op.TypeFloat:
		s.Type = jsonNumber
	case op.TypeDuration:
		// encoding/json writes a time.Duration as integer nanoseconds.
		s.Type, s.Format = jsonInteger, formatDurationNS
	case op.TypeStrings:
		s.Type, s.Items = jsonArray, &jsonSchema{Type: jsonString}
	case op.TypeStringMap:
		s.Type, s.AdditionalProperties = jsonObject, &additional{schema: &jsonSchema{Type: jsonString}}
	case op.TypeObject:
		o := objectSchema(f.Fields, false)
		o.Description = f.Desc
		return o
	case op.TypeList:
		s.Type, s.Items = jsonArray, objectSchema(f.Fields, false)
	}
	return s
}

// errorSchema is the spec 9.4 error object (errs.Envelope).
func errorSchema() *jsonSchema {
	str := &jsonSchema{Type: jsonString}
	classes := make([]string, 0, len(errs.Classes()))
	for _, c := range errs.Classes() {
		classes = append(classes, string(c))
	}
	body := &jsonSchema{
		Type: jsonObject,
		Properties: map[string]*jsonSchema{
			"code":    str,
			"message": str,
			"hint":    str,
			"exit":    {Type: jsonInteger},
			"class":   {Type: jsonString, Enum: classes},
		},
		Required: []string{"code", "message", "exit", "class"},
	}
	return &jsonSchema{Type: jsonObject, Properties: map[string]*jsonSchema{"error": body}, Required: []string{"error"}}
}

// kindSchema is the schema of one dynamic kind's fields.
func kindSchema(k schema.TableDef) *jsonSchema {
	s := &jsonSchema{Type: jsonObject, Description: k.Description, Properties: map[string]*jsonSchema{}}
	for _, f := range k.Fields {
		fs := valueSchema(f.Kind, f.Elem)
		fs.Description = f.Description
		fs.Enum = f.Values
		s.Properties[f.Name] = fs
		if f.Required {
			s.Required = append(s.Required, f.Name)
		}
	}
	return s
}

// valueSchema maps a value kind to JSON Schema; elem is a list's item kind.
func valueSchema(k, elem value.Kind) *jsonSchema {
	switch k {
	case value.KindNumber:
		return &jsonSchema{Type: jsonNumber}
	case value.KindBool:
		return &jsonSchema{Type: jsonBool}
	case value.KindDate:
		return &jsonSchema{Type: jsonString, Format: formatDate}
	case value.KindDateTime:
		return &jsonSchema{Type: jsonString, Format: formatDateTime}
	case value.KindEmail:
		return &jsonSchema{Type: jsonString, Format: formatEmail}
	case value.KindURL:
		return &jsonSchema{Type: jsonString, Format: formatURI}
	case value.KindList:
		item := valueSchema(elem, "")
		return &jsonSchema{Type: jsonArray, Items: item}
	case value.KindJSON, value.KindNull:
		// any JSON value: the empty schema.
		return &jsonSchema{}
	case value.KindText, value.KindDuration, value.KindIP, value.KindCIDR, value.KindDNS, value.KindRef:
	}
	return &jsonSchema{Type: jsonString}
}
