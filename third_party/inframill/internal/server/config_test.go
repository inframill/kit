package server

import (
	"testing"
	"time"

	"github.com/sir-hassan/inframill/internal/appconfig"
	cliconfig "github.com/sir-hassan/inframill/internal/cli/config"
	"github.com/sir-hassan/inframill/internal/connectadapter"
	harnessLogger "github.com/sir-hassan/inframill/pkg/harness/logger"
)

// SPEC-0000 REQ-014 / SPEC-0002 REQ-013: the shipped configuration supplies
// both native settings and CLI defaults without acquiring server resources.
func TestBundledYAMLDescribesCompleteApplication(t *testing.T) {
	application, err := appconfig.Load(t.Context(), "../../.inframill.yaml", nil)
	if err != nil {
		t.Fatal(err)
	}
	loaded := application.Harness
	if loaded.Main.Port != 8080 || loaded.Logger.Type != harnessLogger.TypeSlog || len(loaded.Handlers) != 1 || len(loaded.Workers) != 0 {
		t.Fatalf("unexpected configuration: %#v", loaded)
	}
	if loaded.Handlers[0].Type != connectadapter.ConnectEngineType {
		t.Fatal("incorrect handler type")
	}
	settings, err := appconfig.DecodeConnectSettings(loaded.Handlers[0].Settings)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Path != "/inframill.v1.EngineService/" {
		t.Fatal("incorrect RPC mount")
	}
	if application.DataDir != appconfig.DefaultDataDir {
		t.Fatal("bundled data directory changed", application.DataDir)
	}
	if loaded.Main.ShutdownTimeout != 10*time.Second || loaded.Main.ReadHeaderTimeout != 5*time.Second || loaded.Main.ReadTimeout != 30*time.Second || loaded.Main.WriteTimeout != 150*time.Second || loaded.Main.IdleTimeout != 60*time.Second {
		t.Fatal("server timeouts changed")
	}
	preferences, loadErr := cliconfig.Load("../../.inframill.yaml", true)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if preferences.Endpoint == nil || *preferences.Endpoint != "http://127.0.0.1:8080" || preferences.Timeout == nil || *preferences.Timeout != "2m" || preferences.Output == nil || *preferences.Output != "table" || preferences.Color == nil || *preferences.Color != "auto" {
		t.Fatal("bundled CLI defaults changed", preferences)
	}
}
