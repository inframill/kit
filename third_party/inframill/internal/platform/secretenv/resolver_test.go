package secretenv_test

import (
	"context"
	"testing"

	"github.com/sir-hassan/inframill/internal/platform/secretenv"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

func TestResolverUsesOpaqueReference(t *testing.T) {
	resolver := secretenv.Resolver{Lookup: func(name string) (string, bool) {
		if name != "INFRAMILL_SECRET_DEBUG_TOKEN" {
			t.Fatalf("environment name = %q", name)
		}
		return "sensitive", true
	}}
	value, err := resolver.Resolve(context.Background(), inframill.SecretRef{Name: "debug_token"})
	if err != nil {
		t.Fatal(err)
	}
	if value.Reveal() != "sensitive" {
		t.Fatal("resolved secret differs")
	}
}

func TestResolverRejectsEnvironmentInjection(t *testing.T) {
	resolver := secretenv.Resolver{Lookup: func(string) (string, bool) {
		t.Fatal("Lookup must not be called")
		return "", false
	}}
	if _, err := resolver.Resolve(context.Background(), inframill.SecretRef{Name: "../PATH"}); err == nil {
		t.Fatal("Resolve() unexpectedly succeeded")
	}
}
