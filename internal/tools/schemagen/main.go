// Command schemagen writes the JSON Schema for den.yaml, generated from the
// config structs and their doc comments. Run it through `go generate` (see
// embed.go) from the repository root, where the comments' source is readable.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/invopop/jsonschema"

	"github.com/lukaszgard/den/internal/config"
)

func main() {
	out := flag.String("o", "schema/den.schema.json", "output file")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	r := &jsonschema.Reflector{
		// den.yaml keys come from the yaml tags; the structs have no json tags.
		FieldNameTag:              "yaml",
		AllowAdditionalProperties: false,
		// Only fields tagged jsonschema:"required" are: many untagged keys
		// (region, environment, ...) can come from a named environment.
		RequiredFromJSONSchemaTags: true,
		ExpandedStruct:             true,
	}
	if err := r.AddGoComments("den", "./internal/config"); err != nil {
		return fmt.Errorf("read config comments: %w", err)
	}
	s := r.Reflect(&config.Config{})
	s.ID = "https://den.dev/schema/den.schema.json"
	s.Title = "den.yaml"
	s.Description = "Configuration for den, the developer environment navigator."
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, append(data, '\n'), 0o644) //nolint:gosec // a schema is meant to be read
}
