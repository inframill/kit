package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/sir-hassan/inframill/pkg/runmill"
	"os"
	"path/filepath"
	"time"
)

// Synthetic implementations have distinct schemas and settings contracts.
// No production provider version is registered by changing its version string.
func init() {
	for _, version := range []string{"0.1.0", "0.2.0"} {
		r := Release{Name: "fixture", Version: version, ToolVersion: "none"}
		Register(r)
		factories[r.Name+"@"+version] = func(r Release, _ string) runmill.Provider { return fixtureProvider{r.Version} }
	}
}

type fixtureProvider struct{ version string }

func (d fixtureProvider) Definition() (runmill.ProviderDefinition, error) {
	key := "text"
	if d.version == "0.2.0" {
		key = "message"
	}
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{key: map[string]any{"type": "string"}, "wait": map[string]any{"type": "boolean"}}, "required": []string{key}}
	return runmill.ProviderDefinition{Name: "fixture", Commands: map[string]runmill.CommandDefinition{"probe": {Description: "Test secret snapshots without emitting credentials", ArgumentsSchema: schema}}}, nil
}
func (d fixtureProvider) ValidateSettings(ctx context.Context, c runmill.Settings) error {
	if c["contract"] != d.version {
		return errors.New("fixture contract mismatch")
	}
	for key := range c {
		if key != "contract" && key != "id" {
			return errors.New("unexpected fixture settings field")
		}
	}
	return ctx.Err()
}
func (d fixtureProvider) Prepare(ctx context.Context, s runmill.ExecutionContext, command string, args runmill.Arguments) (runmill.PreparedExecution, error) {
	if err := d.ValidateSettings(ctx, s.Settings); err != nil {
		return nil, err
	}
	key := "text"
	if d.version == "0.2.0" {
		key = "message"
	}
	if command != "probe" {
		return nil, errors.New("unknown command")
	}
	if _, ok := args[key].(string); !ok {
		return nil, errors.New("fixture schema mismatch")
	}
	return fixtureCommand{s: s, wait: args["wait"] == true}, nil
}

type fixtureCommand struct {
	s    runmill.ExecutionContext
	wait bool
}

func (c fixtureCommand) Execute(ctx context.Context, secrets map[string]string) (any, error) {
	if c.wait {
		if err := os.WriteFile(filepath.Join(c.s.ArtifactDir, "ready"), []byte("ready"), 0600); err != nil {
			return nil, err
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(filepath.Join(c.s.ArtifactDir, "release")); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
			}
		}
	}
	if err := c.s.Message("fixture completed"); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(secrets["token"]))
	return map[string]any{"secret_hash": hex.EncodeToString(sum[:]), "secret_count": len(secrets)}, nil
}
