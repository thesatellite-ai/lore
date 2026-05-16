package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"

	"entgo.io/contrib/entgql"
	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
)

func main() {
	var err error

	ex, err := entgql.NewExtension(
		entgql.WithSchemaGenerator(),
		entgql.WithWhereInputs(true),
		// entgql.WithSchemaPath("../apidash/internal/graph/schemas/ent.graphql"),
		// entgql.WithConfigPath("../apidash/gqlgen.yml"),

		entgql.WithSchemaPath("../apidash/internal/graph/schemas/ent.graphql"), // fixed the folder name
		entgql.WithConfigPath("../apidash/gqlgen.yml"),

		// add @canAdmin directive to the node and nodes query in ent.graphql
		// https://github.com/ent/ent/issues/3173
		entgql.WithSchemaHook(func(_ *gen.Graph, s *ast.Schema) error {
			for _, name := range []string{"node", "nodes"} {
				f := s.Types["Query"].Fields.ForName(name)
				if f == nil {
					return fmt.Errorf("missing query field %q", name)
				}
				// f.Directives = append(f.Directives, &ast.Directive{Name: constants.DirectiveCanApp})
			}
			return nil
		}),

		// entgql.WithOutputWriter(outputWriter),
	)
	if err != nil {
		log.Fatalf("creating entgql extension: %v", err)
	}

	opts := []entc.Option{
		// entc.TemplateDir("./tmpl"),
		entc.FeatureNames("sql/execquery", "intercept", "schema/snapshot", "sql/modifier", "sql/upsert"),
		entc.Extensions(ex),
	}

	err = entc.Generate("./schema",
		&gen.Config{
			Target:  "./gen/ent",
			Package: "dbent/gen/ent",
		},
		opts...,
	)
	if err != nil {
		log.Fatal("running ent codegen:", err)
	}
}

func outputWriter(schema *ast.Schema) error {
	// generatePublicGql(schema)
	os.WriteFile("../apidash/graph/ent.graphql", []byte(printSchema(schema)), 0644)

	// should be after apidash generated as schema is pointer so publicgql conditions will affect
	// apidash ent.graphql file

	return nil
}
func printSchema(schema *ast.Schema) string {
	// updateSchema(schema)

	sb := &strings.Builder{}
	formatter.
		NewFormatter(sb, formatter.WithIndent("  ")).
		FormatSchema(schema)
	return sb.String()
}
