package harness

import (
	"fmt"
	"strings"
)

// Violation is one actionable configuration problem.
type Violation struct {
	Path    string
	Code    string
	Message string
}

// Violations is an ordered collection of configuration problems.
type Violations []Violation

// ValidationError reports all configuration problems found before construction.
type ValidationError struct {
	violations Violations
}

// NewValidationError copies violations into a validation error.
func NewValidationError(violations Violations) *ValidationError {
	return &ValidationError{violations: append(Violations(nil), violations...)}
}

// Error summarizes the ordered violations without exposing configuration values.
func (validation *ValidationError) Error() string {
	if validation == nil || len(validation.violations) == 0 {
		return "configuration is invalid"
	}
	parts := make([]string, 0, len(validation.violations))
	for _, violation := range validation.violations {
		parts = append(parts, fmt.Sprintf("%s: %s", violation.Path, violation.Message))
	}

	return "configuration is invalid: " + strings.Join(parts, "; ")
}

// Violations returns a defensive copy of every violation.
func (validation *ValidationError) Violations() Violations {
	if validation == nil {
		return nil
	}

	return append(Violations(nil), validation.violations...)
}
