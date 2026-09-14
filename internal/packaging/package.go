// Package packaging implements Inframill's portable OCI blueprint contract.
package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
func Archive(src string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	var expanded int64
	count := 0
	e := filepath.WalkDir(src, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel != "manifest.yaml" && rel != "schema.json" && rel != "template" && !strings.HasPrefix(rel, "template/") {
			return fmt.Errorf("unexpected source file %s", rel)
		}
		if !fs.ValidPath(rel) || strings.ContainsAny(rel, "\\\x00") {
			return errors.New("unsafe source path")
		}
		for _, part := range strings.Split(rel, "/") {
			if part == ".terraform" || part == ".inframill" || part == "runtime" || part == ".git" || strings.HasPrefix(part, ".env") || strings.HasPrefix(part, ".kit-") || strings.HasSuffix(part, ".tfstate") || strings.HasSuffix(part, ".tfplan") || strings.HasSuffix(part, ".tfvars") || strings.HasSuffix(part, ".tfvars.json") || strings.HasSuffix(part, ".pem") || strings.HasSuffix(part, ".key") {
				return fmt.Errorf("runtime or secret file excluded: %s", rel)
			}
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		if !st.IsDir() && !st.Mode().IsRegular() {
			return errors.New("links and special files rejected")
		}
		count++
		if count > 10000 {
			return errors.New("too many archive entries")
		}
		size := st.Size()
		if st.IsDir() {
			size = 0
		}
		expanded += 512 + ((size+511)/512)*512
		if expanded > 250<<20 {
			return errors.New("archive exceeds consumer expanded limit")
		}
		h := &tar.Header{Name: rel, Size: size, Mode: 0644, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if st.IsDir() {
			h.Mode = 0755
			h.Typeflag = tar.TypeDir
		} else if st.Mode()&0111 != 0 {
			h.Mode = 0755
		}
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if !st.IsDir() {
			if size > 64<<20 {
				return errors.New("oversized source file")
			}
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			if int64(len(b)) != size {
				return errors.New("source changed during packaging")
			}
			_, e = tw.Write(b)
			return e
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if e = tw.Close(); e != nil {
		return nil, e
	}
	if e = gz.Close(); e != nil {
		return nil, e
	}
	if buf.Len() > 64<<20 {
		return nil, errors.New("compressed archive too large")
	}
	return buf.Bytes(), nil
}
func Package(ctx context.Context, root, name, version string) (v1.Descriptor, error) {
	release, src, e := Blueprint(root, name, version)
	if e != nil {
		return v1.Descriptor{}, e
	}
	layer, e := Archive(src)
	if e != nil {
		return v1.Descriptor{}, e
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
		return md, nil
	}
	return md, os.Rename(tmp, dest)
}
