package packaging

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/oci"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("KIT_DIST_DIR", filepath.Join(root, "dist"))
	src := filepath.Join(root, "blueprints/test/versions/0.1.0")
	os.MkdirAll(filepath.Join(src, "template/opentofu-0-2-0"), 0755)
	os.MkdirAll(filepath.Join(root, "releases"), 0755)
	os.WriteFile(filepath.Join(src, "blueprint.yaml"), []byte("api_version: inframill.dev/blueprint/v1\ndescription: fixture\nvalues_schema: {type: object}\ndriver_settings:\n  opentofu-0-2-0:\n    id: opentofu-0-2-0\n    required_driver_version: 0.2.0\n    settings_file: kit-driver.json\n    backend: local\n    node_output: nodes\n    artifact: opentofu-0-2-0-nodes-v1.json\n"), 0644)
	os.WriteFile(filepath.Join(src, "template/opentofu-0-2-0/kit-driver.json"), []byte(`{"schema":1,"backend_config":{},"environment":{}}`), 0644)
	r := BlueprintRelease{InframillVersion: "0.6.0-dev", Name: "test", Version: "0.1.0", Protocol: 1, Platforms: []string{"linux/amd64", "linux/arm64"}, Architectures: []string{"amd64", "arm64"}, Drivers: []Requirement{{"opentofu-0-2-0", "opentofu", "0.2.0"}}, Status: "supported"}
	layer, e := Archive(src)
	if e != nil {
		t.Fatal(e)
	}
	r.SourceSHA256 = digest.FromBytes(layer).String()
	b, _ := json.Marshal(map[string]any{"version": "0.6.0-dev", "inframill": map[string]string{"version": "0.6.0-dev"}, "blueprints": []BlueprintRelease{r}})
	os.WriteFile(filepath.Join(root, "releases/0.6.0-dev.lock.json"), b, 0644)
	return root
}
func TestDeterministicPackageAndImmutableBuild(t *testing.T) {
	ctx := context.Background()
	root := fixture(t)
	a, e := Package(ctx, root, "test", "0.1.0", "0.6.0-dev")
	if e != nil {
		t.Fatal(e)
	}
	b, e := Package(ctx, root, "test", "0.1.0", "0.6.0-dev")
	if e != nil || a.Digest != b.Digest {
		t.Fatalf("not deterministic: %v", e)
	}
	store, e := oci.New(BlueprintLayout(root, "test", "0.1.0"))
	if e != nil {
		t.Fatal(e)
	}
	if e = verifyGraph(ctx, store, a); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "blueprints/test/versions/0.1.0/template/opentofu-0-2-0/main.tf"), []byte("changed"), 0644)
	if _, e = Package(ctx, root, "test", "0.1.0", "0.6.0-dev"); e == nil {
		t.Fatal("replaced immutable build")
	}
	blob := filepath.Join(BlueprintLayout(root, "test", "0.1.0"), "blobs/sha256", a.Digest.Encoded())
	os.Chmod(blob, 0600)
	if e := os.WriteFile(blob, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = verifyGraph(ctx, store, a); e == nil {
		t.Fatal("corruption accepted")
	}
}

func TestBlueprintRequiresSelectedRelease(t *testing.T) {
	root := fixture(t)
	for _, target := range []string{"0.6.0-dev", "0.6.0", "0.6.1", "0.7.0", "", "0.6"} {
		t.Run(target, func(t *testing.T) {
			_, _, err := Blueprint(root, "test", "0.1.0", target)
			if target == "0.6.0-dev" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatalf("got %v, want missing or invalid release rejection", err)
			}
		})
	}
}

func TestBlueprintInheritsEngineFromSelectedKitPatch(t *testing.T) {
	root := fixture(t)
	path := filepath.Join(root, "releases/0.6.0-dev.lock.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var release map[string]any
	if err := json.Unmarshal(data, &release); err != nil {
		t.Fatal(err)
	}
	release["version"] = "0.6.7"
	data, err = json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "releases/0.6.7.lock.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	blueprint, _, err := Blueprint(root, "test", "0.1.0", "0.6.7")
	if err != nil || blueprint.InframillVersion != "0.6.0-dev" {
		t.Fatalf("blueprint must inherit its engine target from the selected Kit patch: %+v, %v", blueprint, err)
	}
}
func TestUnsafeAndRuntimeSourceRejected(t *testing.T) {
	for _, name := range []string{"template/secret.pem", "template/terraform.tfstate", "unexpected.txt", "manifest.yaml", "schema.json"} {
		root := fixture(t)
		src := filepath.Join(root, "blueprints/test/versions/0.1.0")
		os.WriteFile(filepath.Join(src, name), []byte("secret"), 0644)
		if _, e := Archive(src); e == nil {
			t.Fatal("accepted " + name)
		}
	}
	root := fixture(t)
	src := filepath.Join(root, "blueprints/test/versions/0.1.0")
	os.Symlink("/etc/passwd", filepath.Join(src, "template/link"))
	if _, e := Archive(src); e == nil {
		t.Fatal("accepted link")
	}
}
func TestCorruptChildBlobRejected(t *testing.T) {
	root := fixture(t)
	ctx := context.Background()
	d, e := Package(ctx, root, "test", "0.1.0", "0.6.0-dev")
	if e != nil {
		t.Fatal(e)
	}
	store, _ := oci.New(BlueprintLayout(root, "test", "0.1.0"))
	mb, _ := os.ReadFile(filepath.Join(BlueprintLayout(root, "test", "0.1.0"), "blobs/sha256", d.Digest.Encoded()))
	var m v1.Manifest
	json.Unmarshal(mb, &m)
	blob := filepath.Join(BlueprintLayout(root, "test", "0.1.0"), "blobs/sha256", m.Layers[0].Digest.Encoded())
	os.Chmod(blob, 0600)
	if e := os.WriteFile(blob, bytes.Repeat([]byte("x"), int(m.Layers[0].Size)), 0600); e != nil {
		t.Fatal(e)
	}
	if e = verifyGraph(ctx, store, d); e == nil {
		t.Fatal("bad layer accepted")
	}
}

func TestInvalidOrRemoteSchemaRejectedOffline(t *testing.T) {
	for _, schema := range []map[string]any{{"type": 42}, {"$ref": "https://example.invalid/remote.json"}} {
		if e := validateSchema(schema); e == nil {
			t.Fatal("accepted invalid/external schema")
		}
	}
}
