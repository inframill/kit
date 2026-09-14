package packaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/inframill/kit/internal/catalog"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

func Remote(repository string, plain bool) (*remote.Repository, error) {
	if strings.Contains(repository, "://") {
		return nil, errors.New("registry must be an authority without scheme")
	}
	repo, e := remote.NewRepository(repository)
	if e != nil {
		return nil, e
	}
	if plain && !strings.HasPrefix(repository, "localhost:") && !strings.HasPrefix(repository, "127.0.0.1:") && !strings.HasPrefix(repository, "registry:") {
		return nil, errors.New("plain HTTP only allowed for explicit local registries")
	}
	repo.PlainHTTP = plain
	store, e := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if e != nil {
		return nil, e
	}
	repo.Client = &auth.Client{Cache: auth.NewCache(), Credential: credentials.Credential(store)}
	return repo, nil
}
func verifyGraph(ctx context.Context, store oras.ReadOnlyTarget, d v1.Descriptor) error {
	if d.Size < 0 || d.Size > 16<<30 || d.Digest.Validate() != nil {
		return errors.New("invalid descriptor")
	}
	f, e := store.Fetch(ctx, d)
	if e != nil {
		return e
	}
	verifier := d.Digest.Verifier()
	n, e := io.Copy(verifier, io.LimitReader(f, d.Size+1))
	f.Close()
	if e != nil {
		return e
	}
	if n != d.Size || !verifier.Verified() {
		return errors.New("blob checksum/size mismatch")
	}
	var b []byte
	if d.MediaType == v1.MediaTypeImageManifest || d.MediaType == v1.MediaTypeImageIndex {
		if d.Size > 1<<20 {
			return errors.New("oversized manifest")
		}
		b, e = content.FetchAll(ctx, store, d)
		if e != nil {
			return e
		}
	}
	switch d.MediaType {
	case v1.MediaTypeImageManifest:
		var m v1.Manifest
		if e = json.Unmarshal(b, &m); e != nil {
			return e
		}
		for _, child := range append(m.Layers, m.Config) {
			if e = verifyGraph(ctx, store, child); e != nil {
				return e
			}
		}
	case v1.MediaTypeImageIndex:
		var idx v1.Index
		if e = json.Unmarshal(b, &idx); e != nil {
			return e
		}
		for _, child := range idx.Manifests {
			if e = verifyGraph(ctx, store, child); e != nil {
				return e
			}
		}
	}
	return nil
}
func Push(ctx context.Context, layout, version, repository string, plain bool) (v1.Descriptor, error) {
	var zero v1.Descriptor
	if !catalog.ValidVersion(version) {
		return zero, errors.New("exact version required")
	}
	// A filesystem lock serializes manual publishers on this workstation. Across
	// workstations, release coordination is still required by the registry contract.
	lock := layout + ".push-lock"
	f, e := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return zero, fmt.Errorf("publisher lock: %w", e)
	}
	f.Close()
	defer os.Remove(lock)
	store, e := oci.New(layout)
	if e != nil {
		return zero, e
	}
	d, e := store.Resolve(ctx, version)
	if e != nil {
		return d, fmt.Errorf("built version unavailable; run its build/package task first: %w", e)
	}
	recordBytes, e := os.ReadFile(filepath.Join(filepath.Dir(layout), "build.json"))
	if e != nil {
		return d, fmt.Errorf("missing build record: %w", e)
	}
	var record struct {
		Descriptor v1.Descriptor `json:"descriptor"`
	}
	if e = json.Unmarshal(recordBytes, &record); e != nil {
		return d, e
	}
	if record.Descriptor.Digest != d.Digest || record.Descriptor.Size != d.Size {
		return d, errors.New("OCI root differs from build record")
	}
	if e = verifyGraph(ctx, store, d); e != nil {
		return d, e
	}
	repo, e := Remote(repository, plain)
	if e != nil {
		return d, e
	}
	if prior, e := repo.Resolve(ctx, version); e == nil {
		if prior.Digest != d.Digest {
			return d, errors.New("release tag already points to different content")
		}
		return d, nil
	} else if !errors.Is(e, errdef.ErrNotFound) {
		return d, e
	}
	// Copy by digest first; only expose the final tag after every blob is present.
	if _, e = oras.Copy(ctx, store, version, repo, d.Digest.String(), oras.DefaultCopyOptions); e != nil {
		return d, e
	}
	if prior, e := repo.Resolve(ctx, version); e == nil && prior.Digest != d.Digest {
		return d, errors.New("concurrent conflicting publication")
	} else if e != nil && !errors.Is(e, errdef.ErrNotFound) {
		return d, e
	}
	if e = repo.Tag(ctx, d, version); e != nil {
		return d, e
	}
	actual, e := repo.Resolve(ctx, version)
	if e != nil {
		return d, e
	}
	if actual.Digest != d.Digest {
		return d, errors.New("published digest mismatch")
	}
	return d, nil
}
func BlueprintLayout(root, name, version string) string {
	d := os.Getenv("KIT_DIST_DIR")
	if d == "" {
		d = filepath.Join(root, "dist")
	}
	return filepath.Join(d, "blueprints", name, version, "oci")
}
