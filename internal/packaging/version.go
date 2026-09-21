package packaging

import (
	"regexp"
	"strings"
)

var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
var name = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func ValidName(s string) bool { return name.MatchString(s) }
func ValidVersion(s string) bool {
	if !semver.MatchString(s) {
		return false
	}
	if _, pre, ok := strings.Cut(s, "-"); ok {
		for _, part := range strings.Split(pre, ".") {
			numeric := true
			for _, c := range part {
				if c < '0' || c > '9' {
					numeric = false
					break
				}
			}
			if numeric && len(part) > 1 && part[0] == '0' {
				return false
			}
		}
	}
	return true
}
