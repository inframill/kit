// Package appconfig owns Inframill settings shared by daemon and local composition.
package appconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	cliconfig "github.com/sir-hassan/inframill/internal/cli/config"
	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/harness"
	harnessConfig "github.com/sir-hassan/inframill/pkg/harness/config"
)

const (
	DefaultFile       = ".inframill.yaml"
	DefaultDataDir    = ".inframill"
	ConnectEngineType = "connect-engine"
)

type EngineSettings struct {
	DataDir string                 `json:"dataDir"`
	Drivers drivers.Configurations `json:"drivers"`
}

type ConnectSettings struct {
	Path string `json:"path"`
}

type Config struct {
	Harness harness.Config
	DataDir string
	Drivers drivers.Configurations
}

// DecodeConnectSettings restricts the HTTP adapter to its own settings.
func DecodeConnectSettings(settings harness.Settings) (ConnectSettings, error) {
	var result ConnectSettings
	if err := settings.Decode(&result); err != nil {
		return result, errors.New("invalid Connect handler settings; only path is supported")
	}
	return result, nil
}

// decodeEngineSettings validates the application-owned engine section.
func decodeEngineSettings(section any) (EngineSettings, error) {
	var result EngineSettings
	settings, err := harness.NewSettings(section)
	if err != nil {
		return result, errors.New("engine must be an object")
	}
	if err := settings.Decode(&result); err != nil {
		return result, errors.New("invalid engine settings")
	}
	if result.DataDir == "" {
		result.DataDir = DefaultDataDir
	}
	if strings.TrimSpace(result.DataDir) == "" || strings.ContainsRune(result.DataDir, '\x00') {
		return result, errors.New("dataDir must be a nonempty directory path")
	}
	result.DataDir = filepath.Clean(result.DataDir)
	for id := range result.Drivers {
		if !drivers.ValidID(id) {
			return result, errors.New("invalid or reserved configured Driver ID")
		}
	}

	return result, nil
}

// Load uses the unchanged harness loader, including its environment snapshot rules.
func Load(ctx context.Context, path string, environ []string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, errors.New("--config requires a nonempty path")
	}
	config, err := harnessConfig.NewYAMLFile(path, harnessConfig.WithEnvironment(environ), harnessConfig.WithAllowedSections("cli", "engine")).Load(ctx)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, errors.New("application configuration file not found")
		}
		return Config{}, errors.New("cannot read or decode application configuration")
	}
	if section, exists := config.Sections["cli"]; exists {
		if _, sectionErr := cliconfig.Decode(section); sectionErr != nil {
			return Config{}, sectionErr
		}
	}
	if len(config.Handlers) != 1 || config.Handlers[0].Type != ConnectEngineType {
		return Config{}, errors.New("configuration requires exactly one connect-engine handler")
	}
	if _, err := DecodeConnectSettings(config.Handlers[0].Settings); err != nil {
		return Config{}, err
	}
	section, exists := config.Sections["engine"]
	if !exists {
		section = map[string]any{}
	}
	settings, err := decodeEngineSettings(section)
	if err != nil {
		return Config{}, err
	}
	paths := make(drivers.Configurations, len(settings.Drivers))
	for id, executable := range settings.Drivers {
		if executable.Path == "" {
			paths[id] = executable
			continue
		}
		if !filepath.IsAbs(executable.Path) {
			executable.Path = filepath.Join(filepath.Dir(path), executable.Path)
		}
		absolute, err := filepath.Abs(executable.Path)
		if err != nil {
			return Config{}, errors.New("cannot resolve Driver executable path")
		}
		executable.Path = absolute
		paths[id] = executable
	}
	return Config{Harness: config, DataDir: settings.DataDir, Drivers: paths}, nil
}
