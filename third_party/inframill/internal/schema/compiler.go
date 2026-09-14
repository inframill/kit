// Package schema implements offline Blueprint schema compilation and validation.
package schema

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

// Compiler builds immutable Blueprint-name-keyed schema validator catalogs.
type Compiler struct{}

// Validator is an immutable compiled catalog selected by Blueprint name.
// Validation is deterministic, effect-free and safe for concurrent use.
type Validator interface {
	Validate(string, map[string]any) []inframill.SchemaViolation
}

type catalog struct {
	entries map[string]compiledSchema
}

var _ Validator = (*catalog)(nil)

type compiledSchema struct {
	standard   *jsonschema.Schema
	raw        map[string]any
	compileErr error
}

// Compile compiles each supplied schema exactly once. Expected invalid schema
// documents remain catalog entries so Config validation can report violations.
func (Compiler) Compile(schemas map[string]map[string]any) (Validator, error) {
	entries := make(map[string]compiledSchema, len(schemas))
	for _, name := range sortedKeys(schemas) {
		raw := schemas[name]
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		compiler.UseLoader(rejectingLoader{})
		resource := "https://inframill.invalid/blueprints/" + name + "/schema.json"
		if err := compiler.AddResource(resource, raw); err != nil {
			return nil, fmt.Errorf("register Blueprint %q schema: %w", name, err)
		}
		standard, err := compiler.Compile(resource)
		entries[name] = compiledSchema{standard: standard, raw: raw, compileErr: err}
	}
	return &catalog{entries: entries}, nil
}

type rejectingLoader struct{}

func (rejectingLoader) Load(resource string) (any, error) {
	return nil, fmt.Errorf("external schema resource %q is not allowed", resource)
}

func (c *catalog) Validate(blueprintName string, values map[string]any) []inframill.SchemaViolation {
	entry, found := c.entries[blueprintName]
	if !found {
		return []inframill.SchemaViolation{{Message: fmt.Sprintf("compiled schema for Blueprint %q is unavailable", blueprintName)}}
	}
	if entry.compileErr != nil {
		return []inframill.SchemaViolation{{Message: "Blueprint schema is invalid: " + entry.compileErr.Error()}}
	}

	var result []inframill.SchemaViolation
	if err := entry.standard.Validate(values); err != nil {
		validationErr, ok := errors.AsType[*jsonschema.ValidationError](err)
		if !ok {
			result = append(result, inframill.SchemaViolation{Message: "Blueprint schema validation failed: " + err.Error()})
		} else {
			appendStandardViolations(&result, validationErr, values)
		}
	}
	validateExtensions(entry.raw, values, entry.raw, values, "", &result)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path == result[j].Path {
			return result[i].Message < result[j].Message
		}
		return result[i].Path < result[j].Path
	})
	return result
}

func appendStandardViolations(result *[]inframill.SchemaViolation, validationErr *jsonschema.ValidationError, valueRoot map[string]any) {
	if typed, ok := validationErr.ErrorKind.(*kind.PropertyNames); ok {
		message := standardMessage(validationErr)
		if leaf := firstValidationLeaf(validationErr); leaf != validationErr {
			message = standardMessage(leaf)
		}
		location := append([]string(nil), validationErr.InstanceLocation...)
		if corrected, found := propertyNamesLocation(validationErr.SchemaURL, valueRoot, typed.Property); found {
			location = corrected
		}
		*result = append(*result, inframill.SchemaViolation{
			Path:    strings.Join(append(location, typed.Property), "."),
			Message: message,
		})
		return
	}
	if len(validationErr.Causes) > 0 {
		for _, cause := range validationErr.Causes {
			appendStandardViolations(result, cause, valueRoot)
		}
		return
	}
	base := append([]string(nil), validationErr.InstanceLocation...)
	switch typed := validationErr.ErrorKind.(type) {
	case *kind.Required:
		for _, missing := range typed.Missing {
			*result = append(*result, inframill.SchemaViolation{
				Path: strings.Join(append(base, missing), "."), Message: "is required by the Blueprint schema",
			})
		}
	case *kind.AdditionalProperties:
		for _, property := range typed.Properties {
			*result = append(*result, inframill.SchemaViolation{
				Path: strings.Join(append(base, property), "."), Message: "is not allowed by the Blueprint schema",
			})
		}
	case *kind.PropertyNames:
		*result = append(*result, inframill.SchemaViolation{
			Path: strings.Join(append(base, typed.Property), "."), Message: standardMessage(validationErr),
		})
	case *kind.UniqueItems:
		*result = append(*result, inframill.SchemaViolation{
			Path: strings.Join(append(base, strconv.Itoa(typed.Duplicates[1])), "."), Message: "must be unique within the array",
		})
	default:
		*result = append(*result, inframill.SchemaViolation{
			Path: strings.Join(base, "."), Message: standardMessage(validationErr),
		})
	}
}

// jsonschema/v6 propertyNames failures may retain an aliased instance-location
// slice. Later map traversal can overwrite that slice. Recover only direct,
// unambiguous object paths from the compiled schema URL and verify the object
// actually contains the rejected property before replacing the reported path.
func propertyNamesLocation(schemaURL string, valueRoot map[string]any, property string) ([]string, bool) {
	parsed, err := url.Parse(schemaURL)
	if err != nil || parsed.Fragment == "" {
		return nil, false
	}
	tokens := strings.Split(strings.TrimPrefix(parsed.Fragment, "/"), "/")
	location := make([]string, 0, len(tokens)/2)
	for index := 0; index < len(tokens); index++ {
		token := strings.ReplaceAll(strings.ReplaceAll(tokens[index], "~1", "/"), "~0", "~")
		switch token {
		case "properties":
			index++
			if index >= len(tokens) {
				return nil, false
			}
			location = append(location, strings.ReplaceAll(strings.ReplaceAll(tokens[index], "~1", "/"), "~0", "~"))
		case "$defs", "definitions", "allOf", "anyOf", "oneOf":
			index++
		case "propertyNames":
			value, found := lookupPath(valueRoot, location)
			object, ok := value.(map[string]any)
			if found && ok {
				_, exists := object[property]
				return location, exists
			}
			return nil, false
		case "items", "additionalProperties", "patternProperties":
			return nil, false
		}
	}
	return nil, false
}

func lookupPath(root any, path []string) (any, bool) {
	value := root
	for _, segment := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func firstValidationLeaf(validationErr *jsonschema.ValidationError) *jsonschema.ValidationError {
	for len(validationErr.Causes) > 0 {
		validationErr = validationErr.Causes[0]
	}
	return validationErr
}

func standardMessage(validationErr *jsonschema.ValidationError) string {
	switch typed := validationErr.ErrorKind.(type) {
	case *kind.Type:
		return "must be of type " + strings.Join(typed.Want, " or ")
	case *kind.Pattern:
		return "must match Blueprint pattern " + typed.Want
	case *kind.MinLength:
		return fmt.Sprintf("must contain at least %d characters", typed.Want)
	case *kind.MinItems:
		return fmt.Sprintf("must contain at least %d items", typed.Want)
	case *kind.MinProperties:
		return fmt.Sprintf("must contain at least %d properties", typed.Want)
	case *kind.Minimum:
		minimum, _ := typed.Want.Float64()
		return fmt.Sprintf("must be at least %v", minimum)
	case *kind.Maximum:
		maximum, _ := typed.Want.Float64()
		return fmt.Sprintf("must be at most %v", maximum)
	default:
		output := validationErr.BasicOutput()
		if output.Error != nil {
			return output.Error.String()
		}
		return validationErr.Error()
	}
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Check rejects invalid schemas and external references before package publication.
func Check(raw map[string]any) error {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(rejectingLoader{})
	const resource = "https://inframill.invalid/schema.json"
	if err := compiler.AddResource(resource, raw); err != nil {
		return err
	}
	_, err := compiler.Compile(resource)
	return err
}
