package config_test

import (
	"context"
	"reflect"
	"testing"

	harnessconfig "github.com/sir-hassan/inframill/pkg/harness/config"
	harnesshttp "github.com/sir-hassan/inframill/pkg/harness/http"
	harnesslogger "github.com/sir-hassan/inframill/pkg/harness/logger"
)

func TestBundledYAMLDescribesCompleteApplication(t *testing.T) {
	loaded, err := harnessconfig.NewYAMLFile(
		"../../../inframill.yaml",
		harnessconfig.WithEnvironment([]string{
			"ROOT_PASSWORD=root-secret",
			"GOOGLE_WEB_CLIENT_ID=web-client",
			"GOOGLE_IOS_CLIENT_ID=ios-client",
			"GOOGLE_ANDROID_CLIENT_ID=android-client",
			"DATABASE_URL=postgres://database",
		}),
	).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Main.Port != 8080 || loaded.Main.DatabaseURL != "postgres://database" || loaded.Logger.Type != harnesslogger.TypeSlog || len(loaded.Handlers) != 6 || len(loaded.Workers) != 0 {
		t.Fatalf("config = %#v", loaded)
	}
	wantTypes := []string{
		"auth",
		"connect-system",
		"connect-user",
		"connect-todo",
		"connect-book",
		harnesshttp.StaticType,
	}
	for index, handler := range loaded.Handlers {
		if handler.Type != wantTypes[index] {
			t.Fatalf("handlers[%d].type = %q, want %q", index, handler.Type, wantTypes[index])
		}
	}
	var static harnesshttp.StaticSettings
	if err := loaded.Handlers[len(loaded.Handlers)-1].Settings.Decode(&static); err != nil {
		t.Fatal(err)
	}
	if static.Path != "/" {
		t.Fatalf("static handler = %#v", loaded.Handlers[len(loaded.Handlers)-1])
	}
	var auth struct {
		Path      string   `json:"path"`
		RootToken string   `json:"rootToken"`
		Issuer    string   `json:"issuer"`
		JWKSURL   string   `json:"jwksUrl"`
		Audiences []string `json:"audiences"`
	}
	if err := loaded.Handlers[0].Settings.Decode(&auth); err != nil {
		t.Fatal(err)
	}
	if auth.RootToken != "root-secret" {
		t.Fatal("auth root token was not resolved from the supplied environment")
	}
	wantAudiences := []string{"web-client", "ios-client", "android-client"}
	if !reflect.DeepEqual(auth.Audiences, wantAudiences) {
		t.Fatalf("auth audiences = %v, want %v", auth.Audiences, wantAudiences)
	}
}
