// Package secretenv resolves opaque Inframill secret references from process
// environment variables without exposing them through Config configuration.
package secretenv

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

var validReference = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Resolver maps a logical reference such as "debug_token" to an
// environment variable such as "INFRAMILL_SECRET_DEBUG_TOKEN".
type Resolver struct {
	Prefix string
	Lookup func(string) (string, bool)
}

var _ inframill.SecretResolver = Resolver{}

func (r Resolver) Resolve(ctx context.Context, reference inframill.SecretRef) (inframill.Secret, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validReference.MatchString(reference.Name) {
		return nil, fmt.Errorf("invalid secret reference %q", reference.Name)
	}
	prefix := r.Prefix
	if prefix == "" {
		prefix = "INFRAMILL_SECRET_"
	}
	lookup := r.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	name := prefix + strings.ToUpper(reference.Name)
	value, ok := lookup(name)
	if !ok || value == "" {
		return nil, fmt.Errorf("secret %q is unavailable", reference.Name)
	}
	return secret(value), nil
}

type secret string

func (s secret) Reveal() string { return string(s) }
