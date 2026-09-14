package blueprint

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	oci "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/sir-hassan/inframill/internal/platform/filetree"
)

const (
	manifestFilename = "manifest.yaml"
	schemaFilename   = "schema.json"
	ArtifactType     = "application/vnd.inframill.blueprint.v1"
	LayerType        = "application/vnd.inframill.blueprint.layer.v1.tar+gzip"
	maxManifest      = 1 << 20
	maxConfig        = 1 << 10
	maxLayer         = 64 << 20
	maxExpanded      = 256 << 20
	maxEntries       = 10000
)

// Archive is the complete deterministic OCI artifact, ready for local publication.
type Archive struct {
	Manifest []byte
	Config   []byte
	Layer    []byte
}

func descriptor(mediaType string, content []byte) oci.Descriptor {
	return oci.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(content), Size: int64(len(content))}
}

// Pack builds the OCI envelope without volatile timestamps or source paths.
func Pack(files map[string]filetree.File) (Archive, error) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	for _, name := range sortedKeys(files) {
		file := files[name]
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(file.Content)), Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if file.Executable {
			header.Mode = 0o755
		}
		if file.Directory {
			header.Typeflag = tar.TypeDir
			header.Mode = 0o755
			header.Size = 0
		}
		if err := tw.WriteHeader(header); err != nil {
			return Archive{}, err
		}
		if !file.Directory {
			if _, err := tw.Write(file.Content); err != nil {
				return Archive{}, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return Archive{}, err
	}
	if err := gz.Close(); err != nil {
		return Archive{}, err
	}
	a := Archive{Config: []byte("{}"), Layer: compressed.Bytes()}
	m := oci.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: oci.MediaTypeImageManifest, ArtifactType: ArtifactType,
		Config: descriptor(oci.MediaTypeEmptyJSON, a.Config), Layers: []oci.Descriptor{descriptor(LayerType, a.Layer)},
	}
	var err error
	a.Manifest, err = json.Marshal(m)
	return a, err
}

func validDescriptor(d oci.Descriptor, limit int64) bool {
	return d.Size >= 0 && d.Size <= limit && d.Digest.Algorithm() == digest.SHA256 && d.Digest.Validate() == nil && len(d.URLs) == 0
}

func manifest(content []byte) (oci.Manifest, error) {
	var m oci.Manifest
	if len(content) > maxManifest || json.Unmarshal(content, &m) != nil || m.SchemaVersion != 2 || m.MediaType != oci.MediaTypeImageManifest || m.ArtifactType != ArtifactType || m.Subject != nil || len(m.Layers) != 1 {
		return m, errors.New("unsupported Blueprint OCI manifest")
	}
	if m.Config.MediaType != oci.MediaTypeEmptyJSON || !validDescriptor(m.Config, maxConfig) || m.Layers[0].MediaType != LayerType || !validDescriptor(m.Layers[0], maxLayer) {
		return m, errors.New("unsupported or oversized Blueprint OCI descriptors")
	}
	return m, nil
}

func verified(d oci.Descriptor, content []byte) bool {
	return d.Size == int64(len(content)) && d.Digest == digest.FromBytes(content)
}

func unpack(ctx context.Context, a Archive, expected string) (map[string]filetree.File, error) {
	if digest.FromBytes(a.Manifest).String() != expected {
		return nil, errors.New("blueprint manifest digest mismatch")
	}
	m, err := manifest(a.Manifest)
	if err != nil {
		return nil, err
	}
	if !verified(m.Config, a.Config) || !bytes.Equal(a.Config, []byte("{}")) || !verified(m.Layers[0], a.Layer) {
		return nil, errors.New("blueprint blob digest or size mismatch")
	}
	gz, err := gzip.NewReader(bytes.NewReader(a.Layer))
	if err != nil {
		return nil, errors.New("invalid compressed Blueprint layer")
	}
	defer gz.Close() //nolint:errcheck // Read-only decompressor.
	limited := &io.LimitedReader{R: gz, N: maxExpanded + 1}
	tr := tar.NewReader(limited)
	files := map[string]filetree.File{}
	for count := 0; ; count++ {
		if checkErr := ctx.Err(); checkErr != nil {
			return nil, checkErr
		}
		h, readErr := tr.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil || count >= maxEntries {
			return nil, errors.New("invalid or oversized Blueprint archive")
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "" || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || (name != manifestFilename && name != schemaFilename && name != templateDirectory && !strings.HasPrefix(name, "template/")) {
			return nil, errors.New("unsafe Blueprint archive path")
		}
		if _, exists := files[name]; exists {
			return nil, errors.New("duplicate Blueprint archive path")
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			return nil, errors.New("unsupported Blueprint archive entry")
		}
		if h.Size < 0 || h.Size > maxExpanded || h.Size > limited.N {
			return nil, errors.New("blueprint archive exceeds size limit")
		}
		file := filetree.File{Directory: h.Typeflag == tar.TypeDir, Executable: h.Mode&0o111 != 0}
		if !file.Directory {
			file.Content, err = io.ReadAll(&contextReader{ctx: ctx, reader: tr})
			if err != nil {
				return nil, err
			}
		}
		files[name] = file
	}
	// Drain the bounded stream to verify gzip trailers and reject decompression bombs.
	if _, checkErr := io.Copy(io.Discard, &contextReader{ctx: ctx, reader: limited}); checkErr != nil {
		return nil, checkErr
	}
	if limited.N <= 0 {
		return nil, errors.New("blueprint archive exceeds expanded size limit")
	}
	for _, required := range []string{manifestFilename, schemaFilename, templateDirectory} {
		f, found := files[required]
		if !found || f.Directory != (required == templateDirectory) {
			return nil, errors.New("blueprint archive is missing required layout")
		}
	}
	for name := range files {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if f, found := files[parent]; !found || !f.Directory {
				return nil, errors.New("blueprint archive has invalid parent directory")
			}
		}
	}
	return files, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if checkErr := r.ctx.Err(); checkErr != nil {
		return 0, checkErr
	}
	return r.reader.Read(p)
}
