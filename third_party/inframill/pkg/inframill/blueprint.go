package inframill

import "context"

// Blueprint contains local metadata, never loaded schemas or parsed source files.
type Blueprint struct {
	APIVersion  string
	Repository  string
	Description string
	Reference   string
	Digest      string
	Tags        []string
	Directory   string
}
type SchemaViolation struct {
	Path    string
	Message string
}

// BlueprintSource owns explicit acquisition, metadata and offline Values validation.
// Resolve and List never fetch. Implementations return independent snapshots.
type BlueprintSource interface {
	Fetch(context.Context, string) (Blueprint, error)
	List(context.Context) ([]Blueprint, error)
	Resolve(context.Context, string) (Blueprint, error)
	ValidateValues(context.Context, Blueprint, InstanceValues) error
}
