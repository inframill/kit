//go:build testproviders

package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/sir-hassan/inframill/pkg/runmill"
	"os"
	"path/filepath"
	"time"
)

// Synthetic implementations have distinct decoding and settings contracts.
// No production provider version is registered by changing its version string.
func init() {
	for _, version := range []string{"0.1.0", "0.2.0"} {
		r := Release{Name: "fixture", Version: version, Protocol: 1}
		metadata, err := json.Marshal(r)
		if err != nil {
			panic(err)
		}
		testImplementations = append(testImplementations, implementation{metadata, func(r Release, _ string) runmill.Provider {
			return fixtureProvider{r.Version}
		}})
	}
}

type fixtureProvider struct{ version string }

func (d fixtureProvider) Definition() (runmill.ProviderDefinition, error) {
	return runmill.ProviderDefinition{Name: "fixture", Commands: map[string]runmill.CommandDefinition{"probe": {AccessMode: runmill.ReadWrite, Description: "Test secret snapshots without emitting credentials"}}}, nil
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
func (d fixtureProvider) Execute(ctx context.Context, s runmill.ExecutionContext, command string, args runmill.Arguments, secrets map[string]string) (any, error) {
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
		return nil, errors.New("fixture argument contract mismatch")
	}
	for name, value := range args {
		if name == "wait" {
			if _, ok := value.(bool); !ok {
				return nil, errors.New("wait must be a boolean")
			}
		} else if name != key {
			return nil, errors.New("unexpected fixture argument")
		}
	}
	if args["wait"] == true {
		if err := os.WriteFile(filepath.Join(s.ArtifactDir, "ready"), []byte("ready"), 0600); err != nil {
			return nil, err
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(filepath.Join(s.ArtifactDir, "release")); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
			}
		}
	}
	if err := s.Message("fixture completed"); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(secrets["token"]))
	return map[string]any{"secret_hash": hex.EncodeToString(sum[:]), "secret_count": len(secrets)}, nil
}
