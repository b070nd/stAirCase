package conformance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConformance(t *testing.T) {
	schemaFile := "../../proto/ipc.v1.schema.json"
	corpusDir := "corpus"

	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020
	_, err := compiler.Compile(schemaFile)
	require.NoError(t, err, "schema must compile")

	entries, err := os.ReadDir(corpusDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "corpus must not be empty")

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		e := e // capture loop variable
		t.Run(e.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(corpusDir, e.Name()))
			require.NoError(t, err)

			var doc any
			require.NoError(t, json.Unmarshal(data, &doc))

			m, _ := doc.(map[string]any)
			expectError := m["expect_error"] == true

			kind, _ := m["type"].(string)
			if kind == "" {
				// type field is missing or not a string — always an error
				if expectError {
					return
				}
				t.Fatalf("valid corpus file %q has no string 'type' field", e.Name())
			}

			ref := schemaFile + "#/$defs/" + kind
			kindSch, refErr := compiler.Compile(ref)

			if expectError {
				if refErr != nil {
					// unknown kind → expected error
					return
				}
				err = kindSch.Validate(doc)
				assert.Error(t, err, "expected schema validation error for malformed corpus file")
			} else {
				require.NoError(t, refErr, "kind %q must have a $def", kind)
				assert.NoError(t, kindSch.Validate(doc), "valid corpus file must pass schema")
			}
		})
	}
}
