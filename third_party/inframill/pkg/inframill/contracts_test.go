package inframill_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

func TestIdentity(t *testing.T) {
	id := "a57df79d-4bbc-4c92-9997-0eac59927465"
	if got, err := inframill.ParseInstanceID(id); err != nil || string(got) != id {
		t.Fatal(got, err)
	}
	for _, s := range []string{"", "example/dev", "../../etc", "bad"} {
		if _, err := inframill.ParseInstanceID(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	for _, s := range []string{"example/dev", "project-1/cloud-2"} {
		if !inframill.ValidInstanceName(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"example", "example/dev/extra", "Example/dev", "example/invalid_instance"} {
		if inframill.ValidInstanceName(s) {
			t.Fatal(s)
		}
	}
}

func TestMetadataHasNoExecutionContent(t *testing.T) {
	for _, name := range []string{"Files", "Schema", "Systems", "Drivers", "Templates"} {
		if _, ok := reflect.TypeFor[inframill.Blueprint]().FieldByName(name); ok {
			t.Fatal(name)
		}
	}
}

func TestErrorExposesCodeMessageAndCause(t *testing.T) {
	cause := errors.New("cause")
	err := &inframill.Error{Code: inframill.InvalidRequest, Message: "invalid", Path: "field", Cause: cause}
	if err.Error() != "invalid" || !errors.Is(err, cause) {
		t.Fatal(err)
	}
}
