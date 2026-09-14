// Package render owns Backend template interpretation and workspace creation.
package render

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/sir-hassan/inframill/internal/platform/filetree"
)

const (
	artifactsName  = "artifacts"
	templateSuffix = ".gotmpl"
	templateRoot   = "template"
	manifestName   = "manifest.yaml"
	schemaName     = "schema.json"
)

func parse(name string, content []byte) (*template.Template, error) {
	return template.New(name).Option("missingkey=error").Funcs(template.FuncMap{
		"toJSON": func(value any) (string, error) { b, err := json.Marshal(value); return string(b), err },
	}).Parse(string(content))
}

// Build renders templates and preserves every ordinary tree entry.
func Build(files map[string]filetree.File, values map[string]any) (map[string]filetree.File, error) {
	destinations, err := destinations(files)
	if err != nil {
		return nil, err
	}
	result := make(map[string]filetree.File, len(destinations))
	for _, name := range sorted(destinations) {
		file := files[name]
		file.Content = bytes.Clone(file.Content)
		if !file.Directory && strings.HasSuffix(name, templateSuffix) {
			tmpl, err := parse(name, file.Content)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", name, err)
			}
			var output bytes.Buffer
			if checkErr := tmpl.Execute(&output, values); checkErr != nil {
				return nil, fmt.Errorf("render %s: %w", name, checkErr)
			}
			file.Content = output.Bytes()
		}
		result[destinations[name]] = file
	}
	return result, nil
}

func destinations(files map[string]filetree.File) (map[string]string, error) {
	result := map[string]string{}
	owners := map[string]string{}
	for _, name := range sorted(files) {
		if !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") {
			return nil, fmt.Errorf("invalid Blueprint path %q", name)
		}
		if name == templateRoot {
			continue
		}
		if !strings.HasPrefix(name, "template/") {
			continue
		}
		destination := strings.TrimPrefix(name, "template/")
		if !files[name].Directory {
			destination = strings.TrimSuffix(destination, templateSuffix)
		}
		if !fs.ValidPath(destination) || destination == "." || path.Base(destination) == "." {
			return nil, fmt.Errorf("invalid rendered path %q", destination)
		}
		if destination == artifactsName || strings.HasPrefix(destination, "artifacts/") {
			return nil, errors.New("artifacts is reserved")
		}
		if previous, exists := owners[destination]; exists {
			return nil, fmt.Errorf("render collision: %s and %s", previous, name)
		}
		owners[destination] = name
		result[name] = destination
	}
	for destination := range owners {
		for parent := path.Dir(destination); parent != "."; parent = path.Dir(parent) {
			if original, exists := owners[parent]; exists && !files[original].Directory {
				return nil, fmt.Errorf("render file/directory collision: %s", parent)
			}
		}
	}
	return result, nil
}

func sorted[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for name := range items {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

// Render exclusively creates its destination and writes source and rendered content.
func Render(ctx context.Context, filesSource map[string]filetree.File, values map[string]any, destination string) (resultErr error) {
	if checkErr := ctx.Err(); checkErr != nil {
		return checkErr
	}
	for _, name := range []string{manifestName, schemaName, templateRoot} {
		file, ok := filesSource[name]
		if !ok || file.Directory != (name == templateRoot) {
			return fmt.Errorf("blueprint source requires %s", name)
		}
	}
	for name := range filesSource {
		if name != manifestName && name != schemaName && name != templateRoot && !strings.HasPrefix(name, "template/") {
			return fmt.Errorf("unsupported Blueprint source entry %s", name)
		}
	}
	files, err := Build(filesSource, values)
	if err != nil {
		return err
	}
	if checkErr := os.Mkdir(destination, 0o700); checkErr != nil {
		return checkErr
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, os.RemoveAll(destination))
		}
	}()
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close() //nolint:errcheck // Closing directory descriptor cannot change content.
	if writeErr := writeFiles(ctx, root, filesSource); writeErr != nil {
		return writeErr
	}
	if mkdirErr := root.Mkdir("workspace", 0o700); mkdirErr != nil {
		return mkdirErr
	}
	workspace, err := root.OpenRoot("workspace")
	if err != nil {
		return err
	}
	defer workspace.Close() //nolint:errcheck // Closing directory descriptor cannot change content.
	return writeFiles(ctx, workspace, files)
}

func writeFiles(ctx context.Context, root *os.Root, files map[string]filetree.File) error {
	for _, name := range sorted(files) {
		if checkErr := ctx.Err(); checkErr != nil {
			return checkErr
		}
		file := files[name]
		if file.Directory {
			if checkErr := root.MkdirAll(name, 0o700); checkErr != nil {
				return checkErr
			}
			continue
		}
		if checkErr := root.MkdirAll(path.Dir(name), 0o700); checkErr != nil {
			return checkErr
		}
		mode := os.FileMode(0o600)
		if file.Executable {
			mode = 0o700
		}
		if checkErr := root.WriteFile(name, file.Content, mode); checkErr != nil {
			return checkErr
		}
	}
	return ctx.Err()
}
