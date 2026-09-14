package driver

import "regexp"

// Executable selects a file and a literal argument prefix; no shell or PATH lookup is used.
type Executable struct {
	Path string   `json:"path"`
	Args []string `json:"args,omitempty"`
}

// Configurations maps application-configured IDs to executable instances.
type Configurations map[string]Executable

// Config is the opaque configuration object owned by a particular Driver.
type Config map[string]any

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidID reports whether an ID can namespace a Driver's commands.
func ValidID(id string) bool { return validName.MatchString(id) && id != "builtin" }
