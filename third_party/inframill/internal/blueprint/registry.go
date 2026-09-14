package blueprint

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	oci "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

const blueprintKey = "blueprint"

func parseReference(value string) (registry.Reference, error) {
	ref, err := registry.ParseReference(value)
	if err != nil || strings.ContainsAny(value, "?#\\ \t\n\r") || strings.Contains(value, "://") || (!strings.ContainsAny(ref.Registry, ".:") && ref.Registry != "localhost") || ref.Registry != strings.ToLower(ref.Registry) {
		return ref, packageError(inframill.InvalidRequest, "invalid fully qualified package reference")
	}
	if strings.Contains(value, "@") {
		d, digestErr := ref.Digest()
		if digestErr != nil || d.Algorithm() != digest.SHA256 || d.Validate() != nil || value != ref.Registry+"/"+ref.Repository+"@"+d.String() {
			return ref, packageError(inframill.InvalidRequest, "package pin must be repository@sha256:<64 lowercase hex>")
		}
	} else if ref.Reference == "" {
		ref.Reference = "latest"
	}
	return ref, nil
}

func packageError(code inframill.ErrorCode, message string) error {
	return &inframill.Error{Code: code, Path: blueprintKey, Message: message}
}

// remoteArchive only performs anonymous registry reads. Tokens stay in memory.
func remoteArchive(ctx context.Context, ref registry.Reference, plainHTTP bool) (Archive, string, error) {
	repository, err := remote.NewRepository(ref.Registry + "/" + ref.Repository)
	if err != nil {
		return Archive{}, "", err
	}
	repository.PlainHTTP = plainHTTP
	repository.ManifestMediaTypes = []string{oci.MediaTypeImageManifest}
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many registry redirects")
		}
		if req.URL.Scheme != "https" && (!plainHTTP || req.URL.Host != ref.Registry || req.URL.Scheme != "http") {
			return errors.New("registry redirect requires HTTPS")
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			req.Header.Del("Authorization")
		}
		return nil
	}}
	// Check token realm requests as well as redirect targets.
	client.Transport = publicTransport{base: http.DefaultTransport, registry: ref.Registry, plainHTTP: plainHTTP}
	repository.Client = &auth.Client{Client: client, Cache: auth.NewCache()}
	d, err := repository.Resolve(ctx, ref.Reference)
	if err != nil {
		return Archive{}, "", remoteError(ctx)
	}
	if !validDescriptor(d, maxManifest) || d.MediaType != oci.MediaTypeImageManifest {
		return Archive{}, "", packageError(inframill.InvalidConfig, "unsupported or oversized package manifest")
	}
	if strings.Contains(ref.Reference, ":") && d.Digest.String() != ref.Reference {
		return Archive{}, "", packageError(inframill.InvalidConfig, "registry returned a different package digest")
	}
	var a Archive
	a.Manifest, err = fetchBlob(ctx, repository, d, maxManifest)
	if err != nil {
		return Archive{}, "", err
	}
	m, err := manifest(a.Manifest)
	if err != nil {
		return Archive{}, "", packageError(inframill.InvalidConfig, err.Error())
	}
	a.Config, err = fetchBlob(ctx, repository, m.Config, maxConfig)
	if err != nil {
		return Archive{}, "", err
	}
	a.Layer, err = fetchBlob(ctx, repository, m.Layers[0], maxLayer)
	return a, d.Digest.String(), err
}

type publicTransport struct {
	base      http.RoundTripper
	registry  string
	plainHTTP bool
}

func (t publicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.User != nil || (req.URL.Scheme != "https" && (!t.plainHTTP || req.URL.Host != t.registry || req.URL.Scheme != "http")) {
		return nil, errors.New("registry transport requires HTTPS")
	}
	return t.base.RoundTrip(req)
}

func fetchBlob(ctx context.Context, repository *remote.Repository, d oci.Descriptor, limit int64) ([]byte, error) {
	if !validDescriptor(d, limit) {
		return nil, packageError(inframill.InvalidConfig, "invalid package descriptor")
	}
	reader, err := repository.Fetch(ctx, d)
	if err != nil {
		return nil, remoteError(ctx)
	}
	defer reader.Close() //nolint:errcheck // Read response; bytes/digest checked below.
	content, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: reader}, d.Size+1))
	if err != nil {
		return nil, remoteError(ctx)
	}
	if !verified(d, content) {
		return nil, packageError(inframill.InvalidConfig, "package digest or size mismatch")
	}
	return content, nil
}

func remoteError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return packageError(inframill.CommandFailed, "public registry fetch failed; check reference, availability and anonymous pull access")
}
