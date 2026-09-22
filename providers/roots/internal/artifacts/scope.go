package artifacts

import (
	"path/filepath"
	"regexp"
)

var executionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidExecutionID restricts IDs used in saved-plan filenames to UUIDs.
func ValidExecutionID(id string) bool { return executionID.MatchString(id) }

// Scope binds artifacts to the host-supplied shared artifact directory.
// Moving a workspace requires fresh plans and node output.
func Scope(directory string) string { return Hash([]byte(filepath.Clean(directory))) }
