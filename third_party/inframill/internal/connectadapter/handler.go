package connectadapter

import (
	"context"
	"net/http"

	"github.com/sir-hassan/inframill/internal/appconfig"
	"github.com/sir-hassan/inframill/internal/gen/inframill/v1/inframillv1connect"
	"github.com/sir-hassan/inframill/pkg/harness"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

// ConnectEngineType selects the configured Engine RPC handler.
const ConnectEngineType = appconfig.ConnectEngineType

const enginePath = "/" + inframillv1connect.EngineServiceName + "/"

// NewHandlerFactories binds the configured RPC handler to one shared engine.
func NewHandlerFactories(engine *inframill.Engine) harness.HandlerFactories {
	return harness.HandlerFactories{ConnectEngineType: engineHandlerFactory{engine: engine}}
}

type engineHandlerFactory struct{ engine *inframill.Engine }

func (factory engineHandlerFactory) Validate(_ context.Context, config harness.HandlerConfig) (string, harness.Violations) {
	settings, err := appconfig.DecodeConnectSettings(config.Settings)
	if err != nil {
		return "", harness.Violations{{Path: "settings", Code: "invalid", Message: "invalid Connect handler settings"}}
	}
	var violations harness.Violations
	if settings.Path != enginePath {
		violations = append(violations, harness.Violation{Path: "settings.path", Code: "invalid", Message: "path must match the compiled Engine Connect service"})
	}
	if factory.engine == nil {
		violations = append(violations, harness.Violation{Path: "", Code: "required", Message: "engine is required"})
	}
	return settings.Path, violations
}

func (factory engineHandlerFactory) Create(ctx context.Context, _ harness.Logger, _ http.Handler, config harness.HandlerConfig) (http.Handler, error) {
	if _, violations := factory.Validate(ctx, config); len(violations) > 0 {
		return nil, harness.NewValidationError(violations)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return Handler(factory.engine), nil
}
