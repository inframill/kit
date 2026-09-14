package localexec

import (
	"path/filepath"
	"strings"

	"github.com/sir-hassan/inframill/internal/blueprint"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

type execInput struct {
	config inframill.InstanceConfig
	source inframill.BlueprintSource
}

func readInstance(path string) (inframill.InstanceConfig, error) {
	var config inframill.InstanceConfig
	if path == "" {
		return config, invalid("exec requires --instance")
	}
	if path == "-" {
		return config, invalid("--instance requires a regular JSON file; stdin is not supported")
	}
	object, err := readObject(path)
	if err != nil {
		return config, err
	}
	reference, referenceOK := object["blueprint"].(string)
	values, valuesOK := object["values"].(map[string]any)
	if len(object) != 2 || !referenceOK || strings.TrimSpace(reference) == "" || !valuesOK || values == nil {
		return config, invalid("Instance config must contain only a nonempty blueprint string and a values object")
	}
	return inframill.InstanceConfig{Blueprint: reference, Values: values}, nil
}

// Select storage before result reservation and env-file checks. Protect the whole
// package store in fetched mode, including content that is not downloaded yet.
func selectInput(o *options, environ []string) (execInput, error) {
	if !o.blueprintSet {
		o.blueprint = filepath.Join(o.workspace, "blueprints")
	}
	config, err := readInstance(o.instance)
	input := execInput{config: config}
	if err != nil {
		return input, err
	}
	if o.blueprintSet {
		if o.blueprint == "" {
			return input, invalid("--blueprint-dir requires a nonempty path")
		}
		input.config.Blueprint = "blueprint" // SnapshotLocal's private source name.
		return input, nil
	}
	var httpHosts string
	for _, entry := range environ {
		if value, ok := strings.CutPrefix(entry, "INFRAMILL_HTTP_REGISTRIES="); ok {
			httpHosts = value
		}
	}
	store, err := blueprint.NewStore(o.blueprint, strings.Split(httpHosts, ","))
	if err != nil {
		return input, err
	}
	input.source = store
	return input, nil
}
