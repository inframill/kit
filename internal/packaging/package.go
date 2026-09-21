// Package packaging implements Inframill's portable OCI blueprint contract.
package packaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/oci"
)

const ArtifactType = "application/vnd.inframill.blueprint.v1"
const LayerType = "application/vnd.inframill.blueprint.layer.v1.tar+gzip"

func Descriptor(media string, b []byte) v1.Descriptor {
	return v1.Descriptor{MediaType: media, Digest: digest.FromBytes(b), Size: int64(len(b))}
}
func Package(ctx context.Context, root, name, version, kitVersion string) (v1.Descriptor, error) {
	release, src, e := Blueprint(root, name, version, kitVersion)
	if e != nil {
		return v1.Descriptor{}, e
	}
	layer, e := Archive(src)
	if e != nil {
		return v1.Descriptor{}, e
	}
	if digest.FromBytes(layer).String() != release.SourceSHA256 {
		return v1.Descriptor{}, errors.New("source changed after validation")
	}
	dest := filepath.Dir(BlueprintLayout(root, name, version))
	if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
		return v1.Descriptor{}, e
	}
	tmp, e := os.MkdirTemp(filepath.Dir(dest), ".package-")
	if e != nil {
		return v1.Descriptor{}, e
	}
	defer os.RemoveAll(tmp)
	store, e := oci.New(filepath.Join(tmp, "oci"))
	if e != nil {
		return v1.Descriptor{}, e
	}
	config := []byte("{}")
	cd := Descriptor(v1.MediaTypeEmptyJSON, config)
	ld := Descriptor(LayerType, layer)
	// Inframill reconstructs this manifest when validating extracted source.
	// Keep its canonical envelope byte-for-byte, without extra annotations.
	m := v1.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: v1.MediaTypeImageManifest, ArtifactType: ArtifactType, Config: cd, Layers: []v1.Descriptor{ld}}
	mb, _ := json.Marshal(m)
	md := Descriptor(v1.MediaTypeImageManifest, mb)
	for _, item := range []struct {
		d v1.Descriptor
		b []byte
	}{{cd, config}, {ld, layer}, {md, mb}} {
		if e = store.Push(ctx, item.d, bytes.NewReader(item.b)); e != nil {
			return md, e
		}
	}
	if e = store.Tag(ctx, md, version); e != nil {
		return md, e
	}
	record := struct {
		Release    BlueprintRelease `json:"release"`
		Descriptor v1.Descriptor    `json:"descriptor"`
	}{release, md}
	b, _ := json.MarshalIndent(record, "", "  ")
	if e = os.WriteFile(filepath.Join(tmp, "build.json"), b, 0644); e != nil {
		return md, e
	}
	if _, e = os.Stat(dest); e == nil {
		existing, e := oci.New(filepath.Join(dest, "oci"))
		if e != nil {
			return md, e
		}
		d, e := existing.Resolve(ctx, version)
		if e != nil || d.Digest != md.Digest {
			return md, errors.New("different output already exists for this version; choose a new version or explicitly remove the development output")
		}
		if e = verifyGraph(ctx, existing, d); e != nil {
			return md, e
		}
		return md, nil
	}
	return md, os.Rename(tmp, dest)
}
