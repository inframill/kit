package blueprint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/opencontainers/image-spec/specs-go"
	oci "github.com/opencontainers/image-spec/specs-go/v1"

	blueprintcontent "github.com/sir-hassan/inframill/internal/blueprint/content"
)

// BuildLayout writes an OCI image layout that ORAS can publish with `oras cp`.
// Output must not exist. The index reference is always blueprintKey.
func BuildLayout(ctx context.Context, source, output string) (string, error) {
	if _, checkErr := metadata(source); checkErr != nil {
		return "", checkErr
	}
	files, err := blueprintcontent.Snapshot(source)
	if err != nil {
		return "", err
	}
	a, err := Pack(files)
	if err != nil {
		return "", err
	}
	desc := descriptor(oci.MediaTypeImageManifest, a.Manifest)
	if _, checkErr := unpack(ctx, a, desc.Digest.String()); checkErr != nil {
		return "", checkErr
	}
	if checkErr := os.Mkdir(output, 0o700); checkErr != nil {
		return "", checkErr
	}
	if checkErr := os.MkdirAll(filepath.Join(output, "blobs", "sha256"), 0o700); checkErr != nil {
		return "", checkErr
	}
	for _, content := range [][]byte{a.Manifest, a.Config, a.Layer} {
		d := descriptor("", content)
		if checkErr := atomicFile(ctx, filepath.Join(output, "blobs", "sha256"), d.Digest.Encoded(), content); checkErr != nil {
			return "", checkErr
		}
	}
	desc.Annotations = map[string]string{oci.AnnotationRefName: blueprintKey}
	idx := oci.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: oci.MediaTypeImageIndex, Manifests: []oci.Descriptor{desc}}
	content, err := json.Marshal(idx)
	if err != nil {
		return "", err
	}
	if checkErr := atomicFile(ctx, output, "index.json", content); checkErr != nil {
		return "", checkErr
	}
	if checkErr := atomicFile(ctx, output, "oci-layout", []byte("{\"imageLayoutVersion\":\"1.0.0\"}")); checkErr != nil {
		return "", checkErr
	}
	return desc.Digest.String(), nil
}
