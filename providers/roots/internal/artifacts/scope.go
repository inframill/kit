package artifacts

import (
	"path/filepath"
	"regexp"
)

var executionID = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// ValidExecutionID restricts IDs used in saved-plan filenames to nonzero canonical uppercase ULIDs.
func ValidExecutionID(id string) bool {
	return id != "00000000000000000000000000" && executionID.MatchString(id)
}

// Scope binds artifacts to the host-supplied shared artifact directory.
// Moving a workspace requires fresh plans and node output.
func Scope(directory string) string { return Hash([]byte(filepath.Clean(directory))) }
