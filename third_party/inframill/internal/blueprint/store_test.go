package blueprint

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	oci "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/sir-hassan/inframill/internal/backend"
	"github.com/sir-hassan/inframill/internal/platform/filetree"
	drivers "github.com/sir-hassan/inframill/pkg/driver"
	"github.com/sir-hassan/inframill/pkg/inframill"
)

const (
	firstMode = "one"
	modeKey   = "label"
	indexCase = "index"
)

func packageFixture(t *testing.T, mode string) Archive {
	t.Helper()
	schema := fmt.Sprintf(`{"type":"object","required":["label"],"properties":{"label":{"const":%q}}}`, mode)
	a, err := Pack(map[string]filetree.File{
		"manifest.yaml": {Content: []byte("api_version: inframill.dev/blueprint/v1\ndescription: package fixture\ndrivers:\n  debug: {}\n")},
		"schema.json":   {Content: []byte(schema)}, "template": {Directory: true}, "template/debug": {Directory: true},
		"template/debug/main.txt": {Content: []byte("# " + mode + "\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type testRegistry struct {
	server   *httptest.Server
	mu       sync.Mutex
	archive  Archive
	requests atomic.Int64
	broken   bool
	token    bool
}

func newRegistry(t *testing.T, a Archive) *testRegistry {
	t.Helper()
	r := &testRegistry{archive: a}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.broken {
			http.Error(w, "private credential fixture-do-not-leak", http.StatusUnauthorized)
			return
		}
		if r.token {
			if req.URL.Path == "/token" {
				_, _ = w.Write([]byte(`{"token":"fixture-token-do-not-leak"}`)) //nolint:errcheck // Disposable HTTP peer can disconnect.
				return
			}
			if req.Header.Get("Authorization") != "Bearer fixture-token-do-not-leak" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+r.server.URL+`/token",service="fixture",scope="repository:fixture:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		var content []byte
		mediaType := "application/octet-stream"
		switch {
		case strings.Contains(req.URL.Path, "/manifests/"):
			key := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
			if key != "latest" && key != "v1" && key != "alias" && key != digest.FromBytes(r.archive.Manifest).String() {
				http.NotFound(w, req)
				return
			}
			content = r.archive.Manifest
			mediaType = oci.MediaTypeImageManifest
		case strings.HasSuffix(req.URL.Path, digest.FromBytes(r.archive.Config).String()):
			content = r.archive.Config
		case strings.HasSuffix(req.URL.Path, digest.FromBytes(r.archive.Layer).String()):
			content = r.archive.Layer
		default:
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", mediaType)
		w.Header().Set("Docker-Content-Digest", digest.FromBytes(content).String())
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		if req.Method != http.MethodHead {
			_, _ = w.Write(content) //nolint:errcheck // Disposable HTTP peer can disconnect.
		}
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *testRegistry) reference() string {
	return strings.TrimPrefix(r.server.URL, "http://") + "/fixture"
}

func packageStore(t *testing.T, r *testRegistry) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "packages"), []string{strings.TrimPrefix(r.server.URL, "http://")})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLocalPackages(t *testing.T) {
	r := newRegistry(t, packageFixture(t, firstMode))
	s := packageStore(t, r)
	if packages, checkErr := s.List(t.Context()); checkErr != nil || len(packages) != 0 {
		t.Fatal(packages, checkErr)
	}
	if _, checkErr := os.Stat(s.root); !os.IsNotExist(checkErr) {
		t.Fatal("inspection created store", checkErr)
	}
	if _, checkErr := s.Resolve(t.Context(), r.reference()); checkErr == nil || !strings.Contains(checkErr.Error(), "fetch this Blueprint first") {
		t.Fatal(checkErr)
	}
	if r.requests.Load() != 0 {
		t.Fatal("local resolution used registry")
	}
	p, err := s.Fetch(t.Context(), r.reference())
	if err != nil {
		t.Fatal(err)
	}
	if p.Reference != r.reference()+"@"+digest.FromBytes(r.archive.Manifest).String() || len(p.Tags) != 1 || p.Tags[0] != r.reference()+":latest" {
		t.Fatal(p)
	}
	first, err := s.Resolve(t.Context(), r.reference())
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.archive = packageFixture(t, "two")
	r.mu.Unlock()
	secondPackage, err := s.Fetch(t.Context(), r.reference())
	if err != nil || secondPackage.Reference == p.Reference {
		t.Fatal(secondPackage, err)
	}
	second, err := s.Resolve(t.Context(), r.reference()+":latest")
	if err != nil {
		t.Fatal(err)
	}
	if s.ValidateValues(t.Context(), first, inframill.InstanceValues{modeKey: firstMode}) != nil || s.ValidateValues(t.Context(), second, inframill.InstanceValues{modeKey: firstMode}) == nil {
		t.Fatal("same-name validators mixed")
	}
	if _, checkErr := s.Resolve(t.Context(), p.Reference); checkErr != nil {
		t.Fatal("old digest lost", checkErr)
	}
	r.mu.Lock()
	r.broken = true
	r.mu.Unlock()
	if _, checkErr := s.Fetch(t.Context(), r.reference()); checkErr == nil || strings.Contains(checkErr.Error(), "fixture-do-not-leak") {
		t.Fatal("private registry error was not sanitized", checkErr)
	}
	before := r.requests.Load()
	reopened, err := NewStore(s.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, checkErr := reopened.Resolve(t.Context(), r.reference()); checkErr != nil {
		t.Fatal("offline restart", checkErr)
	}
	entries, err := reopened.List(t.Context())
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	blueprints, err := reopened.List(t.Context())
	if err != nil || len(blueprints) != 2 {
		t.Fatal(blueprints, err)
	}
	if before != r.requests.Load() {
		t.Fatal("inspection/render contacted registry")
	}
}

func TestBlueprintReferences(t *testing.T) {
	for _, good := range []string{"ghcr.io/org/fixture", "ghcr.io/org/fixture:v1", "localhost:5000/fixture", "ghcr.io/org/fixture@sha256:" + strings.Repeat("a", 64)} {
		if _, checkErr := parseReference(good); checkErr != nil {
			t.Errorf("%s: %v", good, checkErr)
		}
	}
	for _, bad := range []string{"fixture", "org/fixture:v1", "https://ghcr.io/org/fixture", "user:pass@ghcr.io/fixture", "ghcr.io/fixture?secret=x", "ghcr.io/fixture#x", "ghcr.io/fixture@md5:abcd", "ghcr.io/fixture:v1@sha256:" + strings.Repeat("a", 64), "ghcr.io/fixture@sha256:" + strings.Repeat("A", 64)} {
		if _, checkErr := parseReference(bad); checkErr == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestAnonymousRegistryAndCancellation(t *testing.T) {
	r := newRegistry(t, packageFixture(t, firstMode))
	r.token = true
	s := packageStore(t, r)
	if _, checkErr := s.Fetch(t.Context(), r.reference()); checkErr != nil {
		t.Fatal(checkErr)
	}
	content, err := os.ReadFile(filepath.Join(s.root, "index.json"))
	if err != nil || bytes.Contains(content, []byte("fixture-token")) {
		t.Fatal("token persisted", err)
	}
	secure, err := NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, checkErr := secure.Fetch(t.Context(), r.reference()); checkErr == nil {
		t.Fatal("HTTP was enabled implicitly")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, checkErr := s.Fetch(ctx, r.reference()); !errors.Is(checkErr, context.Canceled) {
		t.Fatal(checkErr)
	}
}

func TestConcurrentFetchAndIntegrity(t *testing.T) {
	r := newRegistry(t, packageFixture(t, firstMode))
	s := packageStore(t, r)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, checkErr := s.Fetch(t.Context(), r.reference()); checkErr != nil {
				t.Error(checkErr)
			}
		})
	}
	wg.Wait()
	packages, err := s.List(t.Context())
	if err != nil || len(packages) != 1 {
		t.Fatal(packages, err)
	}
	layer := filepath.Join(s.root, strings.TrimPrefix(packages[0].Digest, "sha256:"), "layer.tar.gz")
	if checkErr := os.WriteFile(layer, []byte("corrupt"), 0o600); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := s.Resolve(t.Context(), r.reference()); checkErr == nil {
		t.Fatal("corrupt content accepted even with cached schema")
	}
}

func TestPackageInstanceProvenance(t *testing.T) {
	r := newRegistry(t, packageFixture(t, firstMode))
	s := packageStore(t, r)
	pinned, err := s.Fetch(t.Context(), r.reference())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	b, err := backend.New(root, testDrivers(t), backend.Options{})
	if err != nil {
		t.Fatal(err)
	}
	e, err := inframill.New(t.Context(), s, b)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := e.Start(t.Context()); checkErr != nil {
		t.Fatal(checkErr)
	}
	defer e.Close()
	config := inframill.InstanceConfig{Blueprint: r.reference(), Values: inframill.InstanceValues{modeKey: firstMode}}
	info, err := e.InitInstance(t.Context(), "test/dev", config)
	if err != nil || info.Blueprint != pinned.Reference {
		t.Fatal(info, err)
	}
	if _, checkErr := e.InitInstance(t.Context(), "test/dev", config); !errors.Is(checkErr, inframill.ErrAlreadyExists) {
		t.Fatal(checkErr)
	}
	if checkErr := e.Shutdown(t.Context()); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.RemoveAll(s.root); checkErr != nil {
		t.Fatal(checkErr)
	}
	restored, err := b.SpawnInstance(t.Context(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close(context.Background()) //nolint:errcheck // Fixture teardown.
	if restored.Info().Status != inframill.InstanceIdle || restored.Info().Blueprint != pinned.Reference {
		t.Fatal(restored.Info())
	}
	data, err := os.ReadFile(filepath.Join(root, string(info.ID), "workspace/debug/main.txt"))
	if err != nil || !bytes.Contains(data, []byte(firstMode)) {
		t.Fatalf("%s %v", data, err)
	}
}

func TestPackageArchiveRejections(t *testing.T) {
	base := packageFixture(t, firstMode)
	for _, kind := range []string{"manifest", "config", "layer", indexCase, "limit", "traversal", "absolute", "link", "duplicate", "parent", "entry-limit"} {
		t.Run(kind, func(t *testing.T) {
			a := Archive{Manifest: bytes.Clone(base.Manifest), Config: bytes.Clone(base.Config), Layer: bytes.Clone(base.Layer)}
			expected := digest.FromBytes(a.Manifest).String()
			switch kind {
			case "manifest":
				a.Manifest[0] = '!'
			case "config":
				a.Config = []byte("no")
			case "layer":
				a.Layer[0] = '!'
			case indexCase, "limit":
				m, err := manifest(a.Manifest)
				if err != nil {
					t.Fatal(err)
				}
				if kind == indexCase {
					m.MediaType = oci.MediaTypeImageIndex
				} else {
					m.Layers[0].Size = maxLayer + 1
				}
				a.Manifest, err = json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				expected = digest.FromBytes(a.Manifest).String()
			default:
				var buf bytes.Buffer
				gz := gzip.NewWriter(&buf)
				tw := tar.NewWriter(gz)
				headers := []*tar.Header{{Name: "../escape", Mode: 0o600, Typeflag: tar.TypeReg}}
				switch kind {
				case "absolute":
					headers[0].Name = "/escape"
				case "link":
					headers[0].Name = "template/link"
					headers[0].Typeflag = tar.TypeSymlink
					headers[0].Linkname = "../../escape"
				case "duplicate":
					headers[0].Name = "manifest.yaml"
					headers = append(headers, headers[0])
				case "parent":
					headers[0].Name = "template/file/child"
				case "entry-limit":
					headers = make([]*tar.Header, 0, maxEntries+1)
					for i := range maxEntries + 1 {
						headers = append(headers, &tar.Header{Name: fmt.Sprintf("template/%d", i), Typeflag: tar.TypeReg})
					}
				}
				for _, h := range headers {
					if checkErr := tw.WriteHeader(h); checkErr != nil {
						t.Fatal(checkErr)
					}
				}
				if checkErr := tw.Close(); checkErr != nil {
					t.Fatal(checkErr)
				}
				if checkErr := gz.Close(); checkErr != nil {
					t.Fatal(checkErr)
				}
				a.Layer = buf.Bytes()
				m, err := manifest(a.Manifest)
				if err != nil {
					t.Fatal(err)
				}
				m.Layers[0] = descriptor(LayerType, a.Layer)
				a.Manifest, err = json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				expected = digest.FromBytes(a.Manifest).String()
			}
			if _, checkErr := unpack(t.Context(), a, expected); checkErr == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestDeterministicLayout(t *testing.T) {
	a := packageFixture(t, firstMode)
	files, err := unpack(t.Context(), a, digest.FromBytes(a.Manifest).String())
	if err != nil {
		t.Fatal(err)
	}
	var previous string
	for range 2 {
		root := t.TempDir()
		source := filepath.Join(root, "source")
		if checkErr := os.Mkdir(source, 0o700); checkErr != nil {
			t.Fatal(checkErr)
		}
		for _, name := range sortedKeys(files) {
			f := files[name]
			p := filepath.Join(source, filepath.FromSlash(name))
			if f.Directory {
				err = os.MkdirAll(p, 0o700)
			} else {
				err = os.WriteFile(p, f.Content, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if checkErr := os.Chtimes(p, time.Now(), time.Now()); checkErr != nil {
				t.Fatal(checkErr)
			}
		}
		pin, err := BuildLayout(t.Context(), source, filepath.Join(root, "layout"))
		if err != nil {
			t.Fatal(err)
		}
		if previous != "" && pin != previous {
			t.Fatal("nondeterministic layout")
		}
		previous = pin
	}
}

type gatedBlueprintSource struct {
	*Store
	ready   chan struct{}
	proceed chan struct{}
}

func (s *gatedBlueprintSource) Resolve(ctx context.Context, reference string) (inframill.Blueprint, error) {
	catalog, err := s.Store.Resolve(ctx, reference)
	close(s.ready)
	select {
	case <-s.proceed:
		return catalog, err
	case <-ctx.Done():
		return inframill.Blueprint{}, ctx.Err()
	}
}

func TestRenderKeepsLocalResolutionDuringFetch(t *testing.T) {
	r := newRegistry(t, packageFixture(t, firstMode))
	s := packageStore(t, r)
	initial, err := s.Fetch(t.Context(), r.reference())
	if err != nil {
		t.Fatal(err)
	}
	gated := &gatedBlueprintSource{Store: s, ready: make(chan struct{}), proceed: make(chan struct{})}
	e, err := inframill.New(t.Context(), gated, packageBackend(t))
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := e.Start(t.Context()); checkErr != nil {
		t.Fatal(checkErr)
	}
	defer e.Close()
	var info inframill.InstanceInfo
	config := inframill.InstanceConfig{Blueprint: r.reference(), Values: inframill.InstanceValues{modeKey: firstMode}}
	finished := make(chan error, 1)
	go func() {
		var initErr error
		info, initErr = e.InitInstance(t.Context(), "test/pinned", config)
		finished <- initErr
	}()
	<-gated.ready
	r.mu.Lock()
	r.archive = packageFixture(t, "two")
	r.mu.Unlock()
	updated, err := e.FetchBlueprint(t.Context(), r.reference())
	close(gated.proceed)
	if err != nil || updated.Reference == initial.Reference {
		t.Fatal(updated, err)
	}
	if checkErr := <-finished; checkErr != nil {
		t.Fatal(checkErr)
	}
	if info.Blueprint != initial.Reference {
		t.Fatal("initialization binding moved", info)
	}
}

func TestRepositoryIdentity(t *testing.T) {
	r := newRegistry(t, packageFixture(t, firstMode))
	store := packageStore(t, r)
	host := strings.TrimPrefix(r.server.URL, "http://")
	for _, repository := range []string{"your-org/debug", "team.example/cloud_dev"} {
		fetched, err := store.Fetch(t.Context(), host+"/"+repository+":latest")
		if err != nil {
			t.Fatal(err)
		}
		if fetched.Repository != repository {
			t.Fatalf("repository = %q", fetched.Repository)
		}
		resolved, err := store.Resolve(t.Context(), fetched.Reference)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Repository != repository {
			t.Fatal("resolution lost repository")
		}
		if err := store.ValidateValues(t.Context(), resolved, inframill.InstanceValues{modeKey: firstMode}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := store.List(t.Context())
	if err != nil || len(entries) != 2 {
		t.Fatalf("list: %#v %v", entries, err)
	}
	if entries[0].Digest != entries[1].Digest || entries[0].Repository == entries[1].Repository {
		t.Fatal("artifact identity conflated with repository")
	}
}

func TestObsoleteManifestName(t *testing.T) {
	original := packageFixture(t, firstMode)
	files, err := unpack(t.Context(), original, digest.FromBytes(original.Manifest).String())
	if err != nil {
		t.Fatal(err)
	}
	file := files[manifestFilename]
	file.Content = append(file.Content, []byte("name: obsolete\n")...)
	files[manifestFilename] = file
	archive, err := Pack(files)
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistry(t, archive)
	if _, fetchErr := packageStore(t, r).Fetch(t.Context(), r.reference()); fetchErr == nil {
		t.Fatalf("fetch: %v", fetchErr)
	}
}

func packageBackend(t *testing.T) *backend.Backend {
	t.Helper()
	b, err := backend.New(t.TempDir(), testDrivers(t), backend.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func testDrivers(t *testing.T) drivers.Manager {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "inframill")
	command := exec.Command("go", "build", "-o", binary, "./cmd")
	command.Dir = "../.."
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v: %s", err, out)
	}
	manager, err := drivers.New(t.Context(), drivers.Configurations{"debug": {Path: binary, Args: []string{"driver", "debug"}}}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
