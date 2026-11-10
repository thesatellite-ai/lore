package docs

import (
	"net/http"
	"strconv"

	"github.com/thesatellite-ai/lore/schema"
)

// Dynamic-kind paths of the served document (STORE_SERVER_SPEC 9.6,
// RESOURCES_DESIGN 6 step 10): per kind, POST and GET
// /v1/resources/<kind>, the shapes surface/rest/kindroutes.go rewrites onto
// the generic resource operations. The body's "fields" is the kind's
// component schema (kind.<name>), so a client sees every field and type.
const (
	kindPathPrefix = "/v1/resources/"
	kindOpPrefix   = "resource.kind."
	kindTag        = "resource"
	kindWhereParam = "where"
)

// kindPaths adds the two operations of kind k to doc.
func kindPaths(doc *document, k schema.TableDef) {
	name := string(k.Name)
	ref := &jsonSchema{Ref: refSchemas + KindSchemaPrefix + name}
	str := &jsonSchema{Type: jsonString}
	errResp := &response{Description: "error", Content: map[string]mediaType{mediaJSON: {Schema: &jsonSchema{Ref: refSchemas + ErrorSchema}}}}
	okObj := map[string]mediaType{mediaJSON: {Schema: &jsonSchema{Type: jsonObject}}}
	body := &jsonSchema{Type: jsonObject, Required: []string{"name"}, Properties: map[string]*jsonSchema{
		"name":   str,
		"fields": ref,
		"to":     str,
		YesField: {Type: jsonBool},
	}, AdditionalProperties: &additional{closed: true}}
	doc.Paths[kindPathPrefix+name] = map[string]*operation{
		"post": {
			OperationID: kindOpPrefix + name + ".add", Summary: "Add a " + name + " resource", Tags: []string{kindTag},
			RequestBody: &requestBody{Required: true, Content: map[string]mediaType{mediaJSON: {Schema: body}}},
			Responses: map[string]*response{
				strconv.Itoa(http.StatusCreated):    {Description: http.StatusText(http.StatusCreated), Content: okObj},
				strconv.Itoa(http.StatusBadRequest): errResp,
			},
		},
		"get": {
			OperationID: kindOpPrefix + name + ".list", Summary: "List " + name + " resources", Tags: []string{kindTag},
			Parameters: []parameter{{Name: kindWhereParam, In: inQuery, Description: "filter on a field (repeatable)",
				Schema: &jsonSchema{Type: jsonArray, Items: str}}},
			Responses: map[string]*response{
				strconv.Itoa(http.StatusOK):         {Description: http.StatusText(http.StatusOK), Content: okObj},
				strconv.Itoa(http.StatusBadRequest): errResp,
			},
		},
	}
}
