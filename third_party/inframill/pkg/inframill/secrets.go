package inframill

import "context"

type SecretRef struct{ Name string }

// Secret deliberately has no serialization fields.
type (
	Secret         interface{ Reveal() string }
	SecretResolver interface {
		Resolve(context.Context, SecretRef) (Secret, error)
	}
)
