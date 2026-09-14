package packaging

import (
	"bytes"
	"errors"
	"os"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) {
	return nil, errors.New("external schema resources are not allowed")
}
func validateSchema(path string) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	if len(b) > 4<<20 {
		return errors.New("schema exceeds size limit")
	}
	doc, e := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if e != nil {
		return e
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(offlineLoader{})
	const resource = "https://kit.invalid/schema.json"
	if e = compiler.AddResource(resource, doc); e != nil {
		return e
	}
	_, e = compiler.Compile(resource)
	return e
}
