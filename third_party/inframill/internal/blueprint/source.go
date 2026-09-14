// Package blueprint owns Blueprint packages, metadata and offline schemas.
package blueprint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sir-hassan/inframill/internal/blueprint/content"
	"github.com/sir-hassan/inframill/internal/schema"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

const (
	APIVersion        = "inframill.dev/blueprint/v1"
	templateDirectory = "template"
)

type manifestDocument struct {
	APIVersion  string    `yaml:"api_version"`
	Description string    `yaml:"description"`
	Drivers     yaml.Node `yaml:"drivers"` // Opaque to Blueprint metadata.
}
type Source struct{ root string }

func New(root string) (*Source, error) {
	absolute, err := filepath.Abs(root)
	return &Source{root: absolute}, err
}

func (*Source) Fetch(context.Context, string) (inframill.Blueprint, error) {
	return inframill.Blueprint{}, errors.New("directory source cannot fetch; use the OCI store")
}

func (s *Source) List(ctx context.Context) ([]inframill.Blueprint, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	result := []inframill.Blueprint{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		b, err := s.Resolve(ctx, entry.Name())
		if err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, nil
}

func (s *Source) Resolve(ctx context.Context, name string) (inframill.Blueprint, error) {
	if err := ctx.Err(); err != nil {
		return inframill.Blueprint{}, err
	}
	if !inframill.ValidName(name) {
		return inframill.Blueprint{}, inframill.InvalidArgument("blueprint", "invalid local Blueprint name")
	}
	root := filepath.Join(s.root, name)
	b, err := metadata(root)
	if os.IsNotExist(err) {
		return b, inframill.ErrNotFound
	}
	if err != nil {
		return b, err
	}
	files, err := content.Snapshot(root)
	if err != nil {
		return b, err
	}
	a, err := Pack(files)
	if err != nil {
		return b, err
	}
	b.Repository = name
	b.Digest = descriptor("", a.Manifest).Digest.String()
	b.Reference = "local/" + name + "@" + b.Digest
	return b, nil
}

func (*Source) ValidateValues(ctx context.Context, b inframill.Blueprint, values inframill.InstanceValues) error {
	return validateValues(ctx, b, values)
}

func validateValues(ctx context.Context, b inframill.Blueprint, values inframill.InstanceValues) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := readSchema(b.Directory)
	if err != nil {
		return err
	}
	validator, err := schema.Compiler{}.Compile(map[string]map[string]any{"blueprint": raw})
	if err != nil {
		return err
	}
	violations := validator.Validate("blueprint", values)
	if len(violations) > 0 {
		return &inframill.Error{Code: inframill.InvalidConfig, Path: "values." + violations[0].Path, Message: violations[0].Message}
	}
	return nil
}

func readSchema(root string) (map[string]any, error) {
	b, err := readLocal(root, schemaFilename, 1<<20)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, errors.New("invalid Blueprint schema JSON")
	}
	if raw == nil {
		return nil, errors.New("schema must be an object")
	}
	if err := schema.Check(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func metadata(root string) (inframill.Blueprint, error) {
	var result inframill.Blueprint
	if err := validateLayout(root); err != nil {
		return result, err
	}
	b, checkErr := readLocal(root, manifestFilename, 1<<20)
	if checkErr != nil {
		return result, checkErr
	}
	var manifest manifestDocument
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return result, errors.New("invalid Blueprint manifest")
	}
	if manifest.APIVersion != APIVersion || manifest.Description == "" {
		return result, fmt.Errorf("blueprint requires api_version %s and description", APIVersion)
	}
	if _, err := readSchema(root); err != nil {
		return result, err
	}
	absolute, checkErr := filepath.Abs(root)
	if checkErr != nil {
		return result, checkErr
	}
	return inframill.Blueprint{APIVersion: manifest.APIVersion, Description: manifest.Description, Directory: absolute}, nil
}

func validateLayout(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	allowed := map[string]bool{manifestFilename: true, schemaFilename: true, templateDirectory: true, "runtime": true}
	found := map[string]bool{}
	for _, entry := range entries {
		// Reserved runtime data is never source. Local exec validates workspace ownership/type.
		if entry.Name() == ".inframill" {
			continue
		}
		if !allowed[entry.Name()] {
			return fmt.Errorf("unsupported Blueprint entry %s", entry.Name())
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("blueprint links are not allowed")
		}
		found[entry.Name()] = true
		if (entry.Name() == templateDirectory || entry.Name() == "runtime") != entry.IsDir() {
			return errors.New("invalid Blueprint entry type")
		}
	}
	for _, name := range []string{manifestFilename, schemaFilename, templateDirectory} {
		if !found[name] {
			return fmt.Errorf("%s is required", name)
		}
	}
	return nil
}

func sortedKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for k := range items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
