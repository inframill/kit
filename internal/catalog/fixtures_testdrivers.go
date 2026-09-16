//go:build kit_testdrivers

package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	driver "github.com/sir-hassan/inframill/pkg/driver"
	"os"
	"path/filepath"
	"time"
)

// Synthetic implementations have distinct schemas and manifest contracts.
// No production driver version is registered by changing its version string.
func init() {
	for _, version := range []string{"0.1.0", "0.2.0"} {
		r := Release{Name: "fixture", Version: version, ToolVersion: "none"}
		Register(r)
		factories[r.Name+"@"+version] = func(r Release, _ string) driver.Driver { return fixtureDriver{r.Version} }
	}
}

type fixtureDriver struct{ version string }

func (d fixtureDriver) Definition() (driver.Definition, error) {
	key := "text"
	if d.version == "0.2.0" {
		key = "message"
	}
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{key: map[string]any{"type": "string"}, "wait": map[string]any{"type": "boolean"}}, "required": []string{key}}
	return driver.Definition{Name: "fixture", Commands: map[string]driver.CommandDefinition{"probe": {Description: "Test secret snapshots without emitting credentials", ArgumentsSchema: schema}}}, nil
}
func (d fixtureDriver) ValidateManifest(ctx context.Context, c driver.Config) error {
	if c["contract"] != d.version {
		return errors.New("fixture contract mismatch")
	}
	for key := range c {
		if key != "contract" && key != "id" && key != "required_driver_version" {
			return errors.New("unexpected fixture manifest field")
		}
	}
	return ctx.Err()
}
func (d fixtureDriver) Prepare(ctx context.Context, s driver.ExecutionContext, command string, args map[string]any) (driver.PreparedCommand, error) {
	if err := d.ValidateManifest(ctx, s.DriverConfig); err != nil {
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
	s    driver.ExecutionContext
	wait bool
}

func (c fixtureCommand) Execute(ctx context.Context, secrets driver.Secrets) (any, error) {
	if c.wait {
		if err := os.WriteFile(filepath.Join(c.s.ArtifactDirectory, "ready"), []byte("ready"), 0600); err != nil {
			return nil, err
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(filepath.Join(c.s.ArtifactDirectory, "release")); err == nil {
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
